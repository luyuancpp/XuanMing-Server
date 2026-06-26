// trade_settlement_test.go — 交易结算跨分片落点 + ledger 腿幂等键口径单测(2026-06-16)。
//
// 覆盖:SettlementLegKey canonical 口径 / 跨腿区分、TradeParties 跨分片 / 跨 region 判定、
// tradeParties nil-router 退化 / 解析双方 / player_id 为 0 退化。验证 router 为 nil(单 Cell)时
// 行为不变(不做观测),注入后能正确判定跨分片落点。
package biz

import (
	"testing"

	"github.com/luyuancpp/pandora/pkg/cellroute"
)

// twoRegionSettlementRouter 造一张前半 region1 / 后半 region2 的均衡路由表,
// 用于让买卖双方落不同 region 验证跨 region 结算判定。
func twoRegionSettlementRouter(t *testing.T) *cellroute.Router {
	t.Helper()
	half := cellroute.LogicalCellCount / 2
	specs := make([]cellroute.CellSpec, 0, 2)
	specs = append(specs, cellroute.CellSpec{RegionID: 1, CellID: 1})
	specs = append(specs, cellroute.CellSpec{RegionID: 2, CellID: 2})
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
	_ = half
	return r
}

// 落 region1(逻辑格在前半)/ region2(逻辑格在后半)的 player_id 取样。
func settlePlayerRegion1() uint64 { return 1 }
func settlePlayerRegion2() uint64 { return uint64(cellroute.LogicalCellCount/2 + 1) }

func TestSettlementLegKey_Canonical(t *testing.T) {
	got := SettlementLegKey(100, 7, LegBuyerDebit)
	want := "100:7:buyer_debit"
	if got != want {
		t.Fatalf("SettlementLegKey = %q, want %q", got, want)
	}
}

func TestSettlementLegKey_DistinctPerLeg(t *testing.T) {
	debit := SettlementLegKey(100, 7, LegBuyerDebit)
	credit := SettlementLegKey(100, 9, LegSellerCredit)
	item := SettlementLegKey(100, 9, LegItemTransfer)
	refund := SettlementLegKey(100, 7, LegRefund)
	keys := map[string]bool{debit: true, credit: true, item: true, refund: true}
	if len(keys) != 4 {
		t.Fatalf("legs should produce distinct keys, got %v", keys)
	}
}

func TestSettlementLegKey_Deterministic(t *testing.T) {
	a := SettlementLegKey(42, 3, LegSellerCredit)
	b := SettlementLegKey(42, 3, LegSellerCredit)
	if a != b {
		t.Fatalf("SettlementLegKey not deterministic: %q vs %q", a, b)
	}
}

func TestTradeParties_CrossShardAndRegion(t *testing.T) {
	// 不同 region(必然跨分片)。
	diff := TradeParties{BuyerRegionID: 1, BuyerCellID: 1, SellerRegionID: 2, SellerCellID: 2}
	if !diff.CrossRegionSettlement() {
		t.Fatal("different regions should be cross-region")
	}
	if !diff.CrossShardSettlement() {
		t.Fatal("different regions should be cross-shard")
	}
	// 同 region 不同 cell:跨分片但不跨 region。
	sameRegion := TradeParties{BuyerRegionID: 1, BuyerCellID: 1, SellerRegionID: 1, SellerCellID: 2}
	if sameRegion.CrossRegionSettlement() {
		t.Fatal("same region should not be cross-region")
	}
	if !sameRegion.CrossShardSettlement() {
		t.Fatal("different cells should be cross-shard")
	}
	// 同 region 同 cell:本地结算。
	local := TradeParties{BuyerRegionID: 1, BuyerCellID: 1, SellerRegionID: 1, SellerCellID: 1}
	if local.CrossRegionSettlement() || local.CrossShardSettlement() {
		t.Fatal("same shard should be neither cross-region nor cross-shard")
	}
}

func TestTradeParties_NilRouter(t *testing.T) {
	uc, _ := newUC(newFakeRepo(), &fakeLedger{})
	if _, ok := uc.tradeParties(1, 2); ok {
		t.Fatal("nil router should yield ok=false")
	}
}

func TestTradeParties_ResolvesBoth(t *testing.T) {
	uc, _ := newUC(newFakeRepo(), &fakeLedger{})
	uc.SetCellRouter(twoRegionSettlementRouter(t))
	parties, ok := uc.tradeParties(settlePlayerRegion1(), settlePlayerRegion2())
	if !ok {
		t.Fatal("router should resolve both parties")
	}
	if parties.BuyerRegionID != 1 || parties.SellerRegionID != 2 {
		t.Fatalf("want buyer region 1 / seller region 2, got %+v", parties)
	}
	if !parties.CrossRegionSettlement() {
		t.Fatal("buyer region1 / seller region2 should be cross-region")
	}
}

func TestTradeParties_ZeroPlayer(t *testing.T) {
	uc, _ := newUC(newFakeRepo(), &fakeLedger{})
	uc.SetCellRouter(twoRegionSettlementRouter(t))
	if _, ok := uc.tradeParties(0, 2); ok {
		t.Fatal("zero buyer should yield ok=false")
	}
	if _, ok := uc.tradeParties(1, 0); ok {
		t.Fatal("zero seller should yield ok=false")
	}
}
