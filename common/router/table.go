package router

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hardcore-os/plato/common/cache"
)

const (
	gatewayRouterKey = "gateway_router_%d"
	ttl7D            = 7 * 24 * 60 * 60
)

type Record struct {
	Endpoint string
	ConndID  uint64
}

func AddRecord(ctx context.Context, did uint64, endpoint string, conndID uint64) error {
	key := fmt.Sprintf(gatewayRouterKey, did)
	value := fmt.Sprintf("%s-%d", endpoint, conndID)
	return cache.SetString(ctx, key, value, ttl7D*time.Second)
}
func DelRecord(ctx context.Context, did uint64) error {
	key := fmt.Sprintf(gatewayRouterKey, did)
	return cache.Del(ctx, key)
}

// DelRecordIfMatch 只删除仍指向给定连接的路由。重连时新连接可能已经覆盖同一did的路由，
// 此时旧连接的延迟清理不得把新路由一并删除。
func DelRecordIfMatch(ctx context.Context, did uint64, endpoint string, connID uint64) (bool, error) {
	key := fmt.Sprintf(gatewayRouterKey, did)
	expected := fmt.Sprintf("%s-%d", endpoint, connID)
	return cache.DelIfValueMatches(ctx, key, expected)
}
func QueryRecord(ctx context.Context, did uint64) (*Record, error) {
	key := fmt.Sprintf(gatewayRouterKey, did)
	data, err := cache.GetString(ctx, key)
	if err != nil {
		return nil, err
	}
	if data == "" {
		// 路由不存在，说明这个did当前不在线(或从未登录过)，这是正常情况而非错误
		return nil, nil
	}
	// 用最后一个"-"分割，避免endpoint本身含有"-"时(比如某些容器命名规则)错误切分
	idx := strings.LastIndex(data, "-")
	if idx < 0 {
		return nil, fmt.Errorf("router: malformed record value %q", data)
	}
	conndID, err := strconv.ParseUint(data[idx+1:], 10, 64)
	if err != nil {
		return nil, err
	}
	return &Record{
		Endpoint: data[:idx],
		ConndID:  conndID,
	}, nil
}
