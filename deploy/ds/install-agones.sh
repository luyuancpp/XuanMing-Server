#!/usr/bin/env bash
# 安装/升级 Agones 到最新稳定版（本仓库基线 = v1.58.0）。
# 官方仓库已从 googleforgames/agones 迁到 agones-dev/agones；helm repo 地址不变。
#
# 用法：
#   ./deploy/ds/install-agones.sh [VERSION]
#   VERSION 默认 1.58.0（撰写时最新稳定版）。升级时把这里和 Fleet 一起过一遍。
#
# 前置：kubectl 指向目标集群、已装 helm 3。K8s 版本需在 Agones 支持矩阵内
#       （v1.58 支持 K8s 1.33 / 1.34 / 1.35）。
set -euo pipefail

VERSION="${1:-1.58.0}"
NAMESPACE="agones-system"

echo "[install-agones] 目标版本: ${VERSION}"

helm repo add agones https://agones.dev/chart/stable
helm repo update

# featureGates 可按需开关；这里用稳定默认。生产建议设置多副本 + 反亲和（见官方 HA 文档）。
helm upgrade --install agones agones/agones \
  --version "${VERSION}" \
  --namespace "${NAMESPACE}" \
  --create-namespace \
  --wait

echo "[install-agones] 校验 CRD 与控制器："
kubectl get crd | grep agones.dev || true
kubectl -n "${NAMESPACE}" get pods

echo "[install-agones] 完成。接着 apply deploy/k8s/agones 下的 Fleet/RBAC。"
