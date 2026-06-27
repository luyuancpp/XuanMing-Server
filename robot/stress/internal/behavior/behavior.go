// Package behavior 把场景里的操作权重转成"加权随机挑动作 + 泊松抖动间隔"的调度器,
// 模拟真实玩家在大厅里非均匀、带随机性的操作节奏(设计文档 §6)。
package behavior

import (
	"math"
	"math/rand"

	"github.com/luyuancpp/pandora/robot/stress/internal/scenario"
)

// Action 标识一类大厅操作。
type Action int

const (
	ActionLocatorSetLocation Action = iota
	ActionPlayerGetProfile
	ActionTeamGetMyTeam
	ActionFriendListFriends
	ActionChatSendMessage
	ActionAuctionListMarket
	ActionMatchFlow
)

// String 便于日志 / 调试。
func (a Action) String() string {
	switch a {
	case ActionLocatorSetLocation:
		return "locator_set_location"
	case ActionPlayerGetProfile:
		return "player_get_profile"
	case ActionTeamGetMyTeam:
		return "team_get_my_team"
	case ActionFriendListFriends:
		return "friend_list_friends"
	case ActionChatSendMessage:
		return "chat_send_message"
	case ActionAuctionListMarket:
		return "auction_list_market"
	case ActionMatchFlow:
		return "match_flow"
	default:
		return "unknown"
	}
}

// Scheduler 按权重做加权随机选择,并提供泊松分布的操作间隔。
type Scheduler struct {
	actions   []Action
	cumWeight []int // 前缀和,用于二分挑选
	total     int
}

// NewScheduler 用场景权重构建调度器。权重 ≤0 的动作被剔除。
func NewScheduler(w scenario.BehaviorWeights) *Scheduler {
	pairs := []struct {
		a Action
		w int
	}{
		{ActionLocatorSetLocation, w.LocatorSetLocation},
		{ActionPlayerGetProfile, w.PlayerGetProfile},
		{ActionTeamGetMyTeam, w.TeamGetMyTeam},
		{ActionFriendListFriends, w.FriendListFriends},
		{ActionChatSendMessage, w.ChatSendMessage},
		{ActionAuctionListMarket, w.AuctionListMarket},
		{ActionMatchFlow, w.MatchFlow},
	}
	s := &Scheduler{}
	sum := 0
	for _, p := range pairs {
		if p.w <= 0 {
			continue
		}
		sum += p.w
		s.actions = append(s.actions, p.a)
		s.cumWeight = append(s.cumWeight, sum)
	}
	s.total = sum
	return s
}

// Pick 用调用方传入的 rng 加权随机挑一个动作。
// 全部权重为 0 时退化为永远返回心跳(locator),避免空闲 VU。
func (s *Scheduler) Pick(rng *rand.Rand) Action {
	if s.total <= 0 || len(s.actions) == 0 {
		return ActionLocatorSetLocation
	}
	r := rng.Intn(s.total)
	// 线性扫描即可(动作种类很少),无需二分。
	for i, c := range s.cumWeight {
		if r < c {
			return s.actions[i]
		}
	}
	return s.actions[len(s.actions)-1]
}

// NextInterval 返回围绕 base 的泊松(指数)抖动间隔(毫秒),模拟真实玩家随机节奏。
// 下限 1ms,避免 0 间隔死循环。
func NextInterval(rng *rand.Rand, baseMs float64) float64 {
	if baseMs <= 0 {
		baseMs = 1
	}
	// 指数分布的逆变换采样:-mean * ln(U)。
	u := rng.Float64()
	if u <= 0 {
		u = math.SmallestNonzeroFloat64
	}
	v := -baseMs * math.Log(u)
	if v < 1 {
		v = 1
	}
	return v
}
