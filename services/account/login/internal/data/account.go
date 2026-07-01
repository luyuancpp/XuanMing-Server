// Package data 是 login 服务的"数据层"(repository)。
//
// W3 ②(2026-06-05)真实化:
//   - MySQL: pandora_account.accounts / account_devices / account_bans 三表
//   - Redis: pandora:sess:<player_id>      (hash, TTL 24h)        session 状态
//   - Redis: pandora:ticket:<jti>          (string, TTL 5min)     DSTicket 防重放(SETNX)
package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/luyuancpp/pandora/pkg/errcode"
)

// AccountRepo 是账号数据访问接口。biz 层依赖本接口,而不是具体实现,
// 方便在 mock / mysql 实现之间切换不动 biz/service。
type AccountRepo interface {
	// FindByAccount 根据账号名查 player_id + bcrypt 哈希后的密码。
	// 找不到返回 ErrLoginAccountNotFound。
	FindByAccount(ctx context.Context, account string) (playerID uint64, passwordHash string, err error)

	// CreateAccount 新建账号(snowflake 分配的 playerID 传入)。
	// 账号已存在返回 ErrAlreadyExists。
	CreateAccount(ctx context.Context, playerID uint64, account, bcryptHash string) error

	// CheckBanned 检查账号 / 设备是否在有效封禁期内(account_bans 表 expires_at>now 或 NULL)。
	CheckBanned(ctx context.Context, playerID uint64, deviceID string) (banned bool, err error)

	// TouchDevice 记录最近一次登录设备(account_devices upsert)。失败由 biz 层只记日志。
	TouchDevice(ctx context.Context, playerID uint64, deviceID string) error
}

// =====================================================================
// MySQLAccountRepo:W3 ② 真实实现。
// =====================================================================

// MySQLAccountRepo 基于 *sql.DB 的账号仓储。
type MySQLAccountRepo struct {
	db *sql.DB
}

// NewMySQLAccountRepo 构造。db 由 pkg/mysqlx.MustNewClient 提供。
func NewMySQLAccountRepo(db *sql.DB) *MySQLAccountRepo {
	return &MySQLAccountRepo{db: db}
}

func (r *MySQLAccountRepo) FindByAccount(ctx context.Context, account string) (uint64, string, error) {
	const q = `SELECT player_id, password_hash FROM accounts WHERE account = ? LIMIT 1`
	var (
		playerID uint64
		hash     string
	)
	err := r.db.QueryRowContext(ctx, q, account).Scan(&playerID, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", errcode.New(errcode.ErrLoginAccountNotFound, "account=%s not found", account)
	}
	if err != nil {
		return 0, "", errcode.New(errcode.ErrInternal, "mysql find account: %v", err)
	}
	return playerID, hash, nil
}

func (r *MySQLAccountRepo) CreateAccount(ctx context.Context, playerID uint64, account, bcryptHash string) error {
	const q = `INSERT INTO accounts(player_id, account, password_hash) VALUES (?, ?, ?)`
	_, err := r.db.ExecContext(ctx, q, playerID, account, bcryptHash)
	if err != nil {
		// 1062 = ER_DUP_ENTRY,字符串匹配避免强依赖 mysql driver 错误类型
		if isDupErr(err) {
			return errcode.New(errcode.ErrAlreadyExists, "account=%s already exists", account)
		}
		return errcode.New(errcode.ErrInternal, "mysql create account: %v", err)
	}
	return nil
}

func (r *MySQLAccountRepo) CheckBanned(ctx context.Context, playerID uint64, deviceID string) (bool, error) {
	const q = `SELECT COUNT(*) FROM account_bans
WHERE (expires_at IS NULL OR expires_at > UTC_TIMESTAMP())
  AND ((player_id IS NOT NULL AND player_id = ?) OR (device_id IS NOT NULL AND device_id = ?))`
	var cnt int
	if err := r.db.QueryRowContext(ctx, q, playerID, deviceID).Scan(&cnt); err != nil {
		return false, errcode.New(errcode.ErrInternal, "mysql check banned: %v", err)
	}
	return cnt > 0, nil
}

