// 本文件实现 Redis ZSET 排行榜:实时排名的权威计算层(2026-06-27)。
//
// key(同一 board 用 hashtag 锁同一 Redis Cluster slot,SubmitScore 的 Lua 同时碰 z/t 两 key,避免 CROSSSLOT):
//
//	pandora:lb:{<board>}:z   ZSET   member=entity_id,score=packed(见 §3.3 时间 tie-break 打包)
//	pandora:lb:{<board>}:t   HASH   entity_id → updated_at_ms(展示 / 审计)
//
// <board> = "<board_type>:<scope>:<scope_id>:<period>"(period 为空用 "-" 占位避免空段)。
//
// 分数打包(docs/design/decision-revisit-leaderboard.md §3.3):
//   - 不开 tie-break:packed = real(同分按 member 字典序);
//   - 降序榜 + tie-break:packed = real - normTs*1e-13(同分先达者 packed 大 → 名次高);
//   - 升序榜 + tie-break:packed = real + normTs*1e-13(同分先达者 packed 小 → 名次高);
//   - 还原真实分:real = round(packed)(时间项 < 0.5,不影响取整)。
package data

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/redis/go-redis/v9"

	"github.com/luyuancpp/pandora/pkg/errcode"
)

// lbEpochMs 是排行榜时间 tie-break 的纪元(2026-01-01 UTC,毫秒)。normTs = ts_ms - lbEpochMs。
const lbEpochMs int64 = 1767225600000

// Scope 是榜归属维度(对齐 proto LeaderboardScope 数值)。
type Scope int32

const (
	ScopeGlobal   Scope = 1
	ScopeGuild    Scope = 2
	ScopeInstance Scope = 3
	ScopeCustom   Scope = 4
)

// 上报模式(对齐 proto SubmitMode 数值)。
const (
	ModeSetIfHigher int32 = 1
	ModeSet         int32 = 2
	ModeIncrement   int32 = 3
)

// BoardKey 是榜的复合标识(存储层内部结构)。
type BoardKey struct {
	BoardType uint32
	Scope     Scope
	ScopeID   uint64
	Period    string
}

// Options 是建榜 / 写入行为参数。
type Options struct {
	TTLSeconds     int64
	MaxSize        int64
	TieBreakByTime bool
	Ascending      bool
}

// Entry 是榜上一项(存储层视图)。
type Entry struct {
	EntityID    uint64
	Score       int64
	Rank        int64 // 1-based
	UpdatedAtMs int64
}

// String 返回 board 串(period 空用 "-" 占位)。
func (b BoardKey) String() string {
	p := b.Period
	if p == "" {
		p = "-"
	}
	return fmt.Sprintf("%d:%d:%d:%s", b.BoardType, int32(b.Scope), b.ScopeID, p)
}

func (b BoardKey) zKey() string { return fmt.Sprintf("pandora:lb:{%s}:z", b.String()) }
func (b BoardKey) tKey() string { return fmt.Sprintf("pandora:lb:{%s}:t", b.String()) }
func (b BoardKey) mKey() string { return fmt.Sprintf("pandora:lb:{%s}:m", b.String()) }

// unpackReal 把 ZSET packed score 还原成真实整数分(round)。
func unpackReal(packed float64) int64 {
	return int64(math.Floor(packed + 0.5))
}

// BoardStore 是排行榜存储抽象(Redis ZSET 实现)。biz 只依赖此接口。
type BoardStore interface {
	// Submit 按 mode 写入分数并(可选)截断 / 设 TTL,返回写入后的真实分与 1-based 名次(0=未上榜)。
	Submit(ctx context.Context, b BoardKey, entityID uint64, score int64, mode int32, opt Options, tsMs int64) (newScore, rank int64, err error)
	// Rank 查某 entity 的名次 + 分;不在榜 found=false。
	Rank(ctx context.Context, b BoardKey, entityID uint64, ascending bool) (entry Entry, found bool, err error)
	// Range 取榜区间(offset 0-based)。
	Range(ctx context.Context, b BoardKey, offset int64, limit int, ascending bool) ([]Entry, error)
	// Around 取某 entity 上下 radius 名(含自身);不在榜 found=false。
	Around(ctx context.Context, b BoardKey, entityID uint64, radius int, ascending bool) ([]Entry, bool, error)
	// Total 返回榜总人数。
	Total(ctx context.Context, b BoardKey) (int64, error)
	// GetMeta 读榜元信息(ascending / tie-break);榜不存在 exists=false。
	GetMeta(ctx context.Context, b BoardKey) (ascending, tieBreak, exists bool, err error)
	// Remove 移除某 entity。
	Remove(ctx context.Context, b BoardKey, entityID uint64) error
	// Delete 删整个榜(z + t)。
	Delete(ctx context.Context, b BoardKey) error
	// Clear 清空榜分数但保留 key(周期 reset)。
	Clear(ctx context.Context, b BoardKey) error
}

