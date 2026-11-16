# Go 版迁移与部署

本版本将运行时改为 Go，保留 `data/camp.sqlite3`、`data/uploads/`、学生 token 和 `/s/<token>` 路径。服务名继续使用 `camp`。示例新镜像标签为 `v2.0.0-go`，需要先完成构建推送，服务器才能拉取。

## 1. 在开发机发布镜像

先运行本地检查，再登录并构建推送。此操作会向仓库发布镜像，请自行选择发布时机：

```bash
go test -race ./...
node --test tests/test_upload.js
export ACR_REGISTRY=crpi-krl99lxt28fuqfvk.cn-shanghai.personal.cr.aliyuncs.com
export ACR_NAMESPACE=noel-dev
# 按安装的工具二选一；交互输入仓库凭证。
podman login "$ACR_REGISTRY"
# docker login "$ACR_REGISTRY"
./deploy/push-acr.sh v2.0.0-go
```

脚本支持 Docker buildx 或 Podman，默认目标架构 `linux/amd64`。如果已在本机验证镜像，可设置 `SOURCE_IMAGE=localhost/camp-gallery:v2.0.0-go` 后执行脚本，复用它来推送。镜像是否发布，以命令成功结果为准。

将本版本 `docker-compose.yml` 复制到服务器原来的 `~/camp-gallery/`。服务器 `.env` 保留现有 `SECRET_KEY`、密码和 `PUBLIC_BASE_URL=http://noel-psc.cn:8000`，不要用示例文件覆盖它。保持原 Compose 项目名和部署目录。

## 2. 切换前完整备份

以下命令在服务器原部署目录执行。备份前先拉取用于打包的工具镜像，减少停机时间。请先结束正在进行的上传，选择活动低峰操作。

```bash
cd ~/camp-gallery
mkdir -p backups
chmod 700 backups
docker pull docker.io/library/alpine:3.23
container_id=$(docker compose ps -aq camp)
OLD_IMAGE=$(docker inspect "$container_id" --format '{{.Config.Image}}')
volume_name=$(docker inspect "$container_id" --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}')
test -n "$volume_name" || { echo '未找到原 /data 数据卷，请先核对挂载'; exit 1; }
printf '%s\n' "$OLD_IMAGE" > backups/old-image.txt
cp .env backups/env-before-go
chmod 600 backups/env-before-go
docker compose stop camp
backup_file="camp-before-go-$(date +%Y%m%d-%H%M%S).tar.gz"
docker run --rm --user 0:0 \
  --mount "type=volume,src=$volume_name,dst=/data,readonly" \
  --mount "type=bind,src=$(pwd)/backups,dst=/backup" \
  -e "BACKUP_FILE=$backup_file" \
  docker.io/library/alpine:3.23 \
  sh -c 'tar -czf "/backup/$BACKUP_FILE" -C /data .'
test -s "backups/$backup_file"
```

最后一条成功后，再更新镜像。备份包含 SQLite、WAL/SHM（如存在）、作品原文件及原有子目录。请将备份复制到另一台设备或独立存储。若打包失败，保留数据卷，先恢复旧服务并排查，暂缓切换。

## 3. 启动 Go 版

编辑服务器 `.env`，将 `IMAGE` 改为已成功推送的标签，其余原有设置保留：

```dotenv
IMAGE=crpi-krl99lxt28fuqfvk.cn-shanghai.personal.cr.aliyuncs.com/noel-dev/camp-gallery:v2.0.0-go
PUBLIC_BASE_URL=http://noel-psc.cn:8000
MAX_CONCURRENT_UPLOADS=8
MAX_QUEUED_UPLOADS=128
MIN_FREE_DISK_MB=512
UPLOAD_TIMEOUT_SECONDS=7200
UPLOAD_BANDWIDTH_MBPS=90
DOWNLOAD_BANDWIDTH_MBPS=80
```

然后执行：

```bash
docker compose pull camp
docker compose up -d camp
docker compose ps
docker compose logs --tail=100 camp
curl -fsS http://127.0.0.1:8000/healthz
```

原卷由 UID 1000 写入，本版本延续同一 UID。若之前使用自定义宿主目录挂载，需要确认该目录可由 `1000:1000` 读写。避免同时启动两个程序进程访问同一个数据目录。

