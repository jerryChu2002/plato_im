package gwclient

import (
	"context"

	stservice "github.com/hardcore-os/plato/state/rpc/service"
)

// SendMsgToState 把gateway收到的上行字节流转发给按did一致性哈希算出的目标state分片实例。
// gatewayEndpoint是发起方(gateway)自己的RPC地址，state登录时会把它记进路由表，
// 供后续任何服务按did查到"该did当前登录在哪个gateway实例"。
func SendMsgToState(ctx context.Context, stateEndpoint, gatewayEndpoint string, connID uint64, payload []byte) error {
	conn, err := GetConn(stateEndpoint)
	if err != nil {
		return err
	}
	cli := stservice.NewStateClient(conn)
	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	_, err = cli.SendMsg(rpcCtx, &stservice.StateRequest{Endpoint: gatewayEndpoint, ConnID: connID, Data: payload})
	return err
}

// CancelConnOnState 通知负责该did的state分片实例清理某个connID的连接状态。
func CancelConnOnState(ctx context.Context, stateEndpoint, gatewayEndpoint string, connID uint64) error {
	conn, err := GetConn(stateEndpoint)
	if err != nil {
		return err
	}
	cli := stservice.NewStateClient(conn)
	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	_, err = cli.CancelConn(rpcCtx, &stservice.StateRequest{Endpoint: gatewayEndpoint, ConnID: connID})
	return err
}

// PushToConnOnState 委托"持有目标connID内存状态"的那个state实例代为推送，
// 并纳入其既有的ACK重推可靠性保障(connState.appendMsg/ackLastMsg)。
// seq是调用方已经分配好的会话内严格递增消息序号，供state透传给客户端做丢消息检测。
// 供跨分片/跨服务的调用方使用：好友通知、群消息fan-out、Kafka consumer等。
func PushToConnOnState(ctx context.Context, stateEndpoint string, connID, seq uint64, payload []byte) error {
	conn, err := GetConn(stateEndpoint)
	if err != nil {
		return err
	}
	cli := stservice.NewStateClient(conn)
	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	_, err = cli.PushToConn(rpcCtx, &stservice.StateRequest{ConnID: connID, Data: payload, Seq: seq})
	return err
}
