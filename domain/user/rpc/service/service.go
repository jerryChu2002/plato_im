package service

import (
	context "context"
	"errors"
	"strings"
	"time"

	"github.com/hardcore-os/plato/common/auth"
	base "github.com/hardcore-os/plato/common/idl/base"
	"github.com/hardcore-os/plato/domain/user/storage"
)

var sm *storage.StorageManager

func Init(isTest bool) {
	sm = storage.NewStorageManager(isTest)
}

func (s *Service) Register(ctx context.Context, req *RegisterRequest) (*RegisterResponse, error) {
	userID, err := sm.Register(ctx, req.GetAccount(), req.GetPassword(), req.GetNickname(), req.GetDeviceID())
	if err != nil {
		code := uint32(1)
		if errors.Is(err, storage.ErrAccountExists) {
			code = 2
		}
		return &RegisterResponse{Base: &base.BaseResp{Code: code, Msg: err.Error()}}, nil
	}
	return &RegisterResponse{
		Base:         &base.BaseResp{Code: 0, Msg: "ok"},
		UserID:       userID,
		SessionToken: auth.IssueSessionToken(userID, req.GetDeviceID(), time.Now()),
	}, nil
}

func (s *Service) Login(ctx context.Context, req *LoginRequest) (*LoginResponse, error) {
	userID, err := sm.Login(ctx, req.GetAccount(), req.GetPassword(), req.GetDeviceID())
	if err != nil {
		msg := "账号或密码错误"
		if errors.Is(err, storage.ErrDeviceBelongsToUser) {
			msg = "该did已绑定其他账号"
		}
		return &LoginResponse{Base: &base.BaseResp{Code: 1, Msg: msg}}, nil
	}
	return &LoginResponse{
		Base:         &base.BaseResp{Code: 0, Msg: "ok"},
		UserID:       userID,
		SessionToken: auth.IssueSessionToken(userID, req.GetDeviceID(), time.Now()),
	}, nil
}

func (s *Service) ListUserDevices(ctx context.Context, req *ListUserDevicesRequest) (*ListUserDevicesResponse, error) {
	deviceIDs, err := sm.ListUserDeviceIDs(ctx, req.GetUserID())
	if err != nil {
		return &ListUserDevicesResponse{Base: &base.BaseResp{Code: 1, Msg: strings.TrimSpace(err.Error())}}, nil
	}
	return &ListUserDevicesResponse{Base: &base.BaseResp{Code: 0, Msg: "ok"}, DeviceIDs: deviceIDs}, nil
}

type Service struct {
}

func (s *Service) QueryUsers(ctx context.Context, req *QueryUsersRequest) (*QueryUsersResponse, error) {
	userOpts := make(map[uint64]*storage.Options)
	for uid, opt := range req.Opts {
		userOpts[uid] = &storage.Options{AllDevice: opt.ActiveDevice}
	}
	users := sm.QueryUsers(ctx, userOpts)
	return &QueryUsersResponse{Users: users}, nil
}
func (s *Service) CreateUsers(ctx context.Context, req *CreateUsersRequest) (*CreateUsersResponse, error) {
	// TODO 后续会承接复杂的聚合业务逻辑
	err := sm.CreateUsers(ctx, req.GetUsers(), nil)
	if err != nil {
		return nil, err
	}
	return &CreateUsersResponse{Code: 0, Msg: "ok"}, nil
}
func (s *Service) UpdateUsers(ctx context.Context, req *UpdateUsersRequest) (*UpdateUsersResponse, error) {
	// TODO update的权限目前很大，后面需要裁剪其字端
	err := sm.UpdateUsers(ctx, req.GetUsers(), nil)
	if err != nil {
		return nil, err
	}
	return &UpdateUsersResponse{Msg: "ok"}, nil
}

func (s *Service) mustEmbedUnimplementedUserServer() {}
