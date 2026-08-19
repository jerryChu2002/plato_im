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
		sm.db, err = gorm.Open(mysql.Open(config.GetDomainFriendDBDNS()), &gorm.Config{})
	}
	if err != nil {
		panic(err)
	}
	if err := sm.db.AutoMigrate(&FriendRelationDAO{}, &FriendRequestDAO{}); err != nil {
		panic(err)
	}
	return sm
}

// CreateRequest 创建一条好友申请。若双方已经是好友，或已有一条未处理的同向申请，
// 直接复用/拒绝，不重复创建。
func (s *StorageManager) CreateRequest(ctx context.Context, fromDid, toDid uint64, msg string) (*FriendRequestDAO, error) {
	if fromDid == 0 || toDid == 0 {
		return nil, errors.New("did不能为0")
	}
	if fromDid == toDid {
		return nil, errors.New("不能添加自己为好友")
	}
	var existingRelation FriendRelationDAO
	if err := s.db.WithContext(ctx).Where("owner_did = ? AND friend_did = ?", fromDid, toDid).
		First(&existingRelation).Error; err == nil {
		return nil, errors.New("已经是好友了")
	}
	var existing FriendRequestDAO
	err := s.db.WithContext(ctx).
		Where("from_did = ? AND to_did = ? AND status = 0", fromDid, toDid).
		First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	req := &FriendRequestDAO{FromDid: fromDid, ToDid: toDid, Message: msg, Status: 0}
	if err := s.db.WithContext(ctx).Create(req).Error; err != nil {
		return nil, err
	}
	return req, nil
}

// HandleRequest 处理一条好友申请：agree=true时在同一个事务里更新申请状态、
// 插入两行对称的好友关系，保证两者的一致性。
func (s *StorageManager) HandleRequest(ctx context.Context, requestID, operatorDid uint64, agree bool) (*FriendRequestDAO, error) {
	var req FriendRequestDAO
	if err := s.db.WithContext(ctx).First(&req, requestID).Error; err != nil {
		return nil, err
	}
	if req.ToDid != operatorDid {
		return nil, errors.New("无权处理该好友申请")
	}
	if req.Status != 0 {
		return nil, errors.New("该好友申请已经被处理过了")
	}
	status := int32(2)
	if agree {
		status = 1
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&FriendRequestDAO{}).Where("id = ?", requestID).
			Update("status", status).Error; err != nil {
			return err
		}
		if agree {
			relations := []FriendRelationDAO{
				{OwnerDid: req.FromDid, FriendDid: req.ToDid},
				{OwnerDid: req.ToDid, FriendDid: req.FromDid},
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&relations).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	req.Status = status
	return &req, nil
}

func (s *StorageManager) ListFriends(ctx context.Context, did uint64) ([]*FriendRelationDAO, error) {
	var rows []*FriendRelationDAO
	err := s.db.WithContext(ctx).Where("owner_did = ?", did).Find(&rows).Error
	return rows, err
}

// DeleteFriend 删除好友关系：把owner<->friend这一对对称的两行都删掉，事务保证一致性。
func (s *StorageManager) DeleteFriend(ctx context.Context, did, friendDid uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("owner_did = ? AND friend_did = ?", did, friendDid).
			Delete(&FriendRelationDAO{}).Error; err != nil {
			return err
		}
		return tx.Where("owner_did = ? AND friend_did = ?", friendDid, did).
			Delete(&FriendRelationDAO{}).Error
	})
}

func (s *StorageManager) ListPendingRequests(ctx context.Context, did uint64) ([]*FriendRequestDAO, error) {
	var rows []*FriendRequestDAO
	err := s.db.WithContext(ctx).Where("to_did = ? AND status = 0", did).Find(&rows).Error
	return rows, err
}

func (s *StorageManager) ListSentRequests(ctx context.Context, did uint64) ([]*FriendRequestDAO, error) {
	var rows []*FriendRequestDAO
	err := s.db.WithContext(ctx).Where("from_did = ?", did).Find(&rows).Error
	return rows, err
}
