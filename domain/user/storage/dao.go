package storage

import (
	"time"

	"gorm.io/gorm"
)

type UserDAO struct {
	ID                  uint           `gorm:"primaryKey"` // 数据库自增主键
	CreatedAt           time.Time      // 用户资料创建时间
	UpdatedAt           time.Time      // 用户资料最后更新时间
	DeletedAt           gorm.DeletedAt `gorm:"index"`          // 软删除时间，未删除时为空
	UserID              uint64         `gorm:"index;not null"` // 账号对应的全局用户ID
	FontSize            string         // 用户界面字号偏好
	DarkMode            bool           // 是否启用深色模式
	ReceiveNotification bool           // 是否接收消息通知
	Language            string         // 用户界面语言
	Notifications       bool           // 是否启用通知功能
	Nickname            string         // 用户昵称
	Avatar              string         // 用户头像地址
	Signature           string         // 用户个性签名
	Location            string         // 用户所在地区
	Age                 int            // 用户年龄
	Gender              string         // 用户性别
	Tags                string         // 用户标签，按业务约定编码
}

func (u UserDAO) TableName() string {
	return "user"
}

type DeviceDAO struct {
	ID         uint           `gorm:"primaryKey"` // 数据库自增主键
	CreatedAt  time.Time      // 设备首次绑定时间
	UpdatedAt  time.Time      // 设备信息最后更新时间
	DeletedAt  gorm.DeletedAt `gorm:"index"`                // 软删除时间，未删除时为空
	DeviceID   uint64         `gorm:"uniqueIndex;not null"` // 客户端设备唯一标识did
	OS         string         // 设备操作系统
	AppVersion string         // 客户端应用版本
	Type       string         // 设备类型
	Mode       string         // 设备运行模式
	UserID     uint64         `gorm:"index;not null"` // 设备所属账号的用户ID
}

type AccountDAO struct {
	ID           uint           `gorm:"primaryKey"` // 数据库自增主键
	CreatedAt    time.Time      // 账号注册时间
	UpdatedAt    time.Time      // 账号信息最后更新时间
	DeletedAt    gorm.DeletedAt `gorm:"index"`                        // 软删除时间，未删除时为空
	UserID       uint64         `gorm:"uniqueIndex;not null"`         // 账号对应的全局用户ID
	Account      string         `gorm:"size:64;uniqueIndex;not null"` // 用户登录账号，统一转为小写存储
	PasswordHash string         `gorm:"size:100;not null"`            // bcrypt密码哈希，永不保存明文密码
}

func (a AccountDAO) TableName() string {
	return "account"
}

func (d DeviceDAO) TableName() string {
	return "device"
}
