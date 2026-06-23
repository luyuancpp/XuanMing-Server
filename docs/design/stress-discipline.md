# Pandora 压测纪律

> Pandora 压测执行规范,适配 Pandora 项目路径与工具脚本。

## 1. 总原则

1. **没有对比表不许声明"性能提升"**
2. **每轮压测前后必须做完整环境清理**,不许"上一次跑完接着跑"
3. **prom 数据只读 summarize 脚本输出,不许手 grep raw dump**
4. **结果文档复用脚本输出表格,不贴 raw count/sum 数字**
5. **压期间不上传任何日志**
6. **每次登录压测把所有 redis/mysql/etcd 数据全部删除再开新一轮**

## 2. 压测目录结构

```
F:/work/Pandora/
├── robot/
│   ├── stress/                      # 机器人压测客户端
│   └── logs/
│       └── stress-<name>-<ts>/      # 单轮压测目录
│           ├── prom-snapshots/
│           │   ├── t2m_login.txt    # 2 分钟时刻 login 端口快照
│           │   ├── t2m_match.txt    # 2 分钟 matchmaker
│           │   ├── t2m_ds.txt       # 2 分钟 ds_allocator
│           │   ├── t5m_*.txt
│           │   └── ...
│           ├── robot-stats.jsonl    # 机器人侧每分钟统计
│           ├── prev-summary.txt     # ⭐ 上一轮 baseline
│           ├── summary.txt          # ⭐ 本轮 summarize 输出
│           └── round-N-vs-N-1.md    # 二维对比表
└── tools/
    └── scripts/
        ├── stress_summarize.ps1     # 单轮汇总(读 prom 快照,出二维表)
        ├── stress_snap.ps1          # 后台批量拉 prom snapshot
        ├── go_svc_stop.ps1          # 停所有 go 服务
        └── dev_tools.ps1            # 通用开发工具(含 kafka offset reset 等)
```

## 3. 端口分工

| 端口 | 服务组 | 主要看的指标 |
|---|---|---|
| `:51001` | login metrics | 登录 QPS、票据签发耗时 |
| `:51011` | matchmaker metrics | 队列长度、匹配等待、撮合耗时 |
| `:51020` | ds_allocator metrics | DS 拉起耗时、pod 数、Agones 调度 RTT |
| `:51022` | battle_result metrics | kafka lag、幂等命中率、写库耗时 |

`stress_snap.ps1` 默认并行拉这 4 端口,文件命名 `t<N>m_<svc>.txt`,`stress_summarize.ps1` 按后缀分流。

## 4. 压测前后强制流程

### 4.1 跑测前 ⚠️

1. **保存上一轮 summary**:把上次 `summary.txt` 复制为 `prev-summary.txt`
   - `prev-summary.txt` 不存在 → **不许开下一轮**
2. **清空污染数据**(每条都跑):
   ```powershell
   # robot 旧目录(留最近 1 个,其它删)
   Get-ChildItem F:/work/Pandora/robot/logs/stress-* | Sort LastWriteTime -Desc | Select -Skip 1 | Remove-Item -Recurse -Force

   # 各 go service stderr/stdout
   pwsh F:/work/Pandora/tools/scripts/go_svc_stop.ps1
   Remove-Item F:/work/Pandora/tools/scripts/.run/* -Recurse -Force

   # redis 清残留 lock / session
   redis-cli -p 6380 FLUSHALL

   # kafka offset reset
   pwsh F:/work/Pandora/tools/scripts/dev_tools.ps1 -Command kafka-offset-reset

   # mysql 完整删表再重建
   pwsh F:/work/Pandora/tools/scripts/dev_tools.ps1 -Command db-reset

   # etcd 清服务注册
   pwsh F:/work/Pandora/tools/scripts/dev_tools.ps1 -Command etcd-clear

   # prom snapshot 目录新建
   New-Item F:/work/Pandora/robot/logs/stress-<name>-<ts>/prom-snapshots/ -ItemType Directory
   ```
3. **DS pod 清理**:
   ```bash
   kubectl delete gameserver --all -n pandora
   kubectl delete fleet --all -n pandora && kubectl apply -f deploy/k8s/fleets.yaml
   ```

### 4.2 压测中

