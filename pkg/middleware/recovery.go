// Package middleware — Recovery middleware
//
// 复用 Kratos 自带的 recovery.Recovery(),但加 Pandora 风格的 log(用我们的 log 包,
// 自动带 trace_id / player_id)。
//
// 用法:把 Recovery() 放在中间件链 **最外层**(panic 在它内部被捕获)。
package middleware

import (
	"context"
	"runtime/debug"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/middleware"

	plog "github.com/luyuancpp/pandora/pkg/log"
)

// Recovery 捕获 panic,转成 Internal 错误。
//
// 标准链路:
//
//	srv := kgrpc.NewServer(kgrpc.Middleware(
//	    middleware.Recovery(),   // 最外层
//	    middleware.Trace(),
//	    middleware.Logging(),
//	    middleware.Metrics(),
//	    middleware.AuthRequired(),
//	    // 业务 middleware ...
//	))
func Recovery() middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (resp any, err error) {
			defer func() {
				if r := recover(); r != nil {
					plog.With(ctx).Errorw(
						"msg", "panic_recovered",
						"panic", r,
						"stack", string(debug.Stack()),
					)
					err = errors.New(500, "PANIC_RECOVERED", "internal server panic")
				}
			}()
			return handler(ctx, req)
		}
	}
}
