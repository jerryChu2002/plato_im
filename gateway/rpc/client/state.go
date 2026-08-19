package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/gwclient"
	"github.com/hardcore-os/plato/common/stateroute"
)

func initStateClient() {
	stateroute.Init()
}

func getGatewayEndpoint() string {
	return fmt.Sprintf("%s:%d", config.GetGatewaySelfAdvertiseAddr(), config.GetGatewayRPCServerPort())
}

// resolveStateEndpoint 按did一致性哈希算出该did的连接状态应该由哪个state实例处理。
// did==0时(比如ReConn消息在gateway侧找不到旧连接的did映射这种边界情况)退化为按connID路由，
// 仅作降级兜底，不保证与该did其它消息落在同一分片，属于极少数场景下的近似处理。
func resolveStateEndpoint(connID, did uint64) (string, error) {
	var (
		endpoint string
		ok       bool
	)
	if did != 0 {
		endpoint, ok = stateroute.Resolve(did)
	} else {
		endpoint, ok = stateroute.ResolveByKey(fmt.Sprintf("%d", connID))
	}
	if !ok {
		return "", errors.New("no available state instance")
	}
	return endpoint, nil
}

// SendMsg 把gateway收到的客户端上行字节流，按did一致性哈希转发给目标state分片实例
func SendMsg(ctx *context.Context, connID, did uint64, payload []byte) error {
	endpoint, err := resolveStateEndpoint(connID, did)
	if err != nil {
		return err
	}
	rpcCtx, cancel := context.WithTimeout(*ctx, 3*time.Second)
	defer cancel()
	return gwclient.SendMsgToState(rpcCtx, endpoint, getGatewayEndpoint(), connID, payload)
}

// CancelConn 通知负责该did的state分片实例清理某个connID的连接状态。
// did==0表示这条连接从未成功登录过，state侧没有任何状态需要清理，直接跳过。
func CancelConn(ctx *context.Context, connID, did uint64) error {
	if did == 0 {
		return nil
	}
	endpoint, err := resolveStateEndpoint(connID, did)
	if err != nil {
		return err
	}
	rpcCtx, cancel := context.WithTimeout(*ctx, 3*time.Second)
	defer cancel()
	return gwclient.CancelConnOnState(rpcCtx, endpoint, getGatewayEndpoint(), connID)
}
