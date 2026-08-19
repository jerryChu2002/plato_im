// Package shard 提供一致性哈希环，用于把某个业务 key(比如 did)稳定地映射到一组可动态增减的
// 服务实例 endpoint 上。相比简单取模，实例数量变化时只有环上相邻区间(约 1/N)的 key 会被重新
// 分配，其余 key 的映射保持不变，代价随实例数增长而线性下降，适合水平扩展场景下的分片路由。
package shard

import (
	"fmt"
	"sort"
	"sync"

	"github.com/hardcore-os/plato/common/utils"
)

const defaultVirtualNodes = 150

// Ring 一致性哈希环
type Ring struct {
	mu           sync.RWMutex
	virtualNodes int
	hashToNode   map[uint32]string
	sortedHashes []uint32
	nodes        map[string]struct{}
}

// NewRing 创建一致性哈希环，virtualNodes 是每个物理节点在环上注册的虚拟节点数，
// 数值越大分布越均匀，但内存占用和 Get 时的查找成本也越高，<=0 时使用默认值 150。
func NewRing(virtualNodes int) *Ring {
	if virtualNodes <= 0 {
		virtualNodes = defaultVirtualNodes
	}
	return &Ring{
		virtualNodes: virtualNodes,
		hashToNode:   make(map[uint32]string),
		nodes:        make(map[string]struct{}),
	}
}

// AddNode 往环上增加一个物理节点(比如 "ip:port")
func (r *Ring) AddNode(node string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addNodeLocked(node)
	r.rebuildSortedLocked()
}

// RemoveNode 从环上摘除一个物理节点
func (r *Ring) RemoveNode(node string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeNodeLocked(node)
	r.rebuildSortedLocked()
}

// SetNodes 用最新的完整节点列表刷新整个环(增量 diff，只增删变化的部分)，
// 供服务发现的全量快照/watch 回调直接调用。
func (r *Ring) SetNodes(nodes []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	newSet := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		newSet[n] = struct{}{}
		if _, ok := r.nodes[n]; !ok {
			r.addNodeLocked(n)
		}
	}
	for n := range r.nodes {
		if _, ok := newSet[n]; !ok {
			r.removeNodeLocked(n)
		}
	}
	r.rebuildSortedLocked()
}

func (r *Ring) addNodeLocked(node string) {
	if _, ok := r.nodes[node]; ok {
		return
	}
	r.nodes[node] = struct{}{}
	for i := 0; i < r.virtualNodes; i++ {
		h := utils.HashStr(fmt.Sprintf("%s#%d", node, i))
		r.hashToNode[h] = node
	}
}

func (r *Ring) removeNodeLocked(node string) {
	if _, ok := r.nodes[node]; !ok {
		return
	}
	delete(r.nodes, node)
	for i := 0; i < r.virtualNodes; i++ {
		h := utils.HashStr(fmt.Sprintf("%s#%d", node, i))
		delete(r.hashToNode, h)
	}
}

func (r *Ring) rebuildSortedLocked() {
	hashes := make([]uint32, 0, len(r.hashToNode))
	for h := range r.hashToNode {
		hashes = append(hashes, h)
	}
	sort.Slice(hashes, func(i, j int) bool { return hashes[i] < hashes[j] })
	r.sortedHashes = hashes
}

// Get 返回负责处理该 key 的节点 endpoint；环为空(还没有任何实例注册)时 ok 返回 false。
func (r *Ring) Get(key string) (endpoint string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.sortedHashes) == 0 {
		return "", false
	}
	h := utils.HashStr(key)
	idx := sort.Search(len(r.sortedHashes), func(i int) bool { return r.sortedHashes[i] >= h })
	if idx == len(r.sortedHashes) {
		idx = 0
	}
	return r.hashToNode[r.sortedHashes[idx]], true
}

// NodeCount 返回当前环上的物理节点数量
func (r *Ring) NodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}
