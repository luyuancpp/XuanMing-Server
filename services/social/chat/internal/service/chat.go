// Package service 是 chat 服务的 gRPC service 层(2026-06-16)。
//
// 职责:
//   - 实现 chatv1.ChatServiceServer 接口
//   - 从 ctx 取 JWT player_id(R5:override request 字段,防伪造他人身份)
//   - proto Request/Response ↔ biz 入参/出参互转
//   - errcode.Code → commonv1.ErrCode 1:1 映射
//
// 协议原则(R5):SendMessage 的 sender_id / PullHistory 的 player_id 一律以
// ctx 中的 JWT player_id 为准,忽略请求体里的对应字段;player_id=0 → ERR_UNAUTHORIZED。
package service

import (
	"context"

	"github.com/luyuancpp/pandora/pkg/errcode"
	plog "github.com/luyuancpp/pandora/pkg/log"
	chatv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/chat/v1"
	commonv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/common/v1"

	"github.com/luyuancpp/pandora/services/social/chat/internal/biz"
)

// snowflakeGen 是 snowflake.Node 的最小接口,避免 service 直接依赖 snowflake 包。
type snowflakeGen interface {
	Generate() uint64
}

// ChatService 实现 chatv1.ChatServiceServer。
type ChatService struct {
	chatv1.UnimplementedChatServiceServer
	uc *biz.ChatUsecase
	sf snowflakeGen
}

// NewChatService 构造。
func NewChatService(uc *biz.ChatUsecase, sf snowflakeGen) *ChatService {
	return &ChatService{uc: uc, sf: sf}
}

// SendMessage 发一条聊天消息。sender 以 JWT ctx 为准(R5)。
func (s *ChatService) SendMessage(ctx context.Context, req *chatv1.SendMessageRequest) (*chatv1.SendMessageResponse, error) {
	senderID := callerID(ctx)
	if senderID == 0 {
		return &chatv1.SendMessageResponse{Code: commonv1.ErrCode_ERR_UNAUTHORIZED}, nil
	}

	messageID, err := s.uc.SendMessage(ctx, senderID, req.GetChannel(), req.GetTargetId(), req.GetContent(), s.sf.Generate())
	if err != nil {
		return &chatv1.SendMessageResponse{Code: toProtoCode(err)}, nil
	}
	return &chatv1.SendMessageResponse{Code: commonv1.ErrCode_OK, MessageId: messageID}, nil
}

// PullHistory 拉私聊历史。player_id 以 JWT ctx 为准(R5)。
func (s *ChatService) PullHistory(ctx context.Context, req *chatv1.PullHistoryRequest) (*chatv1.PullHistoryResponse, error) {
	playerID := callerID(ctx)
	if playerID == 0 {
		return &chatv1.PullHistoryResponse{Code: commonv1.ErrCode_ERR_UNAUTHORIZED}, nil
	}

	msgs, err := s.uc.PullHistory(ctx, playerID, req.GetChannel(), req.GetPeerId(), int(req.GetLimit()), req.GetBeforeMs())
	if err != nil {
		return &chatv1.PullHistoryResponse{Code: toProtoCode(err)}, nil
	}
	return &chatv1.PullHistoryResponse{Code: commonv1.ErrCode_OK, Messages: msgs}, nil
}

// ── 辅助 ──────────────────────────────────────────────────────────────────────

// callerID 从 ctx 取 JWT 注入的 player_id。
func callerID(ctx context.Context) uint64 {
	id, _ := ctx.Value(plog.CtxKeyPlayerID).(uint64)
	return id
}

// toProtoCode 把 pkg/errcode 1:1 映射成 proto enum(数值相同)。
func toProtoCode(err error) commonv1.ErrCode {
	return commonv1.ErrCode(errcode.As(err))
}