首次检测旧表结构时，程序先在 `/data/backups/` 写入 `camp-before-go-*.sqlite3`，再用事务补充活动表、活动关联、上传批次表和索引。**此自动备份只有数据库，不能替代上一节的完整备份。** 原学生统一归入默认活动；原文件路径与 token 保持不变。迁移失败会停止启动并打印原因，不要删除数据后重试。

## 4. 上线验收

1. 扫描一张已印刷的旧二维码，确认姓名、班级、学号及旧作品正常。
2. 用专门的测试学生上传一张图片、一段实际手机视频，查看进度、默认查看页及编辑功能。
3. 在后台导出本期 ZIP，核对原文件和 CSV。打印预览标签、贺卡，试扫其中二维码。
4. 新建下一期测试活动，确认名单和统计相互独立；归档后扫码仍可查看。
5. 观察 `docker stats`、磁盘余量与日志。正式活动前，按接近实际文件大小和网络条件进行并发测试。

本机自动化包含 81 个并发、各 1 MiB 的 HTTP 上传，并在有限制的本地容器中完成 81 路各 2 MiB 上传，详情见 [验证记录](VALIDATION.md)。服务器 2 核 2 GB 是否满足实际活动，还取决于文件总量、磁盘和公网带宽；视频流量需要另测。程序会让超出保存并发的请求等待，队列满后提示重试。请核对云控制台的公网出站带宽，普通 `iperf3 -c` 测到的是客户端发送、服务器接收方向。

## 5. 回退

保留 `backups/old-image.txt` 中的旧镜像和完整数据备份。Go 版增加了多活动数据，旧 Python 程序不会按活动隔离，因此回退时应使用迁移前的完整备份。回退到该备份会丢失备份之后新增的操作；先另存当前数据，再决定。

建议将迁移前备份恢复到一个**新数据卷**，保留当前 Go 数据卷便于核对。下面的 `camp-rollback-YYYYMMDD`、备份文件路径需替换为实际值：

```bash
docker compose stop camp
docker volume create camp-rollback-YYYYMMDD
docker run --rm --user 0:0 \
  --mount type=volume,src=camp-rollback-YYYYMMDD,dst=/data \
  --mount "type=bind,src=$(pwd)/backups,dst=/backup,readonly" \
  docker.io/library/alpine:3.23 \
  sh -c 'tar -xzf /backup/实际完整备份文件.tar.gz -C /data'
```

创建 `docker-compose.rollback.yml`，让同一个服务使用恢复后的外部卷：

```yaml
volumes:
  camp-data:
    external: true
    name: camp-rollback-YYYYMMDD
```

用旧镜像启动：

```bash
OLD_IMAGE=$(cat backups/old-image.txt)
IMAGE="$OLD_IMAGE" docker compose -f docker-compose.yml -f docker-compose.rollback.yml up -d camp
```

之后所有 Compose 操作继续带上此 override 文件，直到有计划地恢复正式卷配置。不要删除当前卷或执行 `down -v`。

## 本次文件变更

新增：

- `go.mod`、`go.sum`：Go 模块与校验。
- `main.go`、`config.go`：启动、配置和健康检查。
- `models.go`、`store.go`：领域数据、SQLite 和旧库迁移。
- `server.go`、`auth.go`：HTTP、模板、登录与 CSRF。
- `handlers_space.go`、`handlers_admin.go`：学生与活动后台流程。
- `uploads.go`、`export.go`、`bandwidth.go`：流式上传、重试核对、恢复清理、原文件导出及独立收发限速。
- `server_test.go`、`bandwidth_test.go`：HTTP 集成、安全、迁移、并发、排队和共享带宽测试。
- `templates/activities.html`、`templates/error.html`：活动管理、错误页面。
- `static/app.js`：打印、返回及删除确认。
- `docs/MIGRATION.md`、`docs/VALIDATION.md`：本说明与验证记录。

修改：

- `templates/base.html`、`home.html`、`space.html`、`login.html`、`admin.html`、`cards.html`。
- `static/style.css`、`static/upload.js`、`tests/test_upload.js`。
- `Dockerfile`、`docker-compose.yml`、`run.sh`、`.env.example`、`.dockerignore`、`.gitignore`、`README.md`。

移除：`app.py`、`requirements.txt`、`tests/test_export.py`。原实现仍可从 Git 历史检出，服务器旧镜像需要另外保留。

`docker-compose.build.yml` 和 `deploy/push-acr.sh` 沿用原文件。此次开发不自动推送镜像、不访问生产数据、不修改服务器、不创建 Git 提交。
