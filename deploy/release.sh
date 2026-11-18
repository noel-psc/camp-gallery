#!/usr/bin/env bash
# 在开发机构建、发布；可选通过 SSH 更新现有服务器。
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

host=''
if [[ "${1:-}" == --deploy ]]; then
  host="${2:?用法：release.sh --deploy 用户名@服务器 [版本号]}"
  [[ "$host" =~ ^[A-Za-z0-9][A-Za-z0-9_.@:-]*$ ]] || { echo 'SSH 地址格式无效' >&2; exit 1; }
  shift 2
fi
[[ $# -le 1 ]] || { echo '用法：release.sh [--deploy 用户名@服务器] [版本号]' >&2; exit 1; }
tag="${1:-$(date -u +%Y%m%dT%H%M%SZ)-$(git rev-parse --short HEAD 2>/dev/null || echo build)}"
[[ "$tag" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ && "$tag" != latest ]] || { echo '请使用独立版本号，脚本会另行发布 latest' >&2; exit 1; }

export ACR_REGISTRY="${ACR_REGISTRY:-crpi-krl99lxt28fuqfvk.cn-shanghai.personal.cr.aliyuncs.com}"
export ACR_NAMESPACE="${ACR_NAMESPACE:-noel-dev}"
export IMAGE_NAME="${IMAGE_NAME:-camp-gallery}"
export PLATFORM="${PLATFORM:-linux/amd64}"
unset SOURCE_IMAGE
if command -v docker >/dev/null 2>&1; then
  cli=docker
elif command -v podman >/dev/null 2>&1; then
  cli=podman
else
  echo '需要 Docker 或 Podman' >&2; exit 1
fi
image="$ACR_REGISTRY/$ACR_NAMESPACE/$IMAGE_NAME:$tag"
latest="$ACR_REGISTRY/$ACR_NAMESPACE/$IMAGE_NAME:latest"
if [[ -n "$host" ]]; then
  command -v ssh >/dev/null
  # 在构建前确认服务器部署目录、Docker 权限和 Compose 可用。
  ssh "$host" 'cd /opt/camp-gallery && test -f .env && test -f docker-compose.yml && sudo -n docker info >/dev/null && sudo -n docker compose version >/dev/null'
fi
./deploy/push-acr.sh "$tag"
"$cli" tag "$image" "$latest"
"$cli" push "$latest"
echo "发布成功：$image"
if [[ -n "$host" ]]; then
  ssh "$host" 'sudo -n tee /opt/camp-gallery/update.sh >/dev/null' < deploy/update.sh
  ssh "$host" "cd /opt/camp-gallery && sudo -n bash ./update.sh '$image'"
else
  echo '服务器在原部署目录执行：sudo bash ./update.sh'
fi
