package cellroute

import (
	"fmt"
	"sync/atomic"
)

// 本文件提供热更新支撑:AtomicTable(可原子替换的 Table 实现)+ 纯解码函数
// (把 etcd 里的 logical_cell→"region:cell" 文本映射解析成校验过的 StaticTable)。
//
// 分工:解析 / 校验逻辑全部留在本包(无 etcd 依赖,可单测);真正的 etcd watch I/O
// 放在隔离子 module pkg/cellroute/etcdtable(对照 pkg/snowflake/etcdnode 的隔离模式),
// watch 回调只负责「读 KV → 调本包 DecodeEntries/NewStaticTable → AtomicTable.Store」。

// AtomicTable 是可并发读、可原子整体替换的 Table 实现。
//
// 用法:Router 持有 *AtomicTable(满足 Table 接口);etcd watch 收到映射变更时,
// 解析成新的 *StaticTable 后调 Store 整体替换。读路径(Lookup)无锁,始终看到某个
// 完整一致的快照(不会读到改了一半的表)——对照不变量②「映射变更走整表替换,不原地改」。
type AtomicTable struct {
	cur atomic.Pointer[StaticTable]
}

// NewAtomicTable 用一张初始 StaticTable 构造。initial 不可为 nil。
func NewAtomicTable(initial *StaticTable) (*AtomicTable, error) {
	if initial == nil {
		return nil, fmt.Errorf("cellroute: nil initial table")
	}
	at := &AtomicTable{}
	at.cur.Store(initial)
	return at, nil
}

// Store 原子替换为新表。next 不可为 nil(空映射用一张全量 StaticTable 表达,而非 nil)。
func (a *AtomicTable) Store(next *StaticTable) error {
	if next == nil {
		return fmt.Errorf("cellroute: store nil table")
	}
	a.cur.Store(next)
	return nil
}

// Lookup 实现 Table:转发到当前快照,无锁。
func (a *AtomicTable) Lookup(logicalCell uint32) (Entry, bool) {
	return a.cur.Load().Lookup(logicalCell)
}

// Len 实现 Table:当前快照长度。
func (a *AtomicTable) Len() int {
	return a.cur.Load().Len()
}

// DecodeEntries 把 etcd 里的「logical_cell → "regionID:cellID"」原始映射解析成
// entries(下标=logical_cell)+ 配套 regionOfCell 拓扑,可直接喂 NewStaticTable。
//
// raw 的 key 是 logical_cell(0..LogicalCellCount-1),value 形如 "12:34"(regionID:cellID)。
// 要求 raw 恰好覆盖 [0, LogicalCellCount) 全部下标(缺项报错,不静默补 0 号 Cell,
// 避免配置缺口被吞成错误落点)。同一 cellID 在不同 entry 出现时其 regionID 必须一致。
//
// 纯函数,无 etcd 依赖,便于单测;etcd watch 侧只负责把 clientv3 KV 整理成 map 喂进来。
func DecodeEntries(raw map[uint32]string) ([]Entry, map[uint32]uint32, error) {
	if uint64(len(raw)) != LogicalCellCount {
		return nil, nil, fmt.Errorf("cellroute: raw map has %d keys, want LogicalCellCount %d", len(raw), LogicalCellCount)
	}
	entries := make([]Entry, LogicalCellCount)
	regionOfCell := make(map[uint32]uint32)
	for lc := uint64(0); lc < LogicalCellCount; lc++ {
		v, ok := raw[uint32(lc)]
		if !ok {
			return nil, nil, fmt.Errorf("cellroute: missing logical_cell %d in raw map", lc)
		}
		var region, cell uint32
		if _, err := fmt.Sscanf(v, "%d:%d", &region, &cell); err != nil {
			return nil, nil, fmt.Errorf("cellroute: logical_cell %d bad value %q (want \"region:cell\"): %w", lc, v, err)
		}
		if reg, seen := regionOfCell[cell]; seen && reg != region {
			return nil, nil, fmt.Errorf("cellroute: cell %d mapped to region %d and %d", cell, reg, region)
		}
		regionOfCell[cell] = region
		entries[lc] = Entry{RegionID: region, CellID: cell}
	}
	return entries, regionOfCell, nil
}

// EncodeEntry 是 DecodeEntries 的逆:把一个 Entry 编码成 etcd value 文本 "region:cell"。
// 供铺表 / 运维工具写 etcd 用,保证读写两侧格式一致。
func EncodeEntry(e Entry) string {
	return fmt.Sprintf("%d:%d", e.RegionID, e.CellID)
}

// BuildStaticTableFromRaw 是 DecodeEntries + NewStaticTable 的便捷组合:
// etcd watch 回调拿到全量 KV 后一步得到可用的 *StaticTable。
func BuildStaticTableFromRaw(raw map[uint32]string) (*StaticTable, error) {
	entries, regionOfCell, err := DecodeEntries(raw)
	if err != nil {
		return nil, err
	}
	return NewStaticTable(entries, regionOfCell)
}
