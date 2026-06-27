# 本机 P0 压测冒烟记录(2026-06-26)

> 关联:`docs/design/stress-discipline.md`、`docs/design/stress-single-cell-client.md`、
> `docs/design/scale-cellular-20m.md` §7。
>
> 本文只记录本机 P0 冒烟执行结果。它不等同于阶段 1 单 Cell ~40 万 CCU 验收,
> 也不能用于声明性能达标。

## 1. 执行范围

- 执行者:Codex(ops 执行 + harness 修补),压测结论仍待 Claude review。
- 环境:本机 dev docker + 16 个 Go 业务服务,`ds_mode=stub`,未恢复 Agones / k8s。
- 压力:80 VU,10s ramp,150s steady,`action_interval_ms=5000`,`envoy_sample_ratio=0`。
- 状态清理:每轮前执行 `run_services.ps1 -Action down`、`dev_tools.ps1 -Command all -Force`、
  `run_services.ps1 -Profile all -NoBuild`;最终 `run_services.ps1 -Action status` 显示 16 服务端口均 up。

## 2. 本轮修补

- `tools/scripts/dev_tools.ps1`:MySQL 清表列表改为当前 DDL 实际表名;缺表跳过并在 status 显示;
  停服提示统一为 `run_services.ps1 -Action down`。
- `tools/scripts/stress_summarize.ps1`:优先匹配实际 prom 指标 `pandora_rpc_duration_seconds`,
  否则回退通用 `(grpc|rpc).*(handling|duration|latency)`。
- `tools/scripts/stress_snap.ps1`:兼容 `-Stages "0,1,2"` 这类经 `pwsh -File` 外层转发的逗号字符串,
  避免被当作单个阶段导致误等。
- `robot/stress/internal/vu/vu.go`:match flow 在 `CreateTeam` 后补 `Team.SetReady(ready=true,hero_id=1)`,
  再调用 `StartMatch`;`pollMatch` 窗口从约 1s 拉长到约 9s,覆盖本地 `match_interval=2s` 与 DS 分配抖动。

## 3. 验证

- `robot/stress`: `gofmt`、`go build ./...`、`go vet ./...` 均通过。
- `run/dev/bin/stressbot.exe`:重新构建成功。
- `tools/scripts/*.ps1`:parser 校验通过;`stress_snap.ps1 -Stages 0,1` 快速复验能落 t0/t1 快照。

## 4. 有效 P0 运行

- RunDir:`robot/logs/stress-p0-local-20260626-223440`
- Summary:`robot/logs/stress-p0-local-20260626-223440/summary.txt`
- stressbot:exit code 0。
- 快照:完整落盘 `t0/t1/t2` 的 login / match / ds / battle 四组 prom 文件。

### 4.1 robot stats

| ts(UTC) | online | loginOK | loginFail | enq | conf | disp | battle | p99ms | err |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 2026-06-26T14:35:41Z | 80 | 80 | 0 | 56 | 0 | 37 | 37 | 83.4 | 44 |
| 2026-06-26T14:36:41Z | 80 | 80 | 0 | 71 | 0 | 55 | 55 | 26.9 | 108 |
| 2026-06-26T14:37:21Z | 2 | 80 | 0 | 74 | 0 | 57 | 57 | 38.6 | 164 |

说明:`match_confirmed=0` 是本机 dev 配置 `auto_confirm_match=true` 的结果,不是客户端确认链路通过证明。
本轮有效覆盖的是 ready → StartMatch → READY 轮询 → stub ReportResult。

### 4.2 prom summary

| 段 | t0 | t1 | t2 |
|---|---|---|---|
| matchmaker | count=203 avg=0.0503s p50=0.008s p99=2.048s | count=1,563 avg=0.0440s p50=0.004s p99=2.048s | count=1,815 avg=0.0451s p50=0.004s p99=2.048s |
| ds_allocator | count=57 avg=0.2164s p50=0.032s p99=2.048s | count=347 avg=0.1915s p50=0.008s p99=+Inf | count=759 avg=0.1060s p50=0.004s p99=+Inf |
| battle_result | count=43 avg=0.0122s p50=0.008s p99=0.064s | count=191 avg=0.0102s p50=0.008s p99=0.128s | count=235 avg=0.0099s p50=0.008s p99=0.064s |
| hub DS replication | N/A | N/A | N/A |

## 5. 判定

P0 harness 冒烟通过:登录、push 长连接、ready 后入队、matchmaker READY、stub battle_result 上报、
三段 prom snapshot 与 summarize 管道均跑通。

但本轮不得声明阶段 1 达标:

- 不是 40 万 CCU,只是本机 80 VU 冒烟。
- 没有 `prev-summary.txt` 和二维对比表。
- robot 仍累计 164 个 RPC error,需要下一轮分类和压降。
- ds_allocator p99 出现 `+Inf`,说明至少部分请求超过当前 histogram 最大有限桶或有长尾,需 Claude 复核。
- 未恢复 Agones / k8s,未验证真 DS / Hub DS Replication。

## 6. 下一步

1. Claude review 本轮 summary 与 robot errors,决定 P1 是否先做单机标定或先给 errors 分类。
2. 正式阶段 1 前准备多台压测机(8~16 台量级)与 `prev-summary.txt` baseline。
3. 仍按 `stress-discipline.md` 清状态、抓至少三段快照、跑 summarize;无对比表不进入多 Cell。
