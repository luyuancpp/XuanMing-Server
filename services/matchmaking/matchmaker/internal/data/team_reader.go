// team_reader.go 实现 biz.TeamReader:通过 gRPC 拉取 team 服务的队伍快照。
package data

import (
	"context"

	"google.golang.org/grpc"

	"github.com/luyuancpp/pandora/pkg/grpcclient"
	commonv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/common/v1"
	teamv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/team/v1"
)

// GrpcTeamReader 用 team 服务 gRPC client 实现 biz.TeamReader。
type GrpcTeamReader struct {
	conn *grpc.ClientConn
	cli  teamv1.TeamServiceClient
}

// NewGrpcTeamReader 直连 team 服务 endpoint(host:port,内网 insecure)。
func NewGrpcTeamReader(teamAddr string) *GrpcTeamReader {
	conn := grpcclient.MustDialInsecure(teamAddr)
	return &GrpcTeamReader{conn: conn, cli: teamv1.NewTeamServiceClient(conn)}
}

// Close 关闭底层连接。
func (g *GrpcTeamReader) Close() error {
	if g.conn != nil {
		return g.conn.Close()
	}
	return nil
}

// GetTeam 调 team 服务 GetTeam,返回完整队伍快照。
// team 服务返回非 OK code 时,统一转成 (nil, false, nil)(由 biz 决定如何处理)。
func (g *GrpcTeamReader) GetTeam(ctx context.Context, teamID uint64) (*teamv1.Team, bool, error) {
	resp, err := g.cli.GetTeam(ctx, &teamv1.GetTeamRequest{TeamId: teamID})
	if err != nil {
		return nil, false, err
	}
	if resp.GetCode() != commonv1.ErrCode_OK || resp.GetTeam() == nil {
		return nil, false, nil
	}
	return resp.GetTeam(), true, nil
}
