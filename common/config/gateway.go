package config

import (
	"os"
	"sync"

	"github.com/hardcore-os/plato/common/utils"
	"github.com/spf13/viper"
)

func GetGatewayMaxTcpNum() int32 {
	return viper.GetInt32("gateway.tcp_max_num")
}

func GetGatewayEpollerChanNum() int {
	return viper.GetInt("gateway.epoll_channel_size")
}
func GetGatewayEpollerNum() int {
	return viper.GetInt("gateway.epoll_num")
}
func GetGatewayEpollWaitQueueSize() int {
	return viper.GetInt("gateway.epoll_wait_queue_size")
}
func GetGatewayTCPServerPort() int {
	return viper.GetInt("gateway.tcp_server_port")
}
func GetGatewayRPCServerPort() int {
	return viper.GetInt("gateway.rpc_server_port")
}
func GetGatewayWorkerPoolNum() int {
	return viper.GetInt("gateway.worker_pool_num")
}
func GetGatewayCmdChannelNum() int {
	return viper.GetInt("gateway.cmd_channel_num")
}

// GetGatewayServiceAddr 返回配置文件里写死的gateway地址/主机名，给"客户端要拨号连去哪个
// gateway"这类场景用(比如client/cui.go里客户端dial的目标)。跟下面GetGatewaySelfAdvertiseAddr
// 是两个完全不同的语义——一个是"我(gateway自己)是谁"，一个是"外部调用方该去哪找一个gateway"，
// 不能合并，否则运行在gateway进程里的这份配置值一旦切成自动探测，客户端dial逻辑也会被带偏。
func GetGatewayServiceAddr() string {
	return viper.GetString("gateway.service_addr")
}

var (
	gatewaySelfAddrOnce sync.Once
	gatewaySelfAddr     string
)

// GetGatewaySelfAdvertiseAddr 返回本gateway实例对外的可达地址，供etcd注册(discovery)和
// gRPC监听绑定共用，也是state/domain服务反查"这条连接归属哪个gateway"时存进路由表的值——
// 三处必须完全一致，所以这里做进程内memoize，只解析一次。只应该在gateway进程自己内部调用
// (gateway/server.go 的 prpc.NewPServer 和 gateway/rpc/client/state.go 上报自身身份时)，
// 不要跟GetGatewayServiceAddr混用。
// 解析优先级：
//  1. 环境变量 GATEWAY_ADVERTISE_IP 显式指定(留给自动探测选错网卡时的兜底手段)
//  2. 自动探测本机对外可达的网卡IP(见utils.DetectOutboundIP)——这样不管是Docker Compose
//     scale出的无差别副本还是K8s Pod，每个实例天然都有自己独一无二的标识，不需要像"每个实例
//     单独维护一份配置文件"这样手工介入
//  3. 配置文件里的 gateway.service_addr(自动探测失败时的兜底，比如沙箱环境没有默认路由)
func GetGatewaySelfAdvertiseAddr() string {
	gatewaySelfAddrOnce.Do(func() {
		if envIP := os.Getenv("GATEWAY_ADVERTISE_IP"); envIP != "" {
			gatewaySelfAddr = envIP
			return
		}
		if ip, err := utils.DetectOutboundIP(); err == nil {
			gatewaySelfAddr = ip
			return
		}
		gatewaySelfAddr = viper.GetString("gateway.service_addr")
	})
	return gatewaySelfAddr
}

func GetGatewayServiceName() string {
	return viper.GetString("gateway.service_name")
}

func GetGatewayRPCWeight() int {
	return viper.GetInt("gateway.weight")
}

func GetGatewayStateServerEndPoint() string {
	return viper.GetString("gateway.state_server_endpoint")
}
