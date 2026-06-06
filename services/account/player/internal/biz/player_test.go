// player_test.go — PlayerUsecase 业务逻辑单测(W4 ④,2026-06-06)。
//
// 用内存版 fakeRepo 复刻 MySQL 幂等 / clamp / 战绩计数语义,无需真 DB。
package biz

import (
	"context"
	"strconv"
	"testing"

	"github.com/luyuancpp/pandora/pkg/errcode"
	playerv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/player/v1"
	"github.com/luyuancpp/pandora/services/account/player/internal/conf"
	"github.com/luyuancpp/pandora/services/account/player/internal/data"
)

// fakeProfile 是内存玩家档案。
type fakeProfile struct {
	nickname     string
	mmr          int
	totalBattles int32
	totalWins    int32
}

// fakeRepo 是 data.PlayerRepo 的内存实现(复刻 MySQL 幂等语义)。
type fakeRepo struct {
	players map[uint64]*fakeProfile
	heroes  map[uint64]map[uint32]bool
	idem    map[string]int // key=playerID|idempotencyKey → 已记录 new_mmr
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		players: map[uint64]*fakeProfile{},
		heroes:  map[uint64]map[uint32]bool{},
		idem:    map[string]int{},
	}
}

func (f *fakeRepo) EnsureProfile(_ context.Context, playerID uint64, defaultNickname string, baseMMR int) error {
	if _, ok := f.players[playerID]; !ok {
		f.players[playerID] = &fakeProfile{nickname: defaultNickname, mmr: baseMMR}
	}
	return nil
}

func (f *fakeRepo) GetProfile(_ context.Context, playerID uint64) (*playerv1.PlayerProfile, bool, error) {
	p, ok := f.players[playerID]
	if !ok {
		return nil, false, nil
	}
	return &playerv1.PlayerProfile{
		PlayerId:     playerID,
		Nickname:     p.nickname,
		Mmr:          int32(p.mmr),
		TotalBattles: p.totalBattles,
		TotalWins:    p.totalWins,
	}, true, nil
}

func (f *fakeRepo) UpdateNickname(_ context.Context, playerID uint64, nickname string) error {
	for pid, p := range f.players {
		if pid != playerID && p.nickname == nickname {
			return errcode.New(errcode.ErrPlayerNicknameTaken, "taken")
		}
	}
	p, ok := f.players[playerID]
	if !ok {
		return errcode.New(errcode.ErrPlayerNotFound, "not found")
	}
	p.nickname = nickname
	return nil
}

func (f *fakeRepo) ListHeroes(_ context.Context, playerID uint64) ([]uint32, error) {
	var out []uint32
	for h := range f.heroes[playerID] {
		out = append(out, h)
	}
	return out, nil
}

func (f *fakeRepo) UnlockHero(_ context.Context, playerID uint64, heroID uint32, _ string) (bool, error) {
	if f.heroes[playerID] == nil {
		f.heroes[playerID] = map[uint32]bool{}
	}
	if f.heroes[playerID][heroID] {
		return true, nil
	}
	f.heroes[playerID][heroID] = true
	return false, nil
}

func (f *fakeRepo) GetMMR(_ context.Context, playerID uint64) (int, bool, error) {
	p, ok := f.players[playerID]
	if !ok {
		return 0, false, nil
	}
	return p.mmr, true, nil
}

func (f *fakeRepo) ApplyMMRChange(_ context.Context, c data.MMRChange) (int, bool, error) {
	p, ok := f.players[c.PlayerID]
	if !ok {
		return 0, false, errcode.New(errcode.ErrPlayerNotFound, "not found")
	}
	idemKey := keyOf(c.PlayerID, c.IdempotencyKey)
	if recorded, hit := f.idem[idemKey]; hit {
		return recorded, true, nil
	}
	newMMR := p.mmr + int(c.Delta)
	if newMMR < c.Floor {
		newMMR = c.Floor
	}
	p.mmr = newMMR
	if c.IncBattle {
		p.totalBattles++
	}
	if c.IncWin {
		p.totalWins++
	}
	f.idem[idemKey] = newMMR
	return newMMR, false, nil
}

func keyOf(pid uint64, k string) string {
	return strconv.FormatUint(pid, 10) + "|" + k
}

func newUC(repo data.PlayerRepo) *PlayerUsecase {
	return NewPlayerUsecase(repo, conf.PlayerConf{BaseMMR: 1500, MMRFloor: 0, DefaultNicknamePrefix: "Player_", MaxNicknameLen: 32})
}

func TestUpdateMMR_AppliesDelta(t *testing.T) {
	repo := newFakeRepo()
	uc := newUC(repo)
	newMMR, already, err := uc.UpdateMMR(context.Background(), 100, 16, "win", "m1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if already {
		t.Fatal("first call should not be idempotent hit")
	}
	if newMMR != 1516 {
		t.Fatalf("want 1516, got %d", newMMR)
	}
	if repo.players[100].totalBattles != 1 || repo.players[100].totalWins != 1 {
		t.Fatalf("win should inc battle+win, got battles=%d wins=%d",
			repo.players[100].totalBattles, repo.players[100].totalWins)
	}
}

