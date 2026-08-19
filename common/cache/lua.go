package cache

import (
	"context"
	"fmt"
)

const (
	LuaCompareAndIncrClientID = "LuaCompareAndIncrClientID"
	LuaDeleteIfValueMatches   = "LuaDeleteIfValueMatches"
	LuaAddRecentMessage       = "LuaAddRecentMessage"
	LuaWarmRecentMessages     = "LuaWarmRecentMessages"
	LuaGetRecentMessages      = "LuaGetRecentMessages"
)

type luaPart struct {
	LuaScript string
	Sha       string
}

var luaScriptTable map[string]*luaPart = map[string]*luaPart{
	LuaCompareAndIncrClientID: {
		LuaScript: `if redis.call('exists', KEYS[1]) == 0 then redis.call('set', KEYS[1], 0) end;
if redis.call('get', KEYS[1]) == ARGV[1] then redis.call('incr', KEYS[1]);redis.call('expire', KEYS[1], ARGV[2]); 
return 1 else return -1 end`,
	},
	LuaDeleteIfValueMatches: {
		LuaScript: `if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('del', KEYS[1]) end; return 0`,
	},
	LuaAddRecentMessage: {
		LuaScript: `local score = tonumber(ARGV[1])
local oldMax = tonumber(redis.call('hget', KEYS[2], 'max_seq') or '0')
if redis.call('hget', KEYS[2], 'ready') == '1' and oldMax > 0 and redis.call('exists', KEYS[1]) == 0 then
    redis.call('hset', KEYS[2], 'ready', 0)
end
redis.call('zremrangebyscore', KEYS[1], score, score)
redis.call('zadd', KEYS[1], score, ARGV[2])
if score > oldMax then redis.call('hset', KEYS[2], 'max_seq', score) end
local overflow = redis.call('zcard', KEYS[1]) - tonumber(ARGV[3])
if overflow > 0 then
    local removed = redis.call('zrange', KEYS[1], 0, overflow - 1, 'withscores')
    redis.call('zremrangebyrank', KEYS[1], 0, overflow - 1)
    if redis.call('hget', KEYS[2], 'ready') == '1' then
        local floor = tonumber(redis.call('hget', KEYS[2], 'floor_seq') or '0')
        local removedFloor = tonumber(removed[#removed])
        if removedFloor > floor then floor = removedFloor end
        redis.call('hset', KEYS[2], 'floor_seq', floor)
    end
end
return 1`,
	},
	LuaWarmRecentMessages: {
		LuaScript: `local floor = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local maxSeq = tonumber(redis.call('hget', KEYS[2], 'max_seq') or '0')
for i = 3, #ARGV, 2 do
    redis.call('zremrangebyscore', KEYS[1], ARGV[i], ARGV[i])
    redis.call('zadd', KEYS[1], ARGV[i], ARGV[i + 1])
    local score = tonumber(ARGV[i])
    if score > maxSeq then maxSeq = score end
end
local oldFloor = tonumber(redis.call('hget', KEYS[2], 'floor_seq') or '0')
if redis.call('hget', KEYS[2], 'ready') == '1' and oldFloor > floor then floor = oldFloor end
local overflow = redis.call('zcard', KEYS[1]) - limit
if overflow > 0 then
    local removed = redis.call('zrange', KEYS[1], 0, overflow - 1, 'withscores')
    redis.call('zremrangebyrank', KEYS[1], 0, overflow - 1)
    local removedFloor = tonumber(removed[#removed])
    if removedFloor > floor then floor = removedFloor end
end
redis.call('hset', KEYS[2], 'ready', 1, 'floor_seq', floor, 'max_seq', maxSeq)
return 1`,
	},
	LuaGetRecentMessages: {
		LuaScript: `if redis.call('hget', KEYS[2], 'ready') ~= '1' then return {0} end
local floor = tonumber(redis.call('hget', KEYS[2], 'floor_seq') or '0')
if tonumber(ARGV[1]) < floor then return {-1} end
local maxSeq = tonumber(redis.call('hget', KEYS[2], 'max_seq') or '0')
if tonumber(ARGV[1]) < maxSeq and redis.call('exists', KEYS[1]) == 0 then
    redis.call('hset', KEYS[2], 'ready', 0)
    return {0}
end
local values = redis.call('zrangebyscore', KEYS[1], '(' .. ARGV[1], '+inf')
table.insert(values, 1, 1)
return values`,
	},
}

// 初始化lua脚本
func initLuaScript(ctx context.Context) {
	for name, part := range luaScriptTable {
		cmd := rdb.ScriptLoad(ctx, part.LuaScript)
		if cmd == nil {
			panic(fmt.Sprintf("lua init failed lua=%s", name))
		}
		if cmd.Err() != nil {
			panic(cmd.Err())
		}
		part.Sha = cmd.Val()
	}
}
