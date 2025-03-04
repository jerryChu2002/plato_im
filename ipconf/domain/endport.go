package domain

import (
	"sync/atomic"
	"unsafe"
)

type Endport struct {
	IP          string       `json:"ip"`
	Port        string       `json:"port"`
	ActiveSorce float64      `json:"-"`
	StaticSorce float64      `json:"-"`
	Stats       *Stat        `json:"-"`
	window      *stateWindow `json:"-"`
}

func NewEndport(ip, port string) *Endport {
	ed := &Endport{
		IP:   ip,
		Port: port,
	}
	ed.window = newStateWindow()
	ed.Stats = ed.window.getStat()
	go func() {
		for stat := range ed.window.statChan {
			//计算窗口内的连接数的和 和 传输数据的字节数的和 设置到sumStat
			ed.window.appendStat(stat)
			//求窗口内的连接数的和 和 传输数据的字节数的和 的平均值，返回stat出来
			newStat := ed.window.getStat()
			atomic.SwapPointer((*unsafe.Pointer)((unsafe.Pointer)(ed.Stats)), unsafe.Pointer(newStat))
		}
	}()
	return ed
}

func (ed *Endport) UpdateStat(s *Stat) {
	ed.window.statChan <- s
}

func (ed *Endport) CalculateScore(ctx *IpConfContext) {
	// 如果 stats 字段是空的，则直接使用上一次计算的结果，此次不更新
	if ed.Stats != nil {
		ed.ActiveSorce = ed.Stats.CalculateActiveSorce()
		ed.StaticSorce = ed.Stats.CalculateStaticSorce()
	}
}
