package message

import (
	"context"

	"github.com/hardcore-os/plato/common/cache"
	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/prpc"
	"github.com/hardcore-os/plato/domain/message/consumer"
	"github.com/hardcore-os/plato/domain/message/rpc/service"
	"github.com/hardcore-os/plato/domain/message/storage"
	"google.golang.org/grpc"
)

// RunMain 启动消息领域服务：既是"读路径"的rpc server(GetMaxSeq/PullOfflineMsgs)，
// 也是"写路径"的Kafka consumer(落库+推送)——两者共享同一个StorageManager实例。
func RunMain(path string) {
	ctx := context.TODO()
	config.Init(path)
	cache.InitRedis(ctx)
	sm := storage.NewStorageManager(false)
	svc := service.NewService(sm)

	// 启动Kafka consumer：独立goroutine跑，不阻塞rpc server的启动
	go consumer.Start(sm)

	s := prpc.NewPServer(
		prpc.WithServiceName(config.GetDomainMessageServerName()),
		prpc.WithIP(config.GetDomainMessageServerAddr()),
		prpc.WithPort(config.GetDomainMessageServerPoint()),
		prpc.WithWeight(config.GetDomainMessageRPCWeight()))
	s.RegisterService(func(server *grpc.Server) {
		service.RegisterMessageServer(server, svc)
	})
	s.Start(ctx)
}
