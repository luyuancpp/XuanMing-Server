// Package middleware — Logging middleware
//
// 按 docs/design/infra.md §11 字段约定输出 access log:
//   {ts, level, service, trace_id, player_id, op, latency_ms, code, err}
//
// 跟 Kratos 自带 logging.Server() 的区别:
//   - 复用 pandora/pkg/log 的 ctx 字段(trace_id / player_id / match_id 自动带)
//   - 默认 INFO 级 access log(成功)+ ERROR 级 error log(失败)
//   - 失败时打印 err 详情,不打印 req/resp 内容(避免日志爆炸)
package middleware

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"

	plog "github.com/luyuancpp/pandora/pkg/log"
)

// Logging 打印 access log。
//
// 用法:
//
//	srv := kgrpc.NewServer(kgrpc.Middleware(
//	    middleware.Trace(),
//	    middleware.Logging(),
//	    middleware.Metrics(),
//	))
//
// 注意 middleware 顺序:Trace 必须在 Logging 之前(否则 access log 拿不到 trace_id)。
func Logging() middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			start := time.Now()

			op := ""
			kind := ""
			if tr, ok := transport.FromServerContext(ctx); ok {
				op = tr.Operation()
				kind = string(tr.Kind())
			} else if tr, ok := transport.FromClientContext(ctx); ok {
				op = tr.Operation()
				kind = string(tr.Kind()) + "_client"
			}

			resp, err := handler(ctx, req)
			latency := time.Since(start)

			h := plog.With(ctx)
			if err != nil {
				h.Errorw(
					"msg", "rpc_failed",
					"transport", kind,
					"op", op,
					"latency_ms", latency.Milliseconds(),
					"code", errors.Code(err),
					"reason", errors.Reason(err),
					"err", err.Error(),
				)
			} else {
				h.Infow(
					"msg", "rpc_ok",
					"transport", kind,
					"op", op,
					"latency_ms", latency.Milliseconds(),
				)
			}

			return resp, err
		}
	}
}
