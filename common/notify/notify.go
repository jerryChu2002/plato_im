// Package notify 提供"若目标did当前在线，直接把一条系统通知推给它"的公共能力。
// 供好友申请/同意/拒绝、群邀请等低频、非关键路径的通知场景使用：不经过Kafka，
// 也不纳入state的ACK重推可靠性状态机(这类通知丢了不会造成业务错误，
// 因为它们的最终状态总能通过对应的查询接口——比如好友申请表、群成员表——重新读到)。
package notify

import (
	"context"
	"time"

	"github.com/hardcore-os/plato/common/gwclient"
	"github.com/hardcore-os/plato/common/pushenc"
	"github.com/hardcore-os/plato/common/router"
)

const timeout = 3 * time.Second

// PushSystemMsg 若toDid当前在线，把已经序列化好的业务内容(通常是messagedto.ChatMsgDTO)
// 直接推给它所在的gateway连接；对方离线时安静跳过，调用方应保证相应状态可以通过
// 其它查询接口在对方下次上线时被重新看到(比如ListPendingRequests)。
func PushSystemMsg(ctx context.Context, toDid uint64, dtoBytes []byte) {
	rec, err := router.QueryRecord(ctx, toDid)
	if err != nil || rec == nil {
		return
	}
	payload, err := pushenc.WrapPush(dtoBytes, 0)
	if err != nil {
		return
	}
	rpcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_ = gwclient.PushToGateway(rpcCtx, rec.Endpoint, rec.ConndID, payload)
}
