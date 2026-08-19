package storage

import (
	"time"

	"gorm.io/gorm"
)

// MessageDAO 对应messages表，单聊/群聊统一落库。
// (conversation_id, client_msg_id) 唯一约束保证幂等：Kafka consumer因rebalance/重试
// 重复消费同一条消息时，重复insert会被数据库拒绝(而不是产生重复行)。
type MessageDAO struct {
	ID             uint           `gorm:"primaryKey"` // 数据库自增主键
	CreatedAt      time.Time      // 数据库记录创建时间
	UpdatedAt      time.Time      // 数据库记录最后更新时间
	DeletedAt      gorm.DeletedAt `gorm:"index"`                                                                                                  // 软删除时间，未删除时为空
	ConversationID string         `gorm:"column:conversation_id;size:64;uniqueIndex:uk_conv_client_msg,priority:1;index:idx_conv_seq,priority:1"` // 会话ID
	FromDid        uint64         `gorm:"column:from_did"`                                                                                        // 发送者did
	ToDid          uint64         `gorm:"column:to_did"`                                                                                          // 单聊接收者did，群聊为0
	FromUserID     uint64         `gorm:"column:from_user_id;index"`                                                                              // 发送者账号用户ID
	ToUserID       uint64         `gorm:"column:to_user_id;index"`                                                                                // 单聊接收者账号用户ID，群聊为0
	GroupID        uint64         `gorm:"column:group_id"`                                                                                        // 群聊群ID，单聊为0
	BizType        int32          `gorm:"column:biz_type"`                                                                                        // 业务消息类型(对应messagedto.BizMsgType)
	Content        []byte         `gorm:"column:content"`                                                                                         // 消息内容，服务端不解析
	Seq            uint64         `gorm:"column:seq;index:idx_conv_seq,priority:2"`                                                               // 会话内严格递增序号
	ClientMsgID    string         `gorm:"column:client_msg_id;size:64;uniqueIndex:uk_conv_client_msg,priority:2"`                                 // 客户端生成的全局唯一ID
	CreateTimeMS   int64          `gorm:"column:create_time_ms"`                                                                                  // 消息创建时间(毫秒时间戳，由state侧生成后传入，不依赖DB时钟)
}

func (MessageDAO) TableName() string {
	return "messages"
}
