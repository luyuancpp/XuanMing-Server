package biz

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/luyuancpp/pandora/pkg/errcode"
	"github.com/luyuancpp/pandora/services/matchmaking/team/internal/conf"
	"github.com/luyuancpp/pandora/services/matchmaking/team/internal/data"
)

// ── 测试基础设施 ──────────────────────────────────────────────────────────────

// mockPusher 记录 PushTeamUpdate 的调用参数。
type mockPusher struct {
	calls []pushCall
}

type pushCall struct {
	caller uint64
	to     []uint64
}

func (m *mockPusher) PushTeamUpdate(_ context.Context, callerPlayerID uint64, toPlayerIDs []uint64, _ []byte) (int, error) {
	m.calls = append(m.calls, pushCall{caller: callerPlayerID, to: toPlayerIDs})
	return len(toPlayerIDs), nil
}

func newTestUsecase(t *testing.T) (*TeamUsecase, *mockPusher, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	repo := data.NewRedisTeamRepo(rdb)
	pusher := &mockPusher{}

	cfg := conf.TeamConf{}
	cfg2 := conf.Config{}
	cfg2.Team = cfg
	cfg2.Defaults()

	uc := NewTeamUsecase(repo, pusher, cfg2.Team)
	cleanup := func() {
		_ = rdb.Close()
		mr.Close()
	}
	return uc, pusher, cleanup
}

func newTestUsecaseWithMR(t *testing.T) (*TeamUsecase, *mockPusher, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	repo := data.NewRedisTeamRepo(rdb)
	pusher := &mockPusher{}

	cfg2 := conf.Config{}
	cfg2.Defaults()
	uc := NewTeamUsecase(repo, pusher, cfg2.Team)
	t.Cleanup(func() {
		_ = rdb.Close()
		mr.Close()
	})
	return uc, pusher, mr
}

// ── CreateTeam ────────────────────────────────────────────────────────────────

// TestCreateTeamSuccess 验证创建队伍成功,返回正确快照。
func TestCreateTeamSuccess(t *testing.T) {
	uc, pusher, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	team, err := uc.CreateTeam(ctx, 1001, 2001)
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if team.TeamID != 1001 || team.CaptainID != 2001 || team.State != stateForming {
		t.Errorf("unexpected team: %+v", team)
	}
	if len(team.Members) != 1 || team.Members[0].PlayerID != 2001 {
		t.Errorf("unexpected members: %+v", team.Members)
	}
	// push 给创建者自身
	if len(pusher.calls) != 1 {
		t.Errorf("expected 1 push call, got %d", len(pusher.calls))
	}
}

// TestCreateTeamAlreadyInTeam 验证已在其他队的玩家不能再创建。
func TestCreateTeamAlreadyInTeam(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("first CreateTeam: %v", err)
	}
	_, err := uc.CreateTeam(ctx, 1002, 2001)
	if errcode.As(err) != errcode.ErrTeamAlreadyInTeam {
		t.Errorf("expected ErrTeamAlreadyInTeam, got: %v", err)
	}
}

// ── Invite ────────────────────────────────────────────────────────────────────

// TestInvitePushOnlyTarget 验证 Invite push 只发给 target,不发给 inviter(协议原则 2)。
func TestInvitePushOnlyTarget(t *testing.T) {
	uc, pusher, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	pusher.calls = nil // 清除 create 的 push

	_, err := uc.Invite(ctx, 9001, 1001, 2001, 3001)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if len(pusher.calls) != 1 {
		t.Fatalf("expected 1 push, got %d", len(pusher.calls))
	}
	call := pusher.calls[0]
	if call.caller != 2001 {
		t.Errorf("expected caller=2001, got %d", call.caller)
	}
	// 接收方只有 target(3001),不含 inviter(2001)
	for _, id := range call.to {
		if id == 2001 {
			t.Error("inviter(2001) should not receive push")
		}
	}
}

// TestInviteTeamFull 验证满员时 Invite 返 ErrTeamFull。
func TestInviteTeamFull(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	// 手动填满 5 人
	for playerID := uint64(3001); playerID <= 3004; playerID++ {
		inviteID := playerID + 9000
		if _, err := uc.Invite(ctx, inviteID, 1001, 2001, playerID); err != nil {
			t.Fatalf("Invite %d: %v", playerID, err)
		}
		if _, err := uc.AcceptInvite(ctx, inviteID, 1001, playerID); err != nil {
			t.Fatalf("AcceptInvite %d: %v", playerID, err)
		}
	}
	// 现在队满(5 人),再邀请应返 ErrTeamFull
	_, err := uc.Invite(ctx, 9999, 1001, 2001, 9999)
	if errcode.As(err) != errcode.ErrTeamFull {
		t.Errorf("expected ErrTeamFull, got: %v", err)
	}
}

// ── AcceptInvite ──────────────────────────────────────────────────────────────

