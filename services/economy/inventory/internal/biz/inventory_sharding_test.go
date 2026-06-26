// inventory_sharding_test.go — 拍卖成交跨人对转跨分片落点 + ledger 腿幂等键口径单测(2026-06-26)。
//
// 覆盖:AuctionLegKey canonical 口径(与 "auction:settle:<match_id>" 同源)/ 跨腿区分、
// AuctionParties 跨分片 / 跨 region 判定、auctionParties nil-router 退化 / 解析双方 / player_id 为 0 退化。
// 验证 router 为 nil(单 Cell)时行为不变(不做观测),注入后能正确判定跨分片对转落点。
package biz

import (
	"testing"

	"github.com/luyuancpp/pandora/pkg/cellroute"
)

// twoRegionInventoryRouter 造一张前半 region1 / 后半 region2 的均衡路由表,
// 用于让买卖双方落不同 region 验证跨 region 对转判定。
func twoRegionInventoryRouter(t *testing.T) *cellroute.Router {
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
func invPlayerRegion1() uint64 { return 1 }
func invPlayerRegion2() uint64 { return uint64(cellroute.LogicalCellCount/2 + 1) }

func TestAuctionLegKey_Canonical(t *testing.T) {
	got := AuctionLegKey(500, 7, LegSellerDeliver)
	want := "auction:settle:500:7:seller_deliver"
	if got != want {
		t.Fatalf("AuctionLegKey = %q, want %q", got, want)
	}
}

func TestAuctionLegKey_DistinctPerLeg(t *testing.T) {
	deliver := AuctionLegKey(500, 7, LegSellerDeliver)
	recvGold := AuctionLegKey(500, 7, LegSellerReceive)
	pay := AuctionLegKey(500, 9, LegBuyerPay)
	recvItem := AuctionLegKey(500, 9, LegBuyerReceive)
	keys := map[string]bool{deliver: true, recvGold: true, pay: true, recvItem: true}
	if len(keys) != 4 {
		t.Fatalf("legs should produce distinct keys, got %v", keys)
	}
}

func TestAuctionLegKey_Deterministic(t *testing.T) {
	a := AuctionLegKey(42, 3, LegBuyerReceive)
	b := AuctionLegKey(42, 3, LegBuyerReceive)
	if a != b {
		t.Fatalf("AuctionLegKey not deterministic: %q vs %q", a, b)
	}
}

func TestAuctionParties_CrossShardAndRegion(t *testing.T) {
	// 不同 region(必然跨分片)。
	diff := AuctionParties{SellerRegionID: 1, SellerCellID: 1, BuyerRegionID: 2, BuyerCellID: 2}
	if !diff.CrossRegionSettlement() {
		t.Fatal("different regions should be cross-region")
	}
	if !diff.CrossShardSettlement() {
		t.Fatal("different regions should be cross-shard")
	}
	// 同 region 不同 cell:跨分片但不跨 region。
	sameRegion := AuctionParties{SellerRegionID: 1, SellerCellID: 1, BuyerRegionID: 1, BuyerCellID: 2}
	if sameRegion.CrossRegionSettlement() {
		t.Fatal("same region should not be cross-region")
	}
	if !sameRegion.CrossShardSettlement() {
		t.Fatal("different cells should be cross-shard")
	}
	// 同 region 同 cell:本地对转。
	local := AuctionParties{SellerRegionID: 1, SellerCellID: 1, BuyerRegionID: 1, BuyerCellID: 1}
	if local.CrossRegionSettlement() || local.CrossShardSettlement() {
		t.Fatal("same shard should be neither cross-region nor cross-shard")
	}
}

func TestAuctionParties_NilRouter(t *testing.T) {
	uc := newUC(newFakeRepo())
	if _, ok := uc.auctionParties(1, 2); ok {
		t.Fatal("nil router should yield ok=false")
	}
}

func TestAuctionParties_ResolvesBoth(t *testing.T) {
	uc := newUC(newFakeRepo())
	uc.SetCellRouter(twoRegionInventoryRouter(t))
	parties, ok := uc.auctionParties(invPlayerRegion1(), invPlayerRegion2())
	if !ok {
		t.Fatal("router should resolve both parties")
	}
	if parties.SellerRegionID != 1 || parties.BuyerRegionID != 2 {
		t.Fatalf("want seller region 1 / buyer region 2, got %+v", parties)
	}
	if !parties.CrossRegionSettlement() {
		t.Fatal("seller region1 / buyer region2 should be cross-region")
	}
}

func TestAuctionParties_ZeroPlayer(t *testing.T) {
	uc := newUC(newFakeRepo())
	uc.SetCellRouter(twoRegionInventoryRouter(t))
	if _, ok := uc.auctionParties(0, 2); ok {
		t.Fatal("zero seller should yield ok=false")
	}
	if _, ok := uc.auctionParties(1, 0); ok {
		t.Fatal("zero buyer should yield ok=false")
	}
}
