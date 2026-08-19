package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hardcore-os/plato/common/auth"
	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/prpc"
	"google.golang.org/protobuf/proto"

	"github.com/hardcore-os/plato/common/cache"
	"github.com/hardcore-os/plato/common/convid"
	messagedto "github.com/hardcore-os/plato/common/idl/domain/message"
	"github.com/hardcore-os/plato/common/idl/message"
	"github.com/hardcore-os/plato/common/mq"
	"github.com/hardcore-os/plato/common/router"
	"github.com/hardcore-os/plato/common/stateroute"
	"github.com/hardcore-os/plato/state/rpc/client"
	"github.com/hardcore-os/plato/state/rpc/service"
	"google.golang.org/grpc"
)

// RunMain 启动状态服务
func RunMain(path string) {
	// 启动时的全局上下文
	ctx := context.TODO()
	// 初始化全局配置
	config.Init(path)
	// 初始化RPC 客户端
	client.Init()
	// 初始化到state分片的一致性哈希路由(供推送消息时按did查找目标state实例)
	stateroute.Init()
	// 初始化Kafka生产者(上行消息落地到消息队列，供domain/message的consumer落库+推送)
	mq.InitProducer(config.GetKafkaBrokers())
	// 启动时间轮
	InitTimer()
	// 启动远程cache状态机组件
	InitCacheState(ctx)
	// 启动 命令处理写协程
	go cmdHandler()
	// 注册rpc server
	s := prpc.NewPServer(
		prpc.WithServiceName(config.GetStateServiceName()),
		prpc.WithIP(config.GetSateServiceAddr()),
		prpc.WithPort(config.GetSateServerPort()), prpc.WithWeight(config.GetSateRPCWeight()))
	s.RegisterService(func(server *grpc.Server) {
		service.RegisterStateServer(server, cs.server)
	})
	// 启动 rpc server
	s.Start(ctx)
}

// 消费信令通道，识别gateway与state server之间的协议路由
func cmdHandler() {
	for cmdCtx := range cs.server.CmdChannel {
		switch cmdCtx.Cmd {
		case service.CancelConnCmd:
			fmt.Printf("cancel conn endpoint:%s, coonID:%d, data:%+v\n", cmdCtx.Endpoint, cmdCtx.ConnID, cmdCtx.Payload)
			cs.connLogOut(*cmdCtx.Ctx, cmdCtx.ConnID)
		case service.SendMsgCmd:
			msgCmd := &message.MsgCmd{}
			err := proto.Unmarshal(cmdCtx.Payload, msgCmd)
			if err != nil {
				fmt.Printf("SendMsgCmd:err=%s\n", err.Error())
			}
			msgCmdHandler(cmdCtx, msgCmd)
		case service.PushToConnCmd:
			pushToConn(*cmdCtx.Ctx, cmdCtx.ConnID, cmdCtx.Seq, cmdCtx.Payload)
		}
	}
}

// 识别消息类型，识别客户端与state server之间的协议路由
func msgCmdHandler(cmdCtx *service.CmdContext, msgCmd *message.MsgCmd) {
	switch msgCmd.Type {
	case message.CmdType_Login:
		loginMsgHandler(cmdCtx, msgCmd)
	case message.CmdType_Heartbeat:
		hearbeatMsgHandler(cmdCtx, msgCmd)
	case message.CmdType_ReConn:
		reConnMsgHandler(cmdCtx, msgCmd)
	case message.CmdType_UP:
		upMsgHandler(cmdCtx, msgCmd)
	case message.CmdType_ACK:
		ackMsgHandler(cmdCtx, msgCmd)
	case message.CmdType_ReadReceipt:
		readReceiptMsgHandler(cmdCtx, msgCmd)
	}
}

