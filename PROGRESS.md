# Pandora 进度记录

> 本文档**只追加,永不删旧条目**。AI 新会话第一件事就是读这里。

## W1 (2026-06-03 起)

### 立项决策(Round 0)

| 项 | 决策 |
|---|---|
| 项目名 | **Pandora**(项目)/ pandora(资源命名空间) |
| 后端仓库 | https://github.com/luyuancpp/Pandora.git(public) |
| UE 仓库 | 待定(暂用 Pandora-Client 占位) |
| UE 版本 | 5.7(Iris + GAS,默认 Iris,退路 Replication Graph) |
| 类型 | MOBA + 持续在线大厅 |
| 大厅 | 500 人/实例,单城镇约 1km²,**全图自由 PvP** |
| 战斗 | 5v5,~25 分钟 |
| DS 编排 | Agones on k8s(本地先 minikube,生产待定阿里云 ACK / 自建) |
| 协议 | gRPC + Kafka |
| 基础设施 | MySQL 8 / Redis 7 / Kafka 3 / etcd 3(全新搭一套,端口跟 mmorpg 错开) |
| License | MIT |
| Go 版本 | 1.23 |
| 中文回复 | 是(继承 mmorpg) |
| mmorpg 项目状态 | 封存,允许 D2 一次性拷代码作起点,之后两边独立 |
| **D2.1 框架选型** | **继续用 `go-zero`**(2026-06-03 决策)— 复用 mmorpg 90% 公共代码,D2 工作量 4~5 天 |

### 端口规划(避免与 mmorpg 冲突)

| 基础设施 | mmorpg | Pandora |
|---|---|---|
| MySQL | 3306 | **3307** |
| Redis | 6379 | **6380** |
| Kafka | 9092 | **9093** |
| etcd client | 2379 | **2380** |
| Prometheus | 9090 | **9091** |
| Grafana | 3000 | **3001** |

详见 `docs/design/infra.md` §6。

### W1 任务进度

#### 文档草稿(已落盘)
- [x] `CLAUDE.md`(项目宪法)
- [x] `PROGRESS.md`(本文)
- [x] `AGENTS.md`(AI 协作守则)
- [x] `docs/design/pandora-arch.md`(总架构)
- [x] `docs/design/proto-design.md`(协议设计)
- [x] `docs/design/pkg-copy-from-mmorpg.md`(公共框架来源)
- [x] `docs/design/infra.md`(基础设施规范)
- [x] `docs/design/go-services.md`(13 个 go 服务清单)
- [x] `docs/design/stress-discipline.md`(压测纪律)
- [x] `docs/design/ds-arch.md`(UE DS 设计)
- [x] `docs/design/pvp-rules.md`(PvP 规则待定项)

#### W1 计划

| 阶段 | 内容 | 状态 |
|---|---|---|
| **D1** | 仓库骨架 + 11 份文档落盘 | ✅ 完成(2026-06-03,commit b4f6351) |
| **D2** | 拷 mmorpg pkg + docker-compose + dev_up.ps1 | 🟢 进行中(2026-06-03) |
| D3 | 写 .proto + buf 工具链 | ⏸️ |
| D4 | UE 仓库初始化(用户主导) | ⏸️ |
| D5-D6 | UE DS 骨架代码(HubGameMode / BattleGameMode + Agones SDK) | ⏸️ |
| D7 | k8s + Agones + 端到端 hello world | ⏸️ |

### D2 完成清单(2026-06-03)

#### pkg/ 公共框架(12 个模块,~1900 行)
- [x] `pkg/snowflake/` 直接拷自 mmorpg(82 行 + 109 行 test,7 个 test case 全绿)
- [x] `pkg/cache/` 直接拷自 mmorpg(89 行,泛型 cache-aside + singleflight)
- [x] `pkg/redislock/` 直接拷自 mmorpg(131 行,prefix 改 `pandora:lock:`)
- [x] `pkg/grpcstats/` 直接拷自 mmorpg(347 行,gRPC 流量采集 + topN 报告)
- [x] `pkg/log/` 新写,薄包装 logx + ctx trace_id 透传
- [x] `pkg/errcode/` 新写,错误码全段位定义(0-10999)+ Code/Error/IsRetryable
- [x] `pkg/metrics/` 新写,prometheus Register 包装 + StandardBuckets
- [x] `pkg/config/` 改写,以 mmorpg login config 为基础,剥业务字段;BuildTopic / BuildDLQTopic
- [x] `pkg/grpcserver/` 新写,zrpc 包装 + 4 个默认拦截器(recover/trace/metrics/grpcstats)
- [x] `pkg/grpcclient/` 新写,trace_id 出站透传 + 客户端 metrics
- [x] `pkg/kafkax/consistent.go` 直接拷自 mmorpg consistent 包(117 行,FNV-1a + 虚拟节点)
- [x] `pkg/kafkax/producer.go` 改写,SyncProducer + key-ordered + 幂等
- [x] `pkg/kafkax/consumer.go` 改写,sarama ConsumerGroup,Handler 接口给业务实现
- [x] `pkg/svc/base.go` 新写,BaseContext 模板(Redis/Snowflake/Locker)

