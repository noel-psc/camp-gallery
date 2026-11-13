#!/usr/bin/env bash
set -euo pipefail
: "${ADMIN_PASSWORD:?请通过环境变量设置 ADMIN_PASSWORD}"
export ADMIN_PASSWORD
export PUBLIC_BASE_URL="${PUBLIC_BASE_URL:-http://127.0.0.1:8000}"
uv run --with-requirements requirements.txt \
  flask --app app run --host 0.0.0.0 --port 8000
