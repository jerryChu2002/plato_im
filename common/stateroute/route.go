// Package stateroute 提供"按did算出负责该did的state分片实例"的能力，
// 基于etcd服务发现构建一致性哈希环并随实例上下线动态刷新。
//
// gateway(转发客户端上行消息)、domain/message的Kafka consumer(推送聊天消息)、
// domain/friend(推送好友通知)、domain/group(推送群消息)等所有需要"精确找到某个did
// 当前由哪个state实例托管"的调用方都复用这一份实现，避免每个服务各自维护一份
// 服务发现+一致性哈希的重复逻辑。
package stateroute

import (
	"context"
	"fmt"
	"sync"

	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/prpc/discov"
	"github.com/hardcore-os/plato/common/prpc/discov/plugin"
	"github.com/hardcore-os/plato/common/shard"
)

const virtualNodes = 150

var (
	ring       = shard.NewRing(virtualNodes)
	discovInst discov.Discovery
	initOnce   sync.Once
)

// Init 初始化到etcd的服务发现连接，并注册watch回调动态刷新一致性哈希环。
// 多次调用只会真正初始化一次，可以在多个包各自的初始化路径里放心调用。
func Init() {
	initOnce.Do(func() {
		dis, err := plugin.GetDiscovInstance()
		if err != nil {
			panic(err)
		}
		discovInst = dis
		refresh()
		discovInst.AddListener(context.Background(), refresh)
	})
}

func refresh() {
	svc := discovInst.GetService(context.Background(), config.GetStateServiceName())
	if svc == nil {
		return
	}
	nodes := make([]string, 0, len(svc.Endpoints))
	for _, ep := range svc.Endpoints {
		if ep.Enable {
			nodes = append(nodes, fmt.Sprintf("%s:%d", ep.IP, ep.Port))
		}
	}
	ring.SetNodes(nodes)
}

// Resolve 按did一致性哈希算出该did当前应该由哪个state实例处理。
// 环为空(比如还没有任何state实例注册)时ok返回false。
func Resolve(did uint64) (endpoint string, ok bool) {
	return ring.Get(fmt.Sprintf("%d", did))
}

// ResolveByKey 支持传入任意字符串key做路由(用于did==0这类边界场景的降级兜底，
// 比如按connID兜底路由)。
func ResolveByKey(key string) (endpoint string, ok bool) {
	return ring.Get(key)
}
