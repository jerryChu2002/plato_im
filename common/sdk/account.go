package sdk

import (
	"context"
	"errors"
	"time"

	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/prpc"
	userservice "github.com/hardcore-os/plato/domain/user/rpc/service"
)

const accountRPCTimeout = 5 * time.Second

type LoginIdentity struct {
	UserID       uint64
	SessionToken string
}

func newUserClient() (userservice.UserClient, error) {
	pCli, err := prpc.NewPClient(config.GetDomainUserServerName())
	if err != nil {
		return nil, err
	}
	return userservice.NewUserClient(pCli.Conn()), nil
}

func LoginAccount(account, password string, did uint64) (*LoginIdentity, error) {
	client, err := newUserClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), accountRPCTimeout)
	defer cancel()
	resp, err := client.Login(ctx, &userservice.LoginRequest{Account: account, Password: password, DeviceID: did})
	if err != nil {
		return nil, err
	}
	if resp.GetBase().GetCode() != 0 {
		return nil, errors.New(resp.GetBase().GetMsg())
	}
	return &LoginIdentity{UserID: resp.GetUserID(), SessionToken: resp.GetSessionToken()}, nil
}

func RegisterAccount(account, password, nickname string, did uint64) (*LoginIdentity, error) {
	client, err := newUserClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), accountRPCTimeout)
	defer cancel()
	resp, err := client.Register(ctx, &userservice.RegisterRequest{
		Account: account, Password: password, Nickname: nickname, DeviceID: did,
	})
	if err != nil {
		return nil, err
	}
	if resp.GetBase().GetCode() != 0 {
		return nil, errors.New(resp.GetBase().GetMsg())
	}
	return &LoginIdentity{UserID: resp.GetUserID(), SessionToken: resp.GetSessionToken()}, nil
}
