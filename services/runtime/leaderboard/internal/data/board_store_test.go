// board_store 的 Redis ZSET 排行榜单测(miniredis,2026-06-27)。
//
// 覆盖:SET_IF_HIGHER / SET / INCREMENT 三种上报模式、降序 / 升序排名、max_size 截断、
// 时间 tie-break(同分先达者名次高)、Around 邻居、Remove / Delete / Clear、GetMeta。
package data

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestStore 起 miniredis 并返回 RedisBoardStore。
func newTestStore(t *testing.T) (*RedisBoardStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRedisBoardStore(rdb), mr
}

var testBoard = BoardKey{BoardType: 1, Scope: ScopeGlobal, ScopeID: 0, Period: "2026W26"}

// descOpt 降序榜(高分高名次,默认大多数榜)。
func descOpt() Options { return Options{Ascending: false} }

// ── 上报模式 ──────────────────────────────────────────────────────────────────

func TestSubmit_SetIfHigher_KeepsBest(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if _, _, err := s.Submit(ctx, testBoard, 100, 50, ModeSetIfHigher, descOpt(), 1000); err != nil {
		t.Fatalf("submit 50: %v", err)
	}
	// 更高分 → 覆盖
	got, rank, err := s.Submit(ctx, testBoard, 100, 80, ModeSetIfHigher, descOpt(), 2000)
	if err != nil {
		t.Fatalf("submit 80: %v", err)
	}
	if got != 80 || rank != 1 {
		t.Fatalf("after 80: score=%d rank=%d, want 80/1", got, rank)
	}
	// 更低分 → 不降级,仍 80
	got, _, err = s.Submit(ctx, testBoard, 100, 30, ModeSetIfHigher, descOpt(), 3000)
	if err != nil {
		t.Fatalf("submit 30: %v", err)
	}
	if got != 80 {
		t.Fatalf("after lower 30: score=%d, want 80 (no downgrade)", got)
	}
}

func TestSubmit_SetIfHigher_Ascending(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	b := BoardKey{BoardType: 2, Scope: ScopeInstance, ScopeID: 7, Period: "-"}
	asc := Options{Ascending: true} // 升序榜:小分更优(如竞速用时)

	if _, _, err := s.Submit(ctx, b, 1, 5000, ModeSetIfHigher, asc, 1000); err != nil {
		t.Fatalf("submit 5000: %v", err)
	}
	// 升序榜「更优」= 更小;3000 < 5000 → 覆盖
	got, _, err := s.Submit(ctx, b, 1, 3000, ModeSetIfHigher, asc, 2000)
	if err != nil {
		t.Fatalf("submit 3000: %v", err)
	}
	if got != 3000 {
		t.Fatalf("after better(smaller) 3000: score=%d, want 3000", got)
	}
	// 更大(更差)→ 不更新
	got, _, _ = s.Submit(ctx, b, 1, 9000, ModeSetIfHigher, asc, 3000)
	if got != 3000 {
		t.Fatalf("after worse 9000: score=%d, want 3000", got)
	}
}

func TestSubmit_Increment(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if _, _, err := s.Submit(ctx, testBoard, 100, 10, ModeIncrement, descOpt(), 1000); err != nil {
		t.Fatalf("inc 10: %v", err)
	}
	got, _, err := s.Submit(ctx, testBoard, 100, 15, ModeIncrement, descOpt(), 2000)
	if err != nil {
		t.Fatalf("inc 15: %v", err)
	}
	if got != 25 {
		t.Fatalf("after inc: score=%d, want 25", got)
	}
}

func TestSubmit_Set_Overwrites(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	_, _, _ = s.Submit(ctx, testBoard, 100, 80, ModeSet, descOpt(), 1000)
	got, _, err := s.Submit(ctx, testBoard, 100, 40, ModeSet, descOpt(), 2000)
	if err != nil {
		t.Fatalf("set 40: %v", err)
	}
	if got != 40 {
		t.Fatalf("SET should overwrite even lower: score=%d, want 40", got)
	}
}

// ── 排名 / 区间 ───────────────────────────────────────────────────────────────

