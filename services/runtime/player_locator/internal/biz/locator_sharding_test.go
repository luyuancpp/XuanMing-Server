// locator_sharding_test.go — 玩家位置 owner cell 锚定 + 位置存储分片键口径单测(2026-06-26)。
//
// 覆盖:LocationShardKey canonical 口径(= player_id,且与运行时落点 hub_pod/shard_id 无关)、
// locationOwner nil-router 退化 / 解析落点 / player_id 为 0 退化。验证 router 为 nil(单 Cell)时
// 行为不变,注入后能把玩家正确解析到 owner (region, cell)。
package biz

import (
	"testing"
	"time"

	"github.com/luyuancpp/pandora/pkg/cellroute"
)

// twoRegionLocatorRouter 造一张前半 region1 / 后半 region2 的均衡路由表,
// 用于验证不同玩家锚定不同 region 的 owner 落点。
func twoRegionLocatorRouter(t *testing.T) *cellroute.Router {
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
func locPlayerRegion1() uint64 { return 1 }
func locPlayerRegion2() uint64 { return uint64(cellroute.LogicalCellCount/2 + 1) }

func newRoutedLocatorUC(t *testing.T) *LocatorUsecase {
	t.Helper()
	uc := NewLocatorUsecase(newStubRepo(), 30*time.Second)
	uc.SetCellRouter(twoRegionLocatorRouter(t))
	return uc
}

func TestLocationShardKey_IsPlayerID(t *testing.T) {
	if got := LocationShardKey(7); got != "7" {
		t.Fatalf("LocationShardKey(7) = %q, want \"7\"", got)
	}
}

func TestLocationShardKey_IndependentOfRuntimePod(t *testing.T) {
	// 同一玩家的位置无论处于哪种运行时落点(hub/battle pod),分片键恒一致 → 位置恒落同 owner cell。
	a := LocationShardKey(42)
	b := LocationShardKey(42)
	if a != b {
		t.Fatalf("same player should yield same shard key: %q vs %q", a, b)
	}
	if LocationShardKey(42) == LocationShardKey(43) {
		t.Fatal("different players should yield different shard keys")
	}
}

func TestLocationOwner_NilRouter(t *testing.T) {
	uc := NewLocatorUsecase(newStubRepo(), 30*time.Second) // 未注入 router
	if _, ok := uc.locationOwner(7); ok {
		t.Fatal("nil router should yield ok=false")
	}
}

func TestLocationOwner_ZeroPlayer(t *testing.T) {
	uc := newRoutedLocatorUC(t)
	if _, ok := uc.locationOwner(0); ok {
		t.Fatal("zero player should yield ok=false")
	}
}

func TestLocationOwner_Resolves(t *testing.T) {
	uc := newRoutedLocatorUC(t)
	loc1, ok := uc.locationOwner(locPlayerRegion1())
	if !ok {
		t.Fatal("router should resolve region1 player")
	}
	if loc1.RegionID != 1 {
		t.Fatalf("want region 1, got %+v", loc1)
	}
	loc2, ok := uc.locationOwner(locPlayerRegion2())
	if !ok {
		t.Fatal("router should resolve region2 player")
	}
	if loc2.RegionID != 2 {
		t.Fatalf("want region 2, got %+v", loc2)
	}
}

func TestLocationOwner_SamePlayerStableLocation(t *testing.T) {
	uc := newRoutedLocatorUC(t)
	a, _ := uc.locationOwner(locPlayerRegion1())
	b, _ := uc.locationOwner(locPlayerRegion1())
	if a != b {
		t.Fatalf("same player should anchor to stable owner cell: %+v vs %+v", a, b)
	}
}
