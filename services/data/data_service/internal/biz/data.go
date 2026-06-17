// Package biz 是 data_service 的业务逻辑层(cache-aside 读写编排,2026-06-16)。
//
// 职责(docs/design/go-services.md §2.3):
//   - ReadPlayer:Redis 命中直返;miss 读 MySQL → 回填缓存 → 返回
//   - WritePlayer:MySQL 乐观锁版本写(UPDATE ... WHERE version=?)→ 删缓存
//   - InvalidateCache:主动删缓存
//
// 一致性约定:
//   - MySQL 是事实源(source of truth),Redis 仅旁路缓存,弱一致;
//   - 写采用 cache-aside「先写库、后删缓存」,删失败只告警不回滚(缓存最终随 TTL 失效);
//   - 不接 kafka:避免与 player.update 事件语义重复,缓存失效靠写后删 + 主动 InvalidateCache。
package biz

import (
	"context"

	klog "github.com/go-kratos/kratos/v2/log"

	"github.com/luyuancpp/pandora/pkg/errcode"
	plog "github.com/luyuancpp/pandora/pkg/log"
	datav1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/data_service/v1"

	"github.com/luyuancpp/pandora/services/data/data_service/internal/conf"
	"github.com/luyuancpp/pandora/services/data/data_service/internal/data"
)

// DataUsecase 是 data_service 业务逻辑核心。
type DataUsecase struct {
	store data.PlayerStore
	cache data.PlayerCache // 弱依赖,可为 nil(无缓存时直连 MySQL)
	cfg   conf.DataConf
	log   *klog.Helper
}

// NewDataUsecase 构造。cache 允许为 nil(缓存未配置时退化为直连 MySQL)。
func NewDataUsecase(store data.PlayerStore, cache data.PlayerCache, cfg conf.DataConf, logger klog.Logger) *DataUsecase {
	return &DataUsecase{
		store: store,
		cache: cache,
		cfg:   cfg,
		log:   plog.NewHelper(logger),
	}
}

// ReadPlayer cache-aside 读:缓存命中直返;miss 读 MySQL 并回填缓存。
//   - 玩家无数据 → (nil, false, nil),由 service 转 ErrNotFound。
func (u *DataUsecase) ReadPlayer(ctx context.Context, playerID uint64) (*datav1.PlayerData, bool, error) {
	if playerID == 0 {
		return nil, false, nil
	}

	// 1) 查缓存。读失败只告警,继续回落 MySQL。
	if u.cache != nil {
		if pd, hit, err := u.cache.Get(ctx, playerID); err != nil {
			u.log.WithContext(ctx).Warnf("cache get player %d failed: %v", playerID, err)
		} else if hit {
			return pd, true, nil
		}
	}

	// 2) 读 MySQL。
	pd, found, err := u.store.Read(ctx, playerID)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}

	// 3) 回填缓存(失败只告警)。
	u.fillCache(ctx, pd)
	return pd, true, nil
}

// WritePlayer 乐观锁写 MySQL,成功后删缓存(cache-aside 先写库后删缓存)。
// 返回写入后的新版本号。版本不匹配 → ErrDataVersionMismatch。
func (u *DataUsecase) WritePlayer(ctx context.Context, pd *datav1.PlayerData) (int32, error) {
	if pd.GetPlayerId() == 0 {
		return 0, errInvalidPlayer()
	}

	newVersion, err := u.store.Write(ctx, pd.GetPlayerId(), pd.GetVersion(), pd.GetData())
	if err != nil {
		return 0, err
	}

	// 写后删缓存(避免读到旧版本)。删失败只告警,缓存随 TTL 自然失效。
	if u.cache != nil {
		if err := u.cache.Del(ctx, pd.GetPlayerId()); err != nil {
			u.log.WithContext(ctx).Warnf("cache del after write player %d failed: %v", pd.GetPlayerId(), err)
		}
	}
	return newVersion, nil
}

// InvalidateCache 主动删缓存(供上层在外部直写 DB 后强制失效)。
func (u *DataUsecase) InvalidateCache(ctx context.Context, playerID uint64) error {
	if playerID == 0 {
		return errInvalidPlayer()
	}
	if u.cache == nil {
		return nil
	}
	if err := u.cache.Del(ctx, playerID); err != nil {
		u.log.WithContext(ctx).Warnf("invalidate cache player %d failed: %v", playerID, err)
		return err
	}
	return nil
}

// fillCache 回填缓存,失败只告警(缓存是旁路,不影响读正确性)。
func (u *DataUsecase) fillCache(ctx context.Context, pd *datav1.PlayerData) {
	if u.cache == nil {
		return
	}
	if err := u.cache.Set(ctx, pd, u.cfg.CacheTTL.Std()); err != nil {
		u.log.WithContext(ctx).Warnf("cache set player %d failed: %v", pd.GetPlayerId(), err)
	}
}
// errInvalidPlayer 返回 player_id 缺失的参数错误。
func errInvalidPlayer() error {
	return errcode.New(errcode.ErrInvalidArg, "player_id required")
}