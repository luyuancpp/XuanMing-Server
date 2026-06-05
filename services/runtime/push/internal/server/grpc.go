// Package server 负责把 PushService 实现挂到 gRPC server 上。
package server

import (
	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"

	"github.com/luyuancpp/pandora/pkg/grpcserver"
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
func NewGRPCServer(cfg *conf.Config, svc *service.PushService) *kgrpc.Server {
	srv := grpcserver.MustNewServer(cfg.Server)
	pushv1.RegisterPushServiceServer(srv, svc)
	return srv
}
