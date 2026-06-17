// Package biz 是 chat 服务的业务逻辑层(2026-06-16)。
//
// 职责(docs/design/go-services.md §2.5):
//   - 三频道聊天:世界(WORLD)/ 队伍(TEAM)/ 私聊(PRIVATE)
//   - 服务端校验:频道合法性 + 内容长度(utf8 rune ≤ MaxContentLen)+ 敏感词屏蔽
//   - 私聊落 pandora_social(MySQL,支持离线 PullHistory)
//   - 三频道经 kafka pandora.chat.{world,team,private} → push 推送(弱依赖)
//   - 队伍频道成员经 team 服务 gRPC 解析(弱依赖)
//
// 关键规则:
//   - 客户端不能发 SYSTEM / UNSPECIFIED 频道 → ErrChatChannelInvalid
//   - 推送原则 2:队伍 / 私聊只发收件方,不回发自己(客户端本地回显己方消息)
//   - 世界频道是广播:to_player_id=0,key 空,由 push 服务 Broadcast(原则 2 例外)
//   - sender_nickname 留空:由客户端按 sender_id 解析展示名(CLAUDE.md §5.8 最小数据单位)
package biz

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/luyuancpp/pandora/pkg/errcode"
	plog "github.com/luyuancpp/pandora/pkg/log"
	chatv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/chat/v1"

	"github.com/luyuancpp/pandora/services/social/chat/internal/conf"
	"github.com/luyuancpp/pandora/services/social/chat/internal/data"
)

// ChatPusher 把聊天推送事件发到 kafka(main.go 注入 kafkax 适配器;弱依赖,nil 时静默跳过)。
// 三个方法对应三个 topic;key 由适配器按收件方 player_id 设置(世界频道 key 空)。
type ChatPusher interface {
	PushPrivate(ctx context.Context, toPlayerID uint64, evt *chatv1.ChatPushEvent) error
	PushTeam(ctx context.Context, toPlayerID uint64, evt *chatv1.ChatPushEvent) error
	PushWorld(ctx context.Context, evt *chatv1.ChatPushEvent) error
}

// TeamReader 解析队伍成员名单(main.go 注入 team gRPC 适配器;弱依赖,nil 时 TEAM 降级)。
type TeamReader interface {
	GetTeamMembers(ctx context.Context, teamID uint64) ([]uint64, bool, error)
}

// ChatUsecase 是 chat 服务业务逻辑核心。
type ChatUsecase struct {
	repo   data.PrivateRepo
	pusher ChatPusher      // 弱依赖,可为 nil
	team   TeamReader      // 弱依赖,可为 nil
	cfg    conf.ChatConf
}

// NewChatUsecase 构造。pusher / team 允许为 nil(弱依赖未配置时降级)。
func NewChatUsecase(repo data.PrivateRepo, pusher ChatPusher, team TeamReader, cfg conf.ChatConf) *ChatUsecase {
	if cfg.MaxContentLen <= 0 {
		cfg.MaxContentLen = 256
	}
	if cfg.HistoryLimit <= 0 {
		cfg.HistoryLimit = 50
	}
	return &ChatUsecase{repo: repo, pusher: pusher, team: team, cfg: cfg}
}

// SendMessage 发一条聊天消息。senderID 由 service 从 JWT ctx 得到(R5)。
// newMessageID 是 service 用 snowflake 预生成的消息 ID。
func (u *ChatUsecase) SendMessage(
	ctx context.Context,
	senderID uint64,
	channel chatv1.ChatChannel,
	targetID uint64,
	content string,
	newMessageID uint64,
) (uint64, error) {
	if senderID == 0 {
		return 0, errcode.New(errcode.ErrInvalidArg, "sender required")
	}

	// 频道校验:客户端只能发 WORLD / TEAM / PRIVATE。
	switch channel {
	case chatv1.ChatChannel_CHAT_CHANNEL_WORLD,
		chatv1.ChatChannel_CHAT_CHANNEL_TEAM,
		chatv1.ChatChannel_CHAT_CHANNEL_PRIVATE:
	default:
		return 0, errcode.New(errcode.ErrChatChannelInvalid, "channel %d not allowed from client", channel)
	}

	// 内容校验:非空 + utf8 rune 长度 ≤ MaxContentLen。
	content = strings.TrimSpace(content)
	if content == "" {
		return 0, errcode.New(errcode.ErrInvalidArg, "empty content")
	}
	if utf8.RuneCountInString(content) > u.cfg.MaxContentLen {
		return 0, errcode.New(errcode.ErrChatMessageTooLong,
			"content too long: %d > %d", utf8.RuneCountInString(content), u.cfg.MaxContentLen)
	}
	content = u.maskSensitive(content)

	msg := &chatv1.ChatMessage{
		MessageId:  newMessageID,
		SenderId:   senderID,
		Channel:    channel,
		TargetId:   targetID,
		Content:    content,
		SendTimeMs: nowMs(),
		// SenderNickname 留空,客户端按 sender_id 解析(最小数据单位)。
	}

	switch channel {
	case chatv1.ChatChannel_CHAT_CHANNEL_PRIVATE:
		return u.sendPrivate(ctx, msg)
	case chatv1.ChatChannel_CHAT_CHANNEL_TEAM:
		return u.sendTeam(ctx, senderID, msg)
	default: // WORLD
		return u.sendWorld(ctx, msg)
	}
}

