package sdk

import (
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"
	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/idl/message"
	"github.com/hardcore-os/plato/common/prpc"
	messageservice "github.com/hardcore-os/plato/domain/message/rpc/service"
	"google.golang.org/protobuf/proto"
)

const (
	MsgTypeText      = "text"
	MsgTypeAck       = "ack"
	MsgTypeReConn    = "reConn"
	MsgTypeHeartbeat = "heartbeat"
	MsgLogin         = "loginMsg"
	MsgTypeSystem    = "system"
)

type Chat struct {
	Did              uint64 // 当前设备唯一标识，服务端据此做连接路由与逐设备已读隔离
	Nick             string
	UserID           uint64
	SessionToken     string
	SessionID        string
	conn             *connect
	closeChan        chan struct{}
	MsgClientIDTable map[string]uint64
	sync.RWMutex
	reconnectMu sync.Mutex
	closed      atomic.Bool
	connected   atomic.Bool
	loopDone    chan struct{}
	stateMu     sync.RWMutex
	state       string
	loginResult chan error
	loginOnce   sync.Once

	// OnReady 在登录/重连的ACK成功返回后异步触发一次。典型用途：拉取好友+群列表，
	// 对每个会话调用SyncConversation把离线期间(或断线重连期间)错过的消息补回来。
	OnReady func()
	// OnConnectionStateChanged 在连接状态变化时回调，供GUI显示连接中/已连接/重连中。
	OnConnectionStateChanged func(state string)

	msgClient messageservice.MessageClient
	dedup     *recentSet

	seqMu    sync.Mutex
	localSeq map[string]uint64 // conversationID -> 本地已知的最大seq，用于缺口检测和离线补拉
}

type Message struct {
	Type       string
	Name       string
	FormUserID string
	ToUserID   string
	Content    string
	Session    string
	// ConversationID/Seq 仅聊天类消息(单聊/群聊)才有值，供上层UI或调用方
	// 判断"这是哪个会话的第几条消息"，也是SyncConversation/缺口检测依赖的字段。
	ConversationID string
	Seq            uint64
}

// NewChat 建立到gateway的长连接并登录。onReady可以传nil(比如perf压测工具不需要
// 同步会话)，真正的IM客户端应该传一个"拉好友/群列表 + 对每个会话调用SyncConversation"
// 的回调，用来在登录/重连成功后自动补齐离线期间错过的消息。
func DialChat(ip net.IP, port int, userID, did uint64, sessionToken, nick, sessionID string, onReady func()) (*Chat, error) {
	conn, err := newConnect(ip, port)
	if err != nil {
		return nil, err
	}
	msgClient, err := newMessageClient()
	if err != nil {
		conn.close()
		return nil, fmt.Errorf("connect message service: %w", err)
	}
	chat := &Chat{
		Did:          did,
		Nick:         nick,
		UserID:       userID,
		SessionToken: sessionToken,
		SessionID:    sessionID,
		//这里的conn是gateway的conn
		conn:             conn,
		closeChan:        make(chan struct{}),
		loopDone:         make(chan struct{}),
		MsgClientIDTable: make(map[string]uint64),
		OnReady:          onReady,
		msgClient:        msgClient,
		dedup:            newRecentSet(500),
		localSeq:         make(map[string]uint64),
		loginResult:      make(chan error, 1),
	}
	chat.setConnectionState("正在登录")
	go chat.loop()
	if err := chat.login(); err != nil {
		chat.Close()
		return nil, err
	}
	select {
	case err := <-chat.loginResult:
		if err != nil {
			chat.Close()
			return nil, err
		}
	case <-time.After(5 * time.Second):
		chat.Close()
		return nil, errors.New("login acknowledgement timeout")
	}
	go chat.heartbeat()
	return chat, nil
}

