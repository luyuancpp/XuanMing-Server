// Package service 是 player 服务的 gRPC service 层(W4 ④,2026-06-06)。
//
// 职责:
//   - 实现 playerv1.PlayerServiceServer 接口
//   - proto Request/Response ↔ biz 入参/出参互转
//   - errcode.Code → commonv1.ErrCode 1:1 映射
//
// 说明:调用方为后端内部(battle_result GetMMR)/ 经 Envoy 的客户端(GetProfile 等),
// player_id 由 proto 字段显式传入(不从 ctx 取),鉴权由 Envoy jwt_authn 完成。
package service

import (
	"context"

	"github.com/luyuancpp/pandora/pkg/errcode"
	commonv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/common/v1"
	playerv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/player/v1"

	"github.com/luyuancpp/pandora/services/account/player/internal/biz"
)

// PlayerService 实现 playerv1.PlayerServiceServer。
type PlayerService struct {
	playerv1.UnimplementedPlayerServiceServer
	uc *biz.PlayerUsecase
}

// NewPlayerService 构造。
func NewPlayerService(uc *biz.PlayerUsecase) *PlayerService {
	return &PlayerService{uc: uc}
}

// GetProfile 读玩家档案(懒创建)。
func (s *PlayerService) GetProfile(ctx context.Context, req *playerv1.GetProfileRequest) (*playerv1.GetProfileResponse, error) {
	if req.GetPlayerId() == 0 {
		return &playerv1.GetProfileResponse{Code: commonv1.ErrCode_ERR_INVALID_ARG}, nil
	}
	profile, err := s.uc.GetProfile(ctx, req.GetPlayerId())
	if err != nil {
		return &playerv1.GetProfileResponse{Code: toProtoCode(err)}, nil
	}
	return &playerv1.GetProfileResponse{Code: commonv1.ErrCode_OK, Profile: profile}, nil
}

// UpdateNickname 改昵称。
func (s *PlayerService) UpdateNickname(ctx context.Context, req *playerv1.UpdateNicknameRequest) (*playerv1.UpdateNicknameResponse, error) {
	if req.GetPlayerId() == 0 {
		return &playerv1.UpdateNicknameResponse{Code: commonv1.ErrCode_ERR_INVALID_ARG}, nil
	}
	if err := s.uc.UpdateNickname(ctx, req.GetPlayerId(), req.GetNickname()); err != nil {
		return &playerv1.UpdateNicknameResponse{Code: toProtoCode(err)}, nil
	}
	return &playerv1.UpdateNicknameResponse{Code: commonv1.ErrCode_OK}, nil
}

// ListHeroes 列出玩家已解锁英雄。
func (s *PlayerService) ListHeroes(ctx context.Context, req *playerv1.ListHeroesRequest) (*playerv1.ListHeroesResponse, error) {
	if req.GetPlayerId() == 0 {
		return &playerv1.ListHeroesResponse{Code: commonv1.ErrCode_ERR_INVALID_ARG}, nil
	}
	heroes, err := s.uc.ListHeroes(ctx, req.GetPlayerId())
	if err != nil {
		return &playerv1.ListHeroesResponse{Code: toProtoCode(err)}, nil
	}
	return &playerv1.ListHeroesResponse{Code: commonv1.ErrCode_OK, HeroIds: heroes}, nil
}

// UnlockHero 解锁英雄。
func (s *PlayerService) UnlockHero(ctx context.Context, req *playerv1.UnlockHeroRequest) (*playerv1.UnlockHeroResponse, error) {
	if req.GetPlayerId() == 0 || req.GetHeroId() == 0 {
		return &playerv1.UnlockHeroResponse{Code: commonv1.ErrCode_ERR_INVALID_ARG}, nil
	}
	if err := s.uc.UnlockHero(ctx, req.GetPlayerId(), req.GetHeroId(), req.GetSource()); err != nil {
		return &playerv1.UnlockHeroResponse{Code: toProtoCode(err)}, nil
	}
	return &playerv1.UnlockHeroResponse{Code: commonv1.ErrCode_OK}, nil
}

// GetMMR 读玩家当前 MMR(供 battle_result 当 reader)。
func (s *PlayerService) GetMMR(ctx context.Context, req *playerv1.GetMMRRequest) (*playerv1.GetMMRResponse, error) {
	if req.GetPlayerId() == 0 {
		return &playerv1.GetMMRResponse{Code: commonv1.ErrCode_ERR_INVALID_ARG}, nil
	}
	mmr, err := s.uc.GetMMR(ctx, req.GetPlayerId())
	if err != nil {
		return &playerv1.GetMMRResponse{Code: toProtoCode(err)}, nil
	}
	return &playerv1.GetMMRResponse{Code: commonv1.ErrCode_OK, Mmr: int32(mmr)}, nil
}

// UpdateMMR 幂等改 MMR(同步兜底;正常链路走 kafka 消费 player.update)。
func (s *PlayerService) UpdateMMR(ctx context.Context, req *playerv1.UpdateMMRRequest) (*playerv1.UpdateMMRResponse, error) {
	if req.GetPlayerId() == 0 {
		return &playerv1.UpdateMMRResponse{Code: commonv1.ErrCode_ERR_INVALID_ARG}, nil
	}
	if req.GetIdempotencyKey() == "" {
		return &playerv1.UpdateMMRResponse{Code: commonv1.ErrCode_ERR_INVALID_ARG}, nil
	}
	newMMR, _, err := s.uc.UpdateMMR(ctx, req.GetPlayerId(), req.GetDelta(), req.GetReason(), req.GetIdempotencyKey())
	if err != nil {
		return &playerv1.UpdateMMRResponse{Code: toProtoCode(err)}, nil
	}
	return &playerv1.UpdateMMRResponse{Code: commonv1.ErrCode_OK, NewMmr: int32(newMMR)}, nil
}

// toProtoCode 把 pkg/errcode 1:1 映射成 proto enum(数值相同)。
func toProtoCode(err error) commonv1.ErrCode {
	return commonv1.ErrCode(errcode.As(err))
}
