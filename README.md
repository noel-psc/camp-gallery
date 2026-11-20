# SWEET少年的创想空间

面向研学营等多期活动的学生作品网站。Go HTTP + SQLite + 本地文件存储，页面与静态资源内嵌在一个二进制文件中。适用于 Linux 单机部署。

## 学生与活动流程

1. 管理员进入 `/admin`，选择或创建活动，设置北京时间的上传截止时间。
2. 按班级粘贴“学号 姓名”名单，每行一人。支持空格、逗号、制表符；保留学号前导零。同一期、同一班级的学号唯一。
3. 打印个人二维码标签、贺卡，核对班级、学号、姓名后发放。两种印刷品使用相同的 `/s/<token>` 链接。
4. 学生首次扫码直接上传图片、视频或文章；已有作品时默认查看，截止前可进入“编辑作品”，继续添加、改名或删除。
5. 活动归档后保留原二维码和作品，停止上传、编辑；管理员仍可查看、打印和导出。新一期使用独立名单与二维码。

相同活动内重复导入同一学生会保留原二维码。发生学号、姓名冲突时整批取消导入。旧空间缺少学号时，优先匹配同班同名的唯一旧空间并补充学号；有多个匹配时提示老师核对。

## 本地启动

安装 Go 1.27 或更新版本，在项目目录执行：

```bash
cp .env.example .env
chmod 600 .env
# 编辑 .env，填写 ADMIN_PASSWORD 和 SECRET_KEY。
# 本机测试可将 PUBLIC_BASE_URL 改为 http://127.0.0.1:8000。
# 默认 LISTEN_ADDR=:8000；局域网测试把 PUBLIC_BASE_URL 改为本机 IP 即可，流量不加密。
./run.sh
```

打开 `http://127.0.0.1:8000`，管理入口为 `/admin`。程序自动读取当前目录的 `.env`，已设置的环境变量优先。`run.sh` 会先进入项目目录。也可以编译运行：

```bash
go build -o camp .
./camp
```

默认数据目录为项目下 `data/`。启动时若没有设置 `SECRET_KEY`，本地运行会生成临时密钥，重启后登录会失效；正式部署请配置固定随机密钥。

## 容器部署

现有部署的 `.env` 和 `docker-compose.yml` 可以直接沿用：默认仍是 HTTP 8000，旧二维码继续有效，更新后不需要改配置。

```dotenv
PUBLIC_BASE_URL=http://noel-psc.cn:8000
PORT=8000
```

构建并运行：

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

HTTP 是明文传输；要修这一项必须配 TLS 反向代理（Caddy 或已有 nginx）。启用后可只改 `.env` 并加 `--profile tls`：

```dotenv
PUBLIC_BASE_URL=https://noel-psc.cn
SITE_ADDRESS=noel-psc.cn
# 可选：填 Caddy 容器 IP/CIDR 才启用应用层强制跳转和真实客户端 IP
# TRUSTED_PROXIES=
# 不需要兼容旧二维码时设 127.0.0.1；需要 8000 跳转时保持 0.0.0.0
BIND_ADDR=127.0.0.1
```

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml --profile tls up -d --build
```

`TRUSTED_PROXIES` 只有在 `PUBLIC_BASE_URL=https` 且确实存在该代理时才生效；否则程序仍按 HTTP 兼容模式运行。Caddy 需要域名解析到本机并放通 TCP 80/443；不填 `TRUSTED_PROXIES` 时 Caddy 仍会终止 TLS，但应用层不额外做 HTTPS 跳转/真实 IP 解析。已有 nginx 时不用 tls profile，把 `TRUSTED_PROXIES` 改为 camp 侧看到的代理地址（宿主机 nginx + Compose 常见 `172.28.0.1/32`，裸机 `127.0.0.1/32`），并保留 `Host`、设置 `X-Forwarded-Proto` 和 `X-Forwarded-For`。

`HSTS_MAX_AGE` 默认 `0`（不发送 HSTS）。开启后浏览器会把 `http://域名:8000/...` 升级为 `https://域名:8000/...`，而 8000 是明文 HTTP，旧二维码会握手失败；确认旧二维码退役或 8000 也提供 TLS 后再设为 `31536000`。