// NewChat 保留原调用方式；新代码优先使用DialChat显式处理首次连接失败。
func NewChat(ip net.IP, port int, userID, did uint64, sessionToken, nick, sessionID string, onReady func()) *Chat {
	chat, err := DialChat(ip, port, userID, did, sessionToken, nick, sessionID, onReady)
	if err != nil {
		panic(err)
	}
	return chat
}

// newMessageClient 惰性建立到domain/message服务的rpc client，用于离线补拉(GetMaxSeq/
// PullOfflineMsgs)。这是"任意一台健康实例都行"的无状态读请求，走标准的服务发现+
// 负载均衡路径。
func newMessageClient() (messageservice.MessageClient, error) {
	pCli, err := prpc.NewPClient(config.GetDomainMessageServerName())
	if err != nil {
		return nil, err
	}
	return messageservice.NewMessageClient(pCli.Conn()), nil
}

// Send 客户端发送消息入口。toUserID非0表示单聊目标账号，groupID非0表示群聊目标，二者互斥。
func (chat *Chat) Send(msg *Message, toUserID, groupID uint64) error {
	if !chat.connected.Load() {
		return fmt.Errorf("gateway is not connected")
	}
	data, _ := json.Marshal(msg)
	clientMsgID := genClientMsgID(chat.Did)
	// 发送方自己看到这条消息是靠本地立即回显(调用方在Send之后自行展示)，不会走
	// handleChatDTO这条接收路径，所以这里要提前把ClientMsgID记进dedup——否则
	// checkSeqGap后续因为收到别人的消息而发现"seq有缺口"触发补拉时，会把这条
	// 自己发过的消息当成"没见过的新消息"重新推给调用方，出现一条重复的历史消息
	// (且发送者身份还会被误判成对方，因为补拉路径不知道这是自己发的)。
	chat.dedup.checkAndAdd(clientMsgID)
	upMsg := &message.UPMsg{
		Head: &message.UPMsgHead{
			ClientID: chat.getClientID(chat.SessionID),
			// chat.conn的id在login的时候已经初始化
			ConnID:      atomic.LoadUint64(&chat.conn.connID),
			SessionId:   chat.SessionID,
			ToUserID:    toUserID,
			GroupID:     groupID,
			ClientMsgID: clientMsgID,
		},
		UPMsgBody: data,
	}
	palyload, _ := proto.Marshal(upMsg)
	if err := chat.conn.send(message.CmdType_UP, palyload); err != nil {
		go chat.reconnect()
		return err
	}
	return nil
}

// genClientMsgID 生成客户端本地的全局唯一消息ID，用于服务端跨连接的幂等去重。
// demo场景下用"did+纳秒时间戳+随机数"拼接即可，不需要引入uuid依赖。
func genClientMsgID(did uint64) string {
	return fmt.Sprintf("%d_%d_%d", did, time.Now().UnixNano(), rand.Int63())
}

func (chat *Chat) GetCurClientID() uint64 {
	if id, ok := chat.MsgClientIDTable[chat.SessionID]; ok {
		return id
	}
	return 0
}

// Close close chat
func (chat *Chat) Close() {
	if !chat.closed.CompareAndSwap(false, true) {
		return
	}
	chat.conn.close()
	close(chat.closeChan)
	<-chat.loopDone
	close(chat.conn.recvChan)
	chat.setConnectionState("已断开")
}

func (chat *Chat) ReConn() {
	go chat.reconnect()
}

// Recv receive message
func (chat *Chat) Recv() <-chan *Message {
	return chat.conn.recv()
}

func (chat *Chat) loop() {
	defer close(chat.loopDone)
	for {
		select {
		case <-chat.closeChan:
			return
		default:
			data, generation, err := chat.conn.read()
			if err != nil {
				if generation == chat.conn.currentGeneration() {
					chat.reconnect()
				}
				continue
			}
			mc := &message.MsgCmd{}
			err = proto.Unmarshal(data, mc)
			if err != nil {
				continue
			}
			var msg *Message
			switch mc.Type {
			case message.CmdType_ACK:
				msg = chat.handAckMsg(mc.Payload)
			case message.CmdType_Push:
				msg = chat.handPushMsg(mc.Payload)

			}
			if msg == nil {
				continue
			}
			select {
			case chat.conn.recvChan <- msg:
			case <-chat.closeChan:
				return
			}
		}
	}
}

