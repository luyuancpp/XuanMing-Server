// dialogue_sharding_test.go — 对话会话 owner cell 锚定 + 会话存储分片键口径单测(2026-06-26)。
//
// 覆盖:SessionShardKey canonical 口径(= player_id,且与 dialogue_id 无关)、sessionOwner
// nil-router 退化 / 解析落点 / player_id 为 0 退化。验证 router 为 nil(单 Cell)时行为不变,
// 注入后能把玩家正确解析到 owner (region, cell)。
package biz

import (
	"testing"
	"time"

	"github.com/luyuancpp/pandora/pkg/cellroute"
	"github.com/luyuancpp/pandora/services/social/dialogue/internal/data"
)

// twoRegionDialogueRouter 造一张前半 region1 / 后半 region2 的均衡路由表,
// 用于验证不同玩家锚定不同 region 的 owner 落点。
func twoRegionDialogueRouter(t *testing.T) *cellroute.Router {
	t.Helper()
	specs := []cellroute.CellSpec{
		{RegionID: 1, CellID: 1},
		{RegionID: 2, CellID: 2},
	}
	entries, regionOfCell, err := cellroute.BuildBalancedEntries(specs)
	if err != nil {
		t.Fatalf("BuildBalancedEntries: %v", err)
	}
	tbl, err := cellroute.NewStaticTable(entries, regionOfCell)
	if err != nil {
		t.Fatalf("NewStaticTable: %v", err)
	}
	r, err := cellroute.NewRouter(tbl)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

// 落 region1(逻辑格在前半)/ region2(逻辑格在后半)的 player_id 取样。
func dlgPlayerRegion1() uint64 { return 1 }
func dlgPlayerRegion2() uint64 { return uint64(cellroute.LogicalCellCount/2 + 1) }

func newRoutedUsecase(t *testing.T) *DialogueUsecase {
	t.Helper()
	u := NewDialogueUsecase(
		data.NewConfigTreeProvider(newTestTree()),
		data.NewMemorySessionStore(),
		time.Minute,
	)
	u.SetCellRouter(twoRegionDialogueRouter(t))
	return u
}

func TestSessionShardKey_IsPlayerID(t *testing.T) {
	if got := SessionShardKey(7); got != "7" {
		t.Fatalf("SessionShardKey(7) = %q, want \"7\"", got)
	}
}

func TestSessionShardKey_IndependentOfDialogueID(t *testing.T) {
	// 同一玩家的不同对话(不同 dialogue_id)分片键一致 → 会话恒落同 owner cell。
	a := SessionShardKey(42)
	b := SessionShardKey(42)
	if a != b {
		t.Fatalf("same player should yield same shard key: %q vs %q", a, b)
	}
	// 不同玩家分片键不同。
	if SessionShardKey(42) == SessionShardKey(43) {
		t.Fatal("different players should yield different shard keys")
	}
}

func TestSessionOwner_NilRouter(t *testing.T) {
	u := newUsecase() // 未注入 router
	if _, ok := u.sessionOwner(7); ok {
		t.Fatal("nil router should yield ok=false")
	}
}

func TestSessionOwner_ZeroPlayer(t *testing.T) {
	u := newRoutedUsecase(t)
	if _, ok := u.sessionOwner(0); ok {
		t.Fatal("zero player should yield ok=false")
	}
}

func TestSessionOwner_Resolves(t *testing.T) {
	u := newRoutedUsecase(t)
	loc1, ok := u.sessionOwner(dlgPlayerRegion1())
	if !ok {
		t.Fatal("router should resolve region1 player")
	}
	if loc1.RegionID != 1 {
		t.Fatalf("want region 1, got %+v", loc1)
	}
	loc2, ok := u.sessionOwner(dlgPlayerRegion2())
	if !ok {
		t.Fatal("router should resolve region2 player")
	}
	if loc2.RegionID != 2 {
		t.Fatalf("want region 2, got %+v", loc2)
	}
}

func TestSessionOwner_SamePlayerStableLocation(t *testing.T) {
	u := newRoutedUsecase(t)
	a, _ := u.sessionOwner(dlgPlayerRegion1())
	b, _ := u.sessionOwner(dlgPlayerRegion1())
	if a != b {
		t.Fatalf("same player should anchor to stable owner cell: %+v vs %+v", a, b)
	}
}
