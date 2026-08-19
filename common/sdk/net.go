package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	frienddto "github.com/hardcore-os/plato/common/idl/domain/friend"
	messagedto "github.com/hardcore-os/plato/common/idl/domain/message"
	"github.com/hardcore-os/plato/common/idl/message"
	"github.com/hardcore-os/plato/common/tcp"
	"google.golang.org/protobuf/proto"
)

type connect struct {
	sendChan, recvChan chan *Message
	mu                 sync.RWMutex
	writeMu            sync.Mutex
	conn               *net.TCPConn
	connID             uint64
	ip                 net.IP
	port               int
	generation         uint64
}

func newConnect(ip net.IP, port int) (*connect, error) {
	clientConn := &connect{
		sendChan: make(chan *Message),
		recvChan: make(chan *Message),
		ip:       ip,
		port:     port,
	}
	if err := clientConn.dial(); err != nil {
		return nil, err
	}
	return clientConn, nil
}

func (c *connect) dial() error {
	addr := &net.TCPAddr{IP: c.ip, Port: c.port}
	conn, err := net.DialTCP("tcp", nil, addr)
	if err != nil {
		return fmt.Errorf("connect gateway %s: %w", addr, err)
	}
	c.mu.Lock()
	oldConn := c.conn
	c.conn = conn
	c.generation++
	c.mu.Unlock()
	if oldConn != nil {
		_ = oldConn.Close()
	}
	return nil
}

// handAckMsg 处理服务端对上行消息的确认。Login/ReConn类型成功时触发OnReady回调
// (典型用途：拉取好友+群列表、对每个会话做离线补拉)；UP类型失败时提示用户重发，
// 而不是像之前那样把"发送失败"悄悄吞掉。
func (chat *Chat) handAckMsg(data []byte) *Message {
	ackMsg := &message.ACKMsg{}
	proto.Unmarshal(data, ackMsg)
	switch ackMsg.Type {
	case message.CmdType_Login, message.CmdType_ReConn:
		if ackMsg.Code == 0 {
			atomic.StoreUint64(&chat.conn.connID, ackMsg.ConnID)
			chat.connected.Store(true)
			_ = chat.conn.setReadDeadline(time.Time{})
			chat.setConnectionState("已连接")
			chat.loginOnce.Do(func() { chat.loginResult <- nil })
		} else {
			loginErr := fmt.Errorf("登录失败: %s", ackMsg.Msg)
			chat.setConnectionState(loginErr.Error())
			chat.loginOnce.Do(func() { chat.loginResult <- loginErr })
		}
		if ackMsg.Code == 0 && chat.OnReady != nil {
			go chat.OnReady()
		}
	case message.CmdType_UP:
		if ackMsg.Code != 0 {
			return &Message{
				Type:    MsgTypeSystem,
				Name:    "system",
				Content: fmt.Sprintf("消息发送失败(clientMsgID=%s): %s，请重新发送", ackMsg.ClientMsgID, ackMsg.Msg),
			}
		}
	}
	return &Message{
		Type:       MsgTypeAck,
		Name:       "plato",
		FormUserID: "1212121",
		ToUserID:   "222212122",
		Content:    ackMsg.Msg,
	}
}

// handPushMsg 处理服务端下行推送。PushMsg.Content统一是一个序列化的
// messagedto.ChatMsgDTO(单聊/群聊消息和好友系统通知共用同一个信封)，
// 按BizType分支解出真正展示给用户的内容。
func (chat *Chat) handPushMsg(data []byte) *Message {
	pushMsg := &message.PushMsg{}
	if err := proto.Unmarshal(data, pushMsg); err != nil {
		return nil
	}

	dto := &messagedto.ChatMsgDTO{}
	if err := proto.Unmarshal(pushMsg.Content, dto); err != nil {
		return nil
	}

	// 无论什么类型，都先回一个ACK，复用现状UP类型的ACK确认机制。
	// 必须原样带回收到的这条PushMsg的MsgID/SessionID——服务端(state.ackLastMsg)靠
	// "sessionID_msgID"去匹配它当前挂着的重推定时器，如果这里不带回去(全部是0)，
	// 服务端永远匹配不上，100ms重推定时器就会针对同一条消息无限重推下去。
	ackMsg := &message.ACKMsg{
		Type:      message.CmdType_UP,
		ConnID:    atomic.LoadUint64(&chat.conn.connID),
		MsgID:     pushMsg.MsgID,
		SessionID: pushMsg.SessionID,
	}
	ackData, _ := proto.Marshal(ackMsg)
	chat.conn.send(message.CmdType_ACK, ackData)

	switch dto.BizType {
	case messagedto.BizMsgType_ChatSingle, messagedto.BizMsgType_ChatGroup:
		return chat.handleChatDTO(dto, false)
	case messagedto.BizMsgType_FriendRequest, messagedto.BizMsgType_FriendAgree, messagedto.BizMsgType_FriendReject:
		req := &frienddto.FriendRequestDTO{}
		proto.Unmarshal(dto.Content, req)
		return buildFriendNoticeMessage(dto.BizType, req)
	}
	return nil
}