func (chat *Chat) getClientID(sessionID string) uint64 {
	chat.Lock()
	defer chat.Unlock()
	var res uint64
	if id, ok := chat.MsgClientIDTable[sessionID]; ok {
		res = id
	}
	//clientID每次重建连接会重新初始化
	chat.MsgClientIDTable[sessionID] = res + 1
	return res
}

func (chat *Chat) login() error {
	loginMsg := message.LoginMsg{
		Head: &message.LoginMsgHead{
			DeviceID:     chat.Did,
			UserID:       chat.UserID,
			SessionToken: chat.SessionToken,
		},
	}
	palyload, err := proto.Marshal(&loginMsg)
	if err != nil {
		panic(err)
	}
	if err := chat.conn.send(message.CmdType_Login, palyload); err != nil {
		return err
	}
	return chat.conn.setReadDeadline(time.Now().Add(5 * time.Second))
}

func (chat *Chat) reConn(oldConnID uint64) error {
	reConn := message.ReConnMsg{
		Head: &message.ReConnMsgHead{
			ConnID: oldConnID,
		},
	}
	palyload, err := proto.Marshal(&reConn)
	if err != nil {
		panic(err)
	}
	if err := chat.conn.send(message.CmdType_ReConn, palyload); err != nil {
		return err
	}
	return chat.conn.setReadDeadline(time.Now().Add(5 * time.Second))
}

func (chat *Chat) heartbeat() {
	tc := time.NewTicker(1 * time.Second)
	defer tc.Stop()
	for {
		select {
		case <-chat.closeChan:
			return
		case <-tc.C:
			if !chat.connected.Load() {
				continue
			}
			hearbeat := message.HeartbeatMsg{
				Head: &message.HeartbeatMsgHead{},
			}
			palyload, err := proto.Marshal(&hearbeat)
			if err != nil {
				panic(err)
			}
			err = chat.conn.send(message.CmdType_Heartbeat, palyload)
			if err != nil {
				go chat.reconnect()
			}
		}
	}
}

func (chat *Chat) reconnect() {
	chat.reconnectMu.Lock()
	defer chat.reconnectMu.Unlock()
	if chat.closed.Load() {
		return
	}

	chat.connected.Store(false)
	chat.setConnectionState("正在重连")
	for attempt := 0; ; attempt++ {
		if chat.closed.Load() {
			return
		}
		if err := chat.conn.dial(); err == nil {
			chat.Lock()
			chat.MsgClientIDTable = make(map[string]uint64)
			chat.Unlock()
			// 旧连接可能已被gateway的EOF路径立即清理；新TCP统一重新Login，
			// 登录成功后的OnReady会按seq补拉断线期间消息。
			if err := chat.login(); err == nil {
				return
			}
		}

		delay := time.Duration(1<<min(attempt, 4)) * time.Second
		select {
		case <-time.After(delay):
		case <-chat.closeChan:
			return
		}
	}
}

func (chat *Chat) setConnectionState(state string) {
	chat.stateMu.Lock()
	chat.state = state
	handler := chat.OnConnectionStateChanged
	chat.stateMu.Unlock()
	if handler != nil {
		handler(state)
	}
}

func (chat *Chat) SetConnectionStateHandler(handler func(state string)) {
	chat.stateMu.Lock()
	chat.OnConnectionStateChanged = handler
	state := chat.state
	chat.stateMu.Unlock()
	if handler != nil && state != "" {
		handler(state)
	}
}

func (chat *Chat) IsClosed() bool {
	return chat.closed.Load()
}
