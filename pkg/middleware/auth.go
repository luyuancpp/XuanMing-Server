// Package middleware — Auth middleware
//
// W2 简化版:只校验 metadata 中的 player_id 是否存在 + 注入 ctx。
// W3+ 接入真实 JWT 解析(login 服务签发 token,Envoy 用 jwt_authn filter 校验,
// 校验通过后把 player_id 注入到 header,这里只读 header 不重复校验)。
//
// 即:
//   - Envoy(对外)负责 JWT 签名校验,把 player_id 提取出来放进 header
//   - 业务服(Kratos)用本 middleware 从 header 读 player_id 注入 ctx
//   - 业务代码用 ctx.Value(...) 拿 player_id,不用碰 JWT
package middleware

import (
	"context"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/middleware"

	plog "github.com/luyuancpp/pandora/pkg/log"
)

// AuthRequired 校验请求必须带 player_id(从 header 来,由 Envoy / gateway 鉴权后注入)。
// 没有 player_id 返回 Unauthenticated 错误。
//
// 用法:
//
//	srv := kgrpc.NewServer(kgrpc.Middleware(middleware.AuthRequired()))
//
// W3+:Envoy 配 jwt_authn filter + extract_claim 把 sub claim 写到 header:
//   x-pandora-player-id: 1001
func AuthRequired() middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			playerID := extractPlayerID(ctx)
			if playerID == 0 {
				return nil, errors.New(401, "AUTH_REQUIRED", "missing or invalid player_id")
			}
			ctx = plog.WithPlayerID(ctx, playerID)
			return handler(ctx, req)
		}
	}
}

// AuthOptional 跟 AuthRequired 类似,但 player_id 缺失时不报错(供 Login 这种登录前 RPC 用)。
// 有 player_id 就注入 ctx,没有就 pass。
func AuthOptional() middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			playerID := extractPlayerID(ctx)
			if playerID > 0 {
				ctx = plog.WithPlayerID(ctx, playerID)
			}
			return handler(ctx, req)
		}
	}
}
