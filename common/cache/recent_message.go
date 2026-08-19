package cache

import (
	"context"
	"errors"
	"fmt"
)

// ScoredMessage 是写入最近消息ZSET的一条序列化消息。
type ScoredMessage struct {
	Seq     uint64
	Payload []byte
}

func recentMessageKeys(conversationID string) []string {
	return []string{
		fmt.Sprintf(RecentMsgKey, conversationID),
		fmt.Sprintf(RecentMsgMetaKey, conversationID),
	}
}

// AddRecentMessage 幂等追加一条消息并将会话缓存裁剪为最近100条。
// 缓存尚未回填完成时只维护ZSET，不声明覆盖范围，读取仍会回源数据库。
func AddRecentMessage(ctx context.Context, conversationID string, msg ScoredMessage) error {
	_, err := runLuaResult(ctx, LuaAddRecentMessage, recentMessageKeys(conversationID), msg.Seq, msg.Payload, RecentMsgLimit)
	return err
}

// WarmRecentMessages 合并数据库中的最近消息并标记缓存覆盖边界。
// floorSeq表示缓存最早一条消息之前、已经被客户端持有时可完整使用缓存的seq。
func WarmRecentMessages(ctx context.Context, conversationID string, floorSeq uint64, msgs []ScoredMessage) error {
	args := make([]interface{}, 0, 2+len(msgs)*2)
	args = append(args, floorSeq, RecentMsgLimit)
	for _, msg := range msgs {
		args = append(args, msg.Seq, msg.Payload)
	}
	_, err := runLuaResult(ctx, LuaWarmRecentMessages, recentMessageKeys(conversationID), args...)
	return err
}

// GetRecentMessagesAfter 在fromSeq位于缓存完整覆盖范围内时返回其后的全部缓存消息。
// hit=false表示调用方必须回源数据库；ready用于区分尚未回填和请求早于缓存窗口。
func GetRecentMessagesAfter(ctx context.Context, conversationID string, fromSeq uint64) (payloads [][]byte, hit, ready bool, err error) {
	result, err := runLuaResult(ctx, LuaGetRecentMessages, recentMessageKeys(conversationID), fromSeq)
	if err != nil {
		return nil, false, false, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) == 0 {
		return nil, false, false, errors.New("redis recent message script returned invalid result")
	}
	marker, ok := values[0].(int64)
	if !ok {
		return nil, false, false, errors.New("redis recent message script returned invalid marker")
	}
	if marker <= 0 {
		return nil, false, marker < 0, nil
	}
	payloads = make([][]byte, 0, len(values)-1)
	for _, value := range values[1:] {
		switch typed := value.(type) {
		case string:
			payloads = append(payloads, []byte(typed))
		case []byte:
			payloads = append(payloads, typed)
		default:
			return nil, false, true, fmt.Errorf("redis recent message member has type %T", value)
		}
	}
	return payloads, true, true, nil
}

func runLuaResult(ctx context.Context, name string, keys []string, args ...interface{}) (interface{}, error) {
	part, ok := luaScriptTable[name]
	if !ok {
		return nil, errors.New("lua not registered")
	}
	cmd := rdb.EvalSha(ctx, part.Sha, keys, args...)
	if cmd == nil {
		return nil, errors.New("redis lua command is nil")
	}
	return cmd.Result()
}
