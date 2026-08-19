// Package gwclient 提供"精确路由到某一台具体服务实例"的 grpc 连接池。
//
// 这与 common/prpc.PClient.dial() 的服务发现+负载均衡(discov:///服务名，任意一台健康实例都行)
// 是两种不同的调用语义：Push/DelConn/SendMsg/PushToConn 这类调用必须精确寻址到"持有目标连接"
// 的那一台实例，发给别的实例毫无意义，因此这里按 endpoint 惰性建连、复用连接，而不是走
// round-robin 负载均衡。state、domain/message 的 Kafka consumer、domain/friend、domain/group
// 等所有需要"跨实例精确推送"的调用方共用这一套连接池。
package gwclient

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	clientinterceptor "github.com/hardcore-os/plato/common/prpc/interceptor/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const dialTimeout = 5 * time.Second

type poolEntry struct {
	conn     *grpc.ClientConn
	lastUsed int64 // unix秒，atomic读写
}

var connPool sync.Map // map[string]*poolEntry，key为endpoint("ip:port")

// GetConn 按endpoint惰性建连、复用；同一个endpoint并发首次访问时只会真正建立一条连接。
func GetConn(endpoint string) (*grpc.ClientConn, error) {
	if v, ok := connPool.Load(endpoint); ok {
		e := v.(*poolEntry)
		atomic.StoreInt64(&e.lastUsed, time.Now().Unix())
		return e.conn, nil
	}
	conn, err := dial(endpoint)
	if err != nil {
		return nil, err
	}
	entry := &poolEntry{conn: conn, lastUsed: time.Now().Unix()}
	actual, loaded := connPool.LoadOrStore(endpoint, entry)
	if loaded {
		// 竞态：另一个goroutine抢先建好了同一个endpoint的连接，关掉自己这条多余的
		conn.Close()
		return actual.(*poolEntry).conn, nil
	}
	return conn, nil
}

func dial(endpoint string) (*grpc.ClientConn, error) {
	interceptors := []grpc.UnaryClientInterceptor{
		clientinterceptor.TraceUnaryClientInterceptor(),
		clientinterceptor.MetricUnaryClientInterceptor(),
	}
	options := []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(interceptors...),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	return grpc.DialContext(ctx, endpoint, options...)
}

// RemoveConn 主动淘汰一个endpoint对应的连接，供服务发现感知到该实例下线时调用。
func RemoveConn(endpoint string) {
	if v, ok := connPool.LoadAndDelete(endpoint); ok {
		v.(*poolEntry).conn.Close()
	}
}

// StartIdleGC 启动后台协程，定期关闭长期空闲(超过ttl未被GetConn访问)的连接，避免连接池无限增长。
func StartIdleGC(interval, ttl time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			now := time.Now().Unix()
			connPool.Range(func(key, value interface{}) bool {
				e := value.(*poolEntry)
				if now-atomic.LoadInt64(&e.lastUsed) > int64(ttl.Seconds()) {
					connPool.Delete(key)
					e.conn.Close()
				}
				return true
			})
		}
	}()
}
