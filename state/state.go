package state

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hardcore-os/plato/common/cache"
	"github.com/hardcore-os/plato/common/router"
	"github.com/hardcore-os/plato/common/timingwheel"
	"github.com/hardcore-os/plato/state/rpc/client"
)

type connState struct {
	sync.RWMutex
	// 连接登录的时候会初始化，5秒每次
	heartTimer *timingwheel.Timer
	// 10秒
	reConnTimer *timingwheel.Timer
	// 100ms每次，用于当ack丢失时，会进行消息重发
	msgTimer *timingwheel.Timer
	// sessionID_msgID 的结构
	msgTimerLock string
	connID       uint64
	did          uint64
}

func (c *connState) close(ctx context.Context) error {
	c.Lock()
	defer c.Unlock()
	if c.heartTimer != nil {
		c.heartTimer.Stop()
	}
	if c.reConnTimer != nil {
		c.reConnTimer.Stop()
	}
	if c.msgTimer != nil {
		c.msgTimer.Stop()
	}
	// TODO 这里如何保证事务性，值得思考一下，或者说有没有必要保证
	// TODO 这里也可以使用lua或者pipeline 来尽可能合并两次redis的操作 通常在大规模的应用中这是有效的
	// TODO 这里是要好好思考一下，网络调用次数的时间&空间复杂度的
	slotKey := cs.getLoginSlotKey(c.connID)
	meta := cs.loginSlotMarshal(c.did, c.connID)
	//删除登陆槽位
	err := cache.SREM(ctx, slotKey, meta)
	if err != nil {
		return err
	}

	//获取connID的槽位
	slot := cs.getConnStateSlot(c.connID)

	key := fmt.Sprintf(cache.MaxClientIDKey, slot, c.connID, "*")
	keys, err := cache.GetKeys(ctx, key)
	if err != nil {
		return err
	}

	// 删除匹配的键
	if len(keys) > 0 {
		err = cache.Del(ctx, keys...)
		if err != nil {
			return err
		}
	}

	//根据did删除路由
	err = router.DelRecord(ctx, c.did)
	if err != nil {
		return err
	}

	//下行消息的lastMsg
	lastMsg := fmt.Sprintf(cache.LastMsgKey, slot, c.connID)
	err = cache.Del(ctx, lastMsg)
	if err != nil {
		return err
	}

	//通知gateway server关闭长连接socket
	err = client.DelConn(&ctx, c.connID, nil)
	if err != nil {
		return err
	}

	//删除本地的状态
	cs.deleteConnIDState(ctx, c.connID)
	return nil
}

func (c *connState) appendMsg(ctx context.Context, lastMsgKey, msgTimerLock string, msgData []byte) {
	c.Lock()
	defer c.Unlock()
	c.msgTimerLock = msgTimerLock
	if c.msgTimer != nil {
		c.msgTimer.Stop()
		c.msgTimer = nil
	}
	// 创建定时器
	t := AfterFunc(100*time.Millisecond, func() {
		rePush(c.connID)
	})
	c.msgTimer = t
	err := cache.SetBytes(ctx, lastMsgKey, msgData, cache.TTL7D)
	if err != nil {
		panic(lastMsgKey)
	}
}

func (c *connState) reSetMsgTimer(connID, sessionID, msgID uint64) {
	c.Lock()
	defer c.Unlock()
	if c.msgTimer != nil {
		c.msgTimer.Stop()
	}
	c.msgTimerLock = fmt.Sprintf("%d_%d", sessionID, msgID)
	c.msgTimer = AfterFunc(100*time.Millisecond, func() {
		rePush(connID)
	})
}

// 用来重启时恢复
func (c *connState) loadMsgTimer(ctx context.Context) {
	// 创建定时器
	data, err := cs.getLastMsg(ctx, c.connID)
	if err != nil {
		// 这里的处理是粗暴的，如果线上是需要更sloid的方案
		panic(err)
	}
	if data == nil {
		return
	}
	c.reSetMsgTimer(c.connID, data.SessionID, data.MsgID)
}

// 当新连接login的时候以及心跳处理时，会重置connState中的心跳定时器以及重连定时器
func (c *connState) reSetHeartTimer() {
	c.Lock()
	defer c.Unlock()
	if c.heartTimer != nil {
		c.heartTimer.Stop()
	}
	c.heartTimer = AfterFunc(5*time.Second, func() {
		c.reSetReConnTimer()
	})
}

func (c *connState) reSetReConnTimer() {
	c.Lock()
	defer c.Unlock()

	if c.reConnTimer != nil {
		c.reConnTimer.Stop()
	}

	// 初始化重连定时器
	c.reConnTimer = AfterFunc(10*time.Second, func() {
		ctx := context.TODO()
		// 整体connID状态登出
		cs.connLogOut(ctx, c.connID)
	})
}

func (c *connState) ackLastMsg(ctx context.Context, sessionID, msgID uint64) bool {
	c.Lock()
	defer c.Unlock()
	msgTimerLock := fmt.Sprintf("%d_%d", sessionID, msgID)
	if c.msgTimerLock != msgTimerLock {
		return false
	}
	slot := cs.getConnStateSlot(c.connID)
	key := fmt.Sprintf(cache.LastMsgKey, slot, c.connID)
	if err := cache.Del(ctx, key); err != nil {
		return false
	}
	if c.msgTimer != nil {
		c.msgTimer.Stop()
	}
	return true
}
