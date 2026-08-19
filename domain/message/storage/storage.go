package storage

import (
	"context"

	"github.com/hardcore-os/plato/common/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StorageManager 复用domain/user的分层模式：rpc service层依赖它做持久化，
// consumer也依赖它做落库，两者共享同一个StorageManager实例。
type StorageManager struct {
	db *gorm.DB
}

func NewStorageManager(isTest bool) *StorageManager {
	if isTest {
		return newMockStorageManager()
	}
	sm := &StorageManager{}
	var err error
	sm.db, err = gorm.Open(mysql.Open(config.GetDomainMessageDBDNS()), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	if err := sm.db.AutoMigrate(&MessageDAO{}); err != nil {
		panic(err)
	}
	return sm
}

func newMockStorageManager() *StorageManager {
	sm := &StorageManager{}
	var err error
	sm.db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	if err := sm.db.AutoMigrate(&MessageDAO{}); err != nil {
		panic(err)
	}
	return sm
}

// SaveMsg 幂等落库：(conversation_id, client_msg_id)已存在则直接跳过(不覆盖)，
// 这是Kafka consumer"重复消费不出错"的关键保障。isNew区分"这次是真正的新插入"还是
// "已经存在被跳过"——调用方(consumer)必须用它来判断要不要重复推送，否则Kafka的
// at-least-once重复投递会导致同一条消息在数据库里只有一行、却被推给用户两次。
func (s *StorageManager) SaveMsg(ctx context.Context, dao *MessageDAO) (isNew bool, err error) {
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "conversation_id"}, {Name: "client_msg_id"}},
		DoNothing: true,
	}).Create(dao)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// PullMsgs 按会话+seq范围拉取消息(离线补拉/漏消息补拉共用)，按seq升序返回。
// limit传入时应该是"业务期望条数+1"，调用方据此判断hasMore。
func (s *StorageManager) PullMsgs(ctx context.Context, conversationID string, fromSeq uint64, limit int) ([]*MessageDAO, error) {
	var msgs []*MessageDAO
	err := s.db.WithContext(ctx).
		Where("conversation_id = ? AND seq > ?", conversationID, fromSeq).
		Order("seq ASC").
		Limit(limit).
		Find(&msgs).Error
	return msgs, err
}

// PullLatestMsgs 返回会话最新的limit条消息，并按seq升序排列，供最近消息缓存回填使用。
func (s *StorageManager) PullLatestMsgs(ctx context.Context, conversationID string, limit int) ([]*MessageDAO, error) {
	var msgs []*MessageDAO
	err := s.db.WithContext(ctx).
		Where("conversation_id = ?", conversationID).
		Order("seq DESC").
		Limit(limit).
		Find(&msgs).Error
	for left, right := 0, len(msgs)-1; left < right; left, right = left+1, right-1 {
		msgs[left], msgs[right] = msgs[right], msgs[left]
	}
	return msgs, err
}

// ListUserConversationLastMsgs 返回账号参与过的每个单聊会话及群聊会话的最后一条消息。
func (s *StorageManager) ListUserConversationLastMsgs(ctx context.Context, userID uint64) ([]*MessageDAO, error) {
	var conversationIDs []string
	if err := s.db.WithContext(ctx).Model(&MessageDAO{}).
		Where("from_user_id = ? OR to_user_id = ?", userID, userID).
		Distinct("conversation_id").Pluck("conversation_id", &conversationIDs).Error; err != nil {
		return nil, err
	}
	lastMsgs := make([]*MessageDAO, 0, len(conversationIDs))
	for _, conversationID := range conversationIDs {
		var msg MessageDAO
		if err := s.db.WithContext(ctx).Where("conversation_id = ?", conversationID).
			Order("seq DESC").First(&msg).Error; err != nil {
			return nil, err
		}
		lastMsgs = append(lastMsgs, &msg)
	}
	return lastMsgs, nil
}