#### 验证
- [x] `go build ./pkg/...` 全绿(无输出 = 成功)
- [x] `go vet ./pkg/...` 无警告
- [x] `go test ./pkg/snowflake/...` 7 个 case 通过(0.793s)
- [x] `pkg/go.mod` 由 tidy 自动调整到 `go 1.24.0` + toolchain 1.24.5(依赖 go-zero 1.9.x 要求)
- [x] `go.work` 同步到 `go 1.24.0`

#### 基础设施(deploy/)
- [x] `deploy/docker-compose.dev.yml` 7 服务(MySQL 3307 / Redis 6380 / Zookeeper 2182 / Kafka 9093 / etcd 2380 / Prom 9091 / Grafana 3001)
- [x] `deploy/env/dev.env` 开发期凭证(MYSQL_USER=pandora / GRAFANA_USER=admin)
- [x] `deploy/mysql-init/01-create-databases.sql` 创建 6 个数据库(pandora_account / player / social / battle / trade / ops)
- [x] `deploy/prometheus/prometheus.yml` 抓 13 个 go 服务的 51001~51022 metrics 端口
- [x] `docker compose config --quiet` 验证通过

#### 工具脚本(tools/scripts/)
- [x] `dev_up.ps1`(含 -Pull 选项 + healthy 等待 + 连接信息打印)
- [x] `dev_down.ps1`(含 -Volumes 危险选项,需 yes 确认)
- [x] `dev_status.ps1`(docker compose ps + 端口监听检测)

#### 后续提醒
⚠️ Go 版本最终落到 **1.24**(原计划 1.23)— go-zero 1.9.x 等依赖要求 1.24+,被自动升级。1.24 兼容 1.23 代码,不影响计划。CLAUDE.md / docs/ 中的 "Go 1.23" 字样保留(标记历史立项决策),实际编译用 1.24。

⚠️ kafkax 是 **W1-D2 简化版**:无 retry queue / 无 DLQ / 无 plainProducer。W2 写 battle_result 时再补全。

⚠️ Phase 2 docker compose 没有 `up -d` 实跑(留给用户;镜像 pull 需要他网络)。`compose config --quiet` ��验证 yaml 语法 + 端口绑定正确。

### 待用户决策

#### 阻塞 D2 的(必须定)
- [x] **D2.1 框架选型**:**继续用 go-zero**(2026-06-03 决策)
- [ ] **UE 仓库名**(暂用 Pandora-Client 占位,D4 阻塞)
- [ ] **k8s 选型**:阿里云 ACK / 自建 / 先 minikube(D7 阻塞)

#### 非阻塞但要尽快定
- [ ] PvP 死亡惩罚级别(A 轻 / B 掉金币 / C 掉装备 / D 混合)
- [ ] PvP 新人保护方案
- [ ] 击杀奖励公式
- [ ] 大厅安全区方案
- [ ] MOBA 段位划分 / 赛季机制
- [ ] MMR 算法(默认 Glicko-2)
- [ ] AFK 阈值(默认 3 分钟)
- [ ] Ban / Pick 阶段

详见 `docs/design/pvp-rules.md`。建议按 §6 默认值先实现,后期策划再调。

### 后续提醒

⚠️ **W2 写代码时**:13 个 go 服务目录下的 `.gitkeep` 在 `cmd/main.go` 出现后**手动删除**(否则空目录占位污染)。

⚠️ **W2 D2.1 决策**:框架选型一旦定下来,所有 13 个服务的 `internal/svc/servicecontext.go` 模板就锁死,后期换框架成本极高。**慎重**。

### 下一会话 AI 必读清单

1. 本文(掌握当前进度)
2. `CLAUDE.md`(项目规范)
3. `docs/design/pandora-arch.md`(架构总图)
4. `git log -20 --oneline`(最近改动)
5. 当前打开的 PR(如果有)