// 实现登陆功能
func loginMsgHandler(cmdCtx *service.CmdContext, msgCmd *message.MsgCmd) {
	//实际当login的时候，只有Head有值,did是客户端分配的
	loginMsg := &message.LoginMsg{}
	err := proto.Unmarshal(msgCmd.Payload, loginMsg)
	if err != nil {
		fmt.Printf("loginMsgHandler:err=%s\n", err.Error())
		return
	}
	head := loginMsg.GetHead()
	if head == nil || head.GetUserID() == 0 || head.GetDeviceID() == 0 {
		sendACKMsg(message.CmdType_Login, cmdCtx.ConnID, 0, 1, "invalid login identity", 0, 0, "")
		return
	}
	if err := auth.VerifySessionToken(head.GetSessionToken(), head.GetUserID(), head.GetDeviceID(), time.Now()); err != nil {
		sendACKMsg(message.CmdType_Login, cmdCtx.ConnID, 0, 1, "authentication failed", 0, 0, "")
		return
	}
	if err = cs.connLogin(*cmdCtx.Ctx, head.GetUserID(), head.GetDeviceID(), cmdCtx.ConnID, cmdCtx.Endpoint); err != nil {
		sendACKMsg(message.CmdType_Login, cmdCtx.ConnID, 0, 1, "login state failed", 0, 0, "")
		return
	}
	sendACKMsg(message.CmdType_Login, cmdCtx.ConnID, 0, 0, "login ok", head.GetDeviceID(), head.GetUserID(), "")
}

// 心跳消息的处理
func hearbeatMsgHandler(cmdCtx *service.CmdContext, msgCmd *message.MsgCmd) {
	heartMsg := &message.HeartbeatMsg{}
	err := proto.Unmarshal(msgCmd.Payload, heartMsg)
	if err != nil {
		fmt.Printf("hearbeatMsgHandler:err=%s\n", err.Error())
		return
	}
	cs.reSetHeartTimer(cmdCtx.ConnID)
	fmt.Printf("hearbeatMsgHandler connID=%d\n", cmdCtx.ConnID)
	// TODO为减少通信量，可以暂时不回复心跳的ack
}

// 重连逻辑处理
func reConnMsgHandler(cmdCtx *service.CmdContext, msgCmd *message.MsgCmd) {
	reConnMsg := &message.ReConnMsg{}
	err := proto.Unmarshal(msgCmd.Payload, reConnMsg)
	var code uint32
	msg := "reconn ok"
	if err != nil {
		fmt.Printf("reConnMsgHandler:err=%s\n", err.Error())
		return
	}
	// 重连的消息头中的connID才是上一次断开连接的connID
	userID, did, err := cs.reConn(*cmdCtx.Ctx, reConnMsg.Head.ConnID, cmdCtx.ConnID, cmdCtx.Endpoint)
	if err != nil {
		code, msg = 1, "reconn failed"
	}
	sendACKMsg(message.CmdType_ReConn, cmdCtx.ConnID, 0, code, msg, did, userID, "")
}

// 处理上行消息：连接内幂等校验通过后，组装成ChatMsgDTO生产到Kafka，
// 由domain/message的consumer负责落库和推送——state自己不再直接回显/推送业务内容。
//
// ACK必须在Kafka生产确认成功之后才回复"成功"：如果先回ACK再生产，一旦生产失败，
// 客户端会在毫无察觉的情况下把这条消息当成"已送达"，消息就这样安静地丢了。
// 生产失败时回一个非0 code的ACK，客户端据此提示用户重发。
func upMsgHandler(cmdCtx *service.CmdContext, msgCmd *message.MsgCmd) {
	upMsg := &message.UPMsg{}
	err := proto.Unmarshal(msgCmd.Payload, upMsg)
	if err != nil {
		fmt.Printf("upMsgHandler:err=%s\n", err.Error())
		return
	}
	if !cs.compareAndIncrClientID(*cmdCtx.Ctx, cmdCtx.ConnID, upMsg.Head.ClientID, upMsg.Head.SessionId) {
		// 同一物理连接内的重复上行(网络层重传)，已经处理过一次，直接丢弃
		return
	}
	if err := publishChatMsg(*cmdCtx.Ctx, cmdCtx.ConnID, upMsg); err != nil {
		fmt.Printf("upMsgHandler: publishChatMsg err=%s\n", err.Error())
		sendACKMsg(message.CmdType_UP, cmdCtx.ConnID, upMsg.Head.ClientID, 1, "publish failed", 0, 0, upMsg.Head.ClientMsgID)
		return
	}
	sendACKMsg(message.CmdType_UP, cmdCtx.ConnID, upMsg.Head.ClientID, 0, "ok", 0, 0, upMsg.Head.ClientMsgID)
}