func TestRange_Descending(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	_, _, _ = s.Submit(ctx, testBoard, 1, 30, ModeSet, descOpt(), 1000)
	_, _, _ = s.Submit(ctx, testBoard, 2, 90, ModeSet, descOpt(), 1000)
	_, _, _ = s.Submit(ctx, testBoard, 3, 60, ModeSet, descOpt(), 1000)

	got, err := s.Range(ctx, testBoard, 0, 10, false)
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	wantIDs := []uint64{2, 3, 1}
	wantScores := []int64{90, 60, 30}
	if len(got) != 3 {
		t.Fatalf("range len=%d, want 3", len(got))
	}
	for i, e := range got {
		if e.EntityID != wantIDs[i] || e.Score != wantScores[i] || e.Rank != int64(i+1) {
			t.Fatalf("rank %d: id=%d score=%d rank=%d, want id=%d score=%d rank=%d",
				i, e.EntityID, e.Score, e.Rank, wantIDs[i], wantScores[i], i+1)
		}
	}
}

func TestRank_NotFound(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	_, found, err := s.Rank(ctx, testBoard, 999, false)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if found {
		t.Fatalf("found=true for absent entity, want false")
	}
}

func TestRank_Found(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.Submit(ctx, testBoard, 1, 30, ModeSet, descOpt(), 1000)
	_, _, _ = s.Submit(ctx, testBoard, 2, 90, ModeSet, descOpt(), 1000)

	e, found, err := s.Rank(ctx, testBoard, 1, false)
	if err != nil || !found {
		t.Fatalf("rank id=1: found=%v err=%v", found, err)
	}
	if e.Score != 30 || e.Rank != 2 {
		t.Fatalf("id=1: score=%d rank=%d, want 30/2", e.Score, e.Rank)
	}
}

// ── max_size 截断 ────────────────────────────────────────────────────────────

func TestSubmit_MaxSize_TruncatesDescending(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	b := BoardKey{BoardType: 3, Scope: ScopeCustom, ScopeID: 1, Period: "-"}
	opt := Options{Ascending: false, MaxSize: 2} // 只保留 Top-2

	_, _, _ = s.Submit(ctx, b, 1, 10, ModeSet, opt, 1000)
	_, _, _ = s.Submit(ctx, b, 2, 50, ModeSet, opt, 1000)
	_, _, _ = s.Submit(ctx, b, 3, 30, ModeSet, opt, 1000) // 触发截断,挤出最低分(10,id=1)

	total, err := s.Total(ctx, b)
	if err != nil {
		t.Fatalf("total: %v", err)
	}
	if total != 2 {
		t.Fatalf("total=%d, want 2 (truncated)", total)
	}
	if _, found, _ := s.Rank(ctx, b, 1, false); found {
		t.Fatalf("id=1 (lowest) should be truncated out")
	}
	if _, found, _ := s.Rank(ctx, b, 2, false); !found {
		t.Fatalf("id=2 (highest) should remain")
	}
}

// ── 时间 tie-break ───────────────────────────────────────────────────────────

func TestSubmit_TieBreakByTime_EarlierRanksHigher(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	b := BoardKey{BoardType: 4, Scope: ScopeGlobal, ScopeID: 0, Period: "-"}
	opt := Options{Ascending: false, TieBreakByTime: true}

	// 同分 50,id=1 先达(ts 小),id=2 后达(ts 大)。降序 + tie:先达名次更高。
	tsEarly := lbEpochMs + 1_000_000
	tsLate := lbEpochMs + 2_000_000
	_, _, _ = s.Submit(ctx, b, 1, 50, ModeSet, opt, tsEarly)
	_, _, _ = s.Submit(ctx, b, 2, 50, ModeSet, opt, tsLate)

	got, err := s.Range(ctx, b, 0, 10, false)
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if got[0].EntityID != 1 || got[1].EntityID != 2 {
		t.Fatalf("tie order = [%d,%d], want [1,2] (earlier first)", got[0].EntityID, got[1].EntityID)
	}
	// 真实分仍还原为 50(打包的时间项被 round 抹掉)
	if got[0].Score != 50 || got[1].Score != 50 {
		t.Fatalf("scores = [%d,%d], want [50,50]", got[0].Score, got[1].Score)
	}
}

