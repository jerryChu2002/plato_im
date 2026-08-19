package storage

import (
	"context"
	"errors"

	"github.com/hardcore-os/plato/common/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type StorageManager struct {
	db *gorm.DB
}

func NewStorageManager(isTest bool) *StorageManager {
	sm := &StorageManager{}
	var err error
	if isTest {
		sm.db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	} else {
		sm.db, err = gorm.Open(mysql.Open(config.GetDomainGroupDBDNS()), &gorm.Config{})
	}
	if err != nil {
		panic(err)
	}
	if err := sm.db.AutoMigrate(&GroupInfoDAO{}, &GroupMemberDAO{}); err != nil {
		panic(err)
	}
	return sm
}

// CreateGroup 建群，并在同一个事务里把群主加入成员表。
func (s *StorageManager) CreateGroup(ctx context.Context, ownerUserID uint64, name string) (*GroupInfoDAO, error) {
	if ownerUserID == 0 {
		return nil, errors.New("ownerUserID不能为0")
	}
	group := &GroupInfoDAO{Name: name, OwnerDid: ownerUserID, OwnerUserID: ownerUserID}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(group).Error; err != nil {
			return err
		}
		member := &GroupMemberDAO{GroupID: uint64(group.ID), MemberDid: ownerUserID, MemberUserID: ownerUserID, Role: 1}
		return tx.Create(member).Error
	})
	if err != nil {
		return nil, err
	}
	return group, nil
}

func (s *StorageManager) IsMember(ctx context.Context, groupID, userID uint64) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&GroupMemberDAO{}).
		Where("group_id = ? AND member_user_id = ?", groupID, userID).Count(&count).Error
	return count > 0, err
}

// InviteMembers 邀请一批用户入群，operatorUserID必须已经是群成员。
func (s *StorageManager) InviteMembers(ctx context.Context, groupID, operatorUserID uint64, memberUserIDs []uint64) error {
	ok, err := s.IsMember(ctx, groupID, operatorUserID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("只有群成员才能邀请他人入群")
	}
	if len(memberUserIDs) == 0 {
		return nil
	}
	members := make([]GroupMemberDAO, 0, len(memberUserIDs))
	for _, userID := range memberUserIDs {
		if userID == 0 {
			return errors.New("memberUserID不能为0")
		}
		members = append(members, GroupMemberDAO{
			GroupID: groupID, MemberDid: userID, MemberUserID: userID, Role: 0,
		})
	}
	// 已在群里的成员被重复邀请时直接忽略，不报错
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&members).Error
}

func (s *StorageManager) LeaveGroup(ctx context.Context, groupID, userID uint64) error {
	return s.db.WithContext(ctx).
		Where("group_id = ? AND member_user_id = ?", groupID, userID).
		Delete(&GroupMemberDAO{}).Error
}

func (s *StorageManager) ListMembers(ctx context.Context, groupID uint64) ([]*GroupMemberDAO, error) {
	var rows []*GroupMemberDAO
	err := s.db.WithContext(ctx).Where("group_id = ?", groupID).Find(&rows).Error
	return rows, err
}

func (s *StorageManager) ListMyGroups(ctx context.Context, userID uint64) ([]*GroupInfoDAO, error) {
	var memberRows []*GroupMemberDAO
	if err := s.db.WithContext(ctx).Where("member_user_id = ?", userID).Find(&memberRows).Error; err != nil {
		return nil, err
	}
	if len(memberRows) == 0 {
		return nil, nil
	}
	groupIDs := make([]uint64, 0, len(memberRows))
	for _, m := range memberRows {
		groupIDs = append(groupIDs, m.GroupID)
	}
	var groups []*GroupInfoDAO
	err := s.db.WithContext(ctx).Where("id IN (?)", groupIDs).Find(&groups).Error
	return groups, err
}

func (s *StorageManager) MemberCount(ctx context.Context, groupID uint64) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&GroupMemberDAO{}).Where("group_id = ?", groupID).Count(&count).Error
	return count, err
}
