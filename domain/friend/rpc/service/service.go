package service

import (
	"context"
	"time"

	base "github.com/hardcore-os/plato/common/idl/base"
	frienddto "github.com/hardcore-os/plato/common/idl/domain/friend"
	messagedto "github.com/hardcore-os/plato/common/idl/domain/message"
	"github.com/hardcore-os/plato/common/notify"
	"github.com/hardcore-os/plato/domain/friend/storage"
	"google.golang.org/protobuf/proto"
)

type Service struct {
	sm *storage.StorageManager
}

func NewService(sm *storage.StorageManager) *Service {
	return &Service{sm: sm}
}

// mustEmbedUnimplementedFriendServer 满足protoc-gen-go-grpc生成的FriendServer接口
// 要求的前向兼容标记方法。
func (s *Service) mustEmbedUnimplementedFriendServer() {}

// SendFriendRequest 发起一条好友申请；若对方当前在线，顺带推一条实时通知。
func (s *Service) SendFriendRequest(ctx context.Context, req *SendFriendRequestReq) (*SendFriendRequestResp, error) {
	r, err := s.sm.CreateRequest(ctx, req.GetFromDid(), req.GetToDid(), req.GetMessage())
	if err != nil {
		return &SendFriendRequestResp{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	notify.PushSystemMsg(ctx, req.GetToDid(), buildNotifyPayload(messagedto.BizMsgType_FriendRequest, r))
	return &SendFriendRequestResp{Base: &base.BaseResp{Code: 0}, RequestID: uint64(r.ID)}, nil
}

// HandleFriendRequest 被申请方同意/拒绝一条好友申请；处理成功后通知申请方处理结果。
func (s *Service) HandleFriendRequest(ctx context.Context, req *HandleFriendRequestReq) (*HandleFriendRequestResp, error) {
	r, err := s.sm.HandleRequest(ctx, req.GetRequestID(), req.GetOperatorDid(), req.GetAgree())
	if err != nil {
		return &HandleFriendRequestResp{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	bizType := messagedto.BizMsgType_FriendReject
	if req.GetAgree() {
		bizType = messagedto.BizMsgType_FriendAgree
	}
	notify.PushSystemMsg(ctx, r.FromDid, buildNotifyPayload(bizType, r))
	return &HandleFriendRequestResp{Base: &base.BaseResp{Code: 0}}, nil
}

func (s *Service) ListFriends(ctx context.Context, req *ListFriendsReq) (*ListFriendsResp, error) {
	rows, err := s.sm.ListFriends(ctx, req.GetDid())
	if err != nil {
		return &ListFriendsResp{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	friends := make([]*frienddto.FriendDTO, 0, len(rows))
	for _, row := range rows {
		friends = append(friends, &frienddto.FriendDTO{
			Did:        row.FriendDid,
			Remark:     row.Remark,
			CreateTime: row.CreatedAt.UnixMilli(),
		})
	}
	return &ListFriendsResp{Base: &base.BaseResp{Code: 0}, Friends: friends}, nil
}

func (s *Service) DeleteFriend(ctx context.Context, req *DeleteFriendReq) (*DeleteFriendResp, error) {
	if err := s.sm.DeleteFriend(ctx, req.GetDid(), req.GetFriendDid()); err != nil {
		return &DeleteFriendResp{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	return &DeleteFriendResp{Base: &base.BaseResp{Code: 0}}, nil
}

func (s *Service) ListPendingRequests(ctx context.Context, req *ListPendingRequestsReq) (*ListPendingRequestsResp, error) {
	rows, err := s.sm.ListPendingRequests(ctx, req.GetDid())
	if err != nil {
		return &ListPendingRequestsResp{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	return &ListPendingRequestsResp{Base: &base.BaseResp{Code: 0}, Requests: toRequestDTOs(rows)}, nil
}

func (s *Service) ListSentRequests(ctx context.Context, req *ListSentRequestsReq) (*ListSentRequestsResp, error) {
	rows, err := s.sm.ListSentRequests(ctx, req.GetDid())
	if err != nil {
		return &ListSentRequestsResp{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	return &ListSentRequestsResp{Base: &base.BaseResp{Code: 0}, Requests: toRequestDTOs(rows)}, nil
}

func toRequestDTOs(rows []*storage.FriendRequestDAO) []*frienddto.FriendRequestDTO {
	dtos := make([]*frienddto.FriendRequestDTO, 0, len(rows))
	for _, r := range rows {
		dtos = append(dtos, &frienddto.FriendRequestDTO{
			ID:         uint64(r.ID),
			FromDid:    r.FromDid,
			ToDid:      r.ToDid,
			Message:    r.Message,
			Status:     frienddto.FriendRequestStatus(r.Status),
			CreateTime: r.CreatedAt.UnixMilli(),
		})
	}
	return dtos
}

// buildNotifyPayload 把好友申请/处理结果包装成messagedto.ChatMsgDTO序列化字节，
// 客户端收到Push后按BizType分支处理(FriendRequest/FriendAgree/FriendReject)，
// Content里是frienddto.FriendRequestDTO的序列化数据，供客户端展示申请详情。
func buildNotifyPayload(bizType messagedto.BizMsgType, r *storage.FriendRequestDAO) []byte {
	reqDTO := &frienddto.FriendRequestDTO{
		ID:         uint64(r.ID),
		FromDid:    r.FromDid,
		ToDid:      r.ToDid,
		Message:    r.Message,
		Status:     frienddto.FriendRequestStatus(r.Status),
		CreateTime: r.CreatedAt.UnixMilli(),
	}
	content, _ := proto.Marshal(reqDTO)
	dto := &messagedto.ChatMsgDTO{
		FromDid:    r.FromDid,
		ToDid:      r.ToDid,
		BizType:    bizType,
		Content:    content,
		CreateTime: time.Now().UnixMilli(),
	}
	data, _ := proto.Marshal(dto)
	return data
}
