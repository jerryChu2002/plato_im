package friend

import (
	"context"

	"github.com/hardcore-os/plato/common/cache"
	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/prpc"
	"github.com/hardcore-os/plato/common/stateroute"
	"github.com/hardcore-os/plato/domain/friend/rpc/service"
	"github.com/hardcore-os/plato/domain/friend/storage"
	"google.golang.org/grpc"
)

// RunMain 启动好友领域服务
func RunMain(path string) {
	ctx := context.TODO()
	config.Init(path)
	// router.QueryRecord(通知在线用户时用来查其所在gateway)依赖Redis
	cache.InitRedis(ctx)
	// 通知申请方/被申请方时需要按did查路由+找目标state分片，复用与gateway/domain-message
	// 相同的一致性哈希路由组件
	stateroute.Init()
	sm := storage.NewStorageManager(false)
	svc := service.NewService(sm)

	s := prpc.NewPServer(
		prpc.WithServiceName(config.GetDomainFriendServerName()),
		prpc.WithIP(config.GetDomainFriendServerAddr()),
		prpc.WithPort(config.GetDomainFriendServerPoint()),
		prpc.WithWeight(config.GetDomainFriendRPCWeight()))
	s.RegisterService(func(server *grpc.Server) {
		service.RegisterFriendServer(server, svc)
	})
	s.Start(ctx)
}
