// data_test.go —— DataUsecase 业务逻辑单测(2026-06-16)。
//
// 用内存版 fakeStore / fakeCache 复刻 MySQL 乐观锁 + Redis 缓存语义,无需真依赖。
// 覆盖:缓存命中 / miss 回填 / 写后删缓存 / 乐观锁版本冲突 / 新建 / 主动失效 / 降级无缓存。
package biz

import (
	"context"
	"testing"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"

	"github.com/luyuancpp/pandora/pkg/config"
	"github.com/luyuancpp/pandora/pkg/errcode"
	datav1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/data_service/v1"

	"github.com/luyuancpp/pandora/services/data/data_service/internal/conf"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

type row struct {
	version int32
	data    []byte
}

type fakeStore struct {
	rows      map[uint64]*row
	readCalls int
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[uint64]*row{}} }

func (s *fakeStore) Read(_ context.Context, playerID uint64) (*datav1.PlayerData, bool, error) {
	s.readCalls++
	r, ok := s.rows[playerID]
	if !ok {
		return nil, false, nil
	}
	return &datav1.PlayerData{PlayerId: playerID, Version: r.version, Data: r.data}, true, nil
}

func (s *fakeStore) Write(_ context.Context, playerID uint64, expectVersion int32, data []byte) (int32, error) {
	r, ok := s.rows[playerID]
	if expectVersion == 0 {
		if ok {
			return 0, errcode.New(errcode.ErrDataVersionMismatch, "exists")
		}
		s.rows[playerID] = &row{version: 1, data: data}
		return 1, nil
	}
	if !ok || r.version != expectVersion {
		return 0, errcode.New(errcode.ErrDataVersionMismatch, "mismatch")
	}
	r.version++
	r.data = data
	return r.version, nil
}

type fakeCache struct {
	m         map[uint64]*datav1.PlayerData
	getCalls  int
	setCalls  int
	delCalls  int
}

func newFakeCache() *fakeCache { return &fakeCache{m: map[uint64]*datav1.PlayerData{}} }

func (c *fakeCache) Get(_ context.Context, playerID uint64) (*datav1.PlayerData, bool, error) {
	c.getCalls++
	pd, ok := c.m[playerID]
	if !ok {
		return nil, false, nil
	}
	return pd, true, nil
}

func (c *fakeCache) Set(_ context.Context, pd *datav1.PlayerData, _ time.Duration) error {
	c.setCalls++
	c.m[pd.GetPlayerId()] = pd
	return nil
}

func (c *fakeCache) Del(_ context.Context, playerID uint64) error {
	c.delCalls++
	delete(c.m, playerID)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newUC(store *fakeStore, cache *fakeCache) *DataUsecase {
	cfg := conf.DataConf{CacheTTL: config.Duration(5 * time.Minute)}
	return NewDataUsecase(store, cache, cfg, klog.DefaultLogger)
}

func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	if errcode.As(err) != code {
		t.Fatalf("want code %d, got err=%v (code=%d)", code, err, errcode.As(err))
	}
}

// ── 测试 ───────────────────────────────────────────────────────────────────────

func TestRead_CacheHit(t *testing.T) {
	store := newFakeStore()
	cache := newFakeCache()
	cache.m[1] = &datav1.PlayerData{PlayerId: 1, Version: 3, Data: []byte("cached")}
	uc := newUC(store, cache)

	pd, found, err := uc.ReadPlayer(context.Background(), 1)
	if err != nil || !found {
		t.Fatalf("want hit, got found=%v err=%v", found, err)
	}
	if string(pd.GetData()) != "cached" {
		t.Fatalf("want cached data, got %q", pd.GetData())
	}
	if store.readCalls != 0 {
		t.Fatalf("cache hit should not touch store, readCalls=%d", store.readCalls)
	}
}

func TestRead_MissBackfill(t *testing.T) {
	store := newFakeStore()
	store.rows[1] = &row{version: 2, data: []byte("db")}
	cache := newFakeCache()
	uc := newUC(store, cache)

	pd, found, err := uc.ReadPlayer(context.Background(), 1)
	if err != nil || !found {
		t.Fatalf("want found, got found=%v err=%v", found, err)
	}
	if pd.GetVersion() != 2 {
		t.Fatalf("want version 2, got %d", pd.GetVersion())
	}
	if cache.setCalls != 1 {
		t.Fatalf("want 1 backfill set, got %d", cache.setCalls)
	}
	if _, ok := cache.m[1]; !ok {
		t.Fatalf("cache not backfilled")
	}
}

func TestRead_NotFound(t *testing.T) {
	uc := newUC(newFakeStore(), newFakeCache())
	_, found, err := uc.ReadPlayer(context.Background(), 99)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if found {
		t.Fatalf("want not found")
	}
}

func TestWrite_NewPlayer(t *testing.T) {
	store := newFakeStore()
	cache := newFakeCache()
	uc := newUC(store, cache)

	v, err := uc.WritePlayer(context.Background(), &datav1.PlayerData{PlayerId: 1, Version: 0, Data: []byte("v1")})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if v != 1 {
		t.Fatalf("want version 1, got %d", v)
	}
}

func TestWrite_OptimisticOK(t *testing.T) {
	store := newFakeStore()
	store.rows[1] = &row{version: 5, data: []byte("old")}
	cache := newFakeCache()
	cache.m[1] = &datav1.PlayerData{PlayerId: 1, Version: 5, Data: []byte("old")}
	uc := newUC(store, cache)

	v, err := uc.WritePlayer(context.Background(), &datav1.PlayerData{PlayerId: 1, Version: 5, Data: []byte("new")})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if v != 6 {
		t.Fatalf("want version 6, got %d", v)
	}
	// 写后缓存应被删。
	if _, ok := cache.m[1]; ok {
		t.Fatalf("cache should be deleted after write")
	}
	if cache.delCalls != 1 {
		t.Fatalf("want 1 del, got %d", cache.delCalls)
	}
}

func TestWrite_VersionMismatch(t *testing.T) {
	store := newFakeStore()
	store.rows[1] = &row{version: 5, data: []byte("old")}
	uc := newUC(store, newFakeCache())

	_, err := uc.WritePlayer(context.Background(), &datav1.PlayerData{PlayerId: 1, Version: 3, Data: []byte("stale")})
	wantCode(t, err, errcode.ErrDataVersionMismatch)
}

func TestWrite_NoPlayerID(t *testing.T) {
	uc := newUC(newFakeStore(), newFakeCache())
	_, err := uc.WritePlayer(context.Background(), &datav1.PlayerData{PlayerId: 0})
	wantCode(t, err, errcode.ErrInvalidArg)
}

func TestInvalidateCache(t *testing.T) {
	cache := newFakeCache()
	cache.m[1] = &datav1.PlayerData{PlayerId: 1}
	uc := newUC(newFakeStore(), cache)

	if err := uc.InvalidateCache(context.Background(), 1); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if _, ok := cache.m[1]; ok {
		t.Fatalf("cache not invalidated")
	}
}

func TestRead_NoCache_DirectStore(t *testing.T) {
	store := newFakeStore()
	store.rows[1] = &row{version: 1, data: []byte("db")}
	uc := NewDataUsecase(store, nil, conf.DataConf{CacheTTL: config.Duration(time.Minute)}, klog.DefaultLogger)

	pd, found, err := uc.ReadPlayer(context.Background(), 1)
	if err != nil || !found {
		t.Fatalf("want found, got found=%v err=%v", found, err)
	}
	if string(pd.GetData()) != "db" {
		t.Fatalf("want db data, got %q", pd.GetData())
	}
}
