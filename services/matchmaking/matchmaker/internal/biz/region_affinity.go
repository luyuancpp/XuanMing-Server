// region_affinity.go — 两级撮合(region 内优先 + 跨 region 溢出)的核心算法,纯函数。
//
// 对应决策 docs/design/decision-revisit-global-matchmaker.md §2.2:
//   - 默认撮合域 = owner region 内;
//   - 等待超 T_overflow(段位越高越短)且本 region 同段位不足 → 允许跨 region 溢出;
//   - 跨 region 候选评分加 RTT 亲和度惩罚(同 region 0 惩罚,永远优先同 region);
//   - 一局内跨 region 玩家比例软上限(防一局横跨三区体验崩坏);
//   - battle Cell 选参战玩家多数所在 region。
//
// 本文件只实现"算法",不接 etcd / 跨 region Kafka / 溢出池存储——那些是阶段 3 基础设施
// (scale-cellular-20m.md §7、AGENTS.md §11.1 由 Codex/人接)。算法先行 + 单测,落地时直接复用。
//
// 注:这些纯函数当前不接入 matchOnce 主循环(单 Cell / 阶段 1~2 不跨 region);阶段 3 接入
// 跨 region 溢出层时,由溢出撮合路径调用本文件函数。保持与现有 withinWindow/binPack 同风格。
package biz

import (
	"sort"

	matchv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/match/v1"
)

// RegionMatchPolicy 是跨 region 撮合策略(决策文档 §2.2 的可调参数)。
// 自包含,不进 conf.MatchConf:跨 region 溢出是阶段 3 才接的路径,先以策略结构 + 默认值
// 形式落地算法,main 在阶段 3 装配时再从配置填充,避免现在改动 conf YAML 加载。
type RegionMatchPolicy struct {
	// RTTPenaltyPerMs 是跨 region 候选每 1ms 估计 RTT 的评分惩罚(w_rtt)。
	// 同 region RTT 视为 0 → 0 惩罚,永远优先同 region。值越大越抗拒跨 region。
	RTTPenaltyPerMs float64

	// CrossRegionRatioCapPct 是一局内跨 region(非多数 region)玩家比例软上限(百分比,0~100)。
	// 默认 40:一局里少数派 region 玩家占比不超过 40%(决策文档 §2.2)。
	CrossRegionRatioCapPct int

	// OverflowBaseMs 是最低段位的溢出等待阈值(ms)。段位越高,实际阈值越短(见 OverflowThresholdMs)。
	OverflowBaseMs int64

	// OverflowShortenPerTierMs 是每升一个段位档,溢出阈值缩短的 ms。高分段人稀,早点跨 region。
	OverflowShortenPerTierMs int64

	// OverflowMinMs 是溢出阈值下限(ms),再高段位也不低于此,避免过早跨 region。
	OverflowMinMs int64

	// MmrBucketWidth 是段位桶宽度(分):溢出池按 mmr/Width 分桶(key=mmr_bucket),
	// 避免单一全局大池热点(decision-revisit-global-matchmaker.md §2.3)。默认 200。
	MmrBucketWidth int32

	// TierBaseMmr 是段位档 0(普通段)的 MMR 上界:≤ 此值算 tier 0。默认 2000。
	TierBaseMmr int32

	// TierStepMmr 是每升一个段位档所需的 MMR 增量(高于 TierBaseMmr 后)。默认 400。
	// tier 越高溢出阈值越短(高分段人稀,早点跨 region)。默认 400。
	TierStepMmr int32
}

// DefaultRegionMatchPolicy 返回一套保守默认值(决策文档 §2.2 量级:钻石+ ~30s、普通段 ~90s)。
func DefaultRegionMatchPolicy() RegionMatchPolicy {
	return RegionMatchPolicy{
		RTTPenaltyPerMs:          2.0,   // 每 1ms RTT 扣 2 分(与 MMR 差同量纲,见 CandidateScore)
		CrossRegionRatioCapPct:   40,    // 一局跨 region 玩家 ≤40%
		OverflowBaseMs:           90000, // 普通段 90s 才溢出
		OverflowShortenPerTierMs: 20000, // 每高一档减 20s
		OverflowMinMs:            30000, // 不低于 30s(高分段下限)
		MmrBucketWidth:           200,   // 段位桶宽 200 分
		TierBaseMmr:              2000,  // ≤2000 算普通段(tier 0)
		TierStepMmr:              400,   // 每 +400 分升一档
	}
}

// MmrBucket 返回某 MMR 对应的段位桶编号(溢出池 key=mmr_bucket,§2.3)。
// 负 MMR 归桶 0;MmrBucketWidth ≤ 0 时退化为单桶 0(配置缺省保护)。
func (p RegionMatchPolicy) MmrBucket(mmr int32) uint32 {
	if mmr < 0 {
		mmr = 0
	}
	if p.MmrBucketWidth <= 0 {
		return 0
	}
	return uint32(mmr / p.MmrBucketWidth)
}

