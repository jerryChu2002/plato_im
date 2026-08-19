// Package convid 提供会话ID(conversationID)的统一构造规则，
// 被state(生产上行消息到Kafka时)和domain/message(消费/推送时识别单聊或群聊)共用，
// 保证两边算出来的会话ID完全一致。
package convid

import (
	"fmt"
	"strconv"
)

const groupPrefix = "group_"

// Single 返回单聊会话的conversationID。对(userA,userB)做归一化(取min-max)，
// 保证A发给B、B发给A算出的是同一个字符串，从而落到Kafka的同一个分区，
// 保证该会话消息在消费侧严格有序。
func Single(userA, userB uint64) string {
	if userA > userB {
		userA, userB = userB, userA
	}
	return fmt.Sprintf("%d_%d", userA, userB)
}

// Group 返回群聊会话的conversationID
func Group(groupID uint64) string {
	return fmt.Sprintf("%s%d", groupPrefix, groupID)
}

// IsGroup 判断一个conversationID是否为群聊会话
func IsGroup(conversationID string) bool {
	return len(conversationID) > len(groupPrefix) && conversationID[:len(groupPrefix)] == groupPrefix
}

// ParseGroupID 从群聊会话的conversationID里解析出群ID，非群聊conversationID时ok返回false
func ParseGroupID(conversationID string) (groupID uint64, ok bool) {
	if !IsGroup(conversationID) {
		return 0, false
	}
	id, err := strconv.ParseUint(conversationID[len(groupPrefix):], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}
