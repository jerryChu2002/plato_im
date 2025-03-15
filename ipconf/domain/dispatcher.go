package domain

import (
	"sort"
	"sync"

	"github.com/hardcore-os/plato/ipconf/source"
)

type Dispatcher struct {
	candidateTable map[string]*Endport
	sync.RWMutex
}

var dp *Dispatcher

func Init() {
	dp = &Dispatcher{}
	dp.candidateTable = make(map[string]*Endport)
	go func() {
		//上面source层初始化就在监听etcd前缀key事件，有事件就会进入到这里
		for event := range source.EventChan() {
			switch event.Type {
			case source.AddNodeEvent:
				dp.addNode(event)
			case source.DelNodeEvent:
				dp.delNode(event)
			}
		}
	}()
}
func Dispatch(ctx *IpConfContext) []*Endport {
	// Step1: 获得候选endport
	eds := dp.getCandidateEndport(ctx)
	// Step2: 逐一计算得分
	for _, ed := range eds {
		//把对应的分数设置上
		ed.CalculateScore(ctx)
	}
	// Step3: 全局排序，动静结合的排序策略。
	sort.Slice(eds, func(i, j int) bool {
		// 优先基于活跃分数进行排序
		if eds[i].ActiveSorce > eds[j].ActiveSorce {
			return true
		}
		// 如果活跃分数相同，则使用静态分数排序
		if eds[i].ActiveSorce == eds[j].ActiveSorce {
			if eds[i].StaticSorce > eds[j].StaticSorce {
				return true
			}
			return false
		}
		return false
	})
	return eds
}

func (dp *Dispatcher) getCandidateEndport(ctx *IpConfContext) []*Endport {
	dp.RLock()
	defer dp.RUnlock()
	candidateList := make([]*Endport, 0, len(dp.candidateTable))
	for _, ed := range dp.candidateTable {
		candidateList = append(candidateList, ed)
	}
	return candidateList
}
func (dp *Dispatcher) delNode(event *source.Event) {
	dp.Lock()
	defer dp.Unlock()
	delete(dp.candidateTable, event.Key())
}
func (dp *Dispatcher) addNode(event *source.Event) {
	dp.Lock()
	defer dp.Unlock()
	var (
		ed *Endport
		ok bool
	)
	if ed, ok = dp.candidateTable[event.Key()]; !ok { // 不存在
		//说明只是etcd中有了，candidateTable还没有，因此要设置
		//NewEndport里面有个携程维护window
		ed = NewEndport(event.IP, event.Port)
		dp.candidateTable[event.Key()] = ed
	}
	//把stat发送到ed.window.statChan去进行处理
	ed.UpdateStat(&Stat{
		ConnectNum:   event.ConnectNum,
		MessageBytes: event.MessageBytes,
	})

}
