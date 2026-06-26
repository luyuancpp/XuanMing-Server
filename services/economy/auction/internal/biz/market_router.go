// market_router.go — 拍卖「市场 → 实例归属」路由(rendezvous / HRW 一致性哈希),纯函数。
//
// 决策 docs/design/decision-revisit-auction-engine.md §3.2/§7.2 + scale-cellular-20m.md §4.3
// (方案② 跨 region 全局市场,按 market_id 全局分片):
//   - 同一 market_id 固定路由到同一撮合实例,让「跨实例 per-market 单写者」的锁竞争降到最低
//     (绝大多数时间同一 market 只有 owner 实例在撮合 → MarketLocker 几乎不抢锁);
//   - 路由抖动 / rebalance 时仍由 MarketLocker(Redis 单写者 token)兜底跨实例互斥,不会超卖。
//
// 用 rendezvous(HRW,最高随机权重)哈希而非环:无需共享/可变环状态,纯算确定;成员增减时
// 只有 ~1/N 的 market 改归属(与一致性哈希同等的最小迁移),且天然均衡,契合「无状态 + 算不查」
// 架构基因。本文件只算归属;真正的跨实例转发 / 服务发现属基础设施(AGENTS.md §11.1,Codex/人)。
package biz

import "hash/fnv"

// MarketRouter 决定某 market_id 由哪个 auction 实例独占撮合。不可变,构造后并发只读安全。
type MarketRouter struct {
	self  string   // 本实例 ID(必须在 peers 内)
	peers []string // 全部 auction 实例 ID(含 self),已去重(顺序不影响结果,HRW 与顺序无关)
}

// NewMarketRouter 构造路由器。self 必须出现在 peers 中;peers 去重后至少 1 个。
// peers 为空或仅含 self → 单实例,本实例拥有全部 market(退化为现状)。
func NewMarketRouter(self string, peers []string) (*MarketRouter, bool) {
	if self == "" {
		return nil, false
	}
	seen := make(map[string]struct{}, len(peers))
	dedup := make([]string, 0, len(peers)+1)
	for _, p := range peers {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		dedup = append(dedup, p)
	}
	if _, ok := seen[self]; !ok {
		seen[self] = struct{}{}
		dedup = append(dedup, self)
	}
	return &MarketRouter{self: self, peers: dedup}, true
}

// Self 返回本实例 ID。
func (r *MarketRouter) Self() string { return r.self }

// PeerCount 返回参与分片的实例数。
func (r *MarketRouter) PeerCount() int { return len(r.peers) }

// Owner 返回某 market_id 的归属实例 ID(HRW:取 hash(peer, market) 最大者)。
// peers 只有 1 个时恒返回该实例;权重并列时按实例 ID 字典序较大者取胜(确定性 tiebreak)。
func (r *MarketRouter) Owner(marketID uint32) string {
	if len(r.peers) == 0 {
		return r.self
	}
	var bestPeer string
	var bestScore uint64
	for _, p := range r.peers {
		s := hrwScore(p, marketID)
		if bestPeer == "" || s > bestScore || (s == bestScore && p > bestPeer) {
			bestPeer = p
			bestScore = s
		}
	}
	return bestPeer
}

// OwnsMarket 判断本实例是否为某 market_id 的归属撮合者。
func (r *MarketRouter) OwnsMarket(marketID uint32) bool {
	return r.Owner(marketID) == r.self
}

// hrwScore 计算 (peer, market) 的 rendezvous 权重。
//
// 分别独立哈希 peer 与 market(各自经 FNV-1a 末轮乘法充分扩散),再 hash_combine + splitmix64
// 收尾混合:即便实例 ID 仅末位不同(n1/n2/n3…),也能在不同 market 上均匀轮流胜出。
// 单遍 FNV(把 market、peer 顺序写进同一哈希)对这种"近似 ID"扩散不足,会让某实例恒定胜出,
// 故此处显式双哈希 + 强 finalizer。
func hrwScore(peer string, marketID uint32) uint64 {
	hn := fnv64aString(peer)
	hk := fnv64aUint32(marketID)
	// boost 风格 hash_combine,再 splitmix64 finalize 拿到良好雪崩。
	z := hn ^ (hk + 0x9e3779b97f4a7c15 + (hn << 6) + (hn >> 2))
	return splitmix64(z)
}

func fnv64aString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func fnv64aUint32(v uint32) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
	return h.Sum64()
}

// splitmix64 是标准强混合 finalizer(良好 64 位雪崩),用于 HRW 评分去相关。
func splitmix64(z uint64) uint64 {
	z += 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
