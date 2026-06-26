// region_affinity_test.go — 两级撮合核心算法纯函数单测(决策文档 §2.2)。
package biz

import (
	"testing"

	matchv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/match/v1"
)

func TestOverflowThresholdMs_TierShortens(t *testing.T) {
	p := DefaultRegionMatchPolicy()
	low := p.OverflowThresholdMs(0)    // 普通段
	mid := p.OverflowThresholdMs(3)    // 中段
	high := p.OverflowThresholdMs(100) // 极高段 → 撞下限
	if low != 90000 {
		t.Errorf("tier0 threshold = %d, want 90000", low)
	}
	if !(mid < low) {
		t.Errorf("tier3 threshold %d should be < tier0 %d", mid, low)
	}
	if high != p.OverflowMinMs {
		t.Errorf("very high tier threshold = %d, want clamp to min %d", high, p.OverflowMinMs)
	}
	// 负 tier 当 0 处理
	if p.OverflowThresholdMs(-5) != low {
		t.Errorf("negative tier should clamp to tier0")
	}
}

func TestShouldOverflow_DualCondition(t *testing.T) {
	p := DefaultRegionMatchPolicy()
	th := p.OverflowThresholdMs(0) // 90000

	// 本地候选充足 → 永不溢出,哪怕等很久
	if p.ShouldOverflow(th+1000, 0, true) {
		t.Error("should NOT overflow when local candidates enough")
	}
	// 本地不足但还没到阈值 → 不溢出
	if p.ShouldOverflow(th-1, 0, false) {
		t.Error("should NOT overflow before threshold")
	}
	// 本地不足且过阈值 → 溢出
	if !p.ShouldOverflow(th, 0, false) {
		t.Error("should overflow at threshold when local insufficient")
	}
}

func TestCandidateScore_SameRegionPreferred(t *testing.T) {
	p := DefaultRegionMatchPolicy()
	// 同 region:RTT 不计,仅 MMR 差
	same := p.CandidateScore(100, 1, 1, 999 /*ignored*/)
	if same != -100 {
		t.Errorf("same-region score = %v, want -100 (RTT ignored)", same)
	}
	// 跨 region:同样 MMR 差但加 RTT 惩罚 → 分更低(更不优先)
	cross := p.CandidateScore(100, 1, 2, 50)
	if !(cross < same) {
		t.Errorf("cross-region score %v should be < same-region %v", cross, same)
	}
	// MMR 差对称(正负绝对值)
	if p.CandidateScore(-100, 1, 1, 0) != p.CandidateScore(100, 1, 1, 0) {
		t.Error("mmrDiff should be symmetric")
	}
}

func TestCandidateScore_CloserMMRWinsWithinRegion(t *testing.T) {
	p := DefaultRegionMatchPolicy()
	near := p.CandidateScore(50, 1, 1, 0)
	far := p.CandidateScore(300, 1, 1, 0)
	if !(near > far) {
		t.Errorf("closer MMR (%v) should outscore far MMR (%v)", near, far)
	}
}

func TestMajorityRegion(t *testing.T) {
	if _, ok := MajorityRegion(nil); ok {
		t.Error("empty should return ok=false")
	}
	maj, ok := MajorityRegion([]uint32{1, 1, 1, 2, 3})
	if !ok || maj != 1 {
		t.Errorf("MajorityRegion = (%d,%v), want (1,true)", maj, ok)
	}
	// 并列取较小 region(确定性)
	maj2, _ := MajorityRegion([]uint32{2, 2, 3, 3})
	if maj2 != 2 {
		t.Errorf("tie should pick smaller region, got %d", maj2)
	}
}

func TestWithinCrossRegionCap(t *testing.T) {
	p := DefaultRegionMatchPolicy() // cap 40%

	// 10 人全同 region → 0% 跨区,合规
	if !p.WithinCrossRegionCap([]uint32{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}) {
		t.Error("all same region should be within cap")
	}
	// 10 人里 4 个少数派 → 40% 恰好 ≤ 40%,合规
	regions4 := []uint32{1, 1, 1, 1, 1, 1, 2, 2, 2, 2}
	if !p.WithinCrossRegionCap(regions4) {
		t.Error("40% minority should be within cap (<=40)")
	}
	// 10 人里 5 个非多数派 → 50% > 40%,不合规
	regions5 := []uint32{1, 1, 1, 1, 1, 2, 2, 3, 3, 3}
	if p.WithinCrossRegionCap(regions5) {
		t.Error("50% minority should exceed cap")
	}
	// 空输入合规
	if !p.WithinCrossRegionCap(nil) {
		t.Error("empty should be within cap")
	}
}

