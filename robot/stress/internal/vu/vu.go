// Package vu 实现单个虚拟玩家(VU)的状态机:
//
//	CONNECTING(login)→ LOBBY(订阅 push + 大厅操作循环)→ MATCH(组队/匹配/确认)
//	→ BATTLE(battle_result 上报)→ 回 LOBBY。
//
// 一个 VU = 一个 goroutine,几十万 VU 共享 client.Pool 里的少量连接(HTTP/2 多路复用)。
package vu

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/luyuancpp/pandora/robot/stress/internal/behavior"
	"github.com/luyuancpp/pandora/robot/stress/internal/client"
	"github.com/luyuancpp/pandora/robot/stress/internal/scenario"
	"github.com/luyuancpp/pandora/robot/stress/internal/stats"

	auctionv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/auction/v1"
	battlev1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/battle/v1"
	chatv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/chat/v1"
	commonv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/common/v1"
	friendv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/friend/v1"
	locatorv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/locator/v1"
	loginv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/login/v1"
	matchv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/match/v1"
	playerv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/player/v1"
	pushv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/push/v1"
	teamv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/team/v1"
)

// VU 是一个虚拟玩家实例。
type VU struct {
	index int
	cfg   scenario.Config
	pool  *client.Pool
	sched *behavior.Scheduler
	stat  *stats.Collector
	rng   *rand.Rand

	account     string
	playerID    uint64
	sessionTok  string
	envoySample bool // 是否走 Envoy 对照链路登录
}

// New 创建一个 VU。seed 用于让每个 VU 的随机节奏互不相同。
func New(index int, cfg scenario.Config, pool *client.Pool, sched *behavior.Scheduler, stat *stats.Collector) *VU {
	rng := rand.New(rand.NewSource(int64(index)*2654435761 + time.Now().UnixNano()))
	return &VU{
		index:       index,
		cfg:         cfg,
		pool:        pool,
		sched:       sched,
		stat:        stat,
		rng:         rng,
		account:     cfg.AccountPrefix + strconv.Itoa(index),
		envoySample: rng.Float64() < cfg.EnvoySampleRatio,
	}
}