// ── Around ───────────────────────────────────────────────────────────────────

func TestAround(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	b := BoardKey{BoardType: 5, Scope: ScopeGlobal, ScopeID: 0, Period: "-"}
	// 分数 10..100,id=1..10(降序后:id10 第1 … id1 第10)
	for i := uint64(1); i <= 10; i++ {
		_, _, _ = s.Submit(ctx, b, i, int64(i*10), ModeSet, descOpt(), 1000)
	}
	// 取 id=5(降序第6名)上下各 1 名 → id4,id5,id6 对应名次 7,6,5 → 顺序应是 id6,id5,id4
	got, found, err := s.Around(ctx, b, 5, 1, false)
	if err != nil || !found {
		t.Fatalf("around id=5: found=%v err=%v", found, err)
	}
	wantIDs := []uint64{6, 5, 4}
	if len(got) != 3 {
		t.Fatalf("around len=%d, want 3", len(got))
	}
	for i, e := range got {
		if e.EntityID != wantIDs[i] {
			t.Fatalf("around[%d] id=%d, want %d", i, e.EntityID, wantIDs[i])
		}
	}
}

// ── Remove / Delete / Clear / GetMeta ────────────────────────────────────────

func TestRemove(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.Submit(ctx, testBoard, 1, 30, ModeSet, descOpt(), 1000)
	if err := s.Remove(ctx, testBoard, 1); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, found, _ := s.Rank(ctx, testBoard, 1, false); found {
		t.Fatalf("id=1 still found after remove")
	}
}

func TestDelete(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.Submit(ctx, testBoard, 1, 30, ModeSet, descOpt(), 1000)
	if err := s.Delete(ctx, testBoard); err != nil {
		t.Fatalf("delete: %v", err)
	}
	total, _ := s.Total(ctx, testBoard)
	if total != 0 {
		t.Fatalf("total=%d after delete, want 0", total)
	}
	if _, _, exists, _ := s.GetMeta(ctx, testBoard); exists {
		t.Fatalf("meta should be gone after delete")
	}
}

func TestClear_KeepsMeta(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	opt := Options{Ascending: false, TieBreakByTime: true}
	_, _, _ = s.Submit(ctx, testBoard, 1, 30, ModeSet, opt, 1000)
	if err := s.Clear(ctx, testBoard); err != nil {
		t.Fatalf("clear: %v", err)
	}
	total, _ := s.Total(ctx, testBoard)
	if total != 0 {
		t.Fatalf("total=%d after clear, want 0", total)
	}
	// Clear 保留 meta(周期 reset 延续榜配置)
	asc, tie, exists, _ := s.GetMeta(ctx, testBoard)
	if !exists || asc || !tie {
		t.Fatalf("meta after clear: asc=%v tie=%v exists=%v, want false/true/true", asc, tie, exists)
	}
}

func TestGetMeta_AfterFirstSubmit(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	opt := Options{Ascending: true, TieBreakByTime: true}
	_, _, _ = s.Submit(ctx, testBoard, 1, 30, ModeSet, opt, 1000)

	asc, tie, exists, err := s.GetMeta(ctx, testBoard)
	if err != nil {
		t.Fatalf("getmeta: %v", err)
	}
	if !exists || !asc || !tie {
		t.Fatalf("meta = asc:%v tie:%v exists:%v, want true/true/true", asc, tie, exists)
	}
}

// ── TTL ──────────────────────────────────────────────────────────────────────

func TestSubmit_SetsTTL(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()
	b := BoardKey{BoardType: 6, Scope: ScopeInstance, ScopeID: 99, Period: "-"}
	opt := Options{Ascending: false, TTLSeconds: 60} // 临时榜

	_, _, _ = s.Submit(ctx, b, 1, 30, ModeSet, opt, 1000)
	if ttl := mr.TTL(b.zKey()); ttl <= 0 {
		t.Fatalf("zkey TTL=%v, want >0 (temporary board)", ttl)
	}
}
