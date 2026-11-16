#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
if ! command -v go >/dev/null 2>&1; then
  echo "需要安装 Go 1.27 或更新版本；也可使用 Docker Compose 启动。" >&2
  exit 1
fi
exec go run -buildvcs=false .