// MmrTier 返回某 MMR 的段位档(0=普通段,越大段位越高 → 溢出阈值越短)。
// tier = max(0, (mmr - TierBaseMmr) / TierStepMmr);TierStepMmr ≤ 0 时恒 0(单档保护)。
func (p RegionMatchPolicy) MmrTier(mmr int32) int {
	if mmr <= p.TierBaseMmr || p.TierStepMmr <= 0 {
		return 0
	}
	return int((mmr - p.TierBaseMmr) / p.TierStepMmr)
}

// OverflowThresholdMs 返回某段位档 tier 的溢出等待阈值(ms)。
// tier=0 为最低档,越大段位越高;阈值 = clamp(Base - tier×Shorten, Min, Base)。
func (p RegionMatchPolicy) OverflowThresholdMs(tier int) int64 {
	if tier < 0 {
		tier = 0
	}
	th := p.OverflowBaseMs - int64(tier)*p.OverflowShortenPerTierMs
	if th < p.OverflowMinMs {
		th = p.OverflowMinMs
	}
	if th > p.OverflowBaseMs {
		th = p.OverflowBaseMs
	}
	return th
}

// ShouldOverflow 判断一张票据是否到了"允许跨 region 溢出"的时机。
//
// 双条件(决策文档 §2.2):等待时长已过该段位阈值 **且** 本 region 同段位候选不足成局
// (localCandidatesEnough=false)。两者皆满足才放开跨 region,避免人够还跨区。
func (p RegionMatchPolicy) ShouldOverflow(waitMs int64, tier int, localCandidatesEnough bool) bool {
	if localCandidatesEnough {
		return false
	}
	return waitMs >= p.OverflowThresholdMs(tier)
}

// CandidateScore 给一个候选(队伍/玩家)在某锚点视角下打分,分越高越优先入选。
//
// 评分 = -|mmrDiff| - RTT 惩罚:MMR 越接近越好,跨 region RTT 越大惩罚越重。
// 同 region(anchorRegion==candidateRegion)estRTTMs 视为 0 → 仅 MMR 决定,永远优先同 region。
func (p RegionMatchPolicy) CandidateScore(mmrDiff int, anchorRegion, candidateRegion uint32, estRTTMs int) float64 {
	if mmrDiff < 0 {
		mmrDiff = -mmrDiff
	}
	score := -float64(mmrDiff)
	if candidateRegion != anchorRegion {
		score -= p.RTTPenaltyPerMs * float64(estRTTMs)
	}
	return score
}

// MajorityRegion 返回一组玩家所属 region 里的多数派(出现次数最多的 region)。
// 用于决策文档 §2.2 "battle Cell 选参战玩家多数所在 region"。
// regions 为空返回 (0, false);并列时返回 region 值较小者(确定性)。
func MajorityRegion(regions []uint32) (uint32, bool) {
	if len(regions) == 0 {
		return 0, false
	}
	count := make(map[uint32]int, len(regions))
	for _, r := range regions {
		count[r]++
	}
	// 确定性:先按 region 值排序,再取计数最大者(并列取最小 region)。
	keys := make([]uint32, 0, len(count))
	for r := range count {
		keys = append(keys, r)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	best := keys[0]
	for _, r := range keys[1:] {
		if count[r] > count[best] {
			best = r
		}
	}
	return best, true
}

// CellLocation 是一名玩家的物理落点 (region, cell),用于 battle DS 放置选择。
// 与 cellroute.Location 解耦(只取放置决策需要的两维),让放置算法是纯函数、易测。
type CellLocation struct {
	RegionID uint32
	CellID   uint32
}

// MajorityCellLocation 返回一组参战玩家落点里的多数派 (region, cell)。
//
// scale-cellular-20m.md §4.4/§5:对局在"参战玩家多数所在 region 的 Cell"拉起 battle DS,
// 让多数玩家就近连入,少数跨 region 玩家承担稍高 RTT;结算仍各自回 owner cell(不变量不破)。
// locs 为空返回 (CellLocation{}, false);计数并列时按 (region, cell) 升序取最小者(确定性)。
func MajorityCellLocation(locs []CellLocation) (CellLocation, bool) {
	if len(locs) == 0 {
		return CellLocation{}, false
	}
	count := make(map[CellLocation]int, len(locs))
	for _, l := range locs {
		count[l]++
	}
	// 确定性:先按 (region, cell) 升序排候选,再取计数最大者(并列取最小落点)。
	keys := make([]CellLocation, 0, len(count))
	for l := range count {
		keys = append(keys, l)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].RegionID != keys[j].RegionID {
			return keys[i].RegionID < keys[j].RegionID
		}
		return keys[i].CellID < keys[j].CellID
	})
	best := keys[0]
	for _, l := range keys[1:] {
		if count[l] > count[best] {
			best = l
		}
	}
	return best, true
}

