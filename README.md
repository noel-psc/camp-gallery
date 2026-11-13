# 心纹研学营作品空间

按班级、学号和姓名为每位学生生成专属作品空间，批量打印个人二维码标签及贺卡。标签与贺卡使用同一二维码：尚无作品时显示上传页，上传后再次扫码默认查看作品，点击“编辑作品”可继续添加、修改名称或删除作品；截止后仅可查看。默认截止时间为北京时间 **2026 年 11 月 19 日 23:59:59**，可通过 `UPLOAD_DEADLINE` 调整，使用带时区的 ISO 8601 时间。

## 学生身份与作品导出

管理员按班级导入“学号 姓名”，每行一人，支持空格、逗号或制表符分隔，可直接粘贴表格中的学号、姓名两列。例如：

```text
001 小明
002 小红
```

学号支持 1–32 位数字、英文字母或连字符，保留开头的零。同班学号唯一，不同班级可使用相同学号；同名学生以班级和学号区分。名单预先绑定二维码，学生扫码后直接上传，无需重复填写姓名和学号。相同班级、学号和姓名重复导入会保留原空间与二维码；同班学号与姓名冲突时整批取消导入。

后台可分别选择“打印专属二维码”和“打印全部贺卡”，两种印刷品使用同一个学生空间链接。先核对卡片上的班级、学号和姓名，再发给对应学生。

已有作品和二维码会保留。旧空间缺少学号时，导入名单会按班级和姓名匹配唯一的未登记空间并补充学号；同班同名的旧空间有多条时会提示老师核对。未通过名单补充的旧空间，仅在首次上传时要求补充学号。

管理员登录 `/admin` 后，点击“导出全部作品 ZIP”，下载按 `班级/学号_姓名/作品编号_原文件名` 整理的照片和视频原文件，以及包含作品名称、上传时间、导出状态的 UTF-8 CSV 清单。未填写学号的旧空间使用“待补学号”目录；重名目录自动附加空间编号。无作品的学生也保留文件夹。文件缺失会在清单中注明，截止后仍可导出。

作品导出用于收集成果；完整恢复网站仍需备份整个数据目录，包含二维码对应关系的 SQLite 文件。

## 部署

1. 复制 `.env.example` 为 `.env`，设置随机 `SECRET_KEY`、管理员密码及学生手机可访问的 `PUBLIC_BASE_URL`（如 `https://camp.example.com`，不要带末尾斜杠）。公网部署请配置 HTTPS，反向代理的请求大小和超时也要允许视频上传。
2. 本地构建启动：`docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`。打开 `/admin` 登录，按班级逐行导入学号和姓名，打印专属二维码标签及贺卡。导入前核对名单，重复导入相同学生会保留原空间。
3. 在已安装 Docker buildx 或 Podman、已登录阿里云容器镜像服务并已创建对应仓库的机器上，设置 `ACR_REGISTRY` 和 `ACR_NAMESPACE`，运行 `./deploy/push-acr.sh`。可用 `IMAGE_NAME`、`PLATFORM` 环境变量及第一个参数指定仓库名、架构和标签。服务器 `.env` 中将 `IMAGE` 设置为推送的完整镜像地址，然后运行 `docker compose up -d`。

数据保存在 Docker volume `camp-data`，包含 SQLite 和上传文件；升级时保留该 volume，定期同时备份数据库和 uploads（备份前停止服务以确保一致性）。请勿运行 `docker compose down -v`，否则会删除作品。二维码链接是访问空间的凭证，拿到链接的人可查看作品，截止前还可修改；请只发给对应学生和必要的工作人员。活动涉及儿童作品，请控制传播范围。

默认单次请求最大 200 MB，可用 `MAX_UPLOAD_MB` 调整；支持 JPG、PNG、GIF、WebP、MP4、WebM、MOV，浏览器对视频格式的播放支持可能不同。上传时间以服务器时间为准。管理员可重新打印贺卡。

## 构建与推送到阿里云

构建机器可使用 Docker（需要 buildx）或 Podman，服务器使用 Docker 拉取运行。先在阿里云创建镜像仓库，准备仓库域名和命名空间。以下以 `v1.0.0` 为示例标签，部署时请使用实际发布的标签：

```bash
read -rp 'ACR 仓库域名（不含 https://）：' ACR_REGISTRY
read -rp 'ACR 命名空间：' ACR_NAMESPACE
export ACR_REGISTRY ACR_NAMESPACE
# 按本机安装的工具选择其一，登录时交互输入凭证：
podman login "$ACR_REGISTRY"
# docker login "$ACR_REGISTRY"
./deploy/push-acr.sh v1.0.0
```

默认仓库名称为 `camp-gallery`，可用 `IMAGE_NAME` 指定。若构建机器已有经过验证的镜像，设置 `SOURCE_IMAGE=localhost/camp-gallery:v1.0.0` 后运行脚本，将直接打标签并推送，不重复构建。镜像是否已发布，以推送命令成功返回及远端校验结果为准。

## 在服务器部署

准备一台已安装 Docker Engine 和 Compose 插件的 Linux amd64 服务器。复制 `docker-compose.yml` 和 `.env.example` 到服务器的固定部署目录，然后执行：

```bash
cp .env.example .env
chmod 600 .env
# 使用编辑器填写 .env：
# IMAGE：实际 ACR 完整镜像地址及已发布的标签
# SECRET_KEY：随机长字符串（可用 openssl rand -hex 32 生成）
# ADMIN_PASSWORD：自行设置的管理员密码
# PUBLIC_BASE_URL：孩子手机实际访问的域名或服务器地址，含协议及必要的端口
# PORT：默认 8000
# UPLOAD_DEADLINE：2026-11-19T23:59:59+08:00
read -rp 'ACR 仓库域名（不含 https://）：' ACR_REGISTRY
docker login "$ACR_REGISTRY"
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=100 camp
```

首次部署后访问所配置网站的 `/admin`，用 `.env` 中的密码登录，按班级导入学号、姓名名单，并打印专属二维码标签及贺卡。先用手机扫描一张贺卡、上传作品，再导出核对。域名应在印卡前确定；已有纸质二维码内的地址不会自动改变。

若通过服务器 IP 加 8000 端口访问，需在云服务器安全组和系统防火墙放通 TCP 8000；若配置 HTTPS 反向代理，开放 443 并让代理转发到本机 8000，同时允许至少 200 MB 的请求和视频上传所需超时。

更新时修改 `.env` 中的镜像标签，再执行 `docker compose pull && docker compose up -d`。保持部署目录和项目名不变，继续使用同一个数据卷。保留旧标签可回退程序；升级前备份数据目录。

本地 `data/` 不包含在镜像中。若需要迁移已有学生和作品，先停止旧服务，对整个 `data/` 做一致备份，再恢复到服务器的 `camp-data` 卷中，并确保容器内 app 用户可读写。单纯拉取镜像会启动一个空的新站点。