// ── 分区 / 溢出选择纯函数 ──────────────────────────────────────────────────────

// mkTicket 构造一张测试票据(members 人数 = size,captain = id)。
func mkTicket(id uint64, size int, avgMMR int32, enqueuedMs int64) *matchv1.MatchTicketStorageRecord {
	members := make([]*matchv1.MatchMemberStorageRecord, size)
	for i := range members {
		members[i] = &matchv1.MatchMemberStorageRecord{PlayerId: id*100 + uint64(i)}
	}
	return &matchv1.MatchTicketStorageRecord{
		TicketId:     id,
		CaptainId:    id,
		Members:      members,
		AvgMmr:       avgMMR,
		EnqueuedAtMs: enqueuedMs,
	}
}

func TestPartitionTicketsByRegion_GroupsAndOrders(t *testing.T) {
	// region = captain_id % 3(确定性桩)
	regionOf := func(t *matchv1.MatchTicketStorageRecord) uint32 { return uint32(t.CaptainId % 3) }
	tickets := []*matchv1.MatchTicketStorageRecord{
		mkTicket(3, 1, 1000, 0), // region 0
		mkTicket(1, 1, 1010, 0), // region 1
		mkTicket(4, 1, 1020, 0), // region 1
		mkTicket(2, 1, 1030, 0), // region 2
		mkTicket(6, 1, 1040, 0), // region 0
	}
	buckets, order := partitionTicketsByRegion(tickets, regionOf)

	// order 按 region 值升序
	want := []uint32{0, 1, 2}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d] = %d, want %d", i, order[i], want[i])
		}
	}
	// region 1 桶保持原相对顺序(ticket 1 在 ticket 4 前)
	r1 := buckets[1]
	if len(r1) != 2 || r1[0].TicketId != 1 || r1[1].TicketId != 4 {
		t.Fatalf("region1 bucket = %+v, want [1,4] in order", r1)
	}
	if len(buckets[0]) != 2 || len(buckets[2]) != 1 {
		t.Fatalf("bucket sizes wrong: r0=%d r2=%d", len(buckets[0]), len(buckets[2]))
	}
}

func TestPartitionTicketsByRegion_NilResolverSingleBucket(t *testing.T) {
	tickets := []*matchv1.MatchTicketStorageRecord{mkTicket(1, 1, 1000, 0), mkTicket(2, 1, 1000, 0)}
	buckets, order := partitionTicketsByRegion(tickets, nil)
	if len(order) != 1 || order[0] != 0 {
		t.Fatalf("nil resolver order = %v, want [0]", order)
	}
	if len(buckets[0]) != 2 {
		t.Fatalf("nil resolver bucket size = %d, want 2", len(buckets[0]))
	}
}

func TestRegionPlayerTotals(t *testing.T) {
	buckets := map[uint32][]*matchv1.MatchTicketStorageRecord{
		1: {mkTicket(1, 3, 0, 0), mkTicket(2, 2, 0, 0)}, // 5 人
		2: {mkTicket(3, 1, 0, 0)},                       // 1 人
	}
	totals := regionPlayerTotals(buckets)
	if totals[1] != 5 || totals[2] != 1 {
		t.Fatalf("totals = %v, want {1:5,2:1}", totals)
	}
}

func TestSelectOverflowTickets_DualCondition(t *testing.T) {
	p := DefaultRegionMatchPolicy() // tier0 阈值 90s
	regionOf := func(t *matchv1.MatchTicketStorageRecord) uint32 { return uint32(t.CaptainId % 2) }
	now := int64(1_000_000_000)
	need := 10

	// region 0 人不足(totals < need)→ 久等者可溢出;region 1 人足够 → 不溢出。
	regionTotals := map[uint32]int{0: 4, 1: 12}

	waited := mkTicket(2, 1, 1000, now-100_000)       // region 0,等 100s ≥ 90s,本区不足 → 溢出
	tooFresh := mkTicket(4, 1, 1000, now-10_000)      // region 0,等 10s < 90s → 不溢出
	enoughRegion := mkTicket(1, 1, 1000, now-100_000) // region 1,等 100s 但本区足够 → 不溢出

	got := selectOverflowTickets(
		[]*matchv1.MatchTicketStorageRecord{waited, tooFresh, enoughRegion},
		regionOf, regionTotals, need, p, nil, now,
	)
	if len(got) != 1 || got[0].TicketId != 2 {
		ids := make([]uint64, len(got))
		for i, g := range got {
			ids[i] = g.TicketId
		}
		t.Fatalf("overflow selected = %v, want [2]", ids)
	}
}

