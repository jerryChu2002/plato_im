package gateway

import "sync"

var tables table

// table 维护 connID 与 did 的双向映射。
//
// 用途：gateway 只有 connID(没有 did)时需要按 did 做一致性哈希分片路由(见 resolveRouteDid)，
// 登录/重连成功后由 sendMsgByCmd 从下行 ACK 里回填该映射。
//
// 已知取舍：断连时不主动清理该映射。原因是重连场景下，客户端会用"旧连接"的 connID
// (装在 ReConnMsgHead 里)、通过"新连接"发送 ReConn 消息，而旧连接的 EOF 检测与新连接的
// 到达之间存在竞态——如果断连时立即清理，新连接上到达的 ReConn 消息可能查不到旧 connID
// 对应的 did，导致路由到错误的分片。保留映射是更安全的选择，代价是长期运行下 map 会随
// 历史连接数增长，这是当前教学/demo 规模下的简化取舍；生产环境需要补充按最大合理重连
// 窗口(比如几分钟)的 TTL 淘汰。
type table struct {
	did2conn sync.Map // did -> connID，辅助索引(比如未来"踢掉某did的连接"场景)
	conn2did sync.Map // connID -> did，路由分片的核心依赖
}

func InitTables() {
	tables = table{
		did2conn: sync.Map{},
		conn2did: sync.Map{},
	}
}

// bindConnDid 登录/重连ACK成功后回填connID与did的双向映射
func (t *table) bindConnDid(connID, did uint64) {
	t.conn2did.Store(connID, did)
	t.did2conn.Store(did, connID)
}

func (t *table) getDidByConn(connID uint64) (uint64, bool) {
	v, ok := t.conn2did.Load(connID)
	if !ok {
		return 0, false
	}
	return v.(uint64), true
}

func (t *table) getConnByDid(did uint64) (uint64, bool) {
	v, ok := t.did2conn.Load(did)
	if !ok {
		return 0, false
	}
	return v.(uint64), true
}
