package service

import (
	"context"

	base "github.com/hardcore-os/plato/common/idl/base"
	groupdto "github.com/hardcore-os/plato/common/idl/domain/group"
	"github.com/hardcore-os/plato/domain/group/storage"
)

type Service struct {
	sm *storage.StorageManager
}

func NewService(sm *storage.StorageManager) *Service {
	return &Service{sm: sm}
}

// mustEmbedUnimplementedGroupServer 满足protoc-gen-go-grpc生成的GroupServer接口
// 要求的前向兼容标记方法。
func (s *Service) mustEmbedUnimplementedGroupServer() {}

func (s *Service) CreateGroup(ctx context.Context, req *CreateGroupReq) (*CreateGroupResp, error) {
	g, err := s.sm.CreateGroup(ctx, req.GetOwnerUserID(), req.GetName())
	if err != nil {
		return &CreateGroupResp{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	return &CreateGroupResp{Base: &base.BaseResp{Code: 0}, GroupID: uint64(g.ID)}, nil
}

func (s *Service) InviteMembers(ctx context.Context, req *InviteMembersReq) (*InviteMembersResp, error) {
	if err := s.sm.InviteMembers(ctx, req.GetGroupID(), req.GetOperatorUserID(), req.GetMemberUserIDs()); err != nil {
		return &InviteMembersResp{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	return &InviteMembersResp{Base: &base.BaseResp{Code: 0}}, nil
}

func (s *Service) LeaveGroup(ctx context.Context, req *LeaveGroupReq) (*LeaveGroupResp, error) {
	if err := s.sm.LeaveGroup(ctx, req.GetGroupID(), req.GetUserID()); err != nil {
		return &LeaveGroupResp{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	return &LeaveGroupResp{Base: &base.BaseResp{Code: 0}}, nil
}

func (s *Service) ListMembers(ctx context.Context, req *ListMembersReq) (*ListMembersResp, error) {
	rows, err := s.sm.ListMembers(ctx, req.GetGroupID())
	if err != nil {
		return &ListMembersResp{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	members := make([]*groupdto.GroupMemberDTO, 0, len(rows))
	for _, r := range rows {
		members = append(members, &groupdto.GroupMemberDTO{
			GroupID:      r.GroupID,
			MemberDid:    r.MemberDid,
			Role:         r.Role,
			JoinTime:     r.CreatedAt.UnixMilli(),
			MemberUserID: r.MemberUserID,
		})
	}
	return &ListMembersResp{Base: &base.BaseResp{Code: 0}, Members: members}, nil
}

func (s *Service) ListMyGroups(ctx context.Context, req *ListMyGroupsReq) (*ListMyGroupsResp, error) {
	rows, err := s.sm.ListMyGroups(ctx, req.GetUserID())
	if err != nil {
		return &ListMyGroupsResp{Base: &base.BaseResp{Code: 1, Msg: err.Error()}}, nil
	}
	groups := make([]*groupdto.GroupInfoDTO, 0, len(rows))
	for _, g := range rows {
		count, _ := s.sm.MemberCount(ctx, uint64(g.ID))
		groups = append(groups, &groupdto.GroupInfoDTO{
			GroupID:     uint64(g.ID),
			Name:        g.Name,
			OwnerDid:    g.OwnerDid,
			CreateTime:  g.CreatedAt.UnixMilli(),
			MemberCount: int32(count),
			OwnerUserID: g.OwnerUserID,
		})
	}
	return &ListMyGroupsResp{Base: &base.BaseResp{Code: 0}, Groups: groups}, nil
}
