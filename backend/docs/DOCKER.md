# Origin v0.5.1 Docker 部署

当前镜像名 `origin:0.5.1`（首个容器发行版为 v0.5.0）。提供 Linux x86_64 (`amd64`) 和 ARM64 离线部署包，尚未发布到公共镜像仓库。应用镜像已包含 Go 程序、前端、CA 证书、时区数据及 Oracle MySQL 8.4 的 `mysqldump` 和所需运行库，不需要在 VPS 安装 Go 或 MySQL 客户端。

数据库是独立服务，不和应用放在同一个容器。可以继续使用已有 MySQL，也可以选择附带的 Compose 数据库方案。新建数据库方案首次需要下载固定版本的 MySQL 镜像；应用离线包不包含数据库服务端镜像或任何业务数据。

## 1. 加载镜像

先用 `uname -m` 确认 VPS 架构：`x86_64` 对应 amd64，`aarch64` 对应 arm64。将对应压缩包放到 VPS 并解压，在解压目录执行：

```sh
sha256sum -c SHA256SUMS
docker load -i origin-image.tar
docker run --rm origin:0.5.1 -version
cp .env.example .env
docker compose up -d
docker compose logs origin
```

最后一条命令会显示一次性安装码。`.env.example` 是 Compose 参数模板，不是旧应用配置，**不得覆盖旧生产 `.env`**。只加载所需架构的包即可。镜像离线加载后，Compose 不会自动去公共仓库拉取同名镜像。

默认只监听 VPS 的 `127.0.0.1:8080`，避免明文安装页暴露公网。使用原 Nginx/Caddy 为域名配置 HTTPS，反代到该地址。访问 `https://你的域名/install` 完成数据库和管理员设置。

安装接口要求 HTTPS 或容器内部回环请求。Docker 端口映射后的来源通常是网桥地址，不等同容器内回环，因此直接 HTTP 访问宿主机 8080 或 SSH 端口转发不自动绕过 HTTPS 检查。

## 2. 反向代理

将 `.env` 中的 `TRUSTED_PROXIES` 设置为容器实际看到的代理 IP 或精确 CIDR，不要信任整个互联网或全部私有网段。宿主机 Nginx 通过发布端口访问时，通常是该 Compose 网络的网关地址；可以用 `docker network inspect origin_default` 核对。调整网络后也需复核。

