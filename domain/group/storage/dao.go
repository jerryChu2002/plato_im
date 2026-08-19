package storage

import (
	"time"

	"gorm.io/gorm"
)

// GroupInfoDAO 群信息表，群ID直接复用gorm.Model自增主键(ID)对外暴露为GroupID，
// 不需要额外的分布式ID生成器。
type GroupInfoDAO struct {
	ID          uint           `gorm:"primarykey"` // 群记录主键，同时作为群ID
	CreatedAt   time.Time      // 群创建时间
	UpdatedAt   time.Time      // 群信息最后更新时间
	DeletedAt   gorm.DeletedAt `gorm:"index"`                // 群软删除时间，未删除时为空
	Name        string         `gorm:"column:name"`          // 群名称
	OwnerDid    uint64         `gorm:"column:owner_did"`     // 旧版群主设备did，仅用于兼容历史数据
	OwnerUserID uint64         `gorm:"column:owner_user_id"` // 群主账号userID
}

func (GroupInfoDAO) TableName() string {
	return "group_info"
}

// GroupMemberDAO 群成员表
type GroupMemberDAO struct {
	ID           uint           `gorm:"primarykey"` // 群成员记录主键
	CreatedAt    time.Time      // 加入群聊时间
	UpdatedAt    time.Time      // 群成员信息最后更新时间
	DeletedAt    gorm.DeletedAt `gorm:"index"`                                                    // 群成员软删除时间，未删除时为空
	GroupID      uint64         `gorm:"column:group_id;uniqueIndex:uk_group_member,priority:1"`   // 所属群ID
	MemberDid    uint64         `gorm:"column:member_did;uniqueIndex:uk_group_member,priority:2"` // 旧版成员设备did，新记录写入userID以兼容旧唯一索引
	MemberUserID uint64         `gorm:"column:member_user_id;index:idx_member_user"`              // 成员账号userID
	Role         int32          `gorm:"column:role"`                                              // 成员角色：0=普通成员，1=群主
}

func (GroupMemberDAO) TableName() string {
	return "group_member"
}
