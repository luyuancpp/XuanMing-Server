package cellroute

import (
	"testing"
)

// build3Region24Cell 构造一套贴近决策的拓扑:3 个 Region、共 24 个 Cell(每 region 8 个),
// 用 BuildBalancedEntries 铺满 4096 个逻辑分片。返回 Router 与配套校验数据。
func build3Region24Cell(t *testing.T) (*Router, map[uint32]uint32) {
	t.Helper()
	var cells []CellSpec
	var cellID uint32 = 1
	for region := uint32(1); region <= 3; region++ {
		for c := 0; c < 8; c++ {
			cells = append(cells, CellSpec{RegionID: region, CellID: cellID})
			cellID++
		}
	}
	entries, regionOfCell, err := BuildBalancedEntries(cells)
	if err != nil {
		t.Fatalf("BuildBalancedEntries: %v", err)
	}
	tbl, err := NewStaticTable(entries, regionOfCell)
	if err != nil {
		t.Fatalf("NewStaticTable: %v", err)
	}
	r, err := NewRouter(tbl)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r, regionOfCell
}

func TestLogicalCellOf_InRange(t *testing.T) {
	for _, id := range []uint64{0, 1, 4095, 4096, 4097, 1<<63 + 123, ^uint64(0)} {
		lc := LogicalCellOf(id)
		if uint64(lc) >= LogicalCellCount {
			t.Fatalf("LogicalCellOf(%d)=%d out of range [0,%d)", id, lc, LogicalCellCount)
		}
		if uint64(lc) != id%LogicalCellCount {
			t.Fatalf("LogicalCellOf(%d)=%d, want %d", id, lc, id%LogicalCellCount)
		}
	}
}

func TestRoute_Deterministic(t *testing.T) {
	r, _ := build3Region24Cell(t)
	for _, id := range []uint64{0, 1, 42, 4096, 999999, 1 << 40} {
		a, err := r.Route(id)
		if err != nil {
			t.Fatalf("Route(%d): %v", id, err)
		}
		b, err := r.Route(id)
		if err != nil {
			t.Fatalf("Route(%d) 2nd: %v", id, err)
		}
		if a != b {
			t.Fatalf("Route(%d) not deterministic: %+v vs %+v", id, a, b)
		}
		// 同一 logical_cell 的 player 必落同一 (region, cell)
		if a.LogicalCell != LogicalCellOf(id) {
			t.Fatalf("Route(%d).LogicalCell=%d, want %d", id, a.LogicalCell, LogicalCellOf(id))
		}
	}
}

// TestRoute_RegionCellConsistent 验证不变量①:任何 player 的 region 与其 cell 的归属 region 一致。
func TestRoute_RegionCellConsistent(t *testing.T) {
	r, regionOfCell := build3Region24Cell(t)
	for id := uint64(0); id < 20000; id++ {
		loc, err := r.Route(id)
		if err != nil {
			t.Fatalf("Route(%d): %v", id, err)
		}
		if regionOfCell[loc.CellID] != loc.RegionID {
			t.Fatalf("player %d: cell %d belongs to region %d but routed region %d",
				id, loc.CellID, regionOfCell[loc.CellID], loc.RegionID)
		}
	}
}

// TestBuildBalancedEntries_Even 验证铺表后各 Cell 分到的逻辑分片数最多差 1(均匀)。
func TestBuildBalancedEntries_Even(t *testing.T) {
	var cells []CellSpec
	for i := uint32(1); i <= 24; i++ {
		cells = append(cells, CellSpec{RegionID: (i-1)/8 + 1, CellID: i})
	}
	entries, _, err := BuildBalancedEntries(cells)
	if err != nil {
		t.Fatalf("BuildBalancedEntries: %v", err)
	}
	if uint64(len(entries)) != LogicalCellCount {
		t.Fatalf("entries len %d != %d", len(entries), LogicalCellCount)
	}
	counts := make(map[uint32]int)
	for _, e := range entries {
		counts[e.CellID]++
	}
	if len(counts) != 24 {
		t.Fatalf("got %d distinct cells, want 24", len(counts))
	}
	min, max := 1<<30, 0
	for _, c := range counts {
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	if max-min > 1 {
		t.Fatalf("uneven distribution: min=%d max=%d (diff>1)", min, max)
	}
}

func TestNewStaticTable_RejectsWrongLen(t *testing.T) {
	_, err := NewStaticTable([]Entry{{RegionID: 1, CellID: 1}}, map[uint32]uint32{1: 1})
	if err == nil {
		t.Fatal("expected error for wrong entries length")
	}
}

func TestNewStaticTable_RejectsRegionMismatch(t *testing.T) {
	entries := make([]Entry, LogicalCellCount)
	for i := range entries {
		entries[i] = Entry{RegionID: 1, CellID: 7}
	}
	// 拓扑声明 cell 7 属 region 2,但 entry 写 region 1 → 必须被拒
	_, err := NewStaticTable(entries, map[uint32]uint32{7: 2})
	if err == nil {
		t.Fatal("expected region mismatch error")
	}
}

func TestNewStaticTable_RejectsUndeclaredCell(t *testing.T) {
	entries := make([]Entry, LogicalCellCount)
	for i := range entries {
		entries[i] = Entry{RegionID: 1, CellID: 99}
	}
	_, err := NewStaticTable(entries, map[uint32]uint32{1: 1})
	if err == nil {
		t.Fatal("expected undeclared-cell error")
	}
}

func TestNewRouter_RejectsNil(t *testing.T) {
	if _, err := NewRouter(nil); err == nil {
		t.Fatal("expected error for nil table")
	}
}

func TestBuildBalancedEntries_RejectsCellInTwoRegions(t *testing.T) {
	_, _, err := BuildBalancedEntries([]CellSpec{
		{RegionID: 1, CellID: 5},
		{RegionID: 2, CellID: 5},
	})
	if err == nil {
		t.Fatal("expected error for cell declared in two regions")
	}
}

func TestRoute_DistributionAcrossRegions(t *testing.T) {
	r, _ := build3Region24Cell(t)
	regionHits := map[uint32]int{}
	for id := uint64(0); id < 12000; id++ {
		loc, err := r.Route(id)
		if err != nil {
			t.Fatalf("Route(%d): %v", id, err)
		}
		regionHits[loc.RegionID]++
	}
	if len(regionHits) != 3 {
		t.Fatalf("expected players spread over 3 regions, got %d", len(regionHits))
	}
}
