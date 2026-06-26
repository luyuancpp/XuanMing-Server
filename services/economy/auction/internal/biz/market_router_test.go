// market_router_test.go — 市场→实例归属一致性哈希(HRW)纯函数单测。
package biz

import "testing"

func TestNewMarketRouter_DedupsAndIncludesSelf(t *testing.T) {
	r, ok := NewMarketRouter("a", []string{"a", "b", "b", "", "c"})
	if !ok {
		t.Fatal("expected ok")
	}
	if r.Self() != "a" {
		t.Fatalf("self = %q, want a", r.Self())
	}
	if r.PeerCount() != 3 {
		t.Fatalf("peer count = %d, want 3 (a,b,c deduped)", r.PeerCount())
	}
}

func TestNewMarketRouter_SelfAutoAdded(t *testing.T) {
	r, ok := NewMarketRouter("z", []string{"a", "b"})
	if !ok {
		t.Fatal("expected ok")
	}
	if r.PeerCount() != 3 {
		t.Fatalf("peer count = %d, want 3 (self auto-added)", r.PeerCount())
	}
}

func TestNewMarketRouter_EmptySelfNotOk(t *testing.T) {
	if _, ok := NewMarketRouter("", []string{"a"}); ok {
		t.Fatal("empty self should return ok=false")
	}
}

func TestMarketRouter_SingleInstanceOwnsAll(t *testing.T) {
	r, _ := NewMarketRouter("solo", nil)
	for m := uint32(1); m <= 100; m++ {
		if !r.OwnsMarket(m) {
			t.Fatalf("single instance should own market %d", m)
		}
		if r.Owner(m) != "solo" {
			t.Fatalf("owner of %d = %q, want solo", m, r.Owner(m))
		}
	}
}

// Owner 在固定成员集下对同一 market 恒定(确定性),且不同实例视角一致。
func TestMarketRouter_DeterministicAndConsistentAcrossViews(t *testing.T) {
	peers := []string{"n1", "n2", "n3", "n4"}
	views := make([]*MarketRouter, len(peers))
	for i, self := range peers {
		r, ok := NewMarketRouter(self, peers)
		if !ok {
			t.Fatalf("build view %s", self)
		}
		views[i] = r
	}
	for m := uint32(1); m <= 500; m++ {
		owner := views[0].Owner(m)
		// 所有实例对同一 market 的 owner 判定一致
		for _, v := range views[1:] {
			if v.Owner(m) != owner {
				t.Fatalf("market %d owner disagreement: %q vs %q", m, owner, v.Owner(m))
			}
		}
		// 恰好一个实例 OwnsMarket==true
		owned := 0
		for _, v := range views {
			if v.OwnsMarket(m) {
				owned++
			}
		}
		if owned != 1 {
			t.Fatalf("market %d owned by %d instances, want exactly 1", m, owned)
		}
	}
}

// 负载大致均衡:4 个实例分 4000 个 market,每个实例份额应在均值 ±35% 内(HRW 均衡性)。
func TestMarketRouter_RoughlyBalanced(t *testing.T) {
	peers := []string{"n1", "n2", "n3", "n4"}
	r, _ := NewMarketRouter("n1", peers)
	const total = 4000
	counts := map[string]int{}
	for m := uint32(1); m <= total; m++ {
		counts[r.Owner(m)]++
	}
	mean := total / len(peers)
	for _, p := range peers {
		c := counts[p]
		if c < mean*65/100 || c > mean*135/100 {
			t.Fatalf("instance %s got %d markets, want within ±35%% of mean %d", p, c, mean)
		}
	}
}

// 成员增减时,最小迁移:加一个实例后,改变归属的 market 比例应接近 1/N(一致性哈希性质)。
func TestMarketRouter_MinimalReshuffleOnGrow(t *testing.T) {
	before, _ := NewMarketRouter("n1", []string{"n1", "n2", "n3"})
	after, _ := NewMarketRouter("n1", []string{"n1", "n2", "n3", "n4"})
	const total = 6000
	moved := 0
	for m := uint32(1); m <= total; m++ {
		if before.Owner(m) != after.Owner(m) {
			moved++
		}
	}
	// 3→4 实例,理论迁移 ~1/4=25%;放宽到 (10%, 45%) 防偶发
	frac := float64(moved) / float64(total)
	if frac < 0.10 || frac > 0.45 {
		t.Fatalf("moved fraction = %.3f (%d/%d), want ~0.25 (minimal reshuffle)", frac, moved, total)
	}
}

func TestMarketRouter_TieBreakDeterministic(t *testing.T) {
	// 同一 market 多次询问 owner 必须一致(无随机)。
	r, _ := NewMarketRouter("n1", []string{"n1", "n2"})
	first := r.Owner(42)
	for i := 0; i < 20; i++ {
		if got := r.Owner(42); got != first {
			t.Fatalf("owner non-deterministic: %q vs %q", got, first)
		}
	}
}
