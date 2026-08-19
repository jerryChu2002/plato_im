package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/hardcore-os/plato/common/config"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	gatewayNodeKeyPrefix = "/plato/gateway/conn-id-nodes/"
	gatewayNodeLeaseTTL  = int64(15)
)

type gatewayNodeLease struct {
	nodeID    uint64
	client    *clientv3.Client
	leaseID   clientv3.LeaseID
	cancel    context.CancelFunc
	lost      chan error
	closeOnce sync.Once
}

// acquireGatewayNodeLease 通过etcd事务抢占一个节点槽。key绑定租约，gateway异常退出后
// 会在TTL到期时自动释放；正常退出也不主动Revoke，避免节点ID在同一毫秒内被新进程复用。
func acquireGatewayNodeLease(parent context.Context, identity string) (*gatewayNodeLease, error) {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   config.GetEndpointsForDiscovery(),
		DialTimeout: config.GetTimeoutForDiscovery(),
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(parent)
	lease := &gatewayNodeLease{
		client: client,
		cancel: cancel,
		lost:   make(chan error, 1),
	}
	grantCtx, grantCancel := context.WithTimeout(ctx, config.GetTimeoutForDiscovery())
	grant, err := client.Grant(grantCtx, gatewayNodeLeaseTTL)
	grantCancel()
	if err != nil {
		lease.Close()
		return nil, fmt.Errorf("grant gateway node lease: %w", err)
	}
	lease.leaseID = grant.ID

	for nodeID := uint64(0); nodeID <= maxNodeID; nodeID++ {
		key := fmt.Sprintf("%s%d", gatewayNodeKeyPrefix, nodeID)
		txnCtx, txnCancel := context.WithTimeout(ctx, config.GetTimeoutForDiscovery())
		resp, txnErr := client.Txn(txnCtx).
			If(clientv3.Compare(clientv3.CreateRevision(key), "=", 0)).
			Then(clientv3.OpPut(key, identity, clientv3.WithLease(grant.ID))).
			Commit()
		txnCancel()
		if txnErr != nil {
			lease.Close()
			return nil, fmt.Errorf("claim gateway node ID %d: %w", nodeID, txnErr)
		}
		if resp.Succeeded {
			lease.nodeID = nodeID
			keepAliveCh, keepAliveErr := client.KeepAlive(ctx, grant.ID)
			if keepAliveErr != nil {
				lease.Close()
				return nil, fmt.Errorf("keep gateway node ID %d alive: %w", nodeID, keepAliveErr)
			}
			go lease.watchKeepAlive(ctx, keepAliveCh)
			return lease, nil
		}
	}

	lease.Close()
	return nil, errors.New("all gateway node IDs are occupied")
}

func (l *gatewayNodeLease) watchKeepAlive(ctx context.Context, ch <-chan *clientv3.LeaseKeepAliveResponse) {
	for {
		select {
		case resp, ok := <-ch:
			if !ok || resp == nil {
				select {
				case l.lost <- errors.New("gateway node lease keepalive stopped"):
				default:
				}
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (l *gatewayNodeLease) Close() {
	l.closeOnce.Do(func() {
		l.cancel()
		_ = l.client.Close()
	})
}

func (l *gatewayNodeLease) Lost() <-chan error {
	return l.lost
}

func gatewayNodeIdentity() string {
	return fmt.Sprintf("%s:%d", config.GetGatewaySelfAdvertiseAddr(), config.GetGatewayRPCServerPort())
}
