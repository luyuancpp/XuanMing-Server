#!/usr/bin/env bash
# Pandora Linux DS 容器入口。
# - 由 Agones 调度，sidecar 通过 env 注入 AGONES_SDK_HTTP_PORT / AGONES_SDK_GRPC_PORT。
# - DS 进程内 UPandoraAgonesSubsystem 读这些 env 后走本地 HTTP 调 Ready/Health/Shutdown。
# - 关卡 / 端口可由 K8s env 覆盖：PANDORA_DS_MAP / PANDORA_DS_PORT。
set -euo pipefail

MAP="${PANDORA_DS_MAP:-/Game/Entry/Entry}"
PORT="${PANDORA_DS_PORT:-7777}"
EXTRA_ARGS="${PANDORA_DS_EXTRA_ARGS:-}"

SERVER_SH="/home/pandora/server/PandoraServer.sh"
if [[ ! -x "${SERVER_SH}" ]]; then
  # 不同 UE 版本归档脚本名可能不同，做个兜底查找。
  SERVER_SH="$(find /home/pandora/server -maxdepth 2 -name "*Server.sh" | head -n1 || true)"
fi

if [[ -z "${SERVER_SH}" || ! -e "${SERVER_SH}" ]]; then
  echo "[entrypoint] 找不到服务器启动脚本(PandoraServer.sh)，请检查 stage/LinuxServer 打包产物。" >&2
  exit 1
fi

echo "[entrypoint] 启动 Pandora DS: ${SERVER_SH} ${MAP} -port=${PORT} -log ${EXTRA_ARGS}"
echo "[entrypoint] AGONES_SDK_HTTP_PORT=${AGONES_SDK_HTTP_PORT:-<unset>} AGONES_SDK_GRPC_PORT=${AGONES_SDK_GRPC_PORT:-<unset>}"

# exec 让 DS 成为 PID 1，正确接收 SIGTERM（Agones 回收 Pod 时优雅退出）。
exec "${SERVER_SH}" "${MAP}" -port="${PORT}" -log ${EXTRA_ARGS}
