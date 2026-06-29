// Package server — gRPC server 注册(2026-06-29)。
package server

import (
	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"

	"github.com/luyuancpp/pandora/pkg/grpcserver"
	pmw "github.com/luyuancpp/pandora/pkg/middleware"
	mailv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/mail/v1"

	"github.com/luyuancpp/pandora/services/social/mail/internal/conf"
	"github.com/luyuancpp/pandora/services/social/mail/internal/service"
)

// NewGRPCServer 构造 gRPC server 并注册 MailService。
//
// 端口由 cfg.Server.Grpc.Addr 决定(默认 :50009)。
// pmw.AuthOptional() 从 Envoy 注入的 player_id 注入 ctx;玩家 RPC 在 service 层兜底 callerID==0。
// SendSystemMail/SendGuildMail/SendPersonalMail 为内网运营 RPC,不经 Envoy 对客户端开放。
func NewGRPCServer(cfg *conf.Config, mailSvc *service.MailService) *kgrpc.Server {
	srv := grpcserver.MustNewServer(cfg.Server, pmw.AuthOptional())
	mailv1.RegisterMailServiceServer(srv, mailSvc)
	return srv
}
