#!/usr/bin/env bash
# Pandora Linux DS 容器入口。
# - 由 Agones 调度，sidecar 通过 env 注入 AGONES_SDK_HTTP_PORT / AGONES_SDK_GRPC_PORT。
# - DS 进程内 UPandoraAgonesSubsystem 读这些 env 后走本地 HTTP 调 Ready/Health/Shutdown。
# - 关卡 / 端口可由 K8s env 覆盖：PANDORA_DS_MAP / PANDORA_DS_PORT。
# - GameMode 可由 PANDORA_DS_GAMEMODE 指定（UE 标准 ?game= URL option，优先级高于地图 WorldSettings，
#   无需改地图资产）。战斗 Fleet 设成 /Script/Pandora.PandoraBattleGameMode，让其 BeginPlay 起业务心跳。
set -euo pipefail

MAP="${PANDORA_DS_MAP:-/Game/Entry/Entry}"
PORT="${PANDORA_DS_PORT:-7777}"
GAMEMODE="${PANDORA_DS_GAMEMODE:-}"
EXTRA_ARGS="${PANDORA_DS_EXTRA_ARGS:-}"

# 把 GameMode 作为 ?game= URL option 拼到地图 URL 上（UE 标准做法，优先级高于地图 GameModeOverride）。
MAP_URL="${MAP}"
if [[ -n "${GAMEMODE}" ]]; then
  MAP_URL="${MAP}?game=${GAMEMODE}"
fi

SERVER_SH="/home/pandora/server/PandoraServer.sh"
if [[ ! -x "${SERVER_SH}" ]]; then
  # 不同 UE 版本归档脚本名可能不同，做个兜底查找。
  SERVER_SH="$(find /home/pandora/server -maxdepth 2 -name "*Server.sh" | head -n1 || true)"
fi

if [[ -z "${SERVER_SH}" || ! -e "${SERVER_SH}" ]]; then
  echo "[entrypoint] 找不到服务器启动脚本(PandoraServer.sh)，请检查 stage/LinuxServer 打包产物。" >&2
  exit 1
fi

echo "[entrypoint] 启动 Pandora DS: ${SERVER_SH} ${MAP_URL} -port=${PORT} -log ${EXTRA_ARGS}"
echo "[entrypoint] AGONES_SDK_HTTP_PORT=${AGONES_SDK_HTTP_PORT:-<unset>} AGONES_SDK_GRPC_PORT=${AGONES_SDK_GRPC_PORT:-<unset>}"

# exec 让 DS 成为 PID 1，正确接收 SIGTERM（Agones 回收 Pod 时优雅退出）。
exec "${SERVER_SH}" "${MAP_URL}" -port="${PORT}" -log ${EXTRA_ARGS}
