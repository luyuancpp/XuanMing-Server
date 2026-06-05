// Package server 负责把 PushService 实现挂到 gRPC server 上。
package server

import (
	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"

	"github.com/luyuancpp/pandora/pkg/grpcserver"
	pmw "github.com/luyuancpp/pandora/pkg/middleware"
	pushv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/push/v1"

	"github.com/luyuancpp/pandora/services/runtime/push/internal/conf"
	"github.com/luyuancpp/pandora/services/runtime/push/internal/service"
)

// NewGRPCServer 构造 gRPC server 并把 PushService 注册上去。
//
// 端口由 cfg.Server.Grpc.Addr 决定(默认 :50014,见 conf.Defaults)。
//
// ⚠️ Subscribe 是 server stream RPC,Kratos transport/grpc 会自动处理 stream 生命周期,
// 业务侧 PushService.Subscribe 收到的 stream.Context() 在 client 断开时自动 cancel。
//
// W3 ①(2026-06-05):加 pmw.AuthOptional() 中间件,把 Envoy jwt_authn 从 JWT 提到
// x-pandora-player-id 头里的 player_id 注入到 ctx。Subscribe 业务侧 extractPlayerID
// 从 ctx 读到正确的 player_id 用于 ConnectionManager 顶号。Optional 而非 Required:
// W2 mock 阶段(没经 Envoy / 直连 :50014)仍能联调通过,player_id=0。
func NewGRPCServer(cfg *conf.Config, svc *service.PushService) *kgrpc.Server {
	srv := grpcserver.MustNewServer(cfg.Server, pmw.AuthOptional())
	pushv1.RegisterPushServiceServer(srv, svc)
	return srv
}
