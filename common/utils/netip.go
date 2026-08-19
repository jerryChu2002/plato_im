package utils

import "net"

// DetectOutboundIP 探测本机在容器/宿主机网络里对外可达的那张网卡的IP。
// 用UDP连接一个不需要真实可达的地址来触发内核路由决策，不会真的发出任何包，
// 是一个不依赖外部服务、不要求真实网络连通性的常见取本机出口IP技巧——
// 只要本机路由表里有默认路由（Docker容器天然有，指向网桥网关）就能拿到正确结果。
func DetectOutboundIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}
