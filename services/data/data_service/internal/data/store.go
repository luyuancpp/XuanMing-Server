// Package data 是 data_service 的数据层(MySQL 版本化 blob + Redis 缓存,2026-06-16)。
//
// 库表(deploy/mysql-init/07-data-tables.sql,pandora_player 库):
//
//	player_data  玩家数据 blob(player_id PK;version 乐观锁;data 为序列化 PlayerProfile bytes)
//
// 表是结构化列直接映射(CLAUDE.md §5.9 关系型表不强制 proto bytes blob);
// data 列本身是上层业务序列化好的不透明 bytes,data_service 不解释其内容。
package data

import (
	"context"
	"database/sql"
	"strings"

	"github.com/luyuancpp/pandora/pkg/errcode"
	datav1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/data_service/v1"
)

// PlayerStore 是玩家数据的持久层抽象。biz 只依赖此接口,不依赖 *sql.DB。
type PlayerStore interface {
	// Read 读玩家数据。not found → (nil, false, nil)。
	Read(ctx context.Context, playerID uint64) (*datav1.PlayerData, bool, error)

	// Write 乐观锁写:
	//   expectVersion == 0 → 视为新建,INSERT(冲突即已存在 → ErrDataVersionMismatch);
	//   expectVersion  > 0 → UPDATE ... WHERE player_id=? AND version=expectVersion,
	//                        受影响行 0(版本不匹配 / 不存在)→ ErrDataVersionMismatch。
	// 成功返回写入后的新版本号(= expectVersion + 1)。
	Write(ctx context.Context, playerID uint64, expectVersion int32, data []byte) (int32, error)
}

// MySQLPlayerStore 是基于 database/sql 的 PlayerStore 实现。
type MySQLPlayerStore struct {
	db *sql.DB
}

// NewMySQLPlayerStore 构造。db 由 pkg/mysqlx.MustNewClient 提供(连 pandora_player 库)。
func NewMySQLPlayerStore(db *sql.DB) *MySQLPlayerStore {
	return &MySQLPlayerStore{db: db}
}

func (s *MySQLPlayerStore) Read(ctx context.Context, playerID uint64) (*datav1.PlayerData, bool, error) {
	const q = `SELECT version, data FROM player_data WHERE player_id = ?`
	var (
		version int32
		data    []byte
	)
	err := s.db.QueryRowContext(ctx, q, playerID).Scan(&version, &data)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errcode.New(errcode.ErrInternal, "read player_data %d: %v", playerID, err)
	}
	return &datav1.PlayerData{PlayerId: playerID, Version: version, Data: data}, true, nil
}

func (s *MySQLPlayerStore) Write(ctx context.Context, playerID uint64, expectVersion int32, data []byte) (int32, error) {
	if expectVersion == 0 {
		// 新建:INSERT 起始版本 1。主键冲突说明已存在(并发或客户端版本陈旧)→ 版本不匹配。
		const ins = `INSERT INTO player_data (player_id, version, data) VALUES (?, 1, ?)`
		if _, err := s.db.ExecContext(ctx, ins, playerID, data); err != nil {
			if isDuplicateKey(err) {
				return 0, errcode.New(errcode.ErrDataVersionMismatch,
					"player_data %d already exists (expect new)", playerID)
			}
			return 0, errcode.New(errcode.ErrInternal, "insert player_data %d: %v", playerID, err)
		}
		return 1, nil
	}

	// 更新:乐观锁 WHERE version 匹配,版本 +1。
	const upd = `UPDATE player_data SET version = version + 1, data = ?
WHERE player_id = ? AND version = ?`
	res, err := s.db.ExecContext(ctx, upd, data, playerID, expectVersion)
	if err != nil {
		return 0, errcode.New(errcode.ErrInternal, "update player_data %d: %v", playerID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, errcode.New(errcode.ErrInternal, "rows affected player_data %d: %v", playerID, err)
	}
	if rows == 0 {
		return 0, errcode.New(errcode.ErrDataVersionMismatch,
			"player_data %d version mismatch (expect %d)", playerID, expectVersion)
	}
	return expectVersion + 1, nil
}

// isDuplicateKey 判断是否 MySQL 主键 / 唯一键冲突(error 1062)。
// 不直接 import go-sql-driver 类型,按错误文本兜底匹配,避免引额外类型耦合。
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate entry") || strings.Contains(msg, "Error 1062")
}