// publishChatMsg 把一条上行消息组装成ChatMsgDTO并生产到Kafka：
//  1. 按UPMsgHead.GroupID/ToDid决定这是单聊还是群聊，构造出会话内消息双方都能算出同一个值的conversationID
//  2. 用Redis INCR分配该会话内严格递增的seq(允许因produce失败出现空洞，客户端靠seq不连续触发补拉兜底)
//  3. 以conversationID为key生产到Kafka，保证同一会话的消息在消费侧严格有序
func publishChatMsg(ctx context.Context, connID uint64, upMsg *message.UPMsg) error {
	connState, ok := cs.loadConnIDState(connID)
	if !ok {
		return fmt.Errorf("publishChatMsg: connID=%d state not found", connID)
	}
	fromDid := connState.did
	fromUserID := connState.userID
	head := upMsg.Head

	var (
		conversationID string
		bizType        = messagedto.BizMsgType_ChatSingle
	)
	switch {
	case head.GroupID != 0:
		conversationID = convid.Group(head.GroupID)
		bizType = messagedto.BizMsgType_ChatGroup
	case head.ToUserID != 0:
		conversationID = convid.Single(fromUserID, head.ToUserID)
	default:
		return errors.New("publishChatMsg: UPMsgHead既没有设置ToUserID也没有设置GroupID")
	}

	seqKey := fmt.Sprintf(cache.MsgSeqKey, conversationID)
	seq, err := cache.IncrNoExpire(ctx, seqKey)
	if err != nil {
		return err
	}

	clientMsgID := head.ClientMsgID
	if clientMsgID == "" {
		// 兼容未升级到新协议的客户端：退化拼一个(不保证全局强唯一，仅demo兜底，
		// 生产环境应强制要求客户端携带真正全局唯一的ClientMsgID)
		clientMsgID = fmt.Sprintf("%d_%d_%d", fromDid, connID, head.ClientID)
	}

	dto := &messagedto.ChatMsgDTO{
		ConversationID: conversationID,
		FromDid:        fromDid,
		FromUserID:     fromUserID,
		ToUserID:       head.ToUserID,
		GroupID:        head.GroupID,
		BizType:        bizType,
		Content:        upMsg.UPMsgBody,
		Seq:            uint64(seq),
		ClientMsgID:    clientMsgID,
		CreateTime:     time.Now().UnixMilli(),
	}
	data, err := proto.Marshal(dto)
	if err != nil {
		return err
	}
	_, _, err = mq.PublishWithKey(config.GetKafkaChatTopic(), conversationID, data)
	return err
}

// readReceiptMsgHandler 处理已读回执：客户端在打开会话/切走会话时上报"已读到某个seq"。
// 直接写Redis(read_seq_{did})，不经过Kafka、不回ACK——这是轻量的单机状态更新，允许因网络
// 抖动丢失(下次打开会话会重新上报一次)，不需要Kafka那套"不丢不重"的可靠性保障。
// 只在新值更大时才覆盖，防止乱序到达的旧回执把已读进度往回冲。
func readReceiptMsgHandler(cmdCtx *service.CmdContext, msgCmd *message.MsgCmd) {
	readMsg := &message.ReadReceiptMsg{}
	err := proto.Unmarshal(msgCmd.Payload, readMsg)
	if err != nil {
		fmt.Printf("readReceiptMsgHandler:err=%s\n", err.Error())
		return
	}
	connState, ok := cs.loadConnIDState(cmdCtx.ConnID)
	if !ok {
		fmt.Printf("readReceiptMsgHandler: connID=%d state not found\n", cmdCtx.ConnID)
		return
	}
	ctx := *cmdCtx.Ctx
	key := fmt.Sprintf(cache.ReadSeqKey, connState.did)
	cur, err := cache.HGetUInt64(ctx, key, readMsg.GetConversationID())
	if err != nil {
		fmt.Printf("readReceiptMsgHandler: HGetUInt64 err=%s\n", err.Error())
		return
	}
	if readMsg.GetReadSeq() <= cur {
		return
	}
	if err := cache.HSet(ctx, key, readMsg.GetConversationID(), readMsg.GetReadSeq()); err != nil {
		fmt.Printf("readReceiptMsgHandler: HSet err=%s\n", err.Error())
	}
}

