package group

import (
	"context"

	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/prpc"
	"github.com/hardcore-os/plato/domain/group/rpc/service"
	"github.com/hardcore-os/plato/domain/group/storage"
	"google.golang.org/grpc"
)

// RunMain 启动群聊领域服务
func RunMain(path string) {
	ctx := context.TODO()
	config.Init(path)
	sm := storage.NewStorageManager(false)
	svc := service.NewService(sm)

	s := prpc.NewPServer(
		prpc.WithServiceName(config.GetDomainGroupServerName()),
		prpc.WithIP(config.GetDomainGroupServerAddr()),
		prpc.WithPort(config.GetDomainGroupServerPoint()),
		prpc.WithWeight(config.GetDomainGroupRPCWeight()))
	s.RegisterService(func(server *grpc.Server) {
		service.RegisterGroupServer(server, svc)
	})
	s.Start(ctx)
}