镜像采用多阶段构建，运行时为静态 Go 二进制，UID/GID 为 `1000:1000`。Compose 默认只读根文件系统、可写 `/data` 卷，限制为 1.5 CPU、768 MiB 内存，给 2 核 2 GB 主机上的系统留出空间，并设置 Go 运行时内存软上限 `GOMEMLIMIT=512MiB`。实际峰值应结合文件大小、网络与部署压测观察。默认服务名 `camp`、容器内端口 `8000` 和数据卷键 `camp-data` 保持一致。

**已有 Python 站点请先按 [迁移与部署说明](docs/MIGRATION.md) 备份，再切换镜像。** 保持服务器的部署目录、Compose 项目名和数据卷不变。现有 `.env` 可直接沿用，默认 HTTP 8000 和旧二维码继续有效，`update.sh` 不需要改配置。要启用 HTTPS 时再单独配置反向代理。请勿执行 `docker compose down -v`。

生产入口默认是 HTTP 8000，云安全组和防火墙放通 TCP 8000。启用 HTTPS 时再放通 80/443；旧二维码需要 8000 时可继续保留。反代请求大小、读写超时也要允许视频上传（nginx 可设 `client_max_body_size 210m;`、`proxy_read_timeout 2h;`）。

## 一条命令发布与更新

在笔记本项目目录运行：

```bash
./deploy/release.sh
```

脚本从当前源码重新构建镜像，发布到现有阿里云 `noel-dev/camp-gallery` 仓库，同时推送时间戳版本与 `latest`。需要本机已有有效的容器仓库登录。也可指定版本：`./deploy/release.sh v2.1.0-go`。

首次将服务器更新脚本复制过去，在笔记本执行（服务器账号按实际填写）：

```bash
ssh ecs-user@139.196.79.195 'sudo tee /opt/camp-gallery/update.sh >/dev/null' < deploy/update.sh
```

之后每次发布成功，在服务器原部署目录执行：

```bash
cd /opt/camp-gallery
sudo bash ./update.sh
```

可指定某次已发布版本：`sudo bash ./update.sh v2.1.0-go`。服务器需安装 Docker Compose、Python 3 和 `flock`，并通过 `sudo docker login` 登录仓库。脚本先拉取镜像，成功后暂停服务、完整备份 `/data`，再更新容器并等待健康检查。密码、密钥、网站地址和持久化数据沿用原部署配置，现有 HTTP 配置不需要改动。默认健康检查最多等待 120 秒，可通过 `UPDATE_TIMEOUT_SECONDS` 调整。`caddy` 属于 `tls` profile，`update.sh` 只重建 `camp`，不会停止或重建代理。

备份位于部署目录 `backups/update-时间戳/`，包含数据库、作品、原配置和容器信息。备份失败会尝试重启原容器；更新失败会把备份恢复到新数据卷并启动旧镜像，同时保留失败版本的数据卷供核对。恢复配置通过 `.env` 的 `COMPOSE_FILE` 持续生效。备份可能包含密码和私人作品，请妥善保存；完整备份会占用额外磁盘空间，脚本不会自动删除备份或数据卷。

如果笔记本已能 SSH 登录服务器，且服务器账号有免密 sudo Docker 和写入更新脚本的权限，也可以一次完成发布和更新：

```bash
./deploy/release.sh --deploy ecs-user@139.196.79.195
```

此命令先验证 SSH 和权限，再构建推送、复制更新脚本并触发服务器更新。更新时会有备份和启动所需的维护窗口，建议在无学生上传时执行。本地脚本已通过模拟 Docker/SSH 的成功、失败恢复及并发锁测试；真实部署结果以服务器健康检查为准。

## 配置