// RedisBoardStore 是基于 go-redis ZSET 的 BoardStore。
type RedisBoardStore struct {
	rdb        redis.UniversalClient
	submitFunc *redis.Script
}

// NewRedisBoardStore 构造。
func NewRedisBoardStore(rdb redis.UniversalClient) *RedisBoardStore {
	return &RedisBoardStore{rdb: rdb, submitFunc: redis.NewScript(submitLua)}
}

// submitLua 原子完成:读旧分 → 按 mode 算新真实分 → 打包 → ZADD/HSET → 截断 maxSize → 设 TTL → 返回真实分 + 名次。
// KEYS[1]=zkey KEYS[2]=tkey KEYS[3]=mkey
// ARGV: 1 member 2 score 3 mode 4 tieBreak(0/1) 5 ascending(0/1) 6 tsMs 7 epochMs 8 maxSize 9 ttlSeconds
// 返回: {newReal, rank1Based}
const submitLua = `
local zkey, tkey, mkey = KEYS[1], KEYS[2], KEYS[3]
local member = ARGV[1]
local score  = tonumber(ARGV[2])
local mode   = tonumber(ARGV[3])
local tie    = tonumber(ARGV[4])
local asc    = tonumber(ARGV[5])
local ts     = tonumber(ARGV[6])
local epoch  = tonumber(ARGV[7])
local maxSize= tonumber(ARGV[8])
local ttl    = tonumber(ARGV[9])

local function realOf(p) return math.floor(p + 0.5) end

-- 首次写定义榜元信息(asc / tie),供后续读查询判定排序方向
if redis.call('EXISTS', mkey) == 0 then
  redis.call('HSET', mkey, 'asc', asc, 'tie', tie)
end

local cur = redis.call('ZSCORE', zkey, member)
local curReal = nil
if cur then curReal = realOf(tonumber(cur)) end

-- 决定新真实分与是否写入
local newReal
local doWrite = true
if mode == 3 then
  newReal = (curReal or 0) + score
else
  newReal = score
  if mode == 1 and curReal ~= nil then
    if asc == 1 then
      if newReal >= curReal then doWrite = false end
    else
      if newReal <= curReal then doWrite = false end
    end
  end
end

if doWrite then
  local normTs = ts - epoch
  if normTs < 0 then normTs = 0 end
  local packed = newReal
  if tie == 1 then
    if asc == 1 then packed = newReal + normTs * 1e-13
    else packed = newReal - normTs * 1e-13 end
  end
  redis.call('ZADD', zkey, packed, member)
  redis.call('HSET', tkey, member, ts)
else
  newReal = curReal
end

-- 截断 maxSize(保留最优 Top-N,清理被挤出者的 t 记录)
if maxSize > 0 then
  local n = redis.call('ZCARD', zkey)
  if n > maxSize then
    local victims
    if asc == 1 then
      victims = redis.call('ZRANGE', zkey, maxSize, -1)            -- 升序:最优在前,挤出尾部(高分)
    else
      victims = redis.call('ZRANGE', zkey, 0, n - maxSize - 1)     -- 降序:最优在后,挤出头部(低分)
    end
    if victims and #victims > 0 then
      redis.call('ZREM', zkey, unpack(victims))
      redis.call('HDEL', tkey, unpack(victims))
    end
  end
end

-- TTL(临时榜)
if ttl > 0 then
  redis.call('EXPIRE', zkey, ttl)
  redis.call('EXPIRE', tkey, ttl)
  redis.call('EXPIRE', mkey, ttl)
end

-- 名次(1-based;被截断 / 不在榜 → 0)
local rank = 0
local idx
if asc == 1 then idx = redis.call('ZRANK', zkey, member)
else idx = redis.call('ZREVRANK', zkey, member) end
if idx ~= false and idx ~= nil then rank = idx + 1 end

if newReal == nil then newReal = 0 end
return {newReal, rank}
`

