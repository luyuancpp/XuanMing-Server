package cellroute

import (
	"testing"
)

func TestInCellShard(t *testing.T) {
	// 单库:恒 0
	for _, id := range []uint64{0, 1, 4096, 1 << 40, ^uint64(0)} {
		s, err := InCellShard(id, 1)
		if err != nil {
			t.Fatalf("InCellShard(%d,1): %v", id, err)
		}
		if s != 0 {
			t.Fatalf("InCellShard(%d,1)=%d, want 0", id, s)
		}
	}

	// 多库:与 id % N 同口径,且落在 [0,N)
	const n = 8
	for _, id := range []uint64{0, 1, 7, 8, 9, 4096, 999999, 1 << 40} {
		s, err := InCellShard(id, n)
		if err != nil {
			t.Fatalf("InCellShard(%d,%d): %v", id, n, err)
		}
		if s != int(id%n) {
			t.Fatalf("InCellShard(%d,%d)=%d, want %d", id, n, s, id%n)
		}
		if s < 0 || s >= n {
			t.Fatalf("InCellShard(%d,%d)=%d out of range [0,%d)", id, n, s, n)
		}
	}
}

func TestInCellShard_RejectsBadShardCount(t *testing.T) {
	for _, bad := range []int{0, -1, -8} {
		if _, err := InCellShard(123, bad); err == nil {
			t.Fatalf("InCellShard(123,%d) should error", bad)
		}
	}
}

func TestRouteFull_ComposesThreeTiers(t *testing.T) {
	r, _ := build3Region24Cell(t)
	const shardsPerCell = 4
	for _, id := range []uint64{0, 1, 42, 4096, 999999, 1 << 40} {
		loc, err := r.Route(id)
		if err != nil {
			t.Fatalf("Route(%d): %v", id, err)
		}
		full, err := r.RouteFull(id, shardsPerCell)
		if err != nil {
			t.Fatalf("RouteFull(%d): %v", id, err)
		}
		// 前两层与 Route 一致
		if full.RegionID != loc.RegionID || full.CellID != loc.CellID || full.LogicalCell != loc.LogicalCell {
			t.Fatalf("RouteFull(%d) tiers 1/2 mismatch: %+v vs %+v", id, full, loc)
		}
		// 第三层与 InCellShard 同口径
		wantShard, _ := InCellShard(id, shardsPerCell)
		if full.InCellShard != wantShard || full.ShardsPerCell != shardsPerCell {
			t.Fatalf("RouteFull(%d) tier3=%d/%d, want %d/%d", id, full.InCellShard, full.ShardsPerCell, wantShard, shardsPerCell)
		}
	}
}

func TestRouteFull_Deterministic(t *testing.T) {
	r, _ := build3Region24Cell(t)
	for _, id := range []uint64{0, 1, 42, 999999} {
		a, err := r.RouteFull(id, 8)
		if err != nil {
			t.Fatalf("RouteFull(%d): %v", id, err)
		}
		b, err := r.RouteFull(id, 8)
		if err != nil {
			t.Fatalf("RouteFull(%d) 2nd: %v", id, err)
		}
		if a != b {
			t.Fatalf("RouteFull(%d) not deterministic: %+v vs %+v", id, a, b)
		}
	}
}

func TestRouteFull_RejectsBadShardCount(t *testing.T) {
	r, _ := build3Region24Cell(t)
	if _, err := r.RouteFull(123, 0); err == nil {
		t.Fatalf("RouteFull(123,0) should error")
	}
}

func TestCellTag(t *testing.T) {
	cases := []struct {
		region, cell uint32
		want         string
	}{
		{1, 7, "r1c7"},
		{3, 24, "r3c24"},
		{0, 0, "r0c0"},
	}
	for _, c := range cases {
		if got := CellTag(c.region, c.cell); got != c.want {
			t.Fatalf("CellTag(%d,%d)=%q, want %q", c.region, c.cell, got, c.want)
		}
	}
	// 同输入稳定(可作 metrics label / key 前缀)
	if CellTag(2, 5) != CellTag(2, 5) {
		t.Fatalf("CellTag not stable")
	}
	// 不同 Cell 不撞
	if CellTag(1, 2) == CellTag(2, 1) {
		t.Fatalf("CellTag(1,2) collides with CellTag(2,1)")
	}
}
