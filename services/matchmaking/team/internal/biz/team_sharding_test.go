// team_sharding_test.go — 队伍 owner cell 锚定 + 跨 region 组队判定单测(2026-06-26)。
//
// 覆盖:TeamShardKey canonical 口径(= captain_id,与 team_id 无关)、DistinctTeamRegions 去重升序、
// CrossRegionTeam 判定、teamMemberRegions nil-router 退化 / 解析多成员 / 跳过零 player_id。
// 验证 router 为 nil(单 Cell)时行为不变,注入后能正确判定队伍 region 分布。
package biz

import (
	"reflect"
	"testing"

	"github.com/luyuancpp/pandora/pkg/cellroute"
	teamv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/team/v1"
	"github.com/luyuancpp/pandora/services/matchmaking/team/internal/conf"
)

// twoRegionTeamRouter 造一张前半 region1 / 后半 region2 的均衡路由表,
// 用于让不同成员落不同 region 验证跨 region 组队判定。
func twoRegionTeamRouter(t *testing.T) *cellroute.Router {
	t.Helper()
	specs := []cellroute.CellSpec{
		{RegionID: 1, CellID: 1},
		{RegionID: 2, CellID: 2},
	}
	entries, regionOfCell, err := cellroute.BuildBalancedEntries(specs)
	if err != nil {
		t.Fatalf("BuildBalancedEntries: %v", err)
	}
	tbl, err := cellroute.NewStaticTable(entries, regionOfCell)
	if err != nil {
		t.Fatalf("NewStaticTable: %v", err)
	}
	r, err := cellroute.NewRouter(tbl)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

// 落 region1(逻辑格在前半)/ region2(逻辑格在后半)的 player_id 取样。
func teamPlayerRegion1() uint64 { return 1 }
func teamPlayerRegion2() uint64 { return uint64(cellroute.LogicalCellCount/2 + 1) }

func teamWith(captainID uint64, memberIDs ...uint64) *teamv1.TeamStorageRecord {
	members := make([]*teamv1.TeamMemberStorageRecord, 0, len(memberIDs))
	for _, id := range memberIDs {
		members = append(members, &teamv1.TeamMemberStorageRecord{PlayerId: id})
	}
	return &teamv1.TeamStorageRecord{TeamId: 99999, CaptainId: captainID, Members: members}
}

func TestTeamShardKey_IsCaptainID(t *testing.T) {
	if got := TeamShardKey(7); got != "7" {
		t.Fatalf("TeamShardKey(7) = %q, want \"7\"", got)
	}
}

func TestTeamShardKey_IndependentOfTeamID(t *testing.T) {
	// 队长不变 → 队伍恒锚定同 owner cell,与 team_id 无关。
	if TeamShardKey(42) != TeamShardKey(42) {
		t.Fatal("same captain should yield same shard key")
	}
	if TeamShardKey(42) == TeamShardKey(43) {
		t.Fatal("different captains should yield different shard keys")
	}
}

func TestDistinctTeamRegions_SortedDedup(t *testing.T) {
	got := DistinctTeamRegions([]uint32{2, 1, 2, 3, 1})
	want := []uint32{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DistinctTeamRegions = %v, want %v", got, want)
	}
	if DistinctTeamRegions(nil) != nil {
		t.Fatal("empty input should yield nil")
	}
}

func TestCrossRegionTeam(t *testing.T) {
	if CrossRegionTeam([]uint32{1, 1}) {
		t.Fatal("single region should not be cross-region")
	}
	if !CrossRegionTeam([]uint32{1, 2}) {
		t.Fatal("two regions should be cross-region")
	}
	if CrossRegionTeam(nil) {
		t.Fatal("empty should not be cross-region")
	}
}

func TestTeamMemberRegions_NilRouter(t *testing.T) {
	uc := NewTeamUsecase(nil, nil, defaultTeamConf(t))
	if _, ok := uc.teamMemberRegions(teamWith(1, 1, 2)); ok {
		t.Fatal("nil router should yield ok=false")
	}
}

func TestTeamMemberRegions_ResolvesCrossRegion(t *testing.T) {
	uc := NewTeamUsecase(nil, nil, defaultTeamConf(t))
	uc.SetCellRouter(twoRegionTeamRouter(t))
	team := teamWith(teamPlayerRegion1(), teamPlayerRegion1(), teamPlayerRegion2())
	regions, ok := uc.teamMemberRegions(team)
	if !ok {
		t.Fatal("router should resolve member regions")
	}
	if !CrossRegionTeam(regions) {
		t.Fatalf("team spanning region1 + region2 should be cross-region, got regions=%v", regions)
	}
}

func TestTeamMemberRegions_SkipsZeroPlayer(t *testing.T) {
	uc := NewTeamUsecase(nil, nil, defaultTeamConf(t))
	uc.SetCellRouter(twoRegionTeamRouter(t))
	// 含一个 player_id=0 的脏成员,应被跳过,仅解析有效成员。
	team := teamWith(teamPlayerRegion1(), teamPlayerRegion1(), 0)
	regions, ok := uc.teamMemberRegions(team)
	if !ok {
		t.Fatal("router should resolve")
	}
	if len(regions) != 1 || regions[0] != 1 {
		t.Fatalf("want single region [1] (zero player skipped), got %v", regions)
	}
}

func defaultTeamConf(t *testing.T) conf.TeamConf {
	t.Helper()
	var cfg conf.Config
	cfg.Defaults()
	return cfg.Team
}