Nginx 的 HTTPS 站点内可使用：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $remote_addr;
    client_max_body_size 210m;
    proxy_read_timeout 300s;
}
```

代理必须覆盖转发头，不应直接信任客户端提交的头。如果 Nginx 自身也在容器中，应让它加入同一受控网络并反代到 `origin:8080`，不要在该容器内使用 `127.0.0.1:8080`。容器镜像内没有 Shell，排障使用 `docker compose logs`、`docker inspect` 或执行 `/app/zhuapic -healthcheck`。

## 3. 选择数据库

### 已有外部数据库

仅运行 `compose.yaml`，在安装页填写数据库主机、库名及账号。远程数据库建议 TLS 并验证证书。旧库接入需要原管理员密码及原 `JWT_SECRET`，不会创建第二个管理员或重设账号。先备份、停止旧站点及其后台任务，确认图片目录已挂载，再进行接入。

容器里的 `127.0.0.1` 指向应用容器自身，不是 VPS 的 MySQL。宿主机数据库可按示例添加 `host.docker.internal:host-gateway` 并使用该主机名；MySQL 必须监听可达接口，且防火墙和账号仅允许所需来源，不要把 3306 开放给全网。

### 新建独立 MySQL

在 Compose `.env` 中填写两个不同的强随机密码 `MYSQL_PASSWORD`、`MYSQL_ROOT_PASSWORD`，然后运行：

```sh
docker compose -f compose.yaml -f compose.mysql.yaml up -d
```

安装页填主机 `db`、端口 `3306`、库名和用户名均为 `origin`，密码使用 `MYSQL_PASSWORD`。该方案不发布数据库端口；应用与数据库在同一专用网络，可在安装页选择不使用数据库 TLS。账号创建只在 MySQL 数据卷首次为空时发生，后续修改 Compose 密码不会自动修改已有库账号。

这是**新站点方案**，不能代替旧库迁移。不要给生产旧库直接套用 MySQL 容器的数据目录，也不要用应用新版顺带升级现有数据库服务端版本。

## 4. 从 v0.4.x 二进制部署切换

建议先在隔离环境演练，再安排短暂停机窗口。v0.5.0 / v0.5.1 均没有新增数据库表或字段，结构版本仍为 9；从更早版本接入可能执行之前缺失的迁移。

1. 停止旧应用及后台任务，备份数据库、原 `.env` 或 `installation.json`、`installation.json.session-key`、所有原图/副本/背景目录。数据库备份不包含这些文件。
2. 不要运行全新安装覆盖旧账号。两种配置方式选择一种：复制已有安装文件和完整数据目录，或复制原应用 `.env` 为独立的 `legacy.env`，配合 `compose.legacy.yaml.example`。
3. 将示例复制成 `compose.legacy.yaml`，把备份出的完整数据目录放在同目录的 `data/`。保留原 `JWT_SECRET` 和签名密钥，不重新生成。只在配置副本中调整容器需要的数据库主机地址及文件挂载，不改原生产备份。
4. 镜像以 UID/GID `10001:10001` 运行。为**新复制的数据目录**设置该 UID/GID 的读写权限，例如 `chown -R 10001:10001 ./data`。不要对原生产目录、整块 NAS 或宿主机根目录递归修改权限。安装配置和签名密钥应为 0600。
5. 在数据库中配置的相对 `data/...` 路径会相对 `/app` 解析。自定义绝对路径、NAS 和独立磁盘必须挂载到容器中相同的路径。环境变量不会覆盖旧库中已保存的路径；不要仅复制 `.env` 就认为图片已迁入。旧 `static/assets/site-*` 自定义 Logo/背景应复制到 `data/site-assets/`，原 URL 保持不变。
6. 如果保留 `installation.json`，数据库地址等以该文件为准，`DB_HOST` 环境变量不会覆盖它。需要调整时只修改停服后的配置副本。保留自定义 `INSTALL_CONFIG` 所对应的同名 `.session-key`；默认位置是 `/app/data/installation.json.session-key`。
7. 使用下列命令启动，此示例强制暂停后台任务：

```sh
docker compose -f compose.yaml -f compose.legacy.yaml up -d
docker compose -f compose.yaml -f compose.legacy.yaml logs origin
```

确认 `/api/ready` 返回 200、账号可登录、历史图片与原设置可用后，再将覆盖文件中的 `DISABLE_BACKGROUND_WORK` 改为 `"false"` 并重建应用容器。避免旧、新部署同时发信或同步。若旧设置中的 `mysqldump_path` 是宿主机自定义绝对路径，需在后台明确改为容器内 `/usr/bin/mysqldump`；不会自动改写你的数据库设置。

## 5. 持久化、更新与回退

- 新安装默认命名卷 `origin_origin-data` 挂载到 `/app/data`，包括安装配置、签名密钥、图片、缓存及自定义站点资源。Compose 项目名改变会使用不同的默认卷，升级时保持项目名与挂载不变。
- MySQL 方案另有 `origin_mysql-data`。切勿使用 `docker compose down -v` 做普通更新，该命令会删除数据卷。
- 应用根文件系统只读，`/tmp` 是有容量上限的临时空间，移除容器会丢失，不能存放长期图片。自定义存储路径必须额外挂载。
- 更新时备份数据，加载新镜像，调整 `ORIGIN_IMAGE`，再 `docker compose up -d`。数据不在镜像里，不能只备份镜像。
- 本版可停服后回退 v0.4.8 原二进制及原静态资源，继续使用备份的原配置与数据。不要同时运行两套工作器。对后续有数据库迁移的版本，不能假设换镜像即可回退。
- Docker 健康检查是存活检查，安装前也可 healthy。`/api/ready` 才表示已安装且数据库可连接；未安装或数据库故障时返回 503。启动失败应先看日志，不要删数据卷重试。

## 6. 源码构建与测试

在 `backend/` 目录执行：

```sh
docker buildx build --platform linux/amd64,linux/arm64 --tag origin:0.5.1 --provenance=false --load .
GO_BIN=/path/to/go node docker/smoke.cjs
node release/docker-package.cjs
```

Buildx 多架构镜像加载需要支持多架构的本地镜像存储，参见 [Docker 官方说明](https://docs.docker.com/build/building/multi-platform/)。可在旧 Docker 上分别单架构构建。默认构建依赖固定摘要的 Docker 官方镜像在 AWS Public ECR 的副本，不需要生产凭据；可通过 `GO_IMAGE`、`MYSQL_IMAGE` 构建参数切换自己审核过的镜像来源。

`docker/smoke.cjs` 只新建带唯一名称的内部网络、临时数据库和数据卷，关闭后台任务，验证两种架构的安装、上传、数据库备份及容器重建。测试完成后只删除自己创建的资源，不接触生产环境。应用镜像不会包含测试探针。
