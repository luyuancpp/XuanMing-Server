# 决策细化:跨 Region 全局撮合(global matchmaker)

> 类型:决策细化(承接 `scale-cellular-20m.md` §8 第 4 项「允许跨 region 匹配」拍板)。
> 触发:2026-06-26 人拍板 DAU 2000 万三层化方案,放开跨 region 匹配,需定死撮合边界。
> 决策级别:服务级(matchmaker 域);索引见 `pandora-arch.md` §11 全服扩容行。
> 状态:**已拍板方向,细则待评审**;本文档只做设计,撮合代码另起任务。

---

## 1. 旧问题(为什么要这份文档)

`scale-cellular-20m.md` §4.4 拍板「允许跨 region 匹配」,但只给了方向(两级撮合),没定:

- region 内撮合与跨 region 撮合的**边界**(什么时候才允许把玩家拉去别的 region)?
- 溢出**阈值**(等多久 / 池子多空才溢出)?
- 跨 region **亲和度权重**(RTT 惩罚多大)?
- 对局拉在哪个 Cell、结算怎么回各自 owner cell?

不定死这些,撮合代码无法动工,且容易把「跨 region 兜底」写成「跨 region 常态」,破坏 Region 解耦初衷。

---

## 2. 新方案:两级撮合(region 内优先 + 跨 region 溢出兜底)

### 2.1 分层

```
玩家进队列(matchmaker,owner region 内)
        │
        ▼
① region 内 MMR 池撮合(默认,绝大多数对局)
        │  等待超过 T_overflow 且本 region 同段位人数不足
        ▼
② 跨 region 溢出池(全局,按段位桶 key=mmr_bucket)
        │  跨 region 候选带 RTT 亲和度惩罚,优先同 region
        ▼
凑齐 10 人 → 选 battle Cell(参战玩家多数所在 region 的 Cell)→ 拉 battle DS
        │
        ▼
对局结束 → battle_result 算 MMR → 结算分别回各玩家 owner cell(幂等,match_id)
```

### 2.2 关键规则(定死)

| 项 | 取值 | 理由 |
|---|---|---|
| 默认撮合域 | **owner region 内** | Region 近似独立服,绝大多数对局零跨 region |
| 溢出触发阈值 `T_overflow` | **段位越高越短**(如钻石+ 30s,普通段 90s) | 高分段人稀,早点跨 region;低分段人多,尽量本地 |
| 溢出触发条件 | `等待 > T_overflow` **且** 本 region 同段位候选不足成局 | 双条件,避免人够还跨区 |
| 跨 region 亲和度惩罚 | 候选评分加 `-w_rtt × est_rtt`(同 region=0 惩罚) | 同 region 永远优先,跨 region 是兜底 |
| 跨 region 对局上限 | 一局内跨 region 玩家比例软上限(如 ≤40%) | 防一局横跨三区体验崩坏 |
| battle Cell 选择 | 参战玩家**多数所在 region 的 Cell** | 最小化跨 region 数据回流 |
| 结算回流 | **MMR 在 battle_result 算,分别写各 owner cell** | 不变量 §6/§2 不变:DS 不可信、match_id 幂等 |

### 2.3 溢出池实现要点

- 溢出池 key = **段位桶 `mmr_bucket`**(对照 `scale-cellular-20m.md` §4.4「全局 MMR 溢出池,key=段位桶」),不是单一全局大池,避免热点。
- 溢出池**只承载等待超时的少数玩家**,稳态下应近乎为空;若长期非空 = region 容量/分区不均,需告警。
- 溢出层是**独立于 region 的全局服务**(对照 `scale-cellular-20m.md` §4.1 全局协调层),按 `mmr_bucket` 水平分片。

---

## 3. 不变量影响(对照 `CLAUDE.md` §9)

| 不变量 | 是否仍成立 | 说明 |
|---|---|---|
| 1 玩家只在一个 DS | ✅ | 跨 region 玩家进同一 battle DS,player_locator 记 BATTLE,仍单点 |
| 2 战斗结果幂等 | ✅ | match_id 全局唯一,结算回各 owner cell 幂等落库 |
| 3 DS 票据短时效 | ✅ | battle JWT 带 region_id+cell_id+match_id claim,exp 不变 |
| 6 MMR 在 battle_result | ✅ | 跨 region 不改这条:DS 不可信,MMR 仍服务端算 |
| 9 kafka key=实体 ID | ✅ | 结算事件 key=player_id 回各 region;溢出池 key=mmr_bucket |
| §9 新增① owner 落同一 region+cell | ✅ | 跨 region 玩家**只是临时进别 region 的一局**,owner 数据仍在自己 region;不发生跨 region owner 写 |

> 核心安全点:**跨 region 只共享「一局对战」这一短时态,不共享 owner 存储**。对局是 match_id 维度的临时聚合,结束即散,各回各家。

---

## 4. 风险与迁移成本

- **风险 1:跨 region RTT 拖累体验** → 用亲和度惩罚 + 跨 region 比例软上限压制;监控每局跨 region 玩家分布。
- **风险 2:溢出常态化(等于没分区)** → 溢出池稳态非空即告警;`T_overflow` 调参;必要时增 region 内 Cell。
- **风险 3:结算跨 region 回流失败** → 复用现有 battle_result outbox(`deploy/mysql-init/05-battle-outbox.sql` 模式),跨 region 走 §4.4 全局桥异步 + 幂等重试。
- **迁移成本**:阶段 2(单 region 多 Cell)只需 region 内撮合;**跨 region 溢出层是阶段 3 才接**,不阻塞阶段 1/2。

---

## 5. 验收标准

1. region 内撮合:同 region 同段位充足时,**0 跨 region**,撮合延迟不劣于现状。
2. 溢出兜底:人为制造单 region 高分段稀缺,玩家在 `T_overflow` 后能跨 region 成局,且一局跨 region 比例不超软上限。
3. 结算正确:跨 region 对局结束,各玩家 MMR/段位写回**各自 owner cell**,match_id 幂等(重复结算不双写)。
4. 解耦度:稳态压测下跨 region 对局占比 < 5%(对照 `scale-cellular-20m.md`「跨 region 流量压到 <1%」的 owner 写口径,撮合层放宽到 <5%)。
5. 有满载压测对比表(`stress-discipline.md`),否则不许声明「跨 region 撮合可行」。

---

## 6. 落地边界(谁做什么)

- **matchmaker 域代码**(两级撮合、溢出池接入、亲和度评分):Claude 实现 + 单测(`AGENTS.md` §11.1)。
- **溢出层全局服务部署 / 跨 region Kafka 桥 / 多 k8s**:基础设施 ops,Codex/人接。
- **proto 改动**(battle ticket 加 region_id/cell_id claim、撮合 RPC 带 region 维度):Claude 写 proto,Codex 跑 `proto_gen.ps1` 生成(`CLAUDE.md` §5.1)。

> 本文档仅设计。撮合代码 / proto 改动 / 部署等另起任务,阶段 3 才动跨 region 溢出层。