// Run 跑完整生命周期,直到 ctx 取消。
func (v *VU) Run(ctx context.Context) {
	if err := v.login(ctx); err != nil {
		v.stat.Counters.LoginFail.Add(1)
		return
	}
	v.stat.Counters.LoginOK.Add(1)
	v.stat.Counters.VUOnline.Add(1)
	defer v.stat.Counters.VUOnline.Add(-1)

	// 订阅 push(server stream),后台 drain。
	subCtx, cancelSub := context.WithCancel(ctx)
	defer cancelSub()
	go v.subscribePush(subCtx)

	// 大厅操作循环:加权挑动作 + 泊松抖动间隔。
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		v.doAction(ctx, v.sched.Pick(v.rng))

		wait := behavior.NextInterval(v.rng, float64(v.cfg.ActionIntervalMs))
		timer := time.NewTimer(time.Duration(wait) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// authCtx 返回带 player_id / trace_id metadata 的出站 context。
func (v *VU) authCtx(ctx context.Context) context.Context {
	return client.OutgoingContext(ctx, v.playerID)
}

// timed 包一次 RPC:记录时延 + 错误计数。
func (v *VU) timed(fn func() error) error {
	start := time.Now()
	err := fn()
	v.stat.ObserveRPC(float64(time.Since(start).Microseconds()) / 1000.0)
	if err != nil {
		v.stat.Counters.RPCErrors.Add(1)
	}
	return err
}

// login 真实走 LoginService,首次登录由 login 服务 devAutoRegister 自动建号。
func (v *VU) login(ctx context.Context) error {
	cli := v.pool.Login
	if v.envoySample && v.pool.LoginViaEnvoy != nil {
		cli = v.pool.LoginViaEnvoy
	}
	req := &loginv1.LoginRequest{
		Account:       v.account,
		PasswordHash:  "stressbot", // dev 环境 devSkipPassword=true,占位即可
		DeviceId:      "robot-" + strconv.Itoa(v.index),
		ClientVersion: "stress-1",
		Region:        "dev",
		Locale:        "zh-CN",
	}
	var resp *loginv1.LoginResponse
	err := v.timed(func() error {
		var e error
		resp, e = cli.Login(client.OutgoingContext(ctx, 0), req)
		return e
	})
	if err != nil {
		return err
	}
	if resp.GetCode() != commonv1.ErrCode_OK {
		return fmt.Errorf("login code=%v", resp.GetCode())
	}
	v.playerID = resp.GetPlayerId()
	v.sessionTok = resp.GetSessionToken()
	if v.playerID == 0 {
		return fmt.Errorf("login 返回 player_id=0")
	}
	return nil
}

// subscribePush 建立 push server stream 并 drain,直到出错或 ctx 取消。
func (v *VU) subscribePush(ctx context.Context) {
	stream, err := v.pool.Push.Subscribe(v.authCtx(ctx), &pushv1.SubscribeRequest{
		SessionToken: v.sessionTok,
		LastSeenMs:   0,
	})
	if err != nil {
		v.stat.Counters.RPCErrors.Add(1)
		return
	}
	v.stat.Counters.SubscribeActive.Add(1)
	defer v.stat.Counters.SubscribeActive.Add(-1)
	for {
		if _, err := stream.Recv(); err != nil {
			return
		}
	}
}

// doAction 执行一类大厅操作。
func (v *VU) doAction(ctx context.Context, a behavior.Action) {
	switch a {
	case behavior.ActionLocatorSetLocation:
		v.actLocator(ctx)
	case behavior.ActionPlayerGetProfile:
		v.actGetProfile(ctx)
	case behavior.ActionTeamGetMyTeam:
		v.actGetMyTeam(ctx)
	case behavior.ActionFriendListFriends:
		v.actListFriends(ctx)
	case behavior.ActionChatSendMessage:
		v.actSendMessage(ctx)
	case behavior.ActionAuctionListMarket:
		v.actListMarket(ctx)
	case behavior.ActionMatchFlow:
		v.actMatchFlow(ctx)
	}
}

func (v *VU) actLocator(ctx context.Context) {
	_ = v.timed(func() error {
		_, e := v.pool.Locator.SetLocation(v.authCtx(ctx), &locatorv1.SetLocationRequest{
			PlayerId: v.playerID,
			Location: &locatorv1.Location{
				State:       locatorv1.LocationState_LOCATION_STATE_HUB,
				ShardId:     v.cfg.Router.CellID,
				UpdatedAtMs: time.Now().UnixMilli(),
			},
		})
		return e
	})
}

func (v *VU) actGetProfile(ctx context.Context) {
	_ = v.timed(func() error {
		_, e := v.pool.Player.GetProfile(v.authCtx(ctx), &playerv1.GetProfileRequest{PlayerId: v.playerID})
		return e
	})
}

func (v *VU) actGetMyTeam(ctx context.Context) {
	_ = v.timed(func() error {
		_, e := v.pool.Team.GetMyTeam(v.authCtx(ctx), &teamv1.GetMyTeamRequest{PlayerId: v.playerID})
		return e
	})
}

func (v *VU) actListFriends(ctx context.Context) {
	_ = v.timed(func() error {
		_, e := v.pool.Friend.ListFriends(v.authCtx(ctx), &friendv1.ListFriendsRequest{PlayerId: v.playerID})
		return e
	})
}

func (v *VU) actSendMessage(ctx context.Context) {
	_ = v.timed(func() error {
		_, e := v.pool.Chat.SendMessage(v.authCtx(ctx), &chatv1.SendMessageRequest{
			SenderId:  v.playerID,
			Channel:   chatv1.ChatChannel_CHAT_CHANNEL_WORLD,
			Content:   "stress hi",
			RequestId: client.NewTraceID(),
		})
		return e
	})
}

func (v *VU) actListMarket(ctx context.Context) {
	_ = v.timed(func() error {
		_, e := v.pool.Auction.ListMarket(v.authCtx(ctx), &auctionv1.ListMarketRequest{
			MarketId: 1,
			Side:     auctionv1.OrderSide_ORDER_SIDE_UNSPECIFIED,
			Limit:    20,
		})
		return e
	})
}

// actMatchFlow 跑一条简化的组队→匹配→确认→战斗上报链路。
// 失败任意一步即提前返回(压测下后端可能限流 / 撮合不齐,容忍局部失败)。
func (v *VU) actMatchFlow(ctx context.Context) {
	// 1) 建队(单人队即可施压撮合 / 锚定埋点)。
	var teamID uint64
	if err := v.timed(func() error {
		resp, e := v.pool.Team.CreateTeam(v.authCtx(ctx), &teamv1.CreateTeamRequest{PlayerId: v.playerID})
		if e != nil {
			return e
		}
		if resp.GetCode() != commonv1.ErrCode_OK {
			return fmt.Errorf("create_team code=%v", resp.GetCode())
		}
		teamID = resp.GetTeamId()
		return nil
	}); err != nil || teamID == 0 {
		return
	}

	// 本轮结束后尽力离队,避免下一轮 CreateTeam 撞 ErrTeamAlreadyInTeam(harness 清理,非压测指标)。
	defer v.leaveTeamBestEffort(ctx, teamID)

	// 2) 单人队也必须 READY 后才能通过 matchmaker 的 team 校验。
	if err := v.timed(func() error {
		resp, e := v.pool.Team.SetReady(v.authCtx(ctx), &teamv1.SetReadyRequest{
			TeamId:   teamID,
			PlayerId: v.playerID,
			Ready:    true,
			HeroId:   1,
		})
		if e != nil {
			return e
		}
		if resp.GetCode() != commonv1.ErrCode_OK {
			return fmt.Errorf("set_ready code=%v", resp.GetCode())
		}
		return nil
	}); err != nil {
		return
	}

	// 3) 入队匹配。
	var matchID uint64
	if err := v.timed(func() error {
		resp, e := v.pool.Matchmaker.StartMatch(v.authCtx(ctx), &matchv1.StartMatchRequest{TeamId: teamID})
		if e != nil {
			return e
		}
		if resp.GetCode() != commonv1.ErrCode_OK {
			return fmt.Errorf("start_match code=%v", resp.GetCode())
		}
		matchID = resp.GetMatchId()
		return nil
	}); err != nil || matchID == 0 {
		return
	}
	v.stat.Counters.MatchEnqueue.Add(1)

	// 4) 轮询撮合进度,撮到则确认。
	stage := v.pollMatch(ctx, matchID)
	if stage == matchv1.MatchStage_MATCH_STAGE_FOUND || stage == matchv1.MatchStage_MATCH_STAGE_CONFIRM {
		if err := v.timed(func() error {
			resp, e := v.pool.Matchmaker.ConfirmMatch(v.authCtx(ctx), &matchv1.ConfirmMatchRequest{
				PlayerId: v.playerID,
				MatchId:  matchID,
				Accept:   true,
			})
			if e != nil {
				return e
			}
			if resp.GetCode() != commonv1.ErrCode_OK {
				return fmt.Errorf("confirm_match code=%v", resp.GetCode())
			}
			return nil
		}); err == nil {
			v.stat.Counters.MatchConfirmed.Add(1)
			stage = v.pollMatch(ctx, matchID)
		}
	}

	// 5) 撮合分配到战斗后,阶段 1 stub 模式由 robot 代 DS 直接上报 battle_result。
	if v.cfg.DSMode == "stub" &&
		(stage == matchv1.MatchStage_MATCH_STAGE_READY || stage == matchv1.MatchStage_MATCH_STAGE_ALLOCATING) {
		v.stat.Counters.MatchDispatched.Add(1)
		v.reportBattle(ctx, matchID)
	}
}

// leaveTeamBestEffort 尽力离队收尾:记录时延,但「已在战斗 / 已解散 / 不在队」等预期业务码
// 不计入错误(不走 timed),避免每轮重复 CreateTeam 撞 ErrTeamAlreadyInTeam 污染 error 计数。
func (v *VU) leaveTeamBestEffort(ctx context.Context, teamID uint64) {
	start := time.Now()
	_, _ = v.pool.Team.LeaveTeam(v.authCtx(ctx), &teamv1.LeaveTeamRequest{
		TeamId:   teamID,
		PlayerId: v.playerID,
	})
	v.stat.ObserveRPC(float64(time.Since(start).Microseconds()) / 1000.0)
}

// pollMatch 轮询匹配进度若干次,返回最后看到的阶段。
func (v *VU) pollMatch(ctx context.Context, matchID uint64) matchv1.MatchStage {
	stage := matchv1.MatchStage_MATCH_STAGE_UNSPECIFIED
	for i := 0; i < 30; i++ {
		select {
		case <-ctx.Done():
			return stage
		default:
		}
		_ = v.timed(func() error {
			resp, e := v.pool.Matchmaker.GetMatchProgress(v.authCtx(ctx), &matchv1.GetMatchProgressRequest{MatchId: matchID})
			if e != nil {
				return e
			}
			if p := resp.GetProgress(); p != nil {
				stage = p.GetStage()
			}
			return nil
		})
		if stage == matchv1.MatchStage_MATCH_STAGE_READY ||
			stage == matchv1.MatchStage_MATCH_STAGE_FAILED {
			return stage
		}
		time.Sleep(300 * time.Millisecond)
	}
	return stage
}

// reportBattle 模拟 DS 结算上报(stub 模式下由 robot 代 DS,MMR 仍由后端算)。
func (v *VU) reportBattle(ctx context.Context, matchID uint64) {
	now := time.Now().UnixMilli()
	result := &battlev1.BattleResult{
		MatchId:     matchID,
		StartedAtMs: now - 600000,
		EndedAtMs:   now,
		WinnerTeam:  0,
		DsPodName:   "stressbot-stub",
		GameMode:    "5v5",
		MapId:       1,
		Outcome:     battlev1.BattleOutcome_BATTLE_OUTCOME_NORMAL,
		Stats: []*battlev1.PlayerStats{{
			PlayerId: v.playerID,
			HeroId:   1,
			Team:     0,
			Kills:    int32(v.rng.Intn(10)),
			Deaths:   int32(v.rng.Intn(10)),
			Assists:  int32(v.rng.Intn(15)),
		}},
	}
	if err := v.timed(func() error {
		resp, e := v.pool.BattleResult.ReportResult(v.authCtx(ctx), &battlev1.ReportResultRequest{Result: result})
		if e != nil {
			return e
		}
		if resp.GetCode() != commonv1.ErrCode_OK {
			return fmt.Errorf("report_result code=%v", resp.GetCode())
		}
		return nil
	}); err == nil {
		v.stat.Counters.BattleReported.Add(1)
	}
}