func TestUpdateMMR_Idempotent(t *testing.T) {
	repo := newFakeRepo()
	uc := newUC(repo)
	first, _, err := uc.UpdateMMR(context.Background(), 100, 16, "win", "m1")
	if err != nil {
		t.Fatalf("first err: %v", err)
	}
	second, already, err := uc.UpdateMMR(context.Background(), 100, 16, "win", "m1")
	if err != nil {
		t.Fatalf("second err: %v", err)
	}
	if !already {
		t.Fatal("second call with same key should be idempotent hit")
	}
	if second != first {
		t.Fatalf("idempotent return should equal first: first=%d second=%d", first, second)
	}
	if repo.players[100].mmr != 1516 {
		t.Fatalf("mmr must not double-apply, got %d", repo.players[100].mmr)
	}
	if repo.players[100].totalBattles != 1 {
		t.Fatalf("battles must not double-count, got %d", repo.players[100].totalBattles)
	}
}

func TestUpdateMMR_RequiresKey(t *testing.T) {
	uc := newUC(newFakeRepo())
	_, _, err := uc.UpdateMMR(context.Background(), 100, 16, "win", "")
	if errcode.As(err) != errcode.ErrInvalidArg {
		t.Fatalf("empty idempotency_key should be ErrInvalidArg, got %v", err)
	}
}

func TestUpdateMMR_Floor(t *testing.T) {
	repo := newFakeRepo()
	uc := newUC(repo)
	newMMR, _, err := uc.UpdateMMR(context.Background(), 100, -9999, "lose", "m1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if newMMR != 0 {
		t.Fatalf("mmr should clamp to floor 0, got %d", newMMR)
	}
}

func TestUpdateMMR_LoseCountsBattleNotWin(t *testing.T) {
	repo := newFakeRepo()
	uc := newUC(repo)
	if _, _, err := uc.UpdateMMR(context.Background(), 100, -16, "lose", "m1"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if repo.players[100].totalBattles != 1 || repo.players[100].totalWins != 0 {
		t.Fatalf("lose: battle+1 win+0, got battles=%d wins=%d",
			repo.players[100].totalBattles, repo.players[100].totalWins)
	}
}

func TestUpdateMMR_AbandonNoBattleCount(t *testing.T) {
	repo := newFakeRepo()
	uc := newUC(repo)
	if _, _, err := uc.UpdateMMR(context.Background(), 100, 0, "abandon", "m1"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if repo.players[100].totalBattles != 0 {
		t.Fatalf("abandon should not count battle, got %d", repo.players[100].totalBattles)
	}
}

func TestGetMMR_NotFoundReturnsBase(t *testing.T) {
	uc := newUC(newFakeRepo())
	mmr, err := uc.GetMMR(context.Background(), 999)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if mmr != 1500 {
		t.Fatalf("unbuilt player should return base 1500, got %d", mmr)
	}
}

func TestUnlockHero_Idempotent(t *testing.T) {
	uc := newUC(newFakeRepo())
	if err := uc.UnlockHero(context.Background(), 100, 7, "reward"); err != nil {
		t.Fatalf("first unlock err: %v", err)
	}
	err := uc.UnlockHero(context.Background(), 100, 7, "reward")
	if errcode.As(err) != errcode.ErrPlayerHeroAlreadyOwn {
		t.Fatalf("second unlock should be ErrPlayerHeroAlreadyOwn, got %v", err)
	}
}

func TestUpdateNickname_Validation(t *testing.T) {
	uc := newUC(newFakeRepo())
	if err := uc.UpdateNickname(context.Background(), 100, "   "); errcode.As(err) != errcode.ErrInvalidArg {
		t.Fatalf("blank nickname should be ErrInvalidArg, got %v", err)
	}
	if err := uc.UpdateNickname(context.Background(), 0, "ok"); errcode.As(err) != errcode.ErrInvalidArg {
		t.Fatalf("zero player_id should be ErrInvalidArg, got %v", err)
	}
}

func TestBattleFlags(t *testing.T) {
	cases := []struct {
		reason             string
		wantBattle, wantWin bool
	}{
		{"win", true, true},
		{"lose", true, false},
		{"draw", true, false},
		{"abandon", false, false},
		{"rollback", false, false},
		{"", false, false},
	}
	for _, c := range cases {
		b, w := battleFlags(c.reason)
		if b != c.wantBattle || w != c.wantWin {
			t.Fatalf("reason=%q: want (battle=%v win=%v), got (%v %v)", c.reason, c.wantBattle, c.wantWin, b, w)
		}
	}
}