// WithinCrossRegionCap 判断一局玩家的 region 分布是否满足"跨 region 比例软上限"。
//
// 取多数 region 为本局主 region,其余(少数派)玩家即"跨 region";少数派占比 ≤
// CrossRegionRatioCapPct 才合规。空输入视为合规(无玩家无跨区)。
func (p RegionMatchPolicy) WithinCrossRegionCap(playerRegions []uint32) bool {
	n := len(playerRegions)
	if n == 0 {
		return true
	}
	major, ok := MajorityRegion(playerRegions)
	if !ok {
		return true
	}
	minority := 0
	for _, r := range playerRegions {
		if r != major {
			minority++
		}
	}
	// minority/n ≤ cap/100  ⇔  minority×100 ≤ cap×n(整数比较,免浮点)
	return minority*100 <= p.CrossRegionRatioCapPct*n
}

// ── 两级撮合的分区 / 溢出选择(纯函数,接入 matchOnce 用)──────────────────────

// RegionResolver 把一张票据解析到其 owner region(实现用 cellroute.Router.Route(captain_id))。
// 返回 0 表示"未知 / 单 Cell"(router 未配或解析失败时),所有票据落同一桶 → 退化为不分区。
type RegionResolver func(t *matchv1.MatchTicketStorageRecord) uint32

// partitionTicketsByRegion 把(已按 MMR 排序的)票据按 owner region 分桶,保持桶内原相对顺序。
//
// 返回 buckets(region→票据切片)与 order(region 列表,按 region 值升序,保证撮合次序确定性)。
// regionOf 为 nil 或对所有票据恒返回同一值时,落入单桶 —— 等价于单 Cell 不分区行为
// (scale-cellular-20m.md §4.4:绝大多数对局同 region,先在本 region 内撮合)。
func partitionTicketsByRegion(tickets []*matchv1.MatchTicketStorageRecord, regionOf RegionResolver) (buckets map[uint32][]*matchv1.MatchTicketStorageRecord, order []uint32) {
	buckets = make(map[uint32][]*matchv1.MatchTicketStorageRecord)
	for _, t := range tickets {
		var region uint32
		if regionOf != nil {
			region = regionOf(t)
		}
		buckets[region] = append(buckets[region], t)
	}
	order = make([]uint32, 0, len(buckets))
	for r := range buckets {
		order = append(order, r)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	return buckets, order
}

// regionPlayerTotals 统计每个 region 桶的总人数(判 localCandidatesEnough 用)。
func regionPlayerTotals(buckets map[uint32][]*matchv1.MatchTicketStorageRecord) map[uint32]int {
	totals := make(map[uint32]int, len(buckets))
	for region, ts := range buckets {
		sum := 0
		for _, t := range ts {
			sum += len(t.Members)
		}
		totals[region] = sum
	}
	return totals
}

// selectOverflowTickets 从各 region 剩余(本 region 内未成局)票据里挑出"可跨 region 溢出"的票据。
//
// 决策(decision-revisit-global-matchmaker.md §2.2,经 RegionMatchPolicy.ShouldOverflow):
//   - 等待时长已过该段位溢出阈值,且本 region 同段位候选不足成局(总人数 < need)→ 放开跨 region。
//   - tierOf 给票据段位档(高分段早溢出);为 nil 时恒按 tier 0(单档,留待段位表接入)。
//
// 返回的票据保持入参(MMR 升序)顺序,供跨 region 贪心装箱复用同一撮合路径。
func selectOverflowTickets(
	leftover []*matchv1.MatchTicketStorageRecord,
	regionOf RegionResolver,
	regionTotals map[uint32]int,
	need int,
	policy RegionMatchPolicy,
	tierOf func(*matchv1.MatchTicketStorageRecord) int,
	nowMs int64,
) []*matchv1.MatchTicketStorageRecord {
	out := make([]*matchv1.MatchTicketStorageRecord, 0, len(leftover))
	for _, t := range leftover {
		var region uint32
		if regionOf != nil {
			region = regionOf(t)
		}
		localEnough := regionTotals[region] >= need
		tier := 0
		if tierOf != nil {
			tier = tierOf(t)
		}
		waitMs := nowMs - t.EnqueuedAtMs
		if policy.ShouldOverflow(waitMs, tier, localEnough) {
			out = append(out, t)
		}
	}
	return out
}
