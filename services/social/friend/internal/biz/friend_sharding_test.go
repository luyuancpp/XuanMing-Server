// friend_sharding_test.go — 好友图分片落点 + 幂等键口径单测(蜂窝扩容 ⑨)。
//
// 覆盖:
//   - AcceptIdempotencyKey / EdgeBuildKey:canonical 口径(锚定 request_id,§5.1/§5.3)
//   - DistinctEdgeRegions / DistinctEdgeCells:去重 + 升序 + 空输入
//   - CrossShardFriendship / CrossRegionFriendship:跨分片 / 跨 region 判定
//   - edgeOwners:nil router 退化 / 双 region 路由器按玩家解析落点
package biz

import (
	"testing"

	"github.com/luyuancpp/pandora/pkg/cellroute"

	"github.com/luyuancpp/pandora/services/social/friend/internal/conf"
)

// twoRegionRouter 构造一张把逻辑分片前半切给 (region1, cell1)、后半切给 (region2, cell2) 的路由器。
// player_id%4096 落前半 → region1,落后半 → region2,便于确定性构造跨 region 场景。
func twoRegionRouter(t *testing.T, region1, cell1, region2, cell2 uint32) *cellroute.Router {
	t.Helper()
	entries, regionOfCell, err := cellroute.BuildBalancedEntries([]cellroute.CellSpec{
		{RegionID: region1, CellID: cell1},
		{RegionID: region2, CellID: cell2},
	})
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

// playerInRegion1 返回一个 logical_cell 落前半(region1)的 player_id。
func playerInRegion1() uint64 { return 1 }

// playerInRegion2 返回一个 logical_cell 落后半(region2)的 player_id。
func playerInRegion2() uint64 { return uint64(cellroute.LogicalCellCount)/2 + 1 }

func newShardUsecase(t *testing.T) *FriendUsecase {
	t.Helper()
	return NewFriendUsecase(newFakeRepo(), nil, nil, conf.FriendConf{MaxFriends: 200})
}

func TestAcceptIdempotencyKey_Canonical(t *testing.T) {
	if got := AcceptIdempotencyKey(42); got != "friend_accept:42" {
		t.Fatalf("AcceptIdempotencyKey(42) = %q, want %q", got, "friend_accept:42")
	}
}

func TestEdgeBuildKey_CanonicalPerOwner(t *testing.T) {
	a := EdgeBuildKey(42, 1001)
	b := EdgeBuildKey(42, 2002)
	if a != "friend_accept:42:1001" {
		t.Fatalf("EdgeBuildKey(42,1001) = %q", a)
	}
	// 同 request 两条边方向必须不同键(否则互相覆盖)
	if a == b {
		t.Fatalf("two directions of same request must yield distinct edge keys")
	}
}

func TestDistinctEdgeRegions_SortedDedup(t *testing.T) {
	owners := []EdgeOwner{
		{PlayerID: 1, RegionID: 3},
		{PlayerID: 2, RegionID: 1},
		{PlayerID: 3, RegionID: 3},
	}
	got := DistinctEdgeRegions(owners)
	want := []uint32{1, 3}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
	if DistinctEdgeRegions(nil) != nil {
		t.Fatalf("empty input must return nil")
	}
}

func TestDistinctEdgeCells_CountsUniqueLocations(t *testing.T) {
	owners := []EdgeOwner{
		{PlayerID: 1, RegionID: 1, CellID: 10},
		{PlayerID: 2, RegionID: 1, CellID: 11},
		{PlayerID: 3, RegionID: 1, CellID: 10}, // 与 player1 同 Cell
	}
	if got := DistinctEdgeCells(owners); got != 2 {
		t.Fatalf("DistinctEdgeCells = %d, want 2", got)
	}
	if DistinctEdgeCells(nil) != 0 {
		t.Fatalf("empty must be 0")
	}
}

func TestCrossShardFriendship_DiffCell(t *testing.T) {
	diff := []EdgeOwner{
		{PlayerID: 1, RegionID: 1, CellID: 10},
		{PlayerID: 2, RegionID: 1, CellID: 11},
	}
	if !CrossShardFriendship(diff) {
		t.Fatalf("different cells must be cross-shard")
	}
	same := []EdgeOwner{
		{PlayerID: 1, RegionID: 1, CellID: 10},
		{PlayerID: 2, RegionID: 1, CellID: 10},
	}
	if CrossShardFriendship(same) {
		t.Fatalf("same cell must not be cross-shard")
	}
}

func TestCrossRegionFriendship_DiffRegion(t *testing.T) {
	diff := []EdgeOwner{{PlayerID: 1, RegionID: 1}, {PlayerID: 2, RegionID: 2}}
	if !CrossRegionFriendship(diff) {
		t.Fatalf("different regions must be cross-region")
	}
	same := []EdgeOwner{{PlayerID: 1, RegionID: 5}, {PlayerID: 2, RegionID: 5}}
	if CrossRegionFriendship(same) {
		t.Fatalf("same region must not be cross-region")
	}
}

func TestEdgeOwners_NilRouterNotOk(t *testing.T) {
	u := newShardUsecase(t)
	if _, ok := u.edgeOwners(playerInRegion1(), playerInRegion2()); ok {
		t.Fatalf("nil router must yield ok=false (single-Cell behavior unchanged)")
	}
}

func TestEdgeOwners_ResolvesBothAndCrossRegion(t *testing.T) {
	u := newShardUsecase(t)
	u.SetCellRouter(twoRegionRouter(t, 1, 10, 2, 20))

	owners, ok := u.edgeOwners(playerInRegion1(), playerInRegion2())
	if !ok {
		t.Fatalf("router set + valid players must yield ok=true")
	}
	if len(owners) != 2 {
		t.Fatalf("got %d owners, want 2", len(owners))
	}
	if !CrossRegionFriendship(owners) {
		t.Fatalf("region1 + region2 players must be cross-region")
	}
	if !CrossShardFriendship(owners) {
		t.Fatalf("region1 + region2 players land different cells → cross-shard")
	}
}

func TestEdgeOwners_ZeroPlayerNotOk(t *testing.T) {
	u := newShardUsecase(t)
	u.SetCellRouter(twoRegionRouter(t, 1, 10, 2, 20))
	if _, ok := u.edgeOwners(0, playerInRegion2()); ok {
		t.Fatalf("zero requester must yield ok=false")
	}
	if _, ok := u.edgeOwners(playerInRegion1(), 0); ok {
		t.Fatalf("zero target must yield ok=false")
	}
}
