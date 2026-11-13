#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
: "${ACR_REGISTRY:?请设置 ACR_REGISTRY}"
: "${ACR_NAMESPACE:?请设置 ACR_NAMESPACE}"
IMAGE_NAME="${IMAGE_NAME:-camp-gallery}"
TAG="${1:-latest}"
PLATFORM="${PLATFORM:-linux/amd64}"
IMAGE="${ACR_REGISTRY}/${ACR_NAMESPACE}/${IMAGE_NAME}:${TAG}"
if command -v docker >/dev/null 2>&1; then
  CLI=docker
elif command -v podman >/dev/null 2>&1; then
  CLI=podman
else
  echo "需要安装 Docker 或 Podman" >&2
  exit 1
fi
if [[ -n "${SOURCE_IMAGE:-}" ]]; then
  "$CLI" tag "$SOURCE_IMAGE" "$IMAGE"
elif [[ "$CLI" == docker ]]; then
  "$CLI" buildx build --platform "$PLATFORM" --load -t "$IMAGE" .
else
  "$CLI" build --format docker --platform "$PLATFORM" -t "$IMAGE" .
fi
"$CLI" push "$IMAGE"
echo "已推送 $IMAGE"
