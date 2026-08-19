package client

import (
	"context"
	"time"

	"github.com/hardcore-os/plato/common/gwclient"
)

// Push 精确路由到endpoint对应的那台gateway实例，把payload写给它持有的connID连接。
// endpoint必须是调用方已经查好的目标gateway实例地址(比如从router.QueryRecord按did查出来的)，
// 不再像早期实现那样在initGatewayClient()里写死连一个固定的gateway地址。
func Push(ctx *context.Context, endpoint string, connID uint64, Payload []byte) error {
	rpcCtx, cancel := context.WithTimeout(*ctx, 3*time.Second)
	defer cancel()
	return gwclient.PushToGateway(rpcCtx, endpoint, connID, Payload)
}

// DelConn 通知endpoint对应的gateway实例主动关闭某个connID的长连接。
func DelConn(ctx *context.Context, endpoint string, connID uint64, Payload []byte) error {
	rpcCtx, cancel := context.WithTimeout(*ctx, 3*time.Second)
	defer cancel()
	return gwclient.DelConnOnGateway(rpcCtx, endpoint, connID)
}