// Submit 调 Lua 原子写入。
func (s *RedisBoardStore) Submit(ctx context.Context, b BoardKey, entityID uint64, score int64, mode int32, opt Options, tsMs int64) (int64, int64, error) {
	tie := 0
	if opt.TieBreakByTime {
		tie = 1
	}
	asc := 0
	if opt.Ascending {
		asc = 1
	}
	res, err := s.submitFunc.Run(ctx, s.rdb,
		[]string{b.zKey(), b.tKey(), b.mKey()},
		strconv.FormatUint(entityID, 10), score, mode, tie, asc, tsMs, lbEpochMs, opt.MaxSize, opt.TTLSeconds,
	).Result()
	if err != nil {
		return 0, 0, errcode.New(errcode.ErrInternal, "lb submit board=%s entity=%d: %v", b.String(), entityID, err)
	}
	arr, ok := res.([]interface{})
	if !ok || len(arr) != 2 {
		return 0, 0, errcode.New(errcode.ErrInternal, "lb submit bad reply board=%s", b.String())
	}
	newScore, _ := arr[0].(int64)
	rank, _ := arr[1].(int64)
	return newScore, rank, nil
}

// Rank 查名次 + 分。
func (s *RedisBoardStore) Rank(ctx context.Context, b BoardKey, entityID uint64, ascending bool) (Entry, bool, error) {
	member := strconv.FormatUint(entityID, 10)
	zkey := b.zKey()
	var idx int64
	var rerr error
	if ascending {
		idx, rerr = s.rdb.ZRank(ctx, zkey, member).Result()
	} else {
		idx, rerr = s.rdb.ZRevRank(ctx, zkey, member).Result()
	}
	if rerr == redis.Nil {
		return Entry{}, false, nil
	}
	if rerr != nil {
		return Entry{}, false, errcode.New(errcode.ErrInternal, "lb rank board=%s entity=%d: %v", b.String(), entityID, rerr)
	}
	packed, serr := s.rdb.ZScore(ctx, zkey, member).Result()
	if serr != nil {
		return Entry{}, false, errcode.New(errcode.ErrInternal, "lb score board=%s entity=%d: %v", b.String(), entityID, serr)
	}
	updated, _ := s.rdb.HGet(ctx, b.tKey(), member).Int64()
	return Entry{EntityID: entityID, Score: unpackReal(packed), Rank: idx + 1, UpdatedAtMs: updated}, true, nil
}

// Range 取榜区间。
func (s *RedisBoardStore) Range(ctx context.Context, b BoardKey, offset int64, limit int, ascending bool) ([]Entry, error) {
	if limit <= 0 || offset < 0 {
		return nil, nil
	}
	zkey := b.zKey()
	stop := offset + int64(limit) - 1
	var zs []redis.Z
	var rerr error
	if ascending {
		zs, rerr = s.rdb.ZRangeWithScores(ctx, zkey, offset, stop).Result()
	} else {
		zs, rerr = s.rdb.ZRevRangeWithScores(ctx, zkey, offset, stop).Result()
	}
	if rerr != nil {
		return nil, errcode.New(errcode.ErrInternal, "lb range board=%s: %v", b.String(), rerr)
	}
	return s.toEntries(ctx, b, zs, offset)
}

// Around 取某 entity 上下 radius 名。
func (s *RedisBoardStore) Around(ctx context.Context, b BoardKey, entityID uint64, radius int, ascending bool) ([]Entry, bool, error) {
	member := strconv.FormatUint(entityID, 10)
	zkey := b.zKey()
	var idx int64
	var rerr error
	if ascending {
		idx, rerr = s.rdb.ZRank(ctx, zkey, member).Result()
	} else {
		idx, rerr = s.rdb.ZRevRank(ctx, zkey, member).Result()
	}
	if rerr == redis.Nil {
		return nil, false, nil
	}
	if rerr != nil {
		return nil, false, errcode.New(errcode.ErrInternal, "lb around rank board=%s entity=%d: %v", b.String(), entityID, rerr)
	}
	start := idx - int64(radius)
	if start < 0 {
		start = 0
	}
	stop := idx + int64(radius)
	var zs []redis.Z
	if ascending {
		zs, rerr = s.rdb.ZRangeWithScores(ctx, zkey, start, stop).Result()
	} else {
		zs, rerr = s.rdb.ZRevRangeWithScores(ctx, zkey, start, stop).Result()
	}
	if rerr != nil {
		return nil, false, errcode.New(errcode.ErrInternal, "lb around range board=%s: %v", b.String(), rerr)
	}
	entries, eerr := s.toEntries(ctx, b, zs, start)
	if eerr != nil {
		return nil, false, eerr
	}
	return entries, true, nil
}

