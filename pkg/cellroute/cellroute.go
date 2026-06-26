// Package cellroute 实现 Pandora 全服扩容(docs/design/scale-cellular-20m.md)的
// 确定性玩家路由地基:把 uint64 snowflake player_id 映射到 (RegionID, CellID)。
//
// 背景:DAU 2000 万 / ~600 万 CCU 下,单逻辑集群与单一全局协调层均触顶,采用
// Region(大区) → Cell(单元) → Cell 内分片 三层。本包负责最外两层的"算落点":
//
//	logical_cell = player_id % LogicalCellCount   // 第 1 步:算逻辑分片(确定性)
//	(region, cell) = Table.Lookup(logical_cell)    // 第 2 步:查小映射表得物理落点
//	Cell 内 redis_slot = CRC16(player_id)%16384 / mysql_shard = player_id%N // 第 3 层,见 redisx/mysqlx
//
// 设计要点(对照 scale-cellular-20m.md §3.2/§4.2 与 CLAUDE.md §9 不变量):
//
//   - **确定性,算不查热路径**:player_id 经一次取模得 logical_cell,只查一张
//     "logical_cell → (region, cell)" 小映射表(LogicalCellCount 项,可全量缓存 + watch),
//     不做按 player 的在线查库。这是"全程算、不是查"的承接(player_locator 查的是动态
//     DS 位置,不是存储落点)。
//
//   - **Region 由 Cell 派生,结构性保证不变量**:一个 logical_cell 映射到的物理 Cell
//     自带其所属 RegionID,因此同一 player_id 的 region 与 cell 永远一致,不可能出现
//     "region 算 A、cell 算 B"的错配。这把 CLAUDE.md §9 新增不变量①(同一 player_id 所有
//     owner 数据必落同一 region+cell)做成编译期/配置期即成立,而非运行期校验。
//     注:scale-cellular-20m.md §4.2 把 region_route / cell_route 写成两层概念,本实现
//     等价但更强——region_route ≡ RegionOf(cell_route),避免双取模错配。
//
//   - **逻辑分片 ≫ 物理 Cell**:LogicalCellCount=4096 远大于物理 Cell 数(~16~24),
//     加 Cell 时只把部分 logical_cell 区间重指到新 Cell,**不 rehash 全量**
//     (对照 mysqlx.ShardSet "改 N 要全量 rehash"的痛点,本层用逻辑分片规避)。
//     映射变更必须走双写灰度迁移(不变量②),不可热改裸取模。
//
//   - **uint32 拓扑维度**:RegionID / CellID 是部署拓扑维度(非 snowflake 业务 ID),
//     按 CLAUDE.md §9.12 取 uint32;player_id 仍是 uint64 业务 ID。
//
// 本包只做纯路由计算 + 静态映射表;etcd 后端(watch 热更新映射表)、跨 region 撮合、
// 多 k8s 编排等属后续增量与基础设施(AGENTS.md §11.1 由 Codex/人接环境)。
package cellroute

import (
	"fmt"
)

const (
	// LogicalCellCount 是逻辑 Cell 分片数(决策 2026-06-26:采纳 4096)。
	// player_id % LogicalCellCount 得 logical_cell;远大于物理 Cell 数以支持平滑扩容。
	LogicalCellCount uint64 = 4096

	// LogicalRegionCount 是逻辑 Region 分片数上界(决策 2026-06-26:采纳 64)。
	// 物理 Region 数(当前 3)远小于此;保留 64 作为 Region 级协调分片(如跨 region
	// 撮合溢出池 key)的稳定上界,owner 的权威 region 仍由 Cell 派生(见包注释)。
	LogicalRegionCount uint64 = 64
)

// Entry 是映射表里一个 logical_cell 的物理落点:它属于哪个 Region 的哪个 Cell。
type Entry struct {
	RegionID uint32
	CellID   uint32
}

// Location 是一次路由结果。LogicalCell 保留用于调试 / 迁移灰度判定。
type Location struct {
	RegionID    uint32
	CellID      uint32
	LogicalCell uint32
}

// Table 是 "logical_cell → (region, cell)" 映射的只读抽象。
// 静态实现见 StaticTable;后续可接 etcd watch 热更新实现,Router 无需改动。
type Table interface {
	// Lookup 返回 logicalCell 对应的物理落点;logicalCell 越界或未配置返回 ok=false。
	Lookup(logicalCell uint32) (Entry, bool)
	// Len 返回逻辑分片数,必须等于 LogicalCellCount。
	Len() int
}

// StaticTable 是不可变的内存映射表:下标即 logical_cell,长度固定 LogicalCellCount。
// 适合启动期从配置加载;运行期更新走"换整张表"(原子替换 Router 持有的 Table)而非原地改。
type StaticTable struct {
	entries []Entry
}

