// Package data 是 trade 服务的数据层(订单存 Redis,2026-06-16)。
//
// Redis key 模板(所有业务 ID 用 uint64,%d 格式化):
//
//	pandora:trade:order:{%d}   → protobuf bytes(trade/v1.Order)
//	                             hashtag {} 确保同订单的 key 落同一 redis cluster slot(兜底)
//	pandora:trade:player:%d    → set(成员是 order_id,uint64 文本),供 ListMyOrders 反查
//
// 订单主体直接使用 proto trade/v1.Order 序列化为 bytes 存 Redis value:
//   - Order 已是完整的客户端可见结构,且无服务端独有隐藏字段,故存储 / 视图同构,
//     不再额外造 OrderStorageRecord(CLAUDE.md §5.10 仅在有存储独有字段时强制分离);
//   - 结算扣减的幂等键 = order_id,由 biz 层 ResourceLedger 保证,不落在 Order 里。
//
// 状态机写用 WATCH/MULTI/EXEC 乐观锁:
//
//	GET(proto bytes) → fn(modify) → MULTI/SET/EXEC
//	EXEC 失败(key 被并发改) → 重试至 maxRetry → 返 ErrTradeLockFailed(7005)
package data

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"

	"github.com/luyuancpp/pandora/pkg/errcode"
	tradev1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/trade/v1"
)

// orderKey returns "pandora:trade:order:{orderID}" — hashtag 括住 orderID 保 cluster slot 一致。
func orderKey(orderID uint64) string {
	return fmt.Sprintf("pandora:trade:order:{%d}", orderID)
}

// playerKey returns "pandora:trade:player:playerID"(set of order_id)。
func playerKey(playerID uint64) string {
	return fmt.Sprintf("pandora:trade:player:%d", playerID)
}

// TradeRepo 是 trade 数据层抽象。biz 只依赖此接口,不依赖 redis。
type TradeRepo interface {
	// CreateOrder 写订单 proto value(TTL=orderTTL)+ 把 order_id 加入买卖双方的 player set。
	CreateOrder(ctx context.Context, order *tradev1.Order, orderTTL time.Duration) error

	// GetOrder 读订单。not found → (nil, false, nil)。
	GetOrder(ctx context.Context, orderID uint64) (*tradev1.Order, bool, error)

	// UpdateWithLock WATCH/MULTI/EXEC 读-改-写订单 value。
	//   fn 返回业务错误 → 透传不重试;EXEC 冲突 → 重试,耗尽返 ErrTradeLockFailed。
	UpdateWithLock(ctx context.Context, orderID uint64, maxRetry int, fn func(*tradev1.Order) error, orderTTL time.Duration) error

	// ListPlayerOrderIDs 读玩家 order set 里的全部 order_id。
	ListPlayerOrderIDs(ctx context.Context, playerID uint64) ([]uint64, error)
}

// RedisTradeRepo 是基于 go-redis/v9 的 TradeRepo 实现。
type RedisTradeRepo struct {
	rdb *redis.Client
}

// NewRedisTradeRepo 构造。
func NewRedisTradeRepo(rdb *redis.Client) *RedisTradeRepo {
	return &RedisTradeRepo{rdb: rdb}
}

func (r *RedisTradeRepo) CreateOrder(ctx context.Context, order *tradev1.Order, orderTTL time.Duration) error {
	payload, err := proto.Marshal(order)
	if err != nil {
		return errcode.New(errcode.ErrInternal, "marshal order %d: %v", order.GetOrderId(), err)
	}
	pipe := r.rdb.TxPipeline()
	pipe.Set(ctx, orderKey(order.GetOrderId()), payload, orderTTL)
	idStr := strconv.FormatUint(order.GetOrderId(), 10)
	pipe.SAdd(ctx, playerKey(order.GetSellerId()), idStr)
	pipe.Expire(ctx, playerKey(order.GetSellerId()), orderTTL)
	if order.GetBuyerId() != 0 {
		pipe.SAdd(ctx, playerKey(order.GetBuyerId()), idStr)
		pipe.Expire(ctx, playerKey(order.GetBuyerId()), orderTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return errcode.New(errcode.ErrInternal, "create order %d: %v", order.GetOrderId(), err)
	}
	return nil
}

func (r *RedisTradeRepo) GetOrder(ctx context.Context, orderID uint64) (*tradev1.Order, bool, error) {
	b, err := r.rdb.Get(ctx, orderKey(orderID)).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errcode.New(errcode.ErrInternal, "get order %d: %v", orderID, err)
	}
	order := &tradev1.Order{}
	if err := proto.Unmarshal(b, order); err != nil {
		return nil, false, errcode.New(errcode.ErrInternal, "unmarshal order %d: %v", orderID, err)
	}
	return order, true, nil
}

func (r *RedisTradeRepo) UpdateWithLock(
	ctx context.Context,
	orderID uint64,
	maxRetry int,
	fn func(*tradev1.Order) error,
	orderTTL time.Duration,
) error {
	key := orderKey(orderID)

	for attempt := 0; attempt <= maxRetry; attempt++ {
		var fnErr error

		txErr := r.rdb.Watch(ctx, func(tx *redis.Tx) error {
			b, err := tx.Get(ctx, key).Bytes()
			if err == redis.Nil {
				return errcode.New(errcode.ErrTradeOrderNotFound, "order %d not found", orderID)
			}
			if err != nil {
				return err
			}
			order := &tradev1.Order{}
			if err := proto.Unmarshal(b, order); err != nil {
				return errcode.New(errcode.ErrInternal, "unmarshal order %d: %v", orderID, err)
			}

			if fnErr = fn(order); fnErr != nil {
				return fnErr
			}

			payload, err := proto.Marshal(order)
			if err != nil {
				return errcode.New(errcode.ErrInternal, "marshal order %d: %v", orderID, err)
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, payload, orderTTL)
				return nil
			})
			return err
		}, key)

		if txErr == nil {
			return nil
		}
		// fn 自身返回的业务错误 — 不重试,直接透传。
		if fnErr != nil && txErr == fnErr {
			return fnErr
		}
		// WATCH 冲突 — 重试。
		if txErr == redis.TxFailedErr {
			continue
		}
		// 其他 redis 错误 — 不重试。
		return txErr
	}
	return errcode.New(errcode.ErrTradeLockFailed, "order %d update concurrent retry exhausted", orderID)
}

func (r *RedisTradeRepo) ListPlayerOrderIDs(ctx context.Context, playerID uint64) ([]uint64, error) {
	members, err := r.rdb.SMembers(ctx, playerKey(playerID)).Result()
	if err != nil {
		return nil, errcode.New(errcode.ErrInternal, "list player orders %d: %v", playerID, err)
	}
	ids := make([]uint64, 0, len(members))
	for _, m := range members {
		id, perr := strconv.ParseUint(m, 10, 64)
		if perr != nil {
			continue // 跳过脏成员
		}
		ids = append(ids, id)
	}
	return ids, nil
}
