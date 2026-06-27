// reward_client.go 实现 biz.RewardGranter:把结算名次奖励经 gRPC 交给 inventory 服务
// 幂等发放到玩家背包(GrantItems,幂等键 = lb:<settlement_id>:<entity_id>,不变量 §9.7)。
//
// 接线(对齐 auction/settlement_client 直连模式):
//   - main.go 用 pkg/grpcclient.MustDialInsecure 拨号(内网 insecure;GrantItems 是系统接口只认内网直连);
//   - inventory_addr 未配且 allow_noop_reward=true 时 main 才退回 NoopRewardGranter,否则 fail-fast。
//
// 注意:工会榜(scope=GUILD)entity_id 是 guild_id,不是 player_id;GrantItems 是发给玩家的,
// 工会奖励的分发(发到工会仓库 / 拆给成员)不在 leaderboard 职责内 —— biz 仅对「按玩家发奖」的榜
// 调本 granter,工会榜结算默认只落快照 + kafka 事件,由工会服务消费分发。
package data

import (
	"context"

	"google.golang.org/grpc"

	"github.com/luyuancpp/pandora/pkg/errcode"
	"github.com/luyuancpp/pandora/pkg/grpcclient"
	commonv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/common/v1"
	inventoryv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/inventory/v1"
)

// RewardGrant 是发给单个玩家的一份奖励(item + count,对齐 inventory ItemGrant)。
type RewardGrant struct {
	ItemConfigID uint32
	Count        int64
}

// GrpcInventoryRewardGranter 用 inventory 服务 gRPC client 发奖。
type GrpcInventoryRewardGranter struct {
	conn *grpc.ClientConn
	cli  inventoryv1.InventoryServiceClient
}

// NewGrpcInventoryRewardGranter 直连 inventory 服务 endpoint(host:port,内网 insecure)。
func NewGrpcInventoryRewardGranter(inventoryAddr string) *GrpcInventoryRewardGranter {
	conn := grpcclient.MustDialInsecure(inventoryAddr)
	return &GrpcInventoryRewardGranter{conn: conn, cli: inventoryv1.NewInventoryServiceClient(conn)}
}

// Close 关闭底层连接。
func (g *GrpcInventoryRewardGranter) Close() error {
	if g.conn != nil {
		return g.conn.Close()
	}
	return nil
}

// Grant 幂等发放一组奖励给玩家。
//
//   - 返回 OK → nil(发放成功 / 幂等回放)
//   - 其它非 OK code → ErrLeaderboardRewardFailed(透传 code 便于定位)
func (g *GrpcInventoryRewardGranter) Grant(ctx context.Context, playerID uint64, idemKey string, items []RewardGrant) error {
	grants := make([]*inventoryv1.ItemGrant, 0, len(items))
	for _, it := range items {
		if it.Count <= 0 {
			continue
		}
		grants = append(grants, &inventoryv1.ItemGrant{ItemConfigId: it.ItemConfigID, Count: it.Count})
	}
	if len(grants) == 0 {
		return nil
	}
	resp, err := g.cli.GrantItems(ctx, &inventoryv1.GrantItemsRequest{
		PlayerId:       playerID,
		Items:          grants,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		return err
	}
	if resp.GetCode() != commonv1.ErrCode_OK {
		return errcode.New(errcode.ErrLeaderboardRewardFailed,
			"lb reward grant failed player=%d key=%s code=%d", playerID, idemKey, int32(resp.GetCode()))
	}
	return nil
}
