#!/usr/bin/env bash
# 构建 Pandora DS 镜像，并可选做本地 retag。
# 前置：deploy/ds/stage/LinuxServer 已就绪（客户端仓库 build-linux-ds.ps1 拷好）。
#
# 用法：
#   ./deploy/ds/build-image.sh [TAG] [REGISTRY]
#   TAG       默认 pandora/battle-ds:dev
#   REGISTRY  传了只做本地 retag（如 registry.example.com/pandora）；push 由人手动执行
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TAG="${1:-pandora/battle-ds:dev}"
REGISTRY="${2:-}"

if [[ ! -d "${SCRIPT_DIR}/stage/LinuxServer" ]]; then
  echo "[build-image] 缺少 stage/LinuxServer，请先在 Windows 跑客户端仓库的 build-linux-ds.ps1。" >&2
  exit 1
fi

echo "[build-image] docker build -t ${TAG}"
docker build -f "${SCRIPT_DIR}/Dockerfile" -t "${TAG}" "${SCRIPT_DIR}"

if [[ -n "${REGISTRY}" ]]; then
  FULL="${REGISTRY%/}/${TAG##*/}"
  echo "[build-image] tag -> ${FULL}"
  docker tag "${TAG}" "${FULL}"
  echo "[build-image] 已本地标记: ${FULL}"
  echo "[build-image] 按 AGENTS.md，docker push 由人手动执行；推送后记得把 Fleet 的 image 改成这个。"
fi

echo "[build-image] 完成。"