// handleChatDTO 把一条聊天类ChatMsgDTO转成展示用Message。
//   - 先按ClientMsgID去重：Kafka是at-least-once语义，consumer重启/rebalance等场景
//     可能导致同一条消息被推送不止一次，服务端(domain/message)已经在落库+推送两处
//     都做了去重，这里是客户端侧的最后一道兜底防线。
//   - fromOfflinePull=true时跳过缺口检测：补拉本身就是在填缺口，不需要再触发一次。
func (chat *Chat) handleChatDTO(dto *messagedto.ChatMsgDTO, fromOfflinePull bool) *Message {
	if !chat.dedup.checkAndAdd(dto.ClientMsgID) {
		return nil
	}
	if !fromOfflinePull && dto.ConversationID != "" {
		chat.checkSeqGap(dto.ConversationID, dto.Seq)
	}
	msg := &Message{}
	json.Unmarshal(dto.Content, msg)
	msg.FormUserID = fmt.Sprintf("%d", dto.FromUserID)
	if dto.GroupID != 0 {
		msg.ToUserID = fmt.Sprintf("group:%d", dto.GroupID)
	}
	msg.ConversationID = dto.ConversationID
	msg.Seq = dto.Seq
	return msg
}

// checkSeqGap 发现某个会话的seq不连续(中间掉了消息，比如推送时目标恰好在重连间隙)时，
// 异步补拉缺失的那一段；同时把本地已知的最大seq往前推，供SyncConversation判断
// 是否需要追赶(不需要等下一次登录/重连才发现自己落后了)。
func (chat *Chat) checkSeqGap(conversationID string, seq uint64) {
	chat.seqMu.Lock()
	local := chat.localSeq[conversationID]
	if seq > local {
		chat.localSeq[conversationID] = seq
	}
	chat.seqMu.Unlock()
	// 注意:这里不能加"local > 0"这类守卫——全新连接(local从0开始)第一次收到的
	// 实时推送完全可能不是该会话的第1条消息(比如离线期间攒了好几条，只有最后一条
	// 追上了在线状态被实时推送)，如果因为local==0就跳过补拉，会把localSeq直接钉在
	// 这条消息的seq上，导致之后SyncConversation误判"已追上"而永久漏掉更早的消息。
	if seq > local+1 {
		go chat.pullRange(conversationID, local, seq-1)
	}
}

// buildFriendNoticeMessage 把好友申请/同意/拒绝通知渲染成一条系统提示消息
func buildFriendNoticeMessage(bizType messagedto.BizMsgType, req *frienddto.FriendRequestDTO) *Message {
	var text string
	switch bizType {
	case messagedto.BizMsgType_FriendRequest:
		text = fmt.Sprintf("收到好友申请 requestID=%d 来自 did=%d，附言:%s（用 /accept %d 或 /reject %d 处理）",
			req.ID, req.FromDid, req.Message, req.ID, req.ID)
	case messagedto.BizMsgType_FriendAgree:
		text = fmt.Sprintf("did=%d 同意了你的好友申请", req.ToDid)
	case messagedto.BizMsgType_FriendReject:
		text = fmt.Sprintf("did=%d 拒绝了你的好友申请", req.ToDid)
	}
	return &Message{Type: MsgTypeSystem, Name: "system", Content: text}
}

func (c *connect) read() ([]byte, uint64, error) {
	c.mu.RLock()
	conn := c.conn
	generation := c.generation
	c.mu.RUnlock()
	if conn == nil {
		return nil, generation, errors.New("gateway connection is not available")
	}
	data, err := tcp.ReadData(conn)
	return data, generation, err
}

func (c *connect) currentGeneration() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

func (c *connect) setReadDeadline(deadline time.Time) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return errors.New("gateway connection is not available")
	}
	return conn.SetReadDeadline(deadline)
}

func (c *connect) send(ty message.CmdType, palyload []byte) error {
	// 直接发送给接收方
	msgCmd := message.MsgCmd{
		Type:    ty,
		Payload: palyload,
	}
	msg, err := proto.Marshal(&msgCmd)
	if err != nil {
		panic(err)
	}
	dataPgk := tcp.DataPgk{
		Data: msg,
		Len:  uint32(len(msg)),
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return errors.New("gateway connection is not available")
	}
	_, err = conn.Write(dataPgk.Marshal())
	return err
}

func (c *connect) recv() <-chan *Message {
	return c.recvChan
}

func (c *connect) close() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}
