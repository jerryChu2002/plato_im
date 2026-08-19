package cache

import "time"

const (
	MaxClientIDKey   = "max_client_id_{%d}_%d_%s"
	LastMsgKey       = "last_msg_{%d}_%d"
	LoginSlotSetKey  = "login_slot_set_{%d}"  // 通过 hash tag保证在cluster模式上 key和tag相同的都放在一个shard上
	MsgSeqKey        = "msg_seq_{%s}"         // 会话内严格递增的消息seq，%s为conversationID，用hash tag方便未来Redis Cluster同分片
	ReadSeqKey       = "read_seq_{%d}"        // 某个did已读到各会话的seq，Redis Hash，field为conversationID，%d为did
	RecentMsgKey     = "recent_msg_{%s}"      // 会话最近消息ZSET，score为seq，member为序列化后的ChatMsgDTO
	RecentMsgMetaKey = "recent_msg_meta_{%s}" // 最近消息缓存覆盖元数据，与RecentMsgKey使用相同hash tag
	RecentMsgLimit   = 100                    // 每个会话在Redis中保留的最近消息条数
	TTL7D            = 7 * 24 * time.Hour
)
