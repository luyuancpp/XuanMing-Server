# Pandora 接班手册

> 给 Copilot Claude / Claude Code / Cursor Claude 等接班 AI 使用。
> 目标是少读历史、快速进入当前任务。历史演化请去 `PROGRESS.md` 和 `docs/design/` 查。

## 1. 先读这些

按顺序读:

1. `CLAUDE.md`
2. `AGENTS.md`
3. `docs/design/pandora-arch.md`
4. `docs/design/gateway-decision.md`
5. `docs/design/infra.md`
6. `PROGRESS.md` 中最新的 W2 段落

读完后,**自己根据代码和文档确认当前进度**。不要依赖其它 AI 口头说"某服务已完成"。

## 2. 当前锁定架构

- 后端:Go + Kratos
- Edge Gateway:Envoy v1.38.0
- 客户端业务通道:gRPC-Web over HTTP/2 TLS
- 推送:集中 `push` 服务 + gRPC server stream
- 客户端连接:UE NetDriver(游戏同步) + FHttpModule(gRPC-Web)
- Go 服务目录:`services/<域>/<服务>/`
- proto 目录:`proto/pandora/<domain>/v1/*.proto`

不要在本轮重新讨论网关/推送/客户端连接架构。只按最终架构实现当前任务。

## 3. 跨 AI 硬性分工

**Claude 系模型只负责**:

- 深度分析
- plan
- 改代码 / 配置 / 文档
- 跑项目内验证命令

**Claude 系模型不负责**:

- 安装工具
- 改系统环境
- 生成本机证书
- 拉 Docker 镜像
- 启停本机环境
- `git status` / `diff --stat` / commit message 建议 / commit / push / tag

如果发现需要环境配置,只输出:

- 环境配置方案
- 命令
- 风险
- 验收标准

交给 ChatGPT / Codex 执行。环境配好后,Claude 再用项目命令复查确认。

## 4. 当前下一步

继续 **W2 ④ Envoy v1.38.0 本地 docker 配置**。

先自查:

- `deploy/docker-compose.dev.yml`
- `docs/design/gateway-decision.md`
- `docs/design/infra.md`
- `proto/gen/go/pandora/login/v1/`
- `proto/gen/go/pandora/push/v1/`
- `services/account/login/`
- `services/runtime/push/`

然后给 plan,列出:

- 要改哪些文件
- Envoy listener / filter / route / cluster 配置要点
- TLS 文件路径
- 项目内验证命令
- 风险点

## 5. Envoy 目标

需要实现:

1. 新增 `deploy/envoy/envoy.yaml`
2. 修改 `deploy/docker-compose.dev.yml`,新增 `envoy` service
3. Envoy 监听:
   - HTTPS `8443`
   - admin `9901`
4. 配置 upstream cluster:
   - `login` 指向本机 login gRPC 端口
   - `push` 指向本机 push gRPC 端口
5. 配置路由:
   - LoginService 路由到 `login`
   - PushService 路由到 `push`
6. `push` server stream 路由无超时
7. 启用:
   - `grpc_web`
   - `cors`
   - `router`
8. TLS 容器内路径:
   - `/etc/envoy/cert.pem`
   - `/etc/envoy/key.pem`

端口、service path 和 cluster 地址必须由 Claude 自己从代码 / proto / 文档核对。

## 6. 验证

Claude 可跑项目内验证,例如:

```powershell
docker compose -f deploy/docker-compose.dev.yml config --quiet
```

如证书文件和环境已由 ChatGPT / Codex 准备好,Claude 可继续用项目命令验证 Envoy 配置是否能工作。

完成后,不要做 git 收尾。把验证结果交给 ChatGPT / Codex。

## 7. 当前注意事项

- 不要碰与 Envoy 无关的文件
- 不要改业务代码
- 不要处理现有未提交的 `services/runtime/push/internal/server/http.go`,除非用户明确要求
- 不要提交
