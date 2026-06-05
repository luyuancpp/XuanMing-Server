# Pandora push 服务

> Pandora 第二个 Kratos 业务服(W2 ⑤,2026-06-05),server stream 长连推送。

## 职责

详见 [`docs/design/go-services.md`](../../../docs/design/go-services.md) 及 [`docs/design/gateway-decision.md`](../../../docs/design/gateway-decision.md) §5/§6。

- 客户端登录后立刻 `Subscribe(server stream)` 维持长连接
- 服务端持有所有在线客户端 stream,按 `player_id` 路由 kafka 事件
- 转发推送 topics(`pandora.team.update` / `pandora.match.progress` / `pandora.chat.*` / `pandora.player.update` / `pandora.friend.event` / `pandora.system.notify`)
- 离线消息缓存 redis ZSET(5min,断线重连补推)

## 协议铁律(对齐 [`protocol-ordering-rules.md`](../../../docs/design/protocol-ordering-rules.md))

- **原则 2**:发起方不收自己触发的 push(业务服 produce kafka 时排除 caller_player_id)
- **原则 3**:已受理型 RPC(`match.StartMatch` / `ConfirmMatch`)是例外,push 给发起方也发

## 架构边界

- 本服务**不是 WebSocket 服务**(2026-06-03 自研 WebSocket 已被否决)
- 本服务**不是 HTTP 网关**(那是 Envoy 的职责)
- 客户端走 gRPC-Web over HTTP/2 TLS 连 Envoy,Envoy 转标准 gRPC 给本服务
- 业务服推送事件全部走 kafka,本服务消费转 stream(不接业务服直接 gRPC 调用)

## 端口

| 协议 | 端口 | 用途 |
|---|---|---|
| gRPC | 50014 | server stream(客户端 → Envoy gRPC-Web → 本服) |
| HTTP | 51014 | 仅 `/metrics`(`push.proto` 无 `google.api.http` 注解,无 RESTful RPC) |

详见 [`docs/design/infra.md`](../../../docs/design/infra.md) §6.2。

## 目录结构(Kratos 标准分层,对齐 login)

```
cmd/push/main.go              启动入口
etc/push-dev.yaml             开发期配置
internal/
  conf/                       配置结构(嵌入 pkg/config.Base)
  service/                    RPC 入口(实现 pushv1.PushServiceServer)
  biz/                        usecase
    connection.go             player_id → stream 内存索引(顶号语义)
    push.go                   PushUsecase + RunMockStream
  server/                     grpc / http server 注册
                              (data/ 留待 W3 redis ZSET 接入时再加)
```

## W2 mock 行为

- `Subscribe(SubscribeRequest{session_token, last_seen_ms})`:
  - 校验 session_token:**W2 跳过**(W3 走 Envoy jwt_authn + 冗余校验)
  - `last_seen_ms` 补推离线消息:**W2 不做**(W3 redis ZSET)
  - 注册 stream 到 ConnectionManager(顶号语义:同 player_id 旧 stream 自动断)
  - 启 ticker(默认 5s,见 `conf.PushConf.MockTickInterval`)
  - 周期 Send `PushFrame{topic="pandora.system.notify", payload="hello", ts_ms=now, trace_id=ctx}`
  - ctx.Done(client 断 / server stop / 顶号 cancel)→ 反注册退出

## 本地启动

```powershell
# 1. 基础设施(redis 可选,W2 不连也能跑)
pwsh tools/scripts/dev_up.ps1

# 2. 启 push
cd F:\work\Pandora
go run ./services/runtime/push/cmd/push -conf services/runtime/push/etc/push-dev.yaml
```

## 验证(可选,需装 grpcurl)

```powershell
# 直连 gRPC server stream(W2 没经 Envoy,用 -plaintext)
# 期望:首帧立即返回,之后每 5s 一帧 PushFrame
grpcurl -plaintext -d '{\"session_token\":\"mock\",\"last_seen_ms\":0}' `
  127.0.0.1:50014 pandora.push.v1.PushService/Subscribe

# Prometheus 抓 metrics
curl http://127.0.0.1:51014/metrics | Select-String pandora
```

## 下一步(W3 真实化路线)

- [ ] 接 sarama consumer:订阅 6 个 push topic,按 key=player_id 找 `Conns().SendTo`
- [ ] 接 redis:离线消息 ZSET(`pandora:push:offline:<player_id>`)+ 重连补推
- [ ] JWT 校验:从 metadata 取 `x-jwt-payload-sub`,冗余校验防 token 中途过期
- [ ] 系统公告类(`pandora.system.notify`)走 `Conns().Broadcast`
- [ ] /metrics 暴露 `pandora_push_online_streams` / `pandora_push_send_failed_total` 等指标
- [ ] kafka topic key 校验:不变量 §9(同一玩家事件有序 → key=player_id)
