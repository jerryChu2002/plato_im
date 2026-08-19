package client

import (
	"time"

	"github.com/hardcore-os/plato/common/gwclient"
)

// Init 保留作为state服务rpc客户端侧的统一初始化入口。
// 推送/踢连接不再在启动时预先拨号一个写死的gateway地址，而是按需通过
// common/gwclient连接池，精确路由到调用方(state业务代码)已经查好的目标endpoint。
func Init() {
	// 定期清理长期空闲的到gateway实例的连接，避免连接池随endpoint churn无限增长
	gwclient.StartIdleGC(5*time.Minute, 30*time.Minute)
}
