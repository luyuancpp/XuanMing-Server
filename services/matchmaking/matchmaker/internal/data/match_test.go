// match_test.go — matchmaker data 层 Redis 实现测试(miniredis)。
package data

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/luyuancpp/pandora/pkg/errcode"
	matchv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/match/v1"
)

const testTTL = 30 * time.Minute

func newRepo(t *testing.T) (*RedisMatchRepo, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRedisMatchRepo(rdb), mr
}

func TestTicketRoundtripAndQueueOrder(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)

	// 入队三张票,avg_mmr 乱序
	mmrs := map[uint64]int32{10: 1500, 20: 900, 30: 1200}
	for id, mmr := range mmrs {
		ticket := &matchv1.MatchTicketStorageRecord{
			TicketId: id, TeamId: id, AvgMmr: mmr, EnqueuedAtMs: 1,
			Members: []*matchv1.MatchMemberStorageRecord{{PlayerId: id, Mmr: mmr}},
		}
		if err := repo.AddTicket(ctx, ticket, testTTL); err != nil {
			t.Fatalf("add ticket %d: %v", id, err)
		}
	}

	// 读回校验
	got, found, err := repo.GetTicket(ctx, 30)
	if err != nil || !found {
		t.Fatalf("get ticket 30: found=%v err=%v", found, err)
	}
	if got.AvgMmr != 1200 || got.Members[0].PlayerId != 30 {
		t.Fatalf("ticket 30 mismatch: %+v", got)
	}

	// 队列按 avg_mmr 升序:20(900) < 30(1200) < 10(1500)
	order, err := repo.RangeQueueTickets(ctx)
	if err != nil {
		t.Fatalf("range queue: %v", err)
	}
	want := []uint64{20, 30, 10}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Fatalf("queue order = %v, want %v", order, want)
	}

	// DeleteTicket 移出队列 + 删 record
	if err := repo.DeleteTicket(ctx, 20); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, _ := repo.GetTicket(ctx, 20); found {
		t.Fatal("ticket 20 should be deleted")
	}
	order, _ = repo.RangeQueueTickets(ctx)
	if len(order) != 2 {
		t.Fatalf("queue len = %d, want 2", len(order))
	}
}

func TestClaimPlayerSETNX(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)

	if _, ok, err := repo.ClaimPlayer(ctx, 1, 100, testTTL); err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	// 同一玩家被另一票据声明 → 冲突,返回已存在票据
	existing, ok, err := repo.ClaimPlayer(ctx, 1, 200, testTTL)
	if err != nil {
		t.Fatalf("second claim err: %v", err)
	}
	if ok {
		t.Fatal("second claim should fail")
	}
	if existing != 100 {
		t.Fatalf("existing = %d, want 100", existing)
	}

	tid, found, err := repo.GetPlayerTicket(ctx, 1)
	if err != nil || !found || tid != 100 {
		t.Fatalf("get player ticket: tid=%d found=%v err=%v", tid, found, err)
	}

	if err := repo.DeletePlayerIndex(ctx, 1); err != nil {
		t.Fatalf("delete index: %v", err)
	}
	if _, found, _ := repo.GetPlayerTicket(ctx, 1); found {
		t.Fatal("player index should be gone")
	}
}

