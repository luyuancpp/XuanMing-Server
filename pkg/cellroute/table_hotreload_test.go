package cellroute

import (
	"strconv"
	"testing"
)

// rawFor24Cells 构造覆盖全部 4096 个 logical_cell 的 etcd 原始映射(3 region × 8 cell)。
func rawFor24Cells(t *testing.T) map[uint32]string {
	t.Helper()
	var cells []CellSpec
	var cellID uint32 = 1
	for region := uint32(1); region <= 3; region++ {
		for c := 0; c < 8; c++ {
			cells = append(cells, CellSpec{RegionID: region, CellID: cellID})
			cellID++
		}
	}
	entries, _, err := BuildBalancedEntries(cells)
	if err != nil {
		t.Fatalf("BuildBalancedEntries: %v", err)
	}
	raw := make(map[uint32]string, len(entries))
	for lc, e := range entries {
		raw[uint32(lc)] = EncodeEntry(e)
	}
	return raw
}

func TestDecodeEntries_RoundTrip(t *testing.T) {
	raw := rawFor24Cells(t)
	tbl, err := BuildStaticTableFromRaw(raw)
	if err != nil {
		t.Fatalf("BuildStaticTableFromRaw: %v", err)
	}
	r, err := NewRouter(tbl)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	// 抽样验证 region/cell 自洽
	for id := uint64(0); id < 5000; id++ {
		loc, err := r.Route(id)
		if err != nil {
			t.Fatalf("Route(%d): %v", id, err)
		}
		if loc.RegionID == 0 || loc.CellID == 0 {
			t.Fatalf("Route(%d) got zero region/cell: %+v", id, loc)
		}
	}
}

func TestDecodeEntries_RejectsMissingKey(t *testing.T) {
	raw := rawFor24Cells(t)
	delete(raw, 0) // 抠掉一个下标 → 必须报错(不静默补 0 号)
	if _, _, err := DecodeEntries(raw); err == nil {
		t.Fatal("expected error for missing logical_cell")
	}
}

func TestDecodeEntries_RejectsWrongCount(t *testing.T) {
	raw := map[uint32]string{0: "1:1"}
	if _, _, err := DecodeEntries(raw); err == nil {
		t.Fatal("expected error for wrong key count")
	}
}

func TestDecodeEntries_RejectsBadValue(t *testing.T) {
	raw := rawFor24Cells(t)
	raw[10] = "not-a-pair"
	if _, _, err := DecodeEntries(raw); err == nil {
		t.Fatal("expected error for malformed value")
	}
}

func TestDecodeEntries_RejectsCellRegionConflict(t *testing.T) {
	raw := rawFor24Cells(t)
	// 找到 cell 1 的某个下标,把它的 region 改成不同值 → 同 cell 两 region,必须报错
	for lc := uint32(0); lc < uint32(LogicalCellCount); lc++ {
		if raw[lc] == "1:1" {
			raw[lc] = "2:1" // cell 1 现在既属 region 1 又属 region 2
			break
		}
	}
	if _, _, err := DecodeEntries(raw); err == nil {
		t.Fatal("expected error for cell mapped to two regions")
	}
}

func TestAtomicTable_HotSwap(t *testing.T) {
	raw := rawFor24Cells(t)
	tbl1, err := BuildStaticTableFromRaw(raw)
	if err != nil {
		t.Fatalf("build tbl1: %v", err)
	}
	at, err := NewAtomicTable(tbl1)
	if err != nil {
		t.Fatalf("NewAtomicTable: %v", err)
	}
	r, err := NewRouter(at)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	before, err := r.Route(0)
	if err != nil {
		t.Fatalf("Route before: %v", err)
	}

	// 构造一张「把所有 logical_cell 重指到 region 9 / cell 99」的新表,原子替换
	raw2 := make(map[uint32]string, len(raw))
	for lc := uint32(0); lc < uint32(LogicalCellCount); lc++ {
		raw2[lc] = "9:99"
	}
	tbl2, err := BuildStaticTableFromRaw(raw2)
	if err != nil {
		t.Fatalf("build tbl2: %v", err)
	}
	if err := at.Store(tbl2); err != nil {
		t.Fatalf("Store: %v", err)
	}

	after, err := r.Route(0)
	if err != nil {
		t.Fatalf("Route after: %v", err)
	}
	if after.RegionID != 9 || after.CellID != 99 {
		t.Fatalf("hot swap not applied: got %+v", after)
	}
	if before.RegionID == 9 && before.CellID == 99 {
		t.Fatal("before should differ from after; test setup wrong")
	}
}

func TestAtomicTable_RejectsNil(t *testing.T) {
	if _, err := NewAtomicTable(nil); err == nil {
		t.Fatal("expected error for nil initial")
	}
	raw := rawFor24Cells(t)
	tbl, _ := BuildStaticTableFromRaw(raw)
	at, _ := NewAtomicTable(tbl)
	if err := at.Store(nil); err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestEncodeEntry(t *testing.T) {
	got := EncodeEntry(Entry{RegionID: 3, CellID: 17})
	if got != "3:17" {
		t.Fatalf("EncodeEntry = %q, want 3:17", got)
	}
	// 与 strconv 拼接一致性(防格式漂移)
	want := strconv.Itoa(3) + ":" + strconv.Itoa(17)
	if got != want {
		t.Fatalf("EncodeEntry = %q, want %q", got, want)
	}
}
