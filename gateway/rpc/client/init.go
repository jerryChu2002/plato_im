package client

import (
	"time"

	"github.com/hardcore-os/plato/common/gwclient"
)

func Init() {
	initStateClient()
	// 定期清理长期空闲的到state实例的连接，避免连接池随endpoint churn无限增长
	gwclient.StartIdleGC(5*time.Minute, 30*time.Minute)
}