// ── battle 放置选择(参战玩家多数所在 region/cell)────────────────────────────

func TestMajorityCellLocation_PluralityWins(t *testing.T) {
	locs := []CellLocation{
		{RegionID: 1, CellID: 10},
		{RegionID: 1, CellID: 10},
		{RegionID: 1, CellID: 11},
		{RegionID: 2, CellID: 20},
	}
	got, ok := MajorityCellLocation(locs)
	if !ok {
		t.Fatal("expected ok")
	}
	if got.RegionID != 1 || got.CellID != 10 {
		t.Fatalf("majority = %+v, want {1,10}", got)
	}
}

func TestMajorityCellLocation_TieDeterministicSmallest(t *testing.T) {
	// (2,20) 与 (1,10) 各 2 票并列 → 取 (region,cell) 升序最小者 (1,10)
	locs := []CellLocation{
		{RegionID: 2, CellID: 20},
		{RegionID: 2, CellID: 20},
		{RegionID: 1, CellID: 10},
		{RegionID: 1, CellID: 10},
	}
	got, ok := MajorityCellLocation(locs)
	if !ok || got.RegionID != 1 || got.CellID != 10 {
		t.Fatalf("tie majority = %+v ok=%v, want {1,10}", got, ok)
	}
}

func TestMajorityCellLocation_EmptyNotOk(t *testing.T) {
	if _, ok := MajorityCellLocation(nil); ok {
		t.Fatal("empty should return ok=false")
	}
}

// ── 段位桶 / 段位档 ────────────────────────────────────────────────────────────

func TestMmrBucket_SegmentsByWidth(t *testing.T) {
	p := DefaultRegionMatchPolicy() // 宽 200
	if b := p.MmrBucket(0); b != 0 {
		t.Errorf("mmr 0 bucket = %d, want 0", b)
	}
	if b := p.MmrBucket(199); b != 0 {
		t.Errorf("mmr 199 bucket = %d, want 0", b)
	}
	if b := p.MmrBucket(200); b != 1 {
		t.Errorf("mmr 200 bucket = %d, want 1", b)
	}
	if b := p.MmrBucket(2050); b != 10 {
		t.Errorf("mmr 2050 bucket = %d, want 10", b)
	}
	// 负 MMR 归桶 0
	if b := p.MmrBucket(-50); b != 0 {
		t.Errorf("negative mmr bucket = %d, want 0", b)
	}
	// 宽度非法 → 单桶 0
	bad := RegionMatchPolicy{MmrBucketWidth: 0}
	if b := bad.MmrBucket(9999); b != 0 {
		t.Errorf("zero width bucket = %d, want 0", b)
	}
}

func TestMmrTier_HigherMmrHigherTier(t *testing.T) {
	p := DefaultRegionMatchPolicy() // base 2000,step 400
	if tr := p.MmrTier(1500); tr != 0 {
		t.Errorf("mmr 1500 tier = %d, want 0", tr)
	}
	if tr := p.MmrTier(2000); tr != 0 {
		t.Errorf("mmr 2000 (=base) tier = %d, want 0", tr)
	}
	if tr := p.MmrTier(2400); tr != 1 {
		t.Errorf("mmr 2400 tier = %d, want 1", tr)
	}
	if tr := p.MmrTier(3300); tr != 3 {
		t.Errorf("mmr 3300 tier = %d, want 3", tr)
	}
	// step 非法 → 恒 0
	bad := RegionMatchPolicy{TierBaseMmr: 2000, TierStepMmr: 0}
	if tr := bad.MmrTier(9999); tr != 0 {
		t.Errorf("zero step tier = %d, want 0", tr)
	}
}

// 高分段档位更高 → 溢出阈值更短(段位桶与溢出阈值口径打通)。
func TestMmrTier_FeedsShorterOverflowThreshold(t *testing.T) {
	p := DefaultRegionMatchPolicy()
	lowTier := p.MmrTier(1800)  // 普通段 → tier 0
	highTier := p.MmrTier(3300) // 高分段 → tier 3
	if !(highTier > lowTier) {
		t.Fatalf("high tier %d should exceed low tier %d", highTier, lowTier)
	}
	if !(p.OverflowThresholdMs(highTier) < p.OverflowThresholdMs(lowTier)) {
		t.Fatalf("high-tier threshold %d should be < low-tier %d",
			p.OverflowThresholdMs(highTier), p.OverflowThresholdMs(lowTier))
	}
}