| 变量 | 默认值 / 作用 |
| --- | --- |
| `ADMIN_PASSWORD` | 必填，管理员密码 |
| `SECRET_KEY` | 正式部署必填，建议 `openssl rand -hex 32` 生成 |
| `PUBLIC_BASE_URL` | 默认可用 `http://noel-psc.cn:8000`；启用 HTTPS 时改为 `https://...` 并配置 `TRUSTED_PROXIES`；印刷二维码使用此地址 |
| `TRUSTED_PROXIES` | 可选，反向代理 IP/CIDR（逗号分隔）；只有来自这些地址的 `X-Forwarded-Proto` 和 `X-Forwarded-For` 被信任 |
| `ALLOW_INSECURE_HTTP` | `0`；设为 `1` 可强制关闭 HTTPS 跳转（仅测试/回退用） |
| `HSTS_MAX_AGE` | `0`；确认旧 8000 端口二维码退役后再设 `31536000`，否则旧二维码会被浏览器升级到 `https://域名:8000` 而失败 |
| `BIND_ADDR` | `0.0.0.0`；Compose 发布 `camp` 8000 的宿主地址；只让反代访问时设 `127.0.0.1` |
| `SITE_ADDRESS` | 可选，仅 `tls` profile 使用；Caddy 的域名，例如 `noel-psc.cn` |
| `LISTEN_ADDR` | `:8000`；容器镜像默认匹配端口和健康检查；本机测试可保持默认 |
| `DATA_DIR` | 本地 `data`，容器 `/data` |
| `ACTIVITY_TITLE` | `心纹研学营`，仅初始化默认活动时使用 |
| `UPLOAD_DEADLINE` | `2026-11-19T23:59:59+08:00`，仅初始化默认活动时使用 |
| `MAX_UPLOAD_MB` | `200`，单次 multipart 请求上限，包含文件及少量表单开销 |
| `MAX_CONCURRENT_UPLOADS` | `8`，同时接收、保存的上传请求数 |
| `MAX_QUEUED_UPLOADS` | `128`，额外允许等待的上传请求数；满时返回 429 |
| `MIN_FREE_DISK_MB` | `512`，保存上传后预留的磁盘空间；不足时返回 507 |
| `UPLOAD_BANDWIDTH_MBPS` | `90`，全站上传合计接收速率，`0` 不限速 |
| `DOWNLOAD_BANDWIDTH_MBPS` | `80`，作品文件和 ZIP 共用发送速率，`0` 不限速 |
| `GOMEMLIMIT` | 容器默认 `512MiB`，Go 运行时内存软上限；容器硬上限为 768 MiB |
| `UPLOAD_TIMEOUT_SECONDS` | `7200`，从进入上传处理到完成的总时间上限，包含排队 |

大文件带宽按方向独立计量：`UPLOAD_BANDWIDTH_MBPS=90` 控制所有上传的合计接收速度；`DOWNLOAD_BANDWIDTH_MBPS=80` 控制作品文件和 ZIP 的合计发送速度，单位为十进制 Mbps，设为 `0` 可关闭相应限制。首页、后台 HTML、脚本、样式和控制请求直接处理。限速通过流式读写和 TCP 背压生效，会有少量协议、缓冲与瞬时突发开销，需结合云监控观察。

默认最多 8 路上传和 128 路等待，2 小时总超时包含排队；单次 200 MiB、81 人连续上传的纯数据量约 15.8 GiB，按 90 Mbps 理论至少约 25 分钟，实际取决于学生网络与磁盘。队列满、磁盘不足、网络断开等情况会保留文件并提示重试。

活动创建后，名称、截止时间在后台管理；修改初始化环境变量不会覆盖数据库中各期的设置。已截止的活动延长截止后可重新上传；归档活动需要先恢复，再核对截止时间。

## 上传、浏览和导出

