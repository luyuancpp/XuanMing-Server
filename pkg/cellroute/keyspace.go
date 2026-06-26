// keyspace.go — cellroute 的第 3 层(Cell 内分片)与 Cell 作用域命名空间。
//
// cellroute.go 负责最外两层 Region → Cell;本文件补上第 3 层"Cell 内分片"的确定性计算,
// 以及给 Cell 作用域资源(Redis key 前缀 / Kafka consumer group / metrics 维度)用的规范
// 命名标签,把三层路由的"算落点"收敛到同一处、统一测试。
//
// 对照 scale-cellular-20m.md §3.2/§4.2 的三层定位:
//
//	logical_cell  = player_id % LogicalCellCount           // 第 1 步(cellroute.go)
//	(region,cell) = Table.Lookup(logical_cell)             // 第 2 步(cellroute.go)
//	in_cell_shard = player_id % shardsPerCell              // 第 3 步(本文件,MySQL 分库下标)
//
// 说明:
//   - 第 3 层的 MySQL 分库**仍由 mysqlx.ShardSet.For(player_id) 在各 Cell 内独立选库**
//     (owner 数据已落对的 Cell,Cell 内再按 player_id % N 分库)。InCellShard 在这里再导出
//     一份**同口径**的纯计算,用于不持有 *sql.DB 的场景(日志 / 迁移灰度判定 / 路由自检 /
//     给运维算"某 player 落哪个分库")。两者公式一致(player_id % N),不引入第二套口径。
//   - Redis Cluster 的 Cell 内 slot 由客户端 CRC16(key) 原生决定,不在此计算;本文件只提供
//     Cell 作用域 key 前缀,避免多 Cell 部署时 key 撞车。
package cellroute

import "fmt"

// FullLocation 是三层全路径定位结果:Region / Cell / Cell 内 MySQL 分库下标。
// 在 Location(Region+Cell)基础上补 InCellShard,供需要落到具体分库的调用方一次拿全。
type FullLocation struct {
	RegionID    uint32
	CellID      uint32
	LogicalCell uint32
	// InCellShard 是 owner Cell 内的 MySQL 分库下标(player_id % shardsPerCell)。
	// shardsPerCell==1(Cell 内单库)时恒为 0。
	InCellShard int
	// ShardsPerCell 是计算 InCellShard 时所用的 Cell 内分库数,回带便于调用方校验 / 日志。
	ShardsPerCell int
}

// InCellShard 计算 player_id 在其 owner Cell 内的 MySQL 分库下标:player_id % shardsPerCell。
//
// 与 mysqlx.ShardSet.For 同口径(都是 id % N),此处导出纯计算供不持有 *sql.DB 的场景使用。
// shardsPerCell 必须 ≥ 1;否则返回错误(避免除零并强制调用方显式声明分库数,单库传 1)。
func InCellShard(playerID uint64, shardsPerCell int) (int, error) {
	if shardsPerCell < 1 {
		return 0, fmt.Errorf("cellroute: shardsPerCell %d < 1", shardsPerCell)
	}
	return int(playerID % uint64(shardsPerCell)), nil
}

// RouteFull 返回 player_id 的三层全路径定位。先经 Table 得 Region+Cell(第 1/2 层),
// 再算 Cell 内 MySQL 分库下标(第 3 层)。shardsPerCell 是 owner Cell 的分库数(单库传 1)。
//
// 注:本实现按"全服 Cell 内分库数一致"建模(shardsPerCell 由调用方传入);若未来各 Cell
// 分库数不同,改为从 Cell 拓扑表查 shardsPerCell 即可,RouteFull 签名不变。
func (r *Router) RouteFull(playerID uint64, shardsPerCell int) (FullLocation, error) {
	loc, err := r.Route(playerID)
	if err != nil {
		return FullLocation{}, err
	}
	shard, err := InCellShard(playerID, shardsPerCell)
	if err != nil {
		return FullLocation{}, err
	}
	return FullLocation{
		RegionID:      loc.RegionID,
		CellID:        loc.CellID,
		LogicalCell:   loc.LogicalCell,
		InCellShard:   shard,
		ShardsPerCell: shardsPerCell,
	}, nil
}

// CellTag 返回一个规范的 Cell 作用域标签 "r<region>c<cell>"(如 r1c7),用于给 Cell 作用域
// 资源命名:Redis key 前缀、Kafka consumer group 后缀、metrics 低基数维度等,避免多 Cell
// 共享底层存储 / 总线时 key 撞车。
//
// 仅含 region/cell 两个拓扑维度(均低基数),可安全用作 prometheus label;不要把 player_id
// 这类高基数业务 ID 拼进来(CLAUDE.md §12 禁止高基数 label)。
func CellTag(regionID, cellID uint32) string {
	return fmt.Sprintf("r%dc%d", regionID, cellID)
}