func TestUpdateMatchWithLock(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)

	match := &matchv1.MatchStorageRecord{
		MatchId: 999, Stage: matchv1.MatchStage_MATCH_STAGE_CONFIRM,
		ConfirmDeadlineMs: 5000,
		Members: []*matchv1.MatchMemberStorageRecord{
			{PlayerId: 1, Confirm: matchv1.MatchConfirmStatus_MATCH_CONFIRM_STATUS_PENDING},
		},
	}
	if err := repo.CreateMatch(ctx, match, testTTL); err != nil {
		t.Fatalf("create match: %v", err)
	}

	err := repo.UpdateMatchWithLock(ctx, 999, 3, func(m *matchv1.MatchStorageRecord) error {
		m.Members[0].Confirm = matchv1.MatchConfirmStatus_MATCH_CONFIRM_STATUS_ACCEPTED
		m.Stage = matchv1.MatchStage_MATCH_STAGE_ALLOCATING
		return nil
	}, testTTL)
	if err != nil {
		t.Fatalf("update with lock: %v", err)
	}

	got, found, _ := repo.GetMatch(ctx, 999)
	if !found {
		t.Fatal("match gone")
	}
	if got.Stage != matchv1.MatchStage_MATCH_STAGE_ALLOCATING {
		t.Fatalf("stage = %v, want ALLOCATING", got.Stage)
	}
	if got.Members[0].Confirm != matchv1.MatchConfirmStatus_MATCH_CONFIRM_STATUS_ACCEPTED {
		t.Fatalf("confirm not persisted: %v", got.Members[0].Confirm)
	}

	// fn 返回业务错误 → 透传,不写回
	wantErr := errcode.New(errcode.ErrMatchDeclined, "boom")
	if err := repo.UpdateMatchWithLock(ctx, 999, 3, func(m *matchv1.MatchStorageRecord) error {
		m.Stage = matchv1.MatchStage_MATCH_STAGE_READY
		return wantErr
	}, testTTL); err == nil {
		t.Fatal("expected error passthrough")
	}
	got, _, _ = repo.GetMatch(ctx, 999)
	if got.Stage != matchv1.MatchStage_MATCH_STAGE_ALLOCATING {
		t.Fatalf("stage changed despite error: %v", got.Stage)
	}
}

func TestActiveRangeAndExpire(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)

	// 两场:一场 deadline 在过去,一场在未来
	now := time.Now().UnixMilli()
	past := &matchv1.MatchStorageRecord{MatchId: 1, Stage: matchv1.MatchStage_MATCH_STAGE_CONFIRM, ConfirmDeadlineMs: now - 1000}
	future := &matchv1.MatchStorageRecord{MatchId: 2, Stage: matchv1.MatchStage_MATCH_STAGE_CONFIRM, ConfirmDeadlineMs: now + 60000}
	if err := repo.CreateMatch(ctx, past, testTTL); err != nil {
		t.Fatalf("create past: %v", err)
	}
	if err := repo.CreateMatch(ctx, future, testTTL); err != nil {
		t.Fatalf("create future: %v", err)
	}

	expired, err := repo.RangeExpiredMatches(ctx, now)
	if err != nil {
		t.Fatalf("range expired: %v", err)
	}
	if len(expired) != 1 || expired[0] != 1 {
		t.Fatalf("expired = %v, want [1]", expired)
	}

	// RemoveActive 后不再出现在超时扫描
	if err := repo.RemoveActive(ctx, 1); err != nil {
		t.Fatalf("remove active: %v", err)
	}
	expired, _ = repo.RangeExpiredMatches(ctx, now)
	if len(expired) != 0 {
		t.Fatalf("expired after remove = %v, want []", expired)
	}

	// ExpireMatch:match record 仍可查(终态保留),但移出 active
	if err := repo.ExpireMatch(ctx, 2, testTTL); err != nil {
		t.Fatalf("expire match 2: %v", err)
	}
	if _, found, _ := repo.GetMatch(ctx, 2); !found {
		t.Fatal("match 2 record should remain queryable")
	}
}

func TestDeleteMatchRemovesRecordAndActive(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepo(t)

	now := time.Now().UnixMilli()
	match := &matchv1.MatchStorageRecord{
		MatchId:           3,
		Stage:             matchv1.MatchStage_MATCH_STAGE_CONFIRM,
		ConfirmDeadlineMs: now - 1000,
	}
	if err := repo.CreateMatch(ctx, match, testTTL); err != nil {
		t.Fatalf("create match: %v", err)
	}

	if err := repo.DeleteMatch(ctx, 3); err != nil {
		t.Fatalf("delete match: %v", err)
	}
	if _, found, _ := repo.GetMatch(ctx, 3); found {
		t.Fatal("match record should be deleted")
	}
	expired, err := repo.RangeExpiredMatches(ctx, now)
	if err != nil {
		t.Fatalf("range expired: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("expired after delete = %v, want []", expired)
	}
}
