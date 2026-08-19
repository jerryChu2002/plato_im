package sdk

import "sync"

// recentSet 固定容量的"最近见过"集合，用于按ClientMsgID给下行推送做去重——
// Kafka是at-least-once语义，consumer重启/rebalance等场景下同一条消息可能被推送
// 不止一次，服务端(domain/message)已经在落库和推送两处都做了去重，这里是客户端侧
// 的最后一道兜底防线，不依赖服务端每一环都完美去重。
type recentSet struct {
	mu       sync.Mutex
	seen     map[string]struct{}
	order    []string
	capacity int
}

func newRecentSet(capacity int) *recentSet {
	return &recentSet{seen: make(map[string]struct{}), capacity: capacity}
}

// checkAndAdd 返回true表示第一次见到(应该处理)，false表示已经见过(应该丢弃)。
// id为空字符串时(比如旧协议兼容路径生成不出ClientMsgID)不做去重，直接放行。
func (r *recentSet) checkAndAdd(id string) bool {
	if id == "" {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.seen[id]; ok {
		return false
	}
	r.seen[id] = struct{}{}
	r.order = append(r.order, id)
	if len(r.order) > r.capacity {
		oldest := r.order[0]
		r.order = r.order[1:]
		delete(r.seen, oldest)
	}
	return true
}
