package storage

import "gorm.io/gorm"

// FriendRelationDAO 好友关系表，采用"两行对称存储":A加B时插入(owner=A,friend=B)和
// (owner=B,friend=A)两行。查"我的好友列表"直接命中(owner_did)索引，且天然支持
// "owner对friend的备注"这类单向属性——一行+方向字段的存法做不到。
type FriendRelationDAO struct {
	gorm.Model
	OwnerDid  uint64 `gorm:"column:owner_did;uniqueIndex:uk_owner_friend,priority:1"`  // 这条记录属于谁
	FriendDid uint64 `gorm:"column:friend_did;uniqueIndex:uk_owner_friend,priority:2"` // 好友是谁
	Remark    string `gorm:"column:remark"`                                            // owner对friend的备注(单向属性)
}

func (FriendRelationDAO) TableName() string {
	return "friend_relation"
}

// FriendRequestDAO 好友申请表
type FriendRequestDAO struct {
	gorm.Model
	FromDid uint64 `gorm:"column:from_did;index:idx_from"`    // 申请方did
	ToDid   uint64 `gorm:"column:to_did;index:idx_to_status"` // 被申请方did
	Message string `gorm:"column:message"`                    // 申请附言
	Status  int32  `gorm:"column:status;index:idx_to_status"` // 0=待处理 1=已同意 2=已拒绝
}

func (FriendRequestDAO) TableName() string {
	return "friend_request"
}
