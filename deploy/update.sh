#!/usr/bin/env bash
# 在服务器原部署目录运行。保留数据卷；失败时恢复到独立数据卷。
set -euo pipefail
umask 077
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
if [[ -f "$script_dir/../docker-compose.yml" ]]; then
  default_dir="$script_dir/.."
else
  default_dir="$script_dir"
fi
cd -- "${DEPLOY_DIR:-$default_dir}"
deploy_dir=$PWD
[[ $# -le 1 ]] || { echo '用法：update.sh [版本号或完整镜像地址]' >&2; exit 1; }
repository=crpi-krl99lxt28fuqfvk.cn-shanghai.personal.cr.aliyuncs.com/noel-dev/camp-gallery
target="${1:-latest}"
if [[ "$target" != */* ]]; then
  [[ "$target" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]] || { echo '版本号格式无效' >&2; exit 1; }
  target="$repository:$target"
fi
[[ "$target" =~ ^[A-Za-z0-9][A-Za-z0-9_./:@-]+$ ]] || { echo '镜像地址格式无效' >&2; exit 1; }
[[ -f .env && -f docker-compose.yml ]] || { echo '请将脚本放在现有 camp-gallery 部署目录，保留 .env 和 docker-compose.yml' >&2; exit 1; }
for tool in docker python3 flock; do
  command -v "$tool" >/dev/null || { echo "缺少工具：$tool" >&2; exit 1; }
done
docker info >/dev/null 2>&1 || { echo '无法访问 Docker，请使用 sudo bash ./update.sh' >&2; exit 1; }
docker compose version >/dev/null
exec 9>.camp-update.lock
flock -n 9 || { echo '另一次更新正在进行' >&2; exit 1; }
# 以部署目录中的 .env 为准，避免终端残留变量选择错误版本或配置。
unset IMAGE COMPOSE_FILE
timeout="${UPDATE_TIMEOUT_SECONDS:-120}"
[[ "$timeout" =~ ^[1-9][0-9]{0,3}$ ]] || { echo 'UPDATE_TIMEOUT_SECONDS 必须为 1–9999 秒' >&2; exit 1; }

compose() { docker compose --env-file .env "$@"; }
set_env() {
  python3 - "$1" "$2" <<'PY'
import os, re, sys, tempfile
from pathlib import Path
key, value = sys.argv[1:]
path = Path('.env')
text = path.read_text()
# Compose 单引号值保留字面量，避免路径中的 $ 被插值。
line = key + "='" + value.replace('\\', '\\\\').replace("'", "\\'") + "'"
pattern = re.compile(r'^\s*(?:export\s+)?' + re.escape(key) + r'\s*=.*$', re.M)
text = pattern.sub(lambda _: line, text) if pattern.search(text) else text.rstrip() + '\n' + line + '\n'
fd, name = tempfile.mkstemp(prefix='.camp-env-', dir='.')
try:
    with os.fdopen(fd, 'w') as stream:
        stream.write(text)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(name, path)
finally:
    if os.path.exists(name): os.unlink(name)
PY
}
wait_healthy() {
  local cid status health deadline=$((SECONDS + timeout))
  while (( SECONDS < deadline )); do
    cid=$(compose ps -aq camp) || return 1
    [[ -n "$cid" && "$cid" != *$'\n'* ]] || return 1
    status=$(docker inspect "$cid" --format '{{.State.Status}}') || return 1
    case "$status" in exited|dead|removing) return 1 ;; esac
    health=$(docker inspect "$cid" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}}') || return 1
    [[ "$status" == running && "$health" == healthy ]] && return 0
    [[ "$health" != unhealthy && "$health" != missing ]] || return 1
    sleep 2
  done
  return 1
}

cid=$(compose ps -aq camp)
[[ -n "$cid" && "$cid" != *$'\n'* ]] || { echo '需要一个已部署的 camp 容器；首次部署请按 README 操作' >&2; exit 1; }
stamp="$(date -u +%Y%m%dT%H%M%SZ)-$$"
backup="$deploy_dir/backups/update-$stamp"
mkdir -p "$backup"
chmod 700 "$deploy_dir/backups" "$backup"
cp .env "$backup/env-before"
cp docker-compose.yml "$backup/compose-before.yml"
if [[ -f compose.camp-recovery.json ]]; then cp compose.camp-recovery.json "$backup/recovery-before.json"; fi
docker inspect "$cid" > "$backup/container-before.json"
# 将已有 Compose 文件列表和旧镜像 ID 留作恢复依据，记录不输出到终端。
python3 - "$backup/container-before.json" "$backup" <<'PY'
import json, sys
from pathlib import Path
info = json.loads(Path(sys.argv[1]).read_text())[0]
folder = Path(sys.argv[2])
mounts = [m for m in info['Mounts'] if m['Destination'] == '/data' and m['Type'] in ('volume', 'bind')]
if len(mounts) != 1: raise SystemExit('未找到唯一的 /data 持久化挂载，停止更新')
labels = info['Config'].get('Labels') or {}
files = labels.get('com.docker.compose.project.config_files', '')
if not files: raise SystemExit('找不到原 Compose 配置文件记录，停止更新')
paths = files.split(',')
if any(':' in p or '\n' in p or '\r' in p for p in paths):
    raise SystemExit('Compose 配置路径包含不支持的字符')
(folder / 'compose-files.txt').write_text('\n'.join(paths))
(folder / 'old-image-id.txt').write_text(info['Image'])
PY
old_image="camp-gallery-rollback:$stamp"
docker tag "$(cat "$backup/old-image-id.txt")" "$old_image"
echo "拉取镜像：$target"
docker pull "$target"
# 新镜像必须带健康检查，避免仅凭进程存活判断更新成功。
docker image inspect "$target" | python3 -c 'import json,sys; h=json.load(sys.stdin)[0]["Config"].get("Healthcheck") or {}; t=h.get("Test") or []; sys.exit(0 if t and t[0] != "NONE" else "目标镜像缺少健康检查")'

stopped=0
backup_complete=0
restore_container=''
rollback() {
  echo '更新失败，正在从备份恢复旧版本……' >&2
  compose stop camp || return 1
  local volume="camp-recovery-$stamp" recovery="$deploy_dir/compose.camp-recovery.json" files
  docker volume create "$volume" >/dev/null || return 1
  restore_container=$(docker create --user 0:0 --mount "type=volume,src=$volume,dst=/data" "$old_image") || return 1
  docker cp -a "$backup/data/." "$restore_container:/data" || return 1
  docker rm "$restore_container" >/dev/null || return 1
  restore_container=''
  if [[ -f "$recovery" ]]; then cp "$recovery" "$backup/recovery-before.json" || return 1; fi
  python3 - "$recovery" "$volume" "$backup/compose-files.txt" <<'PY'
import json, os, sys, tempfile
from pathlib import Path
path, volume, original = sys.argv[1:]
config = {'services': {'camp': {'image': '${IMAGE}', 'volumes': [
    {'type': 'volume', 'source': 'camp-recovery-data', 'target': '/data'}]}},
    'volumes': {'camp-recovery-data': {'external': True, 'name': volume}}}
fd, tmp = tempfile.mkstemp(prefix='.camp-recovery-', dir='.')
with os.fdopen(fd, 'w') as f: json.dump(config, f)
os.replace(tmp, path)
paths = [p for p in Path(original).read_text().splitlines() if p != path]
Path(original + '.recovery').write_text(':'.join(paths + [path]))
PY
  [[ $? -eq 0 ]] || return 1
  cp "$backup/env-before" .env || return 1
  set_env IMAGE "$old_image" || return 1
  files=$(cat "$backup/compose-files.txt.recovery") || return 1
  set_env COMPOSE_FILE "$files" || return 1
  compose up -d --no-deps --force-recreate --pull never camp || return 1
  wait_healthy || return 1
  echo "已恢复旧镜像和备份时点的数据。失败版本的数据卷仍保留。备份：$backup" >&2
}
finish() {
  local result=$?
  trap - EXIT INT TERM
  if (( result != 0 && stopped )); then
    set +e
    if (( backup_complete )); then
      if ! rollback; then
        echo "自动恢复未完成，请保留全部数据卷，备份位于：$backup" >&2
      fi
    else
      compose start camp || echo '恢复原容器失败，请检查 Docker 状态' >&2
      echo "完整备份未完成，原数据卷保留。备份目录：$backup" >&2
    fi
  fi
  if [[ -n "$restore_container" ]]; then docker rm "$restore_container" >/dev/null 2>&1 || true; fi
  exit "$result"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo '暂停服务并备份全部作品、数据库；请在活动低峰更新。'
stopped=1
compose stop camp
docker cp -a "$cid:/data/." "$backup/data"
[[ -s "$backup/data/camp.sqlite3" ]] || { echo '备份中没有有效的数据库文件' >&2; exit 1; }
backup_complete=1
set_env IMAGE "$target"
compose up -d --no-deps --force-recreate --pull never camp
if ! wait_healthy; then
  docker compose --env-file .env logs --tail=80 camp > "$backup/failed-version.log" 2>&1 || true
  echo '新版本未通过健康检查，日志已保存到备份目录。' >&2
  exit 1
fi
stopped=0
echo "更新成功：$target"
echo "完整备份：$backup"
echo '原有网站地址、名单、二维码和作品继续使用。'
