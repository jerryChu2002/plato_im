package sdk

import (
	"context"
	"time"

	messagedto "github.com/hardcore-os/plato/common/idl/domain/message"
	"github.com/hardcore-os/plato/common/idl/message"
	messageservice "github.com/hardcore-os/plato/domain/message/rpc/service"
	"google.golang.org/protobuf/proto"
)

const (
	pullTimeout = 5 * time.Second
	pullLimit   = 50
)

// SyncConversation 把本地已知的进度和服务端对比，落后就把差的部分补拉回来，
// 返回服务端当前的maxSeq(拉取到底没有更多消息时，调用方常常还要用这个值)。
// 补拉的起点取"本地内存进度(localSeq)"和"服务端记录的已读进度(readSeq)"中较大的一个：
//   - localSeq 覆盖同一进程内已经通过实时推送/补拉见过的消息，避免重复拉取；
//   - readSeq 是服务端持久化的"用户上次读到哪"(见ReadReceipt/MarkRead)，覆盖localSeq
//     从0起步的场景(比如换设备登录、或者进程重启丢了内存态)，这样不会把用户已经读过的
//     历史消息全部当成"未读"重新推一遍。
//
// 典型调用时机：登录/重连成功后(通过Chat.OnReady回调)对每个会话调一次，
// 或者客户端打开某个会话时调用(见OpenConversation)。
func (chat *Chat) SyncConversation(conversationID string) uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	defer cancel()
	resp, err := chat.msgClient.GetMaxSeq(ctx, &messageservice.GetMaxSeqRequest{
		ConversationID: conversationID,
		Did:            chat.Did,
	})
	if err != nil || resp.GetBase().GetCode() != 0 {
		return 0
	}
	serverMax := resp.GetMaxSeq()

	chat.seqMu.Lock()
	local := chat.localSeq[conversationID]
	chat.seqMu.Unlock()
	if resp.GetReadSeq() > local {
		local = resp.GetReadSeq()
	}

	if serverMax > local {
		chat.pullRange(conversationID, local, serverMax)
	}
	return serverMax
}

// SyncUserConversations 按账号列出全部已存在会话，并补拉当前did尚未收到的消息。
func (chat *Chat) SyncUserConversations() []*messagedto.ConversationBrief {
	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	defer cancel()
	resp, err := chat.msgClient.ListConversations(ctx, &messageservice.ListConversationsRequest{
		UserID: chat.UserID,
		Did:    chat.Did,
	})
	if err != nil || resp.GetBase().GetCode() != 0 {
		return nil
	}
	for _, conversation := range resp.GetConversations() {
		chat.SyncConversation(conversation.GetConversationID())
	}
	return resp.GetConversations()
}

// OpenConversation 是"用户点开某个会话"这个交互动作的完整实现：先补拉该会话里
// 已读进度之后的所有消息(SyncConversation)，再把"读到哪了"更新成服务端当时的
// maxSeq上报回去——即"打开会话=看到了截至目前为止的全部消息"。
// 这里没有单独处理"用户只看了一部分就退出会话"的中间状态：demo场景下点开即视为读完，
// 更细粒度的"已读到具体第几条"需要客户端UI层在滚动/关闭时另外调用MarkRead。
func (chat *Chat) OpenConversation(conversationID string) {
	if serverMax := chat.SyncConversation(conversationID); serverMax > 0 {
		chat.MarkRead(conversationID, serverMax)
	}
}

// UnreadCount 查询某个会话当前的未读数：maxSeq-readSeq，由domain/message派生计算，
// 客户端不用自己维护计数。典型用在会话列表展示上(而不是打开会话之后，那时候应该是0)。
func (chat *Chat) UnreadCount(conversationID string) uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	defer cancel()
	resp, err := chat.msgClient.GetMaxSeq(ctx, &messageservice.GetMaxSeqRequest{
		ConversationID: conversationID,
		Did:            chat.Did,
	})
	if err != nil || resp.GetBase().GetCode() != 0 {
		return 0
	}
	return resp.GetUnreadCount()
}

// MarkRead 上报"该会话已读到seq"的已读回执。即发即弃：state直接写Redis，不走Kafka、
// 不等ACK，网络抖动丢了也无妨——下次打开会话会重新上报一次最新进度。
func (chat *Chat) MarkRead(conversationID string, seq uint64) {
	readMsg := &message.ReadReceiptMsg{ConversationID: conversationID, ReadSeq: seq}
	payload, err := proto.Marshal(readMsg)
	if err != nil {
		return
	}
	chat.conn.send(message.CmdType_ReadReceipt, payload)
}

// pullRange 从fromSeq(不含)拉到toSeq(含)，按页拉取直到追上toSeq或者服务端说没有更多了。
func (chat *Chat) pullRange(conversationID string, fromSeq, toSeq uint64) {
	cursor := fromSeq
	for {
		ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
		resp, err := chat.msgClient.PullOfflineMsgs(ctx, &messageservice.PullOfflineMsgsRequest{
			ConversationID: conversationID,
			FromSeq:        cursor,
			Limit:          pullLimit,
		})
		cancel()
		if err != nil || resp.GetBase().GetCode() != 0 {
			return
		}
		for _, dto := range resp.GetMsgs() {
			chat.feedOfflineMsg(dto)
			if dto.GetSeq() > cursor {
				cursor = dto.GetSeq()
			}
		}
		chat.seqMu.Lock()
		if cursor > chat.localSeq[conversationID] {
			chat.localSeq[conversationID] = cursor
		}
		chat.seqMu.Unlock()
		if !resp.GetHasMore() || cursor >= toSeq {
			return
		}
	}
}

// feedOfflineMsg 把补拉到的一条消息转成展示用Message，喂进recvChan——走的是和实时
// 推送完全一样的去重转换逻辑(handleChatDTO)，只是跳过缺口检测，因为这本身就是在补缺口，
// 再触发一次检测没有意义。
func (chat *Chat) feedOfflineMsg(dto *messagedto.ChatMsgDTO) {
	if dto.BizType != messagedto.BizMsgType_ChatSingle && dto.BizType != messagedto.BizMsgType_ChatGroup {
		return
	}
	msg := chat.handleChatDTO(dto, true)
	if msg == nil {
		return
	}
	select {
	case chat.conn.recvChan <- msg:
	case <-chat.closeChan:
	}
}
