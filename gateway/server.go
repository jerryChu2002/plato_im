package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"

	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/prpc"
	"github.com/hardcore-os/plato/common/tcp"
	"github.com/hardcore-os/plato/gateway/rpc/client"
	"github.com/hardcore-os/plato/gateway/rpc/service"
	"google.golang.org/grpc"
)

var cmdChannel chan *service.CmdContext

// RunMain 启动网关服务
func RunMain(path string) {
	config.Init(path)
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
		prpc.WithIP(config.GetGatewayServiceAddr()),
		prpc.WithPort(config.GetGatewayRPCServerPort()), prpc.WithWeight(config.GetGatewayRPCWeight()))

	fmt.Println(config.GetGatewayServiceName(), config.GetGatewayServiceAddr(), config.GetGatewayRPCServerPort(), config.GetGatewayRPCWeight())

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
			client.CancelConn(&ctx, getGatewayEndpoint(), c.id, nil)
		}
		return
	}
	err = wPool.Submit(func() {
		// step2:交给 state server rpc 处理
		client.SendMsg(&ctx, getGatewayEndpoint(), c.id, dataBuf)
	})
	if err != nil {
		fmt.Errorf("runProc:err:%+v\n", err.Error())
	}
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
		dp := tcp.DataPgk{
			Len:  uint32(len(cmd.Payload)),
			Data: cmd.Payload,
		}
		tcp.SendData(conn.conn, dp.Marshal())
	}
}

func getGatewayEndpoint() string {
	return fmt.Sprintf("%s:%d", config.GetGatewayServiceAddr(), config.GetGatewayRPCServerPort())
}