// sendPrivate 私聊:必须有 target,落库(离线历史)+ 推送给接收方(原则 2)。
func (u *ChatUsecase) sendPrivate(ctx context.Context, msg *chatv1.ChatMessage) (uint64, error) {
	if msg.GetTargetId() == 0 {
		return 0, errcode.New(errcode.ErrInvalidArg, "private chat requires target_id")
	}
	if msg.GetTargetId() == msg.GetSenderId() {
		return 0, errcode.New(errcode.ErrInvalidArg, "cannot private chat self")
	}

	// 落库强依赖:私聊历史不可丢(MySQL 失败则整条失败,让客户端重试)。
	if err := u.repo.SavePrivate(ctx, msg); err != nil {
		return 0, err
	}

	// 推送弱依赖:发给接收方;失败只 warn(消息已落库,接收方上线 PullHistory 兜底)。
	if u.pusher != nil {
		evt := &chatv1.ChatPushEvent{Message: msg, ToPlayerId: msg.GetTargetId()}
		if err := u.pusher.PushPrivate(ctx, msg.GetTargetId(), evt); err != nil {
			plog.With(ctx).Warnw("msg", "chat_private_push_failed",
				"to_player_id", msg.GetTargetId(), "message_id", msg.GetMessageId(), "err", err)
		}
	}
	return msg.GetMessageId(), nil
}

// sendTeam 队伍频道:target_id 即 team_id;解析成员逐个推送(排除发送者,原则 2)。
// team / pusher 弱依赖,缺失时静默降级(消息不持久化,队伍频道是即时频道)。
func (u *ChatUsecase) sendTeam(ctx context.Context, senderID uint64, msg *chatv1.ChatMessage) (uint64, error) {
	teamID := msg.GetTargetId()
	if teamID == 0 {
		return 0, errcode.New(errcode.ErrInvalidArg, "team chat requires target_id (team_id)")
	}
	if u.team == nil || u.pusher == nil {
		// 弱依赖未配置:不报错,返回 message_id(客户端本地回显),仅记一条 warn。
		plog.With(ctx).Warnw("msg", "chat_team_degraded", "team_id", teamID,
			"hint", "team reader / pusher not configured, team chat fan-out skipped")
		return msg.GetMessageId(), nil
	}

	members, ok, err := u.team.GetTeamMembers(ctx, teamID)
	if err != nil {
		// team 服务暂时不可达:弱依赖降级,不阻断发送。
		plog.With(ctx).Warnw("msg", "chat_team_resolve_failed", "team_id", teamID, "err", err)
		return msg.GetMessageId(), nil
	}
	if !ok {
		return 0, errcode.New(errcode.ErrChatChannelInvalid, "team %d not found", teamID)
	}

	// 发送者必须是队伍成员才能在队伍频道说话。
	inTeam := false
	for _, m := range members {
		if m == senderID {
			inTeam = true
			break
		}
	}
	if !inTeam {
		return 0, errcode.New(errcode.ErrChatChannelInvalid, "sender %d not in team %d", senderID, teamID)
	}

	for _, m := range members {
		if m == senderID {
			continue // 原则 2:不回发自己
		}
		evt := &chatv1.ChatPushEvent{Message: msg, ToPlayerId: m}
		if perr := u.pusher.PushTeam(ctx, m, evt); perr != nil {
			plog.With(ctx).Warnw("msg", "chat_team_push_failed",
				"to_player_id", m, "team_id", teamID, "err", perr)
		}
	}
	return msg.GetMessageId(), nil
}

// sendWorld 世界频道:广播(to_player_id=0,key 空,push 服务 Broadcast,原则 2 例外)。
func (u *ChatUsecase) sendWorld(ctx context.Context, msg *chatv1.ChatMessage) (uint64, error) {
	if u.pusher == nil {
		plog.With(ctx).Warnw("msg", "chat_world_degraded", "hint", "pusher not configured")
		return msg.GetMessageId(), nil
	}
	evt := &chatv1.ChatPushEvent{Message: msg, ToPlayerId: 0}
	if err := u.pusher.PushWorld(ctx, evt); err != nil {
		plog.With(ctx).Warnw("msg", "chat_world_push_failed", "message_id", msg.GetMessageId(), "err", err)
	}
	return msg.GetMessageId(), nil
}

// PullHistory 拉私聊历史。只有 PRIVATE 频道有持久化历史;其余频道返回空。
// player_id 由 service 从 JWT ctx 得到(R5)。
func (u *ChatUsecase) PullHistory(
	ctx context.Context,
	playerID uint64,
	channel chatv1.ChatChannel,
	peerID uint64,
	limit int,
	beforeMs int64,
) ([]*chatv1.ChatMessage, error) {
	if playerID == 0 {
		return nil, errcode.New(errcode.ErrInvalidArg, "player_id required")
	}
	if channel != chatv1.ChatChannel_CHAT_CHANNEL_PRIVATE {
		// 世界 / 队伍是即时频道,不持久化,无历史可拉。
		return nil, nil
	}
	if peerID == 0 {
		return nil, errcode.New(errcode.ErrInvalidArg, "peer_id required for private history")
	}
	if limit <= 0 || limit > u.cfg.HistoryLimit {
		limit = u.cfg.HistoryLimit
	}
	return u.repo.ListPrivate(ctx, playerID, peerID, limit, beforeMs)
}

// maskSensitive 把命中的敏感词整词替换为等长 *。
// 列表为空时直接返回原文(默认不过滤);仅做最小化屏蔽,真正风控由独立服务接管(后续)。
func (u *ChatUsecase) maskSensitive(content string) string {
	if len(u.cfg.SensitiveWords) == 0 {
		return content
	}
	out := content
	for _, w := range u.cfg.SensitiveWords {
		if w == "" {
			continue
		}
		out = strings.ReplaceAll(out, w, strings.Repeat("*", utf8.RuneCountInString(w)))
	}
	return out
}

// nowMs 返回当前毫秒时间戳。
func nowMs() int64 {
	return time.Now().UnixMilli()
}