// NewStaticTable 校验并构造静态映射表。entries 长度必须为 LogicalCellCount,
// 且每项 CellID 必须在 regionOfCell 中登记(保证 region 与 cell 自洽)。
//
// regionOfCell:物理拓扑声明,cellID → 它所属的 regionID。NewStaticTable 用它校验
// 每个 entry 的 RegionID 与该 Cell 登记的 region 一致,从源头杜绝 region/cell 错配。
func NewStaticTable(entries []Entry, regionOfCell map[uint32]uint32) (*StaticTable, error) {
	if uint64(len(entries)) != LogicalCellCount {
		return nil, fmt.Errorf("cellroute: entries len %d != LogicalCellCount %d", len(entries), LogicalCellCount)
	}
	for lc, e := range entries {
		reg, ok := regionOfCell[e.CellID]
		if !ok {
			return nil, fmt.Errorf("cellroute: logical_cell %d -> cell %d not declared in regionOfCell", lc, e.CellID)
		}
		if reg != e.RegionID {
			return nil, fmt.Errorf("cellroute: logical_cell %d -> cell %d region mismatch: entry=%d topology=%d", lc, e.CellID, e.RegionID, reg)
		}
	}
	cp := make([]Entry, len(entries))
	copy(cp, entries)
	return &StaticTable{entries: cp}, nil
}

// Lookup 实现 Table。
func (t *StaticTable) Lookup(logicalCell uint32) (Entry, bool) {
	if uint64(logicalCell) >= uint64(len(t.entries)) {
		return Entry{}, false
	}
	return t.entries[logicalCell], true
}

// Len 实现 Table。
func (t *StaticTable) Len() int { return len(t.entries) }

// LogicalCellOf 把 player_id 映射到 logical_cell(确定性取模)。
// 单独导出便于迁移灰度判定与测试,无需经 Router。
func LogicalCellOf(playerID uint64) uint32 {
	return uint32(playerID % LogicalCellCount)
}

// Router 按 player_id 路由到 (RegionID, CellID)。持有一张 Table;并发安全前提是
// Table 实现本身只读(StaticTable 不可变)。热更新映射走"构造新 Router / 原子替换 Table 指针"。
type Router struct {
	table Table
}

// NewRouter 用给定映射表构造路由器。table 为 nil 或长度不符返回错误。
func NewRouter(table Table) (*Router, error) {
	if table == nil {
		return nil, fmt.Errorf("cellroute: nil table")
	}
	if uint64(table.Len()) != LogicalCellCount {
		return nil, fmt.Errorf("cellroute: table len %d != LogicalCellCount %d", table.Len(), LogicalCellCount)
	}
	return &Router{table: table}, nil
}

// Route 返回 player_id 的物理落点。映射表缺该 logical_cell 项时返回错误
// (正常配置不应发生,属配置缺口,调用方应 fail-fast 而非默认落 0 号 Cell)。
func (r *Router) Route(playerID uint64) (Location, error) {
	lc := LogicalCellOf(playerID)
	e, ok := r.table.Lookup(lc)
	if !ok {
		return Location{}, fmt.Errorf("cellroute: logical_cell %d not mapped (player_id=%d)", lc, playerID)
	}
	return Location{RegionID: e.RegionID, CellID: e.CellID, LogicalCell: lc}, nil
}

// CellSpec 描述一个物理 Cell 归属(用于 BuildBalancedEntries 初始铺表)。
type CellSpec struct {
	RegionID uint32
	CellID   uint32
}

// BuildBalancedEntries 把 LogicalCellCount 个逻辑分片尽量均匀地连续切给给定的物理 Cell 列表,
// 返回可直接喂给 NewStaticTable 的 entries 和配套 regionOfCell。用于初始部署 / 测试铺表;
// 真实扩缩容时改用"迁移部分区间"的灰度流程,不重铺全表。
//
// 连续区间分配(非 round-robin)便于扩容时按区间迁移:cells 不可为空,且 LogicalCellCount
// 应能被合理分摊(余数摊到前几个 Cell)。
func BuildBalancedEntries(cells []CellSpec) ([]Entry, map[uint32]uint32, error) {
	if len(cells) == 0 {
		return nil, nil, fmt.Errorf("cellroute: empty cells")
	}
	regionOfCell := make(map[uint32]uint32, len(cells))
	for _, c := range cells {
		if reg, ok := regionOfCell[c.CellID]; ok && reg != c.RegionID {
			return nil, nil, fmt.Errorf("cellroute: cell %d declared in two regions %d and %d", c.CellID, reg, c.RegionID)
		}
		regionOfCell[c.CellID] = c.RegionID
	}

	n := uint64(len(cells))
	base := LogicalCellCount / n
	rem := LogicalCellCount % n

	entries := make([]Entry, 0, LogicalCellCount)
	for i, c := range cells {
		span := base
		if uint64(i) < rem {
			span++ // 余数摊到前 rem 个 Cell,保证总和恰为 LogicalCellCount
		}
		for j := uint64(0); j < span; j++ {
			entries = append(entries, Entry{RegionID: c.RegionID, CellID: c.CellID})
		}
	}
	if uint64(len(entries)) != LogicalCellCount {
		return nil, nil, fmt.Errorf("cellroute: built %d entries != LogicalCellCount %d", len(entries), LogicalCellCount)
	}
	return entries, regionOfCell, nil
}