func (r *MySQLAccountRepo) TouchDevice(ctx context.Context, playerID uint64, deviceID string) error {
	if deviceID == "" {
		return nil
	}
	const q = `INSERT INTO account_devices(player_id, device_id, last_login_at)
VALUES (?, ?, UTC_TIMESTAMP())
ON DUPLICATE KEY UPDATE last_login_at = UTC_TIMESTAMP()`
	if _, err := r.db.ExecContext(ctx, q, playerID, deviceID); err != nil {
		return errcode.New(errcode.ErrInternal, "mysql touch device: %v", err)
	}
	return nil
}

// isDupErr 粗略判断 MySQL 唯一键冲突,不依赖 mysql driver 强类型。
func isDupErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "1062") || strings.Contains(s, "Duplicate entry")
}

// =====================================================================
// SessionRepo:Redis 上的玩家 session 状态。
// =====================================================================

// SessionRepo 维护 pandora:sess:<player_id> hash + TTL。
//
// hash 字段:
//
//	token      string  当前签的 session JWT(全文,debug 用)
//	jti        string  session JWT 的 jti(便于将来 jti 黑名单)
//	device_id  string  当前设备
//	exp_ms     int64   session 过期 unix ms
type SessionRepo interface {
	Set(ctx context.Context, playerID uint64, token, jti, deviceID string, ttl time.Duration) error
	Delete(ctx context.Context, playerID uint64) error
}

// RedisSessionRepo 基于 go-redis/v9 的 SessionRepo 实现。
type RedisSessionRepo struct {
	rdb redis.UniversalClient
}

// NewRedisSessionRepo 构造。
func NewRedisSessionRepo(rdb redis.UniversalClient) *RedisSessionRepo {
	return &RedisSessionRepo{rdb: rdb}
}

func sessKey(playerID uint64) string {
	return fmt.Sprintf("pandora:sess:%d", playerID)
}

func (r *RedisSessionRepo) Set(ctx context.Context, playerID uint64, token, jti, deviceID string, ttl time.Duration) error {
	key := sessKey(playerID)
	pipe := r.rdb.TxPipeline()
	pipe.HSet(ctx, key,
		"token", token,
		"jti", jti,
		"device_id", deviceID,
		"exp_ms", time.Now().Add(ttl).UnixMilli(),
	)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return errcode.New(errcode.ErrInternal, "redis sess set: %v", err)
	}
	return nil
}

func (r *RedisSessionRepo) Delete(ctx context.Context, playerID uint64) error {
	if err := r.rdb.Del(ctx, sessKey(playerID)).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return errcode.New(errcode.ErrInternal, "redis sess del: %v", err)
	}
	return nil
}

// =====================================================================
// TicketJTIRepo:DSTicket 防重放(Verify 时 SETNX)。
// =====================================================================

// TicketJTIRepo 维护 pandora:ticket:<jti> 短期标记。
//
// 语义:首次 Verify 时 SETNX 成功 → 票据可用;再次 SETNX 失败 → ErrLoginTicketReplayed。
type TicketJTIRepo interface {
	MarkUsed(ctx context.Context, jti string, ttl time.Duration) error
}

// RedisTicketJTIRepo 基于 go-redis/v9 的 TicketJTIRepo 实现。
type RedisTicketJTIRepo struct {
	rdb redis.UniversalClient
}

// NewRedisTicketJTIRepo 构造。
func NewRedisTicketJTIRepo(rdb redis.UniversalClient) *RedisTicketJTIRepo {
	return &RedisTicketJTIRepo{rdb: rdb}
}

func ticketKey(jti string) string {
	return fmt.Sprintf("pandora:ticket:%s", jti)
}

func (r *RedisTicketJTIRepo) MarkUsed(ctx context.Context, jti string, ttl time.Duration) error {
	if jti == "" {
		return errcode.New(errcode.ErrInvalidArg, "empty jti")
	}
	ok, err := r.rdb.SetNX(ctx, ticketKey(jti), 1, ttl).Result()
	if err != nil {
		return errcode.New(errcode.ErrInternal, "redis ticket setnx: %v", err)
	}
	if !ok {
		return errcode.New(errcode.ErrLoginTicketReplayed, "ticket jti=%s already used", jti)
	}
	return nil
}
