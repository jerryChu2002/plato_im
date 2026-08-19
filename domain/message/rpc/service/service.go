package service

import (
	"context"
	"fmt"

	"github.com/hardcore-os/plato/common/cache"
	base "github.com/hardcore-os/plato/common/idl/base"
	messagedto "github.com/hardcore-os/plato/common/idl/domain/message"
	"github.com/hardcore-os/plato/domain/message/storage"
	"google.golang.org/protobuf/proto"
)

const maxPullLimit = 200

const defaultPullLimit = 50

type Service struct {
	sm *storage.StorageManager
}

func NewService(sm *storage.StorageManager) *Service {
	return &Service{sm: sm}
}

// mustEmbedUnimplementedMessageServer 满足protoc-gen-go-grpc生成的MessageServer接口
// 要求的前向兼容标记方法。
func (s *Service) mustEmbedUnimplementedMessageServer() {}

// GetMaxSeq 查询某个会话当前的最大seq。这个seq是state在produce到Kafka前用
// Redis INCR分配的(见common/cache.MsgSeqKey)，这里直接读同一个key，不需要state额外同步。
// 当req.Did非0时，一并算出未读数：未读数是派生计算(maxSeq-readSeq)而不是单独维护的计数器，
// 这样"新消息到达"和"用户已读清零"两个并发写各自只更新自己的游标，互不覆盖、没有竞态。
func (s *Service) GetMaxSeq(ctx context.Context, req *GetMaxSeqRequest) (*GetMaxSeqResponse, error) {
	key := fmt.Sprintf(cache.MsgSeqKey, req.GetConversationID())
	seq, err := cache.GetInt64(ctx, key)
	if err != nil {
		return &GetMaxSeqResponse{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	maxSeq := uint64(seq)
	resp := &GetMaxSeqResponse{Base: &base.BaseResp{Code: 0}, MaxSeq: maxSeq}
	if req.GetDid() != 0 {
		readSeq, err := cache.HGetUInt64(ctx, fmt.Sprintf(cache.ReadSeqKey, req.GetDid()), req.GetConversationID())
		if err != nil {
			return &GetMaxSeqResponse{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
		}
		resp.ReadSeq = readSeq
		if maxSeq > readSeq {
			resp.UnreadCount = maxSeq - readSeq
		}
	}
	return resp, nil
}

// PullOfflineMsgs 拉取某会话在指定seq之后的消息。请求落在最近100条缓存窗口内时，
// Redis一次返回窗口内全部增量；更早的离线消息从MySQL按limit分页返回。
func (s *Service) PullOfflineMsgs(ctx context.Context, req *PullOfflineMsgsRequest) (*PullOfflineMsgsResponse, error) {
	payloads, hit, ready, cacheErr := cache.GetRecentMessagesAfter(ctx, req.GetConversationID(), req.GetFromSeq())
	if cacheErr == nil && hit {
		msgs := make([]*messagedto.ChatMsgDTO, 0, len(payloads))
		for _, payload := range payloads {
			dto := &messagedto.ChatMsgDTO{}
			if err := proto.Unmarshal(payload, dto); err != nil {
				cacheErr = err
				break
			}
			msgs = append(msgs, dto)
		}
		if cacheErr == nil {
			return &PullOfflineMsgsResponse{Base: &base.BaseResp{Code: 0}, Msgs: msgs, HasMore: false}, nil
		}
		ready = false
	}

	limit := int(req.GetLimit())
	if limit <= 0 || limit > maxPullLimit {
		limit = defaultPullLimit
	}
	daos, err := s.sm.PullMsgs(ctx, req.GetConversationID(), req.GetFromSeq(), limit+1)
	if err != nil {
		return &PullOfflineMsgsResponse{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	hasMore := false
	if len(daos) > limit {
		hasMore = true
		daos = daos[:limit]
	}
	msgs := make([]*messagedto.ChatMsgDTO, 0, len(daos))
	for _, d := range daos {
		msgs = append(msgs, messageDAOToDTO(d))
	}
	if !ready {
		_ = s.warmRecentMessages(ctx, req.GetConversationID())
	}
	return &PullOfflineMsgsResponse{Base: &base.BaseResp{Code: 0}, Msgs: msgs, HasMore: hasMore}, nil
}

func (s *Service) warmRecentMessages(ctx context.Context, conversationID string) error {
	daos, err := s.sm.PullLatestMsgs(ctx, conversationID, cache.RecentMsgLimit+1)
	if err != nil {
		return err
	}
	floorSeq := uint64(0)
	if len(daos) > cache.RecentMsgLimit {
		floorSeq = daos[0].Seq
		daos = daos[1:]
	}
	msgs := make([]cache.ScoredMessage, 0, len(daos))
	for _, dao := range daos {
		payload, err := proto.Marshal(messageDAOToDTO(dao))
		if err != nil {
			return err
		}
		msgs = append(msgs, cache.ScoredMessage{Seq: dao.Seq, Payload: payload})
	}
	return cache.WarmRecentMessages(ctx, conversationID, floorSeq, msgs)
}

func messageDAOToDTO(d *storage.MessageDAO) *messagedto.ChatMsgDTO {
	return &messagedto.ChatMsgDTO{
		ConversationID: d.ConversationID,
		FromDid:        d.FromDid,
		ToDid:          d.ToDid,
		FromUserID:     d.FromUserID,
		ToUserID:       d.ToUserID,
		GroupID:        d.GroupID,
		BizType:        messagedto.BizMsgType(d.BizType),
		Content:        d.Content,
		Seq:            d.Seq,
		ClientMsgID:    d.ClientMsgID,
		CreateTime:     d.CreateTimeMS,
	}
}

func (s *Service) ListConversations(ctx context.Context, req *ListConversationsRequest) (*ListConversationsResponse, error) {
	lastMsgs, err := s.sm.ListUserConversationLastMsgs(ctx, req.GetUserID())
	if err != nil {
		return &ListConversationsResponse{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	conversations := make([]*messagedto.ConversationBrief, 0, len(lastMsgs))
	readKey := fmt.Sprintf(cache.ReadSeqKey, req.GetDid())
	for _, msg := range lastMsgs {
		readSeq, err := cache.HGetUInt64(ctx, readKey, msg.ConversationID)
		if err != nil {
			return &ListConversationsResponse{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
		}
		brief := &messagedto.ConversationBrief{
			ConversationID: msg.ConversationID,
			MaxSeq:         msg.Seq,
			ReadSeq:        readSeq,
			LastMsg: &messagedto.ChatMsgDTO{
				ConversationID: msg.ConversationID,
				FromDid:        msg.FromDid,
				ToDid:          msg.ToDid,
				GroupID:        msg.GroupID,
				BizType:        messagedto.BizMsgType(msg.BizType),
				Content:        msg.Content,
				Seq:            msg.Seq,
				ClientMsgID:    msg.ClientMsgID,
				CreateTime:     msg.CreateTimeMS,
				FromUserID:     msg.FromUserID,
				ToUserID:       msg.ToUserID,
			},
		}
		if msg.Seq > readSeq {
			brief.UnreadCount = msg.Seq - readSeq
		}
		conversations = append(conversations, brief)
	}
	return &ListConversationsResponse{Base: &base.BaseResp{Code: 0}, Conversations: conversations}, nil
}
