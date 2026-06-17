// Package service 是 dialogue 服务的 gRPC service 层(2026-06-16)。
//
// 职责:
//   - 实现 dialoguev1.DialogueServiceServer 接口
//   - 从 ctx 取 JWT player_id(R5:override request 字段,防伪造他人身份)
//   - proto Request/Response ↔ biz 入参/出参互转
//   - errcode.Code → commonv1.ErrCode 1:1 映射
//
// 协议原则(R5):所有 RPC 强制用 ctx 中的 player_id,忽略请求体里的 player_id 字段;
// player_id=0 → ERR_UNAUTHORIZED(Envoy jwt_authn 已在路由层 require JWT,这里兜底)。
package service

import (
	"context"

	"github.com/luyuancpp/pandora/pkg/errcode"
	plog "github.com/luyuancpp/pandora/pkg/log"
	commonv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/common/v1"
	dialoguev1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/dialogue/v1"

	"github.com/luyuancpp/pandora/services/social/dialogue/internal/biz"
)

// snowflakeGen 是 snowflake.Node 的最小接口,避免 service 直接依赖 snowflake 包。
type snowflakeGen interface {
	Generate() uint64
}

// DialogueService 实现 dialoguev1.DialogueServiceServer。
type DialogueService struct {
	dialoguev1.UnimplementedDialogueServiceServer
	uc *biz.DialogueUsecase
	sf snowflakeGen
}

// NewDialogueService 构造。
func NewDialogueService(uc *biz.DialogueUsecase, sf snowflakeGen) *DialogueService {
	return &DialogueService{uc: uc, sf: sf}
}

// StartDialogue 开启一次 NPC 对话。player_id 以 JWT ctx 为准(R5);dialogue_id 服务端生成。
func (s *DialogueService) StartDialogue(ctx context.Context, req *dialoguev1.StartDialogueRequest) (*dialoguev1.StartDialogueResponse, error) {
	playerID := callerID(ctx)
	if playerID == 0 {
		return &dialoguev1.StartDialogueResponse{Code: commonv1.ErrCode_ERR_UNAUTHORIZED}, nil
	}
	if req.GetNpcId() == 0 {
		return &dialoguev1.StartDialogueResponse{Code: commonv1.ErrCode_ERR_INVALID_ARG}, nil
	}

	state, err := s.uc.StartDialogue(ctx, playerID, req.GetNpcId(), s.sf.Generate())
	if err != nil {
		return &dialoguev1.StartDialogueResponse{Code: toProtoCode(err)}, nil
	}
	return &dialoguev1.StartDialogueResponse{Code: commonv1.ErrCode_OK, State: state}, nil
}

// ChooseOption 选择一个选项推进对话。player_id 以 JWT ctx 为准(R5)。
func (s *DialogueService) ChooseOption(ctx context.Context, req *dialoguev1.ChooseOptionRequest) (*dialoguev1.ChooseOptionResponse, error) {
	playerID := callerID(ctx)
	if playerID == 0 {
		return &dialoguev1.ChooseOptionResponse{Code: commonv1.ErrCode_ERR_UNAUTHORIZED}, nil
	}
	if req.GetDialogueId() == 0 || req.GetOptionId() == "" {
		return &dialoguev1.ChooseOptionResponse{Code: commonv1.ErrCode_ERR_INVALID_ARG}, nil
	}

	state, err := s.uc.ChooseOption(ctx, playerID, req.GetDialogueId(), req.GetOptionId())
	if err != nil {
		return &dialoguev1.ChooseOptionResponse{Code: toProtoCode(err)}, nil
	}
	return &dialoguev1.ChooseOptionResponse{Code: commonv1.ErrCode_OK, State: state}, nil
}

// EndDialogue 结束对话(幂等)。player_id 以 JWT ctx 为准(R5)。
func (s *DialogueService) EndDialogue(ctx context.Context, req *dialoguev1.EndDialogueRequest) (*dialoguev1.EndDialogueResponse, error) {
	playerID := callerID(ctx)
	if playerID == 0 {
		return &dialoguev1.EndDialogueResponse{Code: commonv1.ErrCode_ERR_UNAUTHORIZED}, nil
	}
	if req.GetDialogueId() == 0 {
		return &dialoguev1.EndDialogueResponse{Code: commonv1.ErrCode_ERR_INVALID_ARG}, nil
	}

	if err := s.uc.EndDialogue(ctx, playerID, req.GetDialogueId()); err != nil {
		return &dialoguev1.EndDialogueResponse{Code: toProtoCode(err)}, nil
	}
	return &dialoguev1.EndDialogueResponse{Code: commonv1.ErrCode_OK}, nil
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
