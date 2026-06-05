// Package service 是 push 服务的 RPC 入口层。
//
// 职责:
//   - 实现 pushv1.PushServiceServer 接口
//   - Subscribe:校验 session(W2 mock 跳过)→ 注册 stream → 跑 mock 推送循环 → 退出时反注册
//
// 不变量(docs/design/protocol-ordering-rules.md 原则 3):
//   - Subscribe 是"已受理 + 长连"型,不是立即完成型 RPC
//   - 客户端拿到 stream 后,等待 server 推 PushFrame;直到 client 主动关闭或 server 断开
package service

import (
	"context"

	plog "github.com/luyuancpp/pandora/pkg/log"
	pushv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/push/v1"

	"github.com/luyuancpp/pandora/services/runtime/push/internal/biz"
)

// PushService 实现 pushv1.PushServiceServer。
//
// 内嵌 UnimplementedPushServiceServer 以满足 grpc 向前兼容约束。
type PushService struct {
	pushv1.UnimplementedPushServiceServer

	uc *biz.PushUsecase
}

// NewPushService 注入 PushUsecase。
func NewPushService(uc *biz.PushUsecase) *PushService {
	return &PushService{uc: uc}
}

// Subscribe 处理客户端长连接订阅(server stream)。
//
// W3 ① 流程(2026-06-05):
//  1. Envoy jwt_authn filter 已校验 JWT 并把 sub 提到 x-pandora-player-id 头
//  2. pmw.AuthOptional() 中间件把 header 中 player_id 注入到 ctx
//  3. 本方法从 ctx 取 player_id;0 表示匿名(直连 :50014 联调时正常)
//  4. 注册 stream 到 ConnectionManager(顶号语义:旧 stream 会被 close)
//  5. defer 反注册
//  6. 跑 mock 推送循环(RunMockStream)直到 ctx.Done 或 stream 失败
//
// W3 ④ 真实化:
//   - 校验 req.SessionToken(已被 Envoy 校验,业务侧无需重复;DSTicket 由 login.VerifyDSTicket 二次验)
//   - 按 req.LastSeenMs 从 redis ZSET pandora:push:offline:<player_id> 补推离线消息
//   - 不再调 RunMockStream,改阻塞等 ctx.Done(实际推送由 kafka consumer 调 Conns().SendTo)
func (s *PushService) Subscribe(req *pushv1.SubscribeRequest, stream pushv1.PushService_SubscribeServer) error {
	ctx := stream.Context()
	h := plog.With(ctx)

	playerID := extractPlayerID(ctx)

	// 注册到内存索引(W2 mock ticker 用不到,但 W3 kafka 路由会用)
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.uc.Conns().Register(playerID, stream, cancel)
	defer s.uc.Conns().Unregister(playerID, stream)

	h.Infow(
		"msg", "push_stream_open",
		"player_id", playerID,
		"last_seen_ms", req.GetLastSeenMs(),
		"online_total", s.uc.Conns().Size(),
	)

	// W2 mock 推送循环(W3 改成 select { case <-ctx.Done(): return nil })
	return s.uc.RunMockStream(subCtx, stream)
}

// extractPlayerID 从 gRPC metadata 拿 x-player-id(W2 联调用)。
// 取不到返回 0(允许匿名 stream,mock 推送不依赖 player_id)。
//
// W3:Envoy jwt_authn filter 会把 JWT 解出来的 sub 注入 metadata,
// 这里换成 metadata.FromIncomingContext + 读 "x-jwt-payload-sub" 等标准头。
func extractPlayerID(ctx context.Context) int64 {
	if v := ctx.Value(plog.CtxKeyPlayerID); v != nil {
		if id, ok := v.(int64); ok {
			return id
		}
	}
	return 0
}