// 处理下行消息ack回复
func ackMsgHandler(cmdCtx *service.CmdContext, msgCmd *message.MsgCmd) {
	ackMsg := &message.ACKMsg{}
	err := proto.Unmarshal(msgCmd.Payload, ackMsg)
	if err != nil {
		fmt.Printf("ackMsgHandler:err=%s\n", err.Error())
		return
	}
	cs.ackLastMsg(*cmdCtx.Ctx, ackMsg.ConnID, ackMsg.SessionID, ackMsg.MsgID)
}

// pushToConn 供跨分片/跨服务(好友通知、群消息fan-out、Kafka consumer等)调用方使用，
// 委托"持有目标connID内存状态"的本state实例代为推送，纳入既有的ACK重推可靠性保障。
// 调用方必须已经确认该connID的did确实由本state实例托管(按一致性哈希算出)，
// 否则这里会因为本地找不到connState而直接丢弃。seq是调用方已经分配好的会话内消息序号。
func pushToConn(ctx context.Context, connID, seq uint64, payload []byte) {
	if _, ok := cs.loadConnIDState(connID); !ok {
		fmt.Printf("pushToConn: connID=%d not hosted on this state instance\n", connID)
		return
	}
	pushMsg(ctx, connID, 0, seq, payload)
}

// 下行消息处理：组装PushMsg(带上msgID，供客户端做丢消息检测)、推送给客户端、
// 并把这条消息记入last_msg+挂ACK重推定时器，实现"至少一次"送达语义。
func pushMsg(ctx context.Context, connID, sessionID, msgID uint64, data []byte) {
	pushMsg := &message.PushMsg{
		Content:   data,
		MsgID:     msgID,
		SessionID: sessionID,
	}
	if data, err := proto.Marshal(pushMsg); err != nil {
		fmt.Printf("Marshal:err=%s\n", err.Error())
	} else {
		//sendMsg 给回gateway
		sendMsg(connID, message.CmdType_Push, data)
		err = cs.appendLastMsg(ctx, connID, pushMsg)
		if err != nil {
			panic(err)
		}
	}
}

// 发送ack msg
func sendACKMsg(ackType message.CmdType, connID, clientID uint64, code uint32, msg string, deviceID, userID uint64, clientMsgID string) {
	ackMsg := &message.ACKMsg{}
	ackMsg.Code = code
	ackMsg.Msg = msg
	ackMsg.ConnID = connID
	ackMsg.Type = ackType
	ackMsg.ClientID = clientID
	ackMsg.DeviceID = deviceID
	ackMsg.UserID = userID
	ackMsg.ClientMsgID = clientMsgID
	downLoad, err := proto.Marshal(ackMsg)
	if err != nil {
		fmt.Println("sendACKMsg", err)
	}
	sendMsg(connID, message.CmdType_ACK, downLoad)
}

// 发送msg，给到gateway。按connID反查其归属的did，再查路由表找到该did当前登录在
// 哪个gateway实例上，精确路由过去——而不是像早期实现那样固定连一个写死的gateway地址。
func sendMsg(connID uint64, ty message.CmdType, downLoad []byte) {
	mc := &message.MsgCmd{}
	mc.Type = ty
	mc.Payload = downLoad
	data, err := proto.Marshal(mc)
	ctx := context.TODO()
	if err != nil {
		fmt.Println("sendMsg", ty, err)
		return
	}
	connState, ok := cs.loadConnIDState(connID)
	if !ok {
		fmt.Printf("sendMsg: connID=%d state not found\n", connID)
		return
	}
	rec, err := router.QueryRecord(ctx, connState.did)
	if err != nil || rec == nil {
		fmt.Printf("sendMsg: no route for did=%d connID=%d, err=%v\n", connState.did, connID, err)
		return
	}
	client.Push(&ctx, rec.Endpoint, connID, data)
}

// 重新发送push msg
func rePush(connID uint64) {
	pushMsg, err := cs.getLastMsg(context.Background(), connID)
	if err != nil {
		panic(err)
	}
	if pushMsg == nil {
		return
	}
	msgData, err := proto.Marshal(pushMsg)
	if err != nil {
		panic(err)
	}
	sendMsg(connID, message.CmdType_Push, msgData)
	if state, ok := cs.loadConnIDState(connID); ok {
		state.reSetMsgTimer(connID, pushMsg.SessionID, pushMsg.MsgID)
	}
}