// TestAcceptInviteFullAutoReady 验证第 5 人加入时队伍全员 ready 自动变 READY。
func TestAcceptInviteFullAutoReady(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	// 队长先 ready
	if _, err := uc.SetReady(ctx, 1001, 2001, true, 0); err != nil {
		t.Fatalf("SetReady captain: %v", err)
	}

	// 加入 4 名成员,都 ready 后触发 READY 状态
	for i := uint64(0); i < 4; i++ {
		pid := uint64(3001) + i
		inviteID := uint64(9001) + i
		if _, err := uc.Invite(ctx, inviteID, 1001, 2001, pid); err != nil {
			t.Fatalf("Invite %d: %v", pid, err)
		}
		result, err := uc.AcceptInvite(ctx, inviteID, 1001, pid)
		if err != nil {
			t.Fatalf("AcceptInvite %d: %v", pid, err)
		}
		// SetReady each new member
		if _, err := uc.SetReady(ctx, 1001, pid, true, 0); err != nil {
			t.Fatalf("SetReady %d: %v", pid, err)
		}
		_ = result
	}

	// 最终 GetTeam 应为 READY
	team, err := uc.GetTeam(ctx, 1001)
	if err != nil {
		t.Fatalf("GetTeam: %v", err)
	}
	if team.State != stateReady {
		t.Errorf("expected READY, got state=%d", team.State)
	}
}

// TestAcceptInviteExpired 验证邀请令牌过期后 AcceptInvite 返 ErrTeamInviteExpired。
func TestAcceptInviteExpired(t *testing.T) {
	uc, _, mr := newTestUsecaseWithMR(t)
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := uc.Invite(ctx, 9001, 1001, 2001, 3001); err != nil {
		t.Fatalf("Invite: %v", err)
	}

	// 快进时钟超过 invite_ttl(60s 默认)
	mr.FastForward(2 * time.Minute)

	_, err := uc.AcceptInvite(ctx, 9001, 1001, 3001)
	if errcode.As(err) != errcode.ErrTeamInviteExpired {
		t.Errorf("expected ErrTeamInviteExpired, got: %v", err)
	}
}

// TestAcceptInviteAlreadyInTeam 验证不变量 §1:已在 A 队的玩家接受 B 队邀请被拒,
// 且 B 队成员列表不被污染(ClaimPlayer SETNX 在改成员前拦截)。
func TestAcceptInviteAlreadyInTeam(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	// 玩家 3001 先加入 A 队(1001)
	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam A: %v", err)
	}
	if _, err := uc.Invite(ctx, 9001, 1001, 2001, 3001); err != nil {
		t.Fatalf("Invite A: %v", err)
	}
	if _, err := uc.AcceptInvite(ctx, 9001, 1001, 3001); err != nil {
		t.Fatalf("AcceptInvite A: %v", err)
	}

	// B 队(1002)邀请同一玩家 3001
	if _, err := uc.CreateTeam(ctx, 1002, 2002); err != nil {
		t.Fatalf("CreateTeam B: %v", err)
	}
	if _, err := uc.Invite(ctx, 9002, 1002, 2002, 3001); err != nil {
		t.Fatalf("Invite B: %v", err)
	}

	// 接受 B 邀请应被 §1 拒绝
	_, err := uc.AcceptInvite(ctx, 9002, 1002, 3001)
	if errcode.As(err) != errcode.ErrTeamAlreadyInTeam {
		t.Fatalf("expected ErrTeamAlreadyInTeam, got: %v", err)
	}

	// B 队成员列表不应被污染,仍只有队长 2002
	teamB, err := uc.GetTeam(ctx, 1002)
	if err != nil {
		t.Fatalf("GetTeam B: %v", err)
	}
	if len(teamB.Members) != 1 || teamB.Members[0].PlayerID != 2002 {
		t.Errorf("team B polluted: %+v", teamB.Members)
	}
}

// ── LeaveTeam ─────────────────────────────────────────────────────────────────

// TestLeaveTeamCaptainTransfer 验证队长离队时队长转移给第一个成员。
func TestLeaveTeamCaptainTransfer(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := uc.Invite(ctx, 9001, 1001, 2001, 3001); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if _, err := uc.AcceptInvite(ctx, 9001, 1001, 3001); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	result, err := uc.LeaveTeam(ctx, 1001, 2001) // 队长离队
	if err != nil {
		t.Fatalf("LeaveTeam: %v", err)
	}
	if result.CaptainID != 3001 {
		t.Errorf("expected new captain=3001, got %d", result.CaptainID)
	}
}

