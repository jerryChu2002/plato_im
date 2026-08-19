package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"

	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/idl/message"
	"github.com/hardcore-os/plato/common/prpc"
	"github.com/hardcore-os/plato/common/tcp"
	"github.com/hardcore-os/plato/gateway/rpc/client"
	"github.com/hardcore-os/plato/gateway/rpc/service"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

var cmdChannel chan *service.CmdContext

// RunMain 启动网关服务
func RunMain(path string) {
	config.Init(path)
	nodeLease, err := acquireGatewayNodeLease(context.Background(), gatewayNodeIdentity())
	if err != nil {
		log.Fatalf("acquire gateway node ID: %v", err)
	}
	defer nodeLease.Close()
	node, err = NewConnIDGenerater(nodeLease.nodeID)
	if err != nil {
		log.Fatalf("initialize connID generator: %v", err)
	}
	log.Printf("gateway connID node ID: %d", nodeLease.nodeID)
	go func() {
		if leaseErr := <-nodeLease.Lost(); leaseErr != nil {
			log.Fatalf("gateway connID node lease lost: %v", leaseErr)
		}
	}()

	ln, err := net.ListenTCP("tcp", &net.TCPAddr{Port: config.GetGatewayTCPServerPort()})
	if err != nil {
		log.Fatalf("StartTCPEPollServer err:%s", err.Error())
		panic(err)
	}
	//gateway里的全局协程池
	initWorkPoll()
	initEpoll(ln, runProc)
	fmt.Println("-------------im gateway stated------------")
	cmdChannel = make(chan *service.CmdContext, config.GetGatewayCmdChannelNum())
	s := prpc.NewPServer(
		prpc.WithServiceName(config.GetGatewayServiceName()),
		prpc.WithIP(config.GetGatewaySelfAdvertiseAddr()),
		prpc.WithPort(config.GetGatewayRPCServerPort()), prpc.WithWeight(config.GetGatewayRPCWeight()))

	fmt.Println(config.GetGatewayServiceName(), config.GetGatewaySelfAdvertiseAddr(), config.GetGatewayRPCServerPort(), config.GetGatewayRPCWeight())

	s.RegisterService(func(server *grpc.Server) {
		service.RegisterGatewayServer(server, &service.Service{CmdChannel: cmdChannel})
	})
	// 启动rpc 请求state的客户端
	client.Init()
	// 启动 命令处理写协程
	go cmdHandler()
	// 启动 rpc server
	s.Start(context.TODO())
}

// 当监听到可读事件，只是解析报文，把报体弄出来，给逻辑层做处理，网关层不做业务处理
func runProc(c *connection, ep *epoller) {
	ctx := context.Background() // 起始的contenxt
	// step1: 读取一个完整的消息包
	dataBuf, err := tcp.ReadData(c.conn)
	if err != nil {
		// 如果读取conn时发现连接关闭，则直接断开连接
		// 通知 state 清理掉意外退出的 conn的状态信息
		if errors.Is(err, io.EOF) {
			// 这步操作是异步的，不需要等到返回成功在进行，因为消息可靠性的保障是通过协议完成的而非某次cmd
			ep.remove(c)
			did, _ := tables.getDidByConn(c.id)
			client.CancelConn(&ctx, c.id, did)
		}
		return
	}
	// step2: 从报文里解析出用于分片路由的did，交给按did一致性哈希算出的目标state实例处理
	did := resolveRouteDid(c.id, dataBuf)
	err = wPool.Submit(func() {
		client.SendMsg(&ctx, c.id, did, dataBuf)
	})
	if err != nil {
		log.Printf("submit message task failed: connID=%d did=%d err=%v", c.id, did, err)
	}
}

// resolveRouteDid 从上行报文里解析出用于一致性哈希分片路由的did：
//   - Login消息：直接取LoginMsgHead.DeviceID(此时该连接还没有did映射，只能从报文本身拿)
//   - ReConn消息：取旧connID(装在ReConnMsgHead里)，反查已缓存的did绑定
//   - 其余消息(Heartbeat/UP/ACK)：取当前connID已缓存的did(登录/重连成功时由sendMsgByCmd回填)
//
// 解析失败或映射缺失时返回0，由 gateway/rpc/client 里的 resolveStateEndpoint 做降级兜底
// (按connID路由，无法保证与该did其它消息落在同一分片，属于极少数场景下的近似处理)。
func resolveRouteDid(connID uint64, dataBuf []byte) uint64 {
	mc := &message.MsgCmd{}
	if err := proto.Unmarshal(dataBuf, mc); err != nil {
		return 0
	}
	switch mc.Type {
	case message.CmdType_Login:
		loginMsg := &message.LoginMsg{}
		if err := proto.Unmarshal(mc.Payload, loginMsg); err == nil && loginMsg.Head != nil {
			return loginMsg.Head.DeviceID
		}
	case message.CmdType_ReConn:
		reConnMsg := &message.ReConnMsg{}
		if err := proto.Unmarshal(mc.Payload, reConnMsg); err == nil && reConnMsg.Head != nil {
			if did, ok := tables.getDidByConn(reConnMsg.Head.ConnID); ok {
				return did
			}
		}
	default:
		if did, ok := tables.getDidByConn(connID); ok {
			return did
		}
	}
	return 0
}

func cmdHandler() {
	for cmd := range cmdChannel {
		// 异步提交到协池中完成发送任务
		switch cmd.Cmd {
		case service.DelConnCmd:
			wPool.Submit(func() { closeConn(cmd) })
		case service.PushCmd:
			wPool.Submit(func() { sendMsgByCmd(cmd) })
		default:
			panic("command undefined")
		}
	}
}
func closeConn(cmd *service.CmdContext) {
	if connPtr, ok := ep.tables.Load(cmd.ConnID); ok {
		conn, _ := connPtr.(*connection)
		conn.Close()
	}
}
func sendMsgByCmd(cmd *service.CmdContext) {
	if connPtr, ok := ep.tables.Load(cmd.ConnID); ok {
		conn, _ := connPtr.(*connection)
		bindDidFromPushPayload(cmd.ConnID, cmd.Payload)
		dp := tcp.DataPgk{
			Len:  uint32(len(cmd.Payload)),
			Data: cmd.Payload,
		}
		tcp.SendData(conn.conn, dp.Marshal())
	}
}

// bindDidFromPushPayload 窥探下行数据包：若为登录/重连成功的ACK，则记录connID<->did的映射，
// 供后续该连接的上行消息(Heartbeat/UP/ACK)做一致性哈希分片路由——gateway无法从这些报文类型
// 本身直接拿到did，必须依赖登录/重连时的一次性回填。
func bindDidFromPushPayload(connID uint64, payload []byte) {
	mc := &message.MsgCmd{}
	if err := proto.Unmarshal(payload, mc); err != nil || mc.Type != message.CmdType_ACK {
		return
	}
	ack := &message.ACKMsg{}
	if err := proto.Unmarshal(mc.Payload, ack); err != nil {
		return
	}
	if (ack.Type == message.CmdType_Login || ack.Type == message.CmdType_ReConn) && ack.Code == 0 && ack.DeviceID != 0 {
		tables.bindConnDid(connID, ack.DeviceID)
	}
}
