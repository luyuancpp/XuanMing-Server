// Package biz 是 push 服务的业务逻辑层(usecase)。
//
// 职责分层(对齐 login 服务):
//
//	service/  RPC 入口,只做 proto 与 biz 类型互转、stream 注册
//	biz/      usecase,纯业务逻辑(本 W2 mock 只产 mock PushFrame)
//	data/     仓储,W3 接 redis ZSET 离线缓存
//
// W2 mock 行为:RunMockStream 给单个 stream 开一个 ticker,周期性 Send mock PushFrame,
// 直到 stream ctx.Done(client 断开 / server 关闭)。
package biz

import (
	"context"
	"time"

	plog "github.com/luyuancpp/pandora/pkg/log"
	pushv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/push/v1"
)

// PushUsecase 是 push 服务用例。
//
// W2:只持有 ConnectionManager + mock 推送参数。
// W3:加 kafka consumer 启停、offline cache repo 等依赖。
type PushUsecase struct {
	conns *ConnectionManager

	mockTick    time.Duration
	mockTopic   string
	mockPayload string
}

// NewPushUsecase 构造 PushUsecase。
func NewPushUsecase(
	conns *ConnectionManager,
	mockTick time.Duration,
	mockTopic string,
	mockPayload string,
) *PushUsecase {
	return &PushUsecase{
		conns:       conns,
		mockTick:    mockTick,
		mockTopic:   mockTopic,
		mockPayload: mockPayload,
	}
}

// Conns 暴露 ConnectionManager,给 service 层 Register/Unregister 用。
func (u *PushUsecase) Conns() *ConnectionManager {
	return u.conns
}

// RunMockStream 给单个 stream 跑 W2 mock 推送循环。
//
// 行为:
//  1. 立刻先推一帧(便于 grpcurl 测试不用等 5s)
//  2. ticker 每 mockTick 周期推一帧
//  3. ctx.Done 时退出(client 断开 / server 关闭 / 顶号)
//  4. stream.Send 失败也退出(client 已断,继续推没意义)
//
// 返回 nil 表示正常退出(ctx.Done),非 nil 表示 stream 失败。
func (u *PushUsecase) RunMockStream(ctx context.Context, stream PushStream) error {
	h := plog.With(ctx)

	makeFrame := func() *pushv1.PushFrame {
		return &pushv1.PushFrame{
			Topic:   u.mockTopic,
			Payload: []byte(u.mockPayload),
			TsMs:    time.Now().UnixMilli(),
			TraceId: traceIDFromCtx(ctx),
		}
	}

	// 1. 首帧立发
	if err := stream.Send(makeFrame()); err != nil {
		h.Warnw("msg", "mock_push_first_send_failed", "err", err)
		return err
	}

	// 2. 周期推
	ticker := time.NewTicker(u.mockTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.Infow("msg", "mock_push_stream_closed", "reason", ctx.Err())
			return nil
		case <-ticker.C:
			if err := stream.Send(makeFrame()); err != nil {
				h.Warnw("msg", "mock_push_send_failed", "err", err)
				return err
			}
		}
	}
}

// traceIDFromCtx 从 ctx 取 trace_id(pkg/log 标准 key),取不到返空串。
func traceIDFromCtx(ctx context.Context) string {
	if v := ctx.Value(plog.CtxKeyTraceID); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
