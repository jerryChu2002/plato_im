package gateway

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

var node *ConnIDGenerater

// connID 位布局：1 bit版本 + 41 bit毫秒时间戳 + 10 bit网关节点ID + 12 bit毫秒内序号。
// 节点ID由gateway启动时通过etcd租约抢占，保证多个在线gateway使用不同节点位。
const (
	versionBits  = uint8(1)
	timeBits     = uint8(41)
	nodeBits     = uint8(10)
	sequenceBits = uint8(12)

	maxNodeID    = uint64(1<<nodeBits) - 1
	maxSequence  = int64(1<<sequenceBits) - 1
	maxTimestamp = int64(1<<timeBits) - 1

	nodeLeft    = sequenceBits
	timeLeft    = nodeBits + sequenceBits
	versionLeft = uint8(64) - versionBits
	// 2020-05-20 08:00:00 +0800 CST
	twepoch = int64(1589923200000) // 常量时间戳(毫秒)
	version = uint64(1)
)

type ConnIDGenerater struct {
	mu        sync.Mutex
	nodeID    uint64
	lastStamp int64
	sequence  int64
	now       func() int64
}

type connection struct {
	id        uint64 // 进程级别的生命周期
	fd        int
	e         *epoller
	conn      *net.TCPConn
	closeOnce sync.Once // 防止EOF处理和state主动踢连接同时重复关闭同一个socket
}

func (c *ConnIDGenerater) getMilliSeconds() int64 {
	return c.now()
}

func NewConnIDGenerater(nodeID uint64) (*ConnIDGenerater, error) {
	if nodeID > maxNodeID {
		return nil, fmt.Errorf("gateway node ID %d exceeds max %d", nodeID, maxNodeID)
	}
	return &ConnIDGenerater{
		nodeID: nodeID,
		now: func() int64 {
			return time.Now().UnixNano() / 1e6
		},
	}, nil
}

func NewConnection(conn *net.TCPConn) *connection {
	var id uint64
	var err error
	if id, err = node.NextID(); err != nil {
		panic(err) // 在线服务需要解决这个问题 ，报错而不能panic
	}
	return &connection{
		id:   id,
		fd:   socketFD(conn),
		conn: conn,
	}
}
func (c *connection) Close() {
	c.closeOnce.Do(func() {
		if ep != nil {
			ep.tables.Delete(c.id)
		}
		if c.e != nil {
			c.e.fdToConnTable.Delete(c.fd)
		}
		// 连接关闭是幂等清理路径，socket已经被对端关闭时的错误不应杀掉gateway进程。
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
}

func (c *connection) RemoteAddr() string {
	return c.conn.RemoteAddr().String()
}

func (c *connection) BindEpoller(e *epoller) {
	c.e = e
}

// 这里的锁会自旋，不会多么影响性能，主要是临界区小
func (w *ConnIDGenerater) NextID() (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.nextID()
}

func (w *ConnIDGenerater) nextID() (uint64, error) {
	timeStamp := w.getMilliSeconds()
	if timeStamp < twepoch {
		return 0, errors.New("current time is before connID epoch")
	}
	if timeStamp < w.lastStamp {
		return 0, errors.New("time is moving backwards, waiting until")
	}

	if w.lastStamp == timeStamp {
		w.sequence = (w.sequence + 1) & maxSequence
		if w.sequence == 0 {
			for timeStamp <= w.lastStamp {
				timeStamp = w.getMilliSeconds()
			}
		}
	} else {
		w.sequence = 0
	}
	w.lastStamp = timeStamp
	timestamp := timeStamp - twepoch
	if timestamp > maxTimestamp {
		return 0, errors.New("connID timestamp bits exhausted")
	}
	connID := (version << versionLeft) |
		(uint64(timestamp) << timeLeft) |
		(w.nodeID << nodeLeft) |
		uint64(w.sequence)
	return connID, nil
}
