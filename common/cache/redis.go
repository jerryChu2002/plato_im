package cache

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/go-redis/redis/v9"
	"github.com/hardcore-os/plato/common/config"
)

// 声明一个全局的rdb变量，这是一个单机client
var rdb *redis.Client

func InitRedis(ctx context.Context) {
	if rdb != nil {
		return
	}
	endpoints := config.GetCacheRedisEndpointList()
	opt := &redis.Options{Addr: endpoints[0], PoolSize: 10000}
	rdb = redis.NewClient(opt)
	if _, err := rdb.Ping(ctx).Result(); err != nil {
		panic(err)
	}
	initLuaScript(ctx)
}
func GetBytes(ctx context.Context, key string) ([]byte, error) {
	cmd := rdb.Conn().Get(ctx, key)
	if cmd == nil {
		return nil, errors.New("redis GetBytes cmd is nil")
	}
	data, err := cmd.Bytes()
	if redis.Nil == err {
		return nil, nil
	}
	return data, err
}

func GetUInt64(ctx context.Context, key string) (uint64, error) {
	cmd := rdb.Conn().Get(ctx, key)
	if cmd == nil {
		return 0, errors.New("redis GetUInt64 cmd is nil")
	}
	return cmd.Uint64()
}

func SetBytes(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	cmd := rdb.Set(ctx, key, value, ttl)
	if cmd == nil {
		return errors.New("redis SetBytes cmd is nil")
	}
	return cmd.Err()
}

func Del(ctx context.Context, key ...string) error {
	cmd := rdb.Conn().Del(ctx, key...)
	if cmd == nil {
		return errors.New("redis Del cmd is nil")
	}
	return cmd.Err()
}

// DelIfValueMatches 仅当key仍等于expected时删除，避免旧连接下线误删新连接刚更新的路由。
func DelIfValueMatches(ctx context.Context, key, expected string) (bool, error) {
	res, err := RunLuaInt(ctx, LuaDeleteIfValueMatches, []string{key}, expected)
	return res > 0, err
}

func SADD(ctx context.Context, key string, member interface{}) error {
	cmd := rdb.SAdd(ctx, key, member)
	if cmd == nil {
		return errors.New("redis SADD cmd is nil")
	}
	return cmd.Err()
}

func SREM(ctx context.Context, key string, members ...interface{}) error {
	cmd := rdb.Conn().SRem(ctx, key, members...)
	if cmd == nil {
		return errors.New("redis SREM cmd is nil")
	}
	return cmd.Err()
}

func SmembersStrSlice(ctx context.Context, key string) ([]string, error) {
	cmd := rdb.Conn().SMembers(ctx, key)
	if cmd == nil {
		return nil, errors.New("redis SmembersUint64StructMap cmd is nil")
	}
	return cmd.Result()
}

func Incr(ctx context.Context, key string, ttl time.Duration) error {
	_, err := rdb.Conn().Pipelined(ctx, func(p redis.Pipeliner) error {
		p.Incr(ctx, key)
		p.Expire(ctx, key, ttl)
		return nil
	})
	return err
}

// IncrNoExpire 对key做自增并返回自增后的值，不设置过期时间——用于需要永久保留、
// 严格递增的序列，比如会话内消息seq(msg_seq_{conversationID})。
func IncrNoExpire(ctx context.Context, key string) (int64, error) {
	cmd := rdb.Conn().Incr(ctx, key)
	if cmd == nil {
		return 0, errors.New("redis IncrNoExpire cmd is nil")
	}
	return cmd.Result()
}

// GetInt64 读取一个int64类型的值，key不存在时返回0(比如读取还未产生过消息的会话的maxSeq)。
func GetInt64(ctx context.Context, key string) (int64, error) {
	cmd := rdb.Conn().Get(ctx, key)
	if cmd == nil {
		return 0, errors.New("redis GetInt64 cmd is nil")
	}
	val, err := cmd.Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}

func SetString(ctx context.Context, key string, value string, ttl time.Duration) error {
	cmd := rdb.Set(ctx, key, value, ttl)
	if cmd == nil {
		return errors.New("redis SetString cmd is nil")
	}
	return cmd.Err()
}

// HSet 写入一个hash字段，用于read_seq_{did}这类"一个key下按conversationID分field"的场景。
func HSet(ctx context.Context, key, field string, value interface{}) error {
	cmd := rdb.HSet(ctx, key, field, value)
	if cmd == nil {
		return errors.New("redis HSet cmd is nil")
	}
	return cmd.Err()
}

// HGetUInt64 读取一个hash字段并转成uint64，field不存在时返回0(表示"从未上报过已读，视为0")。
func HGetUInt64(ctx context.Context, key, field string) (uint64, error) {
	cmd := rdb.HGet(ctx, key, field)
	if cmd == nil {
		return 0, errors.New("redis HGetUInt64 cmd is nil")
	}
	val, err := cmd.Uint64()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}

// HGetAllUInt64 读取一个hash的全部字段，用于"一次性列出某个did所有会话的已读进度"。
func HGetAllUInt64(ctx context.Context, key string) (map[string]uint64, error) {
	cmd := rdb.HGetAll(ctx, key)
	if cmd == nil {
		return nil, errors.New("redis HGetAllUInt64 cmd is nil")
	}
	raw, err := cmd.Result()
	if err != nil {
		return nil, err
	}
	res := make(map[string]uint64, len(raw))
	for field, v := range raw {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			continue
		}
		res[field] = n
	}
	return res, nil
}

// GetString 读取一个字符串值。
// 注意：这里必须用cmd.Val()而不是cmd.String()——go-redis的Cmder.String()返回的是
// "get key: value"这样的调试用描述文本，不是真正取到的值，误用会导致调用方拿到的
// "值"其实是一段无法正常解析的调试字符串(比如router.QueryRecord会因此解析出错)。
func GetString(ctx context.Context, key string) (string, error) {
	cmd := rdb.Get(ctx, key)
	if cmd == nil {
		return "", errors.New("redis GetString cmd is nil")
	}
	if err := cmd.Err(); err != nil {
		if err == redis.Nil {
			return "", nil
		}
		return "", err
	}
	return cmd.Val(), nil
}

// 这里用的key为MaxClientIDKey，val为clientID
func RunLuaInt(ctx context.Context, name string, keys []string, args ...interface{}) (int, error) {
	if part, ok := luaScriptTable[name]; !ok {
		return -1, errors.New("lua not registered")
	} else {
		cmd := rdb.EvalSha(ctx, part.Sha, keys, args...)
		if cmd == nil {
			return -1, errors.New("redis RunLua cmd is nil")
		}

		return cmd.Int()
	}
}

func GetKeys(ctx context.Context, key string) ([]string, error) {
	return rdb.Keys(ctx, key).Result()
}

// redis cache
type redisCache struct {
}

func newRedisCache(opt *Options) *redisCache {
	return &redisCache{}
}

func (r *redisCache) MSet(keys map[string]interface{}) {

}

func (r *redisCache) MGet(key []string) map[string]interface{} {
	return nil
}

func (r *redisCache) MDel(key []string) {

}