- **至少 3 次 snapshot**:ramp 完成 / 稳态中段 / 稳态末
- snapshot 命令:
  ```powershell
  pwsh tools/scripts/stress_snap.ps1 `
    -RunDir robot/logs/stress-<name>-<ts> `
    -StartTime '<yyyy-MM-dd HH:mm:ss>' `
    -Stages 2,5,10,15,18
  ```
- **不许手拉单端口**(`curl :51001/metrics > t2m.txt` 这种临时抓取不再用)

### 4.3 跑测后

1. 跑 `stress_summarize.ps1`:
   ```powershell
   pwsh tools/scripts/stress_summarize.ps1 -RunDir robot/logs/stress-<name>-<ts>
   ```
2. 与 `prev-summary.txt` 二维对比,写进 `round-N-vs-N-1.md`
3. 贴决策行 + 更新 `docs/design/pandora-arch.md` §11
4. 更新 `PROGRESS.md`
5. **压期间不上传日志,只上传 summary 表格**

### 4.4 完成清单

```
[ ] prev-summary.txt 已存
[ ] redis/mysql/etcd 已清空
[ ] kafka offset 已 reset
[ ] DS pod 已清干净
[ ] prom-snapshots/ 目录已建
[ ] 至少 3 次 snapshot 已抓
[ ] summarize.ps1 输出五段表
[ ] 对比表已写
[ ] 决策行已贴
[ ] PROGRESS.md 已更新
```

**漏一项重来**。

## 5. summarize 脚本输出五段表

适配 Pandora 关键路径:

| 段 | 内容 | 数据源 |
|---|---|---|
| 1. robot 每分钟 stats | 在线、登录、匹配、进 DS、断开 | robot-stats.jsonl |
| 2. matchmaker 关键阶段 | enqueue / matched / confirmed / dispatched 各阶段平均耗时 + p99 | `:51011` 指标 |
| 3. ds_allocator 子阶段 | k8s api / agones allocate / pod ready / first-conn 各阶段耗时 | `:51020` 指标 |
| 4. battle_result 子阶段 | kafka lag / decode / db write / ack 各阶段耗时 | `:51022` 指标 |
| 5. 大厅 DS Replication | hub 在线人数 / 包大小 / NetCullDistance 实际触发 / Iris stat | DS prom 端口 |

## 6. 反模式禁令

- ❌ 不许跨轮共用 `robot/logs/` 目录
- ❌ 不许在没清 redis 的情况下接着跑(残留 lock 会让 trade 测试错乱)
- ❌ 不许在跑测中途调整 go 服务参数(中段调参 = 数据废了)
- ❌ 不许把 raw count/sum 数字塞进文档(只贴 summarize 输出表)
- ❌ 不许在没有 `prev-summary.txt` 的情况下声明"性能提升"

## 7. Round N 命名规则

```
docs/design/stress-<round>-<topic>-<date>.md
```

例:
```
stress-1-login-burst-20260620.md
stress-2-match-throughput-20260625.md
stress-3-hub500ppl-20260701.md      ⭐ 关键里程碑:500 人 hub PvP
stress-4-battle-50rooms-20260710.md
```

每篇必须含:
1. 测试目标(一句话)
2. 测试参数(robot 数、ramp 时长、稳态时长)
3. 环境(go 版本 / UE 版本 / k8s 版本 / DS pod replica)
4. summarize 输出表
5. vs prev 二维对比
6. 瓶颈分析
7. 决策行(写回 pandora-arch.md §11)

## 8. Pandora 特有关注点

Pandora 是分布式后端 + UE DS,压测时额外关注:

| 维度 | 关注点 |
|---|---|
| 受测组件 | 14 个 go 服务 + UE Hub DS + UE Battle DS |
| 压测客户端 | go robot + UE headless client(后期) |
| 关键瓶颈 | matchmaker MMR / Replication Graph / Iris |
| 必看指标 | match.found 链路 / hub_player_count / ds_pod_ready_p99 |
| 清理重点 | redis lock / kafka offset / k8s GameServer / Agones Fleet |

## 9. UE 机器人压测分层

UE 侧压测必须按目标分层,不要把不同压力来源混成一个结论:

| 层级 | 工具形态 | 主要验证 | 不适合验证 |
|---|---|---|---|
| 服务端 Bot | DS 内 `AIController + Pawn` / `StressBotManager` 批量生成 | 单 DS tick、AI、GAS、技能、Actor 数、内存、CPU | 真实客户端连接数、NetDriver 握手、客户端带宽 |
| 无渲染 UE Client Bot | 批量启动打包客户端,带 `-nullrhi -nosound -unattended` | 真实登录、匹配、进房、NetDriver 连接、Replication、RPC、服务端带宽 | 大规模后端 API QPS 极限 |
| 轻量协议 Robot | Go / 脚本直接压 gRPC-Web / gRPC / HTTP 入口 | login、matchmaker、push、商城、队伍等后端链路 QPS | UE Replication / GAS / 客户端渲染相关问题 |

### 9.1 服务端 Bot

用于先把玩法服务器自身压力打满。推荐在 Hub DS / Battle DS 内做 `StressBotManager`,通过 GM 指令或启动参数生成 N 个 Bot,行为包含移动、寻路、攻击、释放技能、拾取、死亡复活、交互等真实玩法路径。

示例控制命令:

```text
SpawnStressBots 500
ClearStressBots
```

结论只允许表述为"DS 内部玩法负载 / AI / GAS / Actor 承载",不能等同于"真实 500 玩家在线"。

### 9.2 无渲染 UE Client Bot

用于验证真实客户端链路。机器人客户端必须走完整登录、匹配、DS ticket、连接 Hub/Battle DS 的流程,并尽量复用真实客户端代码路径。

Windows 批量启动示例:

```powershell
1..50 | ForEach-Object {
  Start-Process .\Pandora.exe -ArgumentList "127.0.0.1 -game -nullrhi -nosound -unattended -nopause -log -botId=$_"
}
```

常用参数:

```text
-nullrhi
-nosound
-unattended
-nopause
-log
```

单机能跑的 UE Client Bot 数量通常有限,不要用一台机器的上限反推服务器真实上限。需要更高并发时,按压测机器横向分摊,并在结果文档记录压测机 CPU / 内存 / 网卡 / Bot 数。

### 9.3 后端协议压测

登录、匹配、组队、商城、推送等后端链路优先用轻量 robot 压测,不要用 UE 客户端硬怼接口 QPS。UE Client Bot 只用于端到端链路与 NetDriver / Replication 验证。

### 9.4 指标与结论口径

每轮 UE 机器人压测必须同时记录:

1. Bot 类型:服务端 Bot / UE Client Bot / 轻量协议 Robot。
2. Bot 数量、ramp 时长、稳态时长、压测机规格。
3. DS 指标:`stat unit`、`stat game`、`stat net`、`stat memory`、CSV profile、Unreal Insights trace。
4. 后端指标:login / matchmaker / ds_allocator / battle_result 的 summarize 输出。
5. 结论边界:本轮结论能说明什么,不能说明什么。

推荐路线:

1. 先用服务端 Bot 测单 DS 玩法承载。
2. 再用无渲染 UE Client Bot 测真实连接、Replication、进出 DS。
3. 最后用轻量协议 Robot 单独压后端接口极限。
