package gwclient

import (
	"context"
	"time"

	gwservice "github.com/hardcore-os/plato/gateway/rpc/service"
)

const rpcTimeout = 3 * time.Second

// PushToGateway 精确路由到endpoint对应的那台gateway实例，把payload写给它持有的connID连接。
func PushToGateway(ctx context.Context, endpoint string, connID uint64, payload []byte) error {
	conn, err := GetConn(endpoint)
	if err != nil {
		return err
	}
	cli := gwservice.NewGatewayClient(conn)
	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	_, err = cli.Push(rpcCtx, &gwservice.GatewayRequest{ConnID: connID, Data: payload})
	return err
}

// DelConnOnGateway 通知endpoint对应的gateway实例主动关闭某个connID的长连接。
func DelConnOnGateway(ctx context.Context, endpoint string, connID uint64) error {
	conn, err := GetConn(endpoint)
	if err != nil {
		return err
	}
	cli := gwservice.NewGatewayClient(conn)
	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	_, err = cli.DelConn(rpcCtx, &gwservice.GatewayRequest{ConnID: connID})
	return err
}