// toEntries 把 ZSET 区间结果 + updated_at 拼成 Entry 列表(startRank 为该批首项的 0-based 名次)。
func (s *RedisBoardStore) toEntries(ctx context.Context, b BoardKey, zs []redis.Z, startRank int64) ([]Entry, error) {
	if len(zs) == 0 {
		return nil, nil
	}
	members := make([]string, len(zs))
	for i, z := range zs {
		members[i], _ = z.Member.(string)
	}
	updated, err := s.rdb.HMGet(ctx, b.tKey(), members...).Result()
	if err != nil {
		return nil, errcode.New(errcode.ErrInternal, "lb hmget board=%s: %v", b.String(), err)
	}
	out := make([]Entry, 0, len(zs))
	for i, z := range zs {
		id, perr := strconv.ParseUint(members[i], 10, 64)
		if perr != nil {
			continue
		}
		var up int64
		if i < len(updated) {
			if sv, ok := updated[i].(string); ok {
				up, _ = strconv.ParseInt(sv, 10, 64)
			}
		}
		out = append(out, Entry{
			EntityID:    id,
			Score:       unpackReal(z.Score),
			Rank:        startRank + int64(i) + 1,
			UpdatedAtMs: up,
		})
	}
	return out, nil
}

// Total 返回榜总人数。
func (s *RedisBoardStore) Total(ctx context.Context, b BoardKey) (int64, error) {
	n, err := s.rdb.ZCard(ctx, b.zKey()).Result()
	if err != nil {
		return 0, errcode.New(errcode.ErrInternal, "lb total board=%s: %v", b.String(), err)
	}
	return n, nil
}

// GetMeta 读榜元信息(ascending / tie-break)。
func (s *RedisBoardStore) GetMeta(ctx context.Context, b BoardKey) (bool, bool, bool, error) {
	vals, err := s.rdb.HMGet(ctx, b.mKey(), "asc", "tie").Result()
	if err != nil {
		return false, false, false, errcode.New(errcode.ErrInternal, "lb meta board=%s: %v", b.String(), err)
	}
	if len(vals) < 2 || vals[0] == nil {
		return false, false, false, nil // 榜不存在
	}
	asc := vals[0] == "1"
	tie := len(vals) > 1 && vals[1] == "1"
	return asc, tie, true, nil
}

// Remove 移除某 entity。
func (s *RedisBoardStore) Remove(ctx context.Context, b BoardKey, entityID uint64) error {
	member := strconv.FormatUint(entityID, 10)
	pipe := s.rdb.TxPipeline()
	pipe.ZRem(ctx, b.zKey(), member)
	pipe.HDel(ctx, b.tKey(), member)
	if _, err := pipe.Exec(ctx); err != nil {
		return errcode.New(errcode.ErrInternal, "lb remove board=%s entity=%d: %v", b.String(), entityID, err)
	}
	return nil
}

// Delete 删整个榜。
func (s *RedisBoardStore) Delete(ctx context.Context, b BoardKey) error {
	if err := s.rdb.Del(ctx, b.zKey(), b.tKey(), b.mKey()).Err(); err != nil {
		return errcode.New(errcode.ErrInternal, "lb delete board=%s: %v", b.String(), err)
	}
	return nil
}

// Clear 清空榜分数(周期 reset;保留 meta 以延续榜配置)。
func (s *RedisBoardStore) Clear(ctx context.Context, b BoardKey) error {
	if err := s.rdb.Del(ctx, b.zKey(), b.tKey()).Err(); err != nil {
		return errcode.New(errcode.ErrInternal, "lb clear board=%s: %v", b.String(), err)
	}
	return nil
}
