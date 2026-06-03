// Package log 是 Pandora 服务的日志门面,基于 go-zero/core/logx 做薄包装。
//
// 设计目标:
//   - 统一所有服务的 service / trace_id / 业务字段约定(docs/design/infra.md §11)
//   - 提供一站式 Setup,业务侧不需要手写 logx.MustSetup
//   - 提供 With(ctx) 从 ctx 提取 trace_id / player_id 等业务字段
//
// 不引入 zap,避免与 go-zero 双写日志。
package log

import (
	"context"
	"os"

	"github.com/zeromicro/go-zero/core/logx"
)

// ContextKey 是 log 包专用的 context key 类型,避免与业务 key 冲突。
type ContextKey string

const (
	CtxKeyTraceID  ContextKey = "trace_id"
	CtxKeyPlayerID ContextKey = "player_id"
	CtxKeyMatchID  ContextKey = "match_id"
	CtxKeyTeamID   ContextKey = "team_id"
)

// Setup 初始化日志系统,所有服务在 main() 开始处调用一次。
//
// 默认输出 JSON 到 stdout,Level=info。生产环境通过 LogConf 覆盖:
//
//	log.Setup("login", logx.LogConf{Mode: "file", Path: "logs/login"})
func Setup(serviceName string, conf ...logx.LogConf) {
	c := logx.LogConf{
		ServiceName: serviceName,
		Mode:        "console",
		Encoding:    "json",
		Level:       "info",
		TimeFormat:  "2006-01-02T15:04:05.000Z07:00",
	}
	if len(conf) > 0 {
		// 用户提供的 conf 覆盖默认值,但 ServiceName 始终用 Setup 参数
		userConf := conf[0]
		userConf.ServiceName = serviceName
		c = userConf
	}

	if err := logx.SetUp(c); err != nil {
		// 启动期日志失败 → 直接退出,否则跑起来都是哑巴
		_, _ = os.Stderr.WriteString("log setup failed: " + err.Error() + "\n")
		os.Exit(2)
	}
}

// With 从 ctx 抽出标准业务字段,返回带这些字段的 logx.Logger。
//
// 调用方:
//
//	log.With(ctx).Infow("match found", logx.Field("mmr", 1234))
//
// 标准字段(infra.md §11):trace_id / player_id / match_id / team_id
func With(ctx context.Context) logx.Logger {
	l := logx.WithContext(ctx)

	if v := ctx.Value(CtxKeyTraceID); v != nil {
		l = l.WithFields(logx.Field("trace_id", v))
	}
	if v := ctx.Value(CtxKeyPlayerID); v != nil {
		l = l.WithFields(logx.Field("player_id", v))
	}
	if v := ctx.Value(CtxKeyMatchID); v != nil {
		l = l.WithFields(logx.Field("match_id", v))
	}
	if v := ctx.Value(CtxKeyTeamID); v != nil {
		l = l.WithFields(logx.Field("team_id", v))
	}

	return l
}

// WithTraceID 把 trace_id 塞进 ctx,后续 With(ctx) 自动带上。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, CtxKeyTraceID, traceID)
}

// WithPlayerID 把 player_id 塞进 ctx。
func WithPlayerID(ctx context.Context, playerID int64) context.Context {
	return context.WithValue(ctx, CtxKeyPlayerID, playerID)
}

// WithMatchID 把 match_id 塞进 ctx。
func WithMatchID(ctx context.Context, matchID string) context.Context {
	return context.WithValue(ctx, CtxKeyMatchID, matchID)
}

// WithTeamID 把 team_id 塞进 ctx。
func WithTeamID(ctx context.Context, teamID string) context.Context {
	return context.WithValue(ctx, CtxKeyTeamID, teamID)
}