- 支持 JPG、PNG、GIF、WebP、MP4、MOV、WebM 和 DOCX。后端核对扩展名和文件头；视频能否播放还取决于浏览器和编码。程序保留原文件，不转码。
- 文章可直接输入或复制粘贴正文（最多 20000 字），单独提交或与文件一起提交；正文按 UTF-8 纯文本保存、在线查看，段落换行保留。也可上传 Word `.docx`，页面提供原文件下载；文档需为有效 Word ZIP 包，解压后总量不超过 100 MB、正文 XML 不超过 4 MB。文章沿用截止、归档、改名和删除规则；更新正文时先上传新版本，确认后删除旧版本。
- 粘贴文章以 `.txt`、Word 文章以原始 `.docx` 纳入同一个班级/学生 ZIP 导出目录，CSV 同时记录文章。无需新增数据库字段。
- 流式写入临时目录，完整接收、校验后再发布并提交数据库，避免半份作品出现在页面中。网络上传期间不占用 SQLite 连接。
- 页面显示真实已发送字节进度与“正在保存”状态。失败后保留所选文件，相同批次、相同内容重试会核对已有记录，避免重复作品；刷新页面或重新选择文件会产生新批次。
- 断开连接、无效格式、超限等正常失败会清理本批临时文件；异常退出留下的临时目录在之后启动时检查，超过 24 小时后清理，同时核对发布日志中的孤儿文件。
- 截止判断以请求开始处理的服务器时间为准；截止前已开始的上传可在超时范围内完成。归档后尚未提交的上传会被拒绝。
- 学生可在专属空间预览和下载图片、视频原文件（保留原文件名），截止后仍可下载。图片懒加载；视频默认不预加载，支持 HTTP Range。浏览、下载和 ZIP 导出共用作品出站带宽。
- 后台按活动导出原文件 ZIP，路径为 `班级/学号_姓名/作品编号_原文件名`，包含 UTF-8 BOM 的 `作品清单.csv`，注明活动、作品及缺失文件状态。一次只运行一个 ZIP 导出，并流式输出。

当前规模先使用单 Go 进程、SQLite WAL 和本地盘。上传限流由程序内完成，不需要另起消息队列。以后需要视频转码、缩略图生成、内容审核或大量后台任务时，再把这些处理放入异步任务；需要横向多实例时再统一调整共享存储、数据库和并发控制。

二维码链接是空间访问凭证，持有者可查看作品，开放期间可修改。请只发给相应学生和必要工作人员。管理员登录有签名会话、CSRF 校验和登录失败限流；密码变更后原登录失效。

## 检查与容量验证

```bash
go test -race -v ./...
go vet ./...
node --test tests/test_upload.js
```

Go 集成测试覆盖旧库迁移、二维码与 token 保留、多活动隔离、名单重复导入、CSRF、文件类型和大小限制、上传幂等、超时清理、Range、ZIP、归档，以及慢上传时首页和健康检查仍可响应。并发用例在本机通过真实 HTTP 同时提交 81 个各 1 MiB 的测试图片，检查全部作品入库、文件完整及保存并发上限。

另在 1.5 CPU、768 MiB 的本地容器中完成 81 路各 2 MiB 上传：81/81 成功，应用吞吐约 89 Mbps；期间页面探测全部成功。完整条件、资源采样和浏览器结果见 [验证记录](docs/VALIDATION.md)。

这项测试验证程序行为，不能代表 ECS 公网视频吞吐。上线前应在同规格环境使用接近活动实际大小的图片、视频复测。81 人每人 100 MiB，原文件约需 7.9 GiB；磁盘还需留出临时上传、数据库及备份空间。可重点观察 `docker stats`、可用磁盘、上传耗时、429/507 返回和页面响应。

## 文件位置

- `main.go`、`config.go`：启动、配置、健康检查和退出。
- `store.go`、`models.go`：SQLite 迁移、活动、学生与作品。
- `server.go`、`auth.go`、`handlers_*.go`：页面、路由、登录和表单保护。
- `uploads.go`、`articles.go`、`export.go`、`bandwidth.go`：流式上传、批次核对、文件发布、ZIP 与收发速率控制。
- `templates/`、`static/`：页面、响应式样式和浏览器脚本。
- `server_test.go`、`bandwidth_test.go`、`tests/test_upload.js`：后端集成测试和上传交互测试。
- `docs/MIGRATION.md`、`docs/VALIDATION.md`：发布迁移、备份回退、文件清单与验证结果。
