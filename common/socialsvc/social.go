// Package socialsvc 是好友/群管理业务调用层的可复用实现，从client/social.go搬迁而来
// 并导出，供不同UI形态(TUI的client包、Fyne的guiclient包)共用同一份业务代码，避免各自
// UI包重复实现一遍相同的rpc调用逻辑。
package socialsvc

import (
	"context"
	"errors"
	"time"

	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/convid"
	frienddto "github.com/hardcore-os/plato/common/idl/domain/friend"
	groupdto "github.com/hardcore-os/plato/common/idl/domain/group"
	"github.com/hardcore-os/plato/common/prpc"
	"github.com/hardcore-os/plato/common/sdk"
	friendservice "github.com/hardcore-os/plato/domain/friend/rpc/service"
	groupservice "github.com/hardcore-os/plato/domain/group/rpc/service"
)

const rpcTimeout = 3 * time.Second

var (
	friendClient friendservice.FriendClient
	groupClient  groupservice.GroupClient
)

// InitClients 初始化到domain/friend、domain/group的rpc client。
// 好友/群管理是无状态的CRUD操作，走common/prpc标准的服务发现+负载均衡(NewPClient)，
// 这与common/gwclient"精确路由到某一台持有连接状态的实例"是不同的调用场景——
// 任何一台健康的friend/group服务实例都能处理这类请求。
func InitClients() error {
	fCli, err := prpc.NewPClient(config.GetDomainFriendServerName())
	if err != nil {
		return err
	}
	friendClient = friendservice.NewFriendClient(fCli.Conn())

	gCli, err := prpc.NewPClient(config.GetDomainGroupServerName())
	if err != nil {
		return err
	}
	groupClient = groupservice.NewGroupClient(gCli.Conn())
	return nil
}

func AddFriend(myDid, targetDid uint64, msg string) (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	resp, err := friendClient.SendFriendRequest(ctx, &friendservice.SendFriendRequestReq{
		FromDid: myDid, ToDid: targetDid, Message: msg,
	})
	if err != nil {
		return 0, err
	}
	if resp.GetBase().GetCode() != 0 {
		return 0, errors.New(resp.GetBase().GetMsg())
	}
	return resp.GetRequestID(), nil
}

func HandleFriendRequest(requestID, myDid uint64, agree bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	resp, err := friendClient.HandleFriendRequest(ctx, &friendservice.HandleFriendRequestReq{
		RequestID: requestID, OperatorDid: myDid, Agree: agree,
	})
	if err != nil {
		return err
	}
	if resp.GetBase().GetCode() != 0 {
		return errors.New(resp.GetBase().GetMsg())
	}
	return nil
}

func ListFriends(myDid uint64) ([]*frienddto.FriendDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	resp, err := friendClient.ListFriends(ctx, &friendservice.ListFriendsReq{Did: myDid})
	if err != nil {
		return nil, err
	}
	if resp.GetBase().GetCode() != 0 {
		return nil, errors.New(resp.GetBase().GetMsg())
	}
	return resp.GetFriends(), nil
}

func DeleteFriend(myDid, friendDid uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	resp, err := friendClient.DeleteFriend(ctx, &friendservice.DeleteFriendReq{Did: myDid, FriendDid: friendDid})
	if err != nil {
		return err
	}
	if resp.GetBase().GetCode() != 0 {
		return errors.New(resp.GetBase().GetMsg())
	}
	return nil
}

func ListPendingRequests(myDid uint64) ([]*frienddto.FriendRequestDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	resp, err := friendClient.ListPendingRequests(ctx, &friendservice.ListPendingRequestsReq{Did: myDid})
	if err != nil {
		return nil, err
	}
	if resp.GetBase().GetCode() != 0 {
		return nil, errors.New(resp.GetBase().GetMsg())
	}
	return resp.GetRequests(), nil
}

func ListSentRequests(myDid uint64) ([]*frienddto.FriendRequestDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	resp, err := friendClient.ListSentRequests(ctx, &friendservice.ListSentRequestsReq{Did: myDid})
	if err != nil {
		return nil, err
	}
	if resp.GetBase().GetCode() != 0 {
		return nil, errors.New(resp.GetBase().GetMsg())
	}
	return resp.GetRequests(), nil
}

func CreateGroup(ownerUserID uint64, name string) (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	resp, err := groupClient.CreateGroup(ctx, &groupservice.CreateGroupReq{OwnerUserID: ownerUserID, Name: name})
	if err != nil {
		return 0, err
	}
	if resp.GetBase().GetCode() != 0 {
		return 0, errors.New(resp.GetBase().GetMsg())
	}
	return resp.GetGroupID(), nil
}

func InviteToGroup(groupID, operatorUserID, memberUserID uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	resp, err := groupClient.InviteMembers(ctx, &groupservice.InviteMembersReq{
		GroupID: groupID, OperatorUserID: operatorUserID, MemberUserIDs: []uint64{memberUserID},
	})
	if err != nil {
		return err
	}
	if resp.GetBase().GetCode() != 0 {
		return errors.New(resp.GetBase().GetMsg())
	}
	return nil
}

func ListMyGroups(userID uint64) ([]*groupdto.GroupInfoDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	resp, err := groupClient.ListMyGroups(ctx, &groupservice.ListMyGroupsReq{UserID: userID})
	if err != nil {
		return nil, err
	}
	if resp.GetBase().GetCode() != 0 {
		return nil, errors.New(resp.GetBase().GetMsg())
	}
	return resp.GetGroups(), nil
}

func ListGroupMembers(groupID uint64) ([]*groupdto.GroupMemberDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	resp, err := groupClient.ListMembers(ctx, &groupservice.ListMembersReq{GroupID: groupID})
	if err != nil {
		return nil, err
	}
	if resp.GetBase().GetCode() != 0 {
		return nil, errors.New(resp.GetBase().GetMsg())
	}
	return resp.GetMembers(), nil
}

// SyncAllConversations 是Chat.OnReady的典型实现：登录/重连成功后，把好友列表和我加入的群
// 各自换算成conversationID，对每一个都调一次SyncConversation——这样断线期间/离线期间
// 错过的消息会在重新连上的第一时间被补拉回来，而不用等用户手动打开那个会话才发现。
// chat/userID原先是client包里的隐式全局变量，这里改成显式参数，供多个UI包各自传入自己的实例。
func SyncAllConversations(chat *sdk.Chat, userID uint64) {
	chat.SyncUserConversations()
	groups, err := ListMyGroups(userID)
	if err == nil {
		for _, g := range groups {
			chat.SyncConversation(convid.Group(g.GetGroupID()))
		}
	}
}
