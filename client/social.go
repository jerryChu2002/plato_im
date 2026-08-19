// client/social.go 是socialsvc的薄适配层：签名保持跟重构前完全一致，内部转调
// common/socialsvc，这样client/cui.go不需要任何改动。业务实现见common/socialsvc/social.go——
// 之所以搬出去而不是直接在这里导出函数名，是因为guiclient包也需要复用同一份好友/群调用
// 逻辑，而syncAllConversations原先隐式依赖这个包里的全局chat/myDid，其它包拿不到，
// 所以socialsvc把这两个依赖显式变成了函数参数。
package client

import (
	frienddto "github.com/hardcore-os/plato/common/idl/domain/friend"
	groupdto "github.com/hardcore-os/plato/common/idl/domain/group"
	"github.com/hardcore-os/plato/common/socialsvc"
)

func initSocialClients() {
	if err := socialsvc.InitClients(); err != nil {
		panic(err)
	}
}

func addFriend(myDid, targetDid uint64, msg string) (uint64, error) {
	return socialsvc.AddFriend(myDid, targetDid, msg)
}

func handleFriendRequest(requestID, myDid uint64, agree bool) error {
	return socialsvc.HandleFriendRequest(requestID, myDid, agree)
}

func listFriends(myDid uint64) ([]*frienddto.FriendDTO, error) {
	return socialsvc.ListFriends(myDid)
}

func deleteFriend(myDid, friendDid uint64) error {
	return socialsvc.DeleteFriend(myDid, friendDid)
}

func listPendingRequests(myDid uint64) ([]*frienddto.FriendRequestDTO, error) {
	return socialsvc.ListPendingRequests(myDid)
}

func listSentRequests(myDid uint64) ([]*frienddto.FriendRequestDTO, error) {
	return socialsvc.ListSentRequests(myDid)
}

func createGroup(ownerUserID uint64, name string) (uint64, error) {
	return socialsvc.CreateGroup(ownerUserID, name)
}

func inviteToGroup(groupID, operatorUserID, memberUserID uint64) error {
	return socialsvc.InviteToGroup(groupID, operatorUserID, memberUserID)
}

func listMyGroups(userID uint64) ([]*groupdto.GroupInfoDTO, error) {
	return socialsvc.ListMyGroups(userID)
}

func listGroupMembers(groupID uint64) ([]*groupdto.GroupMemberDTO, error) {
	return socialsvc.ListGroupMembers(groupID)
}

// syncAllConversations 是Chat.OnReady的实现：登录/重连成功后，把好友列表和我加入的群
// 各自换算成conversationID，对每一个都调一次SyncConversation，补拉离线期间错过的消息。
// 这里做闭包捕获cui.go里的包级全局chat/myDid，转调socialsvc.SyncAllConversations的
// 显式参数版本，cui.go调用侧完全不用改。
func syncAllConversations() {
	socialsvc.SyncAllConversations(chat, myUserID)
}
