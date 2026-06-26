// consumer_sharding_test.go — push 消费者按 owner cell 归属定向路由单测(2026-06-26)。
//
// 覆盖:ownsPlayer nil-router 退化(拥有全部 / known=false)/ player_id 为 0 退化 / 本 cell 玩家
// owned=true / 非本 cell 玩家 owned=false。验证 router 为 nil(单 Cell)时本实例拥有全部玩家
// (行为不变),注入后能正确判定归属;且非 owner 玩家不阻断交付(guardPlayerOwnership 仅观测)。
package biz

import (
	"context"
	"strconv"
	"testing"

	"github.com/luyuancpp/pandora/pkg/cellroute"
)

// twoRegionPushRouter 造一张前半 region1/cell1、后半 region2/cell2 的均衡路由表,
// 用于让不同玩家落不同 cell 验证归属判定。
func twoRegionPushRouter(t *testing.T) *cellroute.Router {
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

// 落 cell1(逻辑格在前半)/ cell2(逻辑格在后半)的 player_id 取样。
func pushPlayerCell1() uint64 { return 1 }
func pushPlayerCell2() uint64 { return uint64(cellroute.LogicalCellCount/2 + 1) }

func TestOwnsPlayer_NilRouterOwnsAll(t *testing.T) {
	kc := makeConsumer(t, newMockSender(), newMockOffline()) // 未注入 router
	_, owned, known := kc.ownsPlayer(42)
	if !owned {
		t.Fatal("nil router should own all players")
	}
	if known {
		t.Fatal("nil router should report known=false (ownership not applicable)")
	}
}

func TestOwnsPlayer_ZeroPlayer(t *testing.T) {
	kc := makeConsumer(t, newMockSender(), newMockOffline())
	kc.SetCellOwnership(twoRegionPushRouter(t), 1, 1)
	_, owned, known := kc.ownsPlayer(0)
	if !owned || known {
		t.Fatalf("zero player should be owned=true known=false, got owned=%v known=%v", owned, known)
	}
}

func TestOwnsPlayer_LocalCellOwned(t *testing.T) {
	kc := makeConsumer(t, newMockSender(), newMockOffline())
	// 本实例所在 cell = region1/cell1。
	kc.SetCellOwnership(twoRegionPushRouter(t), 1, 1)
	owner, owned, known := kc.ownsPlayer(pushPlayerCell1())
	if !known {
		t.Fatal("router set should yield known=true")
	}
	if !owned {
		t.Fatalf("cell1 player should be owned by region1/cell1 instance, owner=%+v", owner)
	}
	if owner.RegionID != 1 || owner.CellID != 1 {
		t.Fatalf("want owner region1/cell1, got %+v", owner)
	}
}

func TestOwnsPlayer_ForeignCellNotOwned(t *testing.T) {
	kc := makeConsumer(t, newMockSender(), newMockOffline())
	// 本实例所在 cell = region1/cell1,但玩家落 region2/cell2。
	kc.SetCellOwnership(twoRegionPushRouter(t), 1, 1)
	owner, owned, known := kc.ownsPlayer(pushPlayerCell2())
	if !known {
		t.Fatal("router set should yield known=true")
	}
	if owned {
		t.Fatalf("cell2 player should NOT be owned by region1/cell1 instance, owner=%+v", owner)
	}
	if owner.RegionID != 2 || owner.CellID != 2 {
		t.Fatalf("want owner region2/cell2, got %+v", owner)
	}
}

// 非 owner 玩家不阻断交付:在线玩家仍被 SendTo(guardPlayerOwnership 仅观测,handle 行为不变)。
func TestHandle_ForeignPlayerStillDelivered(t *testing.T) {
	sender := newMockSender()
	foreign := pushPlayerCell2()
	sender.online[foreign] = true
	offline := newMockOffline()
	kc := makeConsumer(t, sender, offline)
	kc.SetCellOwnership(twoRegionPushRouter(t), 1, 1) // 本实例 region1/cell1,玩家是 cell2 外来户

	msg := makeMsg("pandora.team.update", strconv.FormatUint(foreign, 10), []byte("evt"), "")
	if err := kc.handle(context.Background(), msg); err != nil {
		t.Fatalf("handle err=%v", err)
	}
	if _, ok := sender.frames[foreign]; !ok {
		t.Fatal("foreign-cell online player should still be delivered (guard is observe-only)")
	}
}