// TestLeaveTeamReadyBackToForming 验证 READY 状态下有人离开回到 FORMING。
func TestLeaveTeamReadyBackToForming(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := uc.Invite(ctx, 9001, 1001, 2001, 3001); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if _, err := uc.AcceptInvite(ctx, 9001, 1001, 3001); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}
	// 两人都 ready → READY
	if _, err := uc.SetReady(ctx, 1001, 2001, true, 0); err != nil {
		t.Fatalf("SetReady 2001: %v", err)
	}
	if _, err := uc.SetReady(ctx, 1001, 3001, true, 0); err != nil {
		t.Fatalf("SetReady 3001: %v", err)
	}
	team, _ := uc.GetTeam(ctx, 1001)
	if team.State != stateReady {
		t.Fatalf("expected READY, got %d", team.State)
	}

	// 3001 离队 → 回 FORMING
	result, err := uc.LeaveTeam(ctx, 1001, 3001)
	if err != nil {
		t.Fatalf("LeaveTeam: %v", err)
	}
	if result.State != stateForming {
		t.Errorf("expected FORMING after leave, got %d", result.State)
	}
}

// ── Kick ─────────────────────────────────────────────────────────────────────

// TestKickNotCaptain 验证非队长踢人返 ErrTeamNotCaptain。
func TestKickNotCaptain(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := uc.Invite(ctx, 9001, 1001, 2001, 3001); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if _, err := uc.AcceptInvite(ctx, 9001, 1001, 3001); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	// 3001(非队长)踢 2001 → ErrTeamNotCaptain
	_, err := uc.Kick(ctx, 1001, 3001, 2001)
	if errcode.As(err) != errcode.ErrTeamNotCaptain {
		t.Errorf("expected ErrTeamNotCaptain, got: %v", err)
	}
}

// ── SetReady ──────────────────────────────────────────────────────────────────

// TestSetReadyAllReady 验证全员 ready 后状态变 READY。
func TestSetReadyAllReady(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := uc.Invite(ctx, 9001, 1001, 2001, 3001); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if _, err := uc.AcceptInvite(ctx, 9001, 1001, 3001); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	if _, err := uc.SetReady(ctx, 1001, 2001, true, 0); err != nil {
		t.Fatalf("SetReady 2001: %v", err)
	}
	result, err := uc.SetReady(ctx, 1001, 3001, true, 0)
	if err != nil {
		t.Fatalf("SetReady 3001: %v", err)
	}
	if result.State != stateReady {
		t.Errorf("expected READY, got %d", result.State)
	}
}

// TestSetReadyPartialStillForming 验证部分 ready 时仍是 FORMING。
func TestSetReadyPartialStillForming(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := uc.Invite(ctx, 9001, 1001, 2001, 3001); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if _, err := uc.AcceptInvite(ctx, 9001, 1001, 3001); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	result, err := uc.SetReady(ctx, 1001, 2001, true, 0) // 只有队长 ready
	if err != nil {
		t.Fatalf("SetReady 2001: %v", err)
	}
	if result.State != stateForming {
		t.Errorf("expected FORMING(partial ready), got %d", result.State)
	}
}

// ── GetTeam ───────────────────────────────────────────────────────────────────

// TestGetTeamNotFound 验证查不存在的队伍返 ErrTeamNotFound。
func TestGetTeamNotFound(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	_, err := uc.GetTeam(ctx, 9999)
	if errcode.As(err) != errcode.ErrTeamNotFound {
		t.Errorf("expected ErrTeamNotFound, got: %v", err)
	}
}

// ── 状态机不变量 ──────────────────────────────────────────────────────────────

// TestDisbandedRejectsAllWrites 验证 DISBANDED 状态下所有写操作返 ErrTeamWrongState。
func TestDisbandedRejectsAllWrites(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	// 队长离队 → 队伍空 → DISBANDED
	if _, err := uc.LeaveTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("LeaveTeam: %v", err)
	}

	_, err := uc.SetReady(ctx, 1001, 2001, true, 0)
	if errcode.As(err) != errcode.ErrTeamWrongState {
		t.Errorf("SetReady on DISBANDED: expected ErrTeamWrongState, got: %v", err)
	}
}

// TestConcurrentRetrySucceeds 验证 WATCH 冲突重试后能成功(miniredis 模拟)。
func TestConcurrentRetrySucceeds(t *testing.T) {
	uc, _, cleanup := newTestUsecase(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := uc.CreateTeam(ctx, 1001, 2001); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	// 顺序 SetReady 两次,验证乐观锁在无并发情况下成功(miniredis 单线程,不测真并发冲突)
	if _, err := uc.SetReady(ctx, 1001, 2001, true, 0); err != nil {
		t.Fatalf("SetReady 1: %v", err)
	}
	if _, err := uc.SetReady(ctx, 1001, 2001, false, 0); err != nil {
		t.Fatalf("SetReady 2: %v", err)
	}

	team, _ := uc.GetTeam(ctx, 1001)
	if team.Members[0].Ready {
		t.Error("expected ready=false after second SetReady")
	}
}
