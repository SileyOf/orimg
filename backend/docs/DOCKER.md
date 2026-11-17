# Origin Docker 部署

镜像发布在 Docker Hub：`yuki955/orimg`，提供 Linux x86_64 (`amd64`) 和 ARM64，同一标签自动匹配架构。每个版本同时有版本号标签（如 `0.6.0`）和 `latest`；生产环境请固定版本号。镜像包含 Go 程序、前端、CA 证书、时区数据及 Oracle MySQL 8.4 的 `mysqldump` 和所需运行库，不需要在 VPS 安装 Go 或 MySQL 客户端。

数据库是独立服务，不和应用放在同一个容器。可以继续使用已有 MySQL，也可以选择附带的 Compose 数据库方案。

**已有二进制站点请直接看 [迁移指南](MIGRATION.md)**，不要按下面的全新安装流程操作。不用 Docker 的话见 [二进制部署](BINARY.md)。

## 1. 安装

从仓库 `backend/docker/` 取 `compose.yaml`（可选 `compose.mysql.yaml`）和 `.env.example` 放到服务器同一目录：

```sh
mkdir origin && cd origin
base=https://raw.githubusercontent.com/SileyOf/orimg/main/backend/docker
curl -fsSLO $base/compose.yaml
curl -fsSLO $base/compose.mysql.yaml          # 可选：同时部署 MySQL 容器
curl -fsSL -o .env.example $base/.env.example
```

然后：

```sh
cp .env.example .env          # Compose 参数，不是应用配置
docker compose pull
docker compose up -d
docker compose logs origin
```

最后一条命令会显示一次性安装码。`.env.example` 是 Compose 参数模板，不是旧应用配置，**不得覆盖旧生产 `.env`**。

无法访问 Docker Hub 的服务器可以使用发布页的离线包：`sha256sum -c SHA256SUMS && docker load -i origin-image.tar`，载入的镜像名与 Docker Hub 相同，Compose 不会再去拉取。

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

容器里的 `127.0.0.1` 指向应用容器自身，不是 VPS 的 MySQL。宿主机上的数据库在安装页填写主机名 `host.docker.internal`（`compose.yaml` 已配置）；MySQL 必须监听可达接口，且防火墙和账号仅允许所需来源，不要把 3306 开放给全网。

### 新建独立 MySQL

在 Compose `.env` 中填写两个不同的强随机密码 `MYSQL_PASSWORD`、`MYSQL_ROOT_PASSWORD`，然后运行：

```sh
docker compose -f compose.yaml -f compose.mysql.yaml up -d
```

安装页填主机 `db`、端口 `3306`、库名和用户名均为 `origin`，密码使用 `MYSQL_PASSWORD`。该方案不发布数据库端口；应用与数据库在同一专用网络，可在安装页选择不使用数据库 TLS。账号创建只在 MySQL 数据卷首次为空时发生，后续修改 Compose 密码不会自动修改已有库账号。

这是**新站点方案**，不能代替旧库迁移。不要给生产旧库直接套用 MySQL 容器的数据目录，也不要用应用新版顺带升级现有数据库服务端版本。

## 4. 从 v0.4.x 二进制部署切换

见 [迁移指南](MIGRATION.md)：使用独立的 `compose.legacy.yaml`（主机网络、旧 `.env` 原样只读挂载、后台任务先暂停），并用只读的 `migrate-check.sh` 预检查路径、端口与数据库设置。

## 5. 持久化、更新与回退

- 新安装默认命名卷 `origin_origin-data` 挂载到 `/app/data`，包括安装配置、签名密钥、图片、缓存及自定义站点资源。Compose 项目名改变会使用不同的默认卷，升级时保持项目名与挂载不变。
- MySQL 方案另有 `origin_mysql-data`。切勿使用 `docker compose down -v` 做普通更新，该命令会删除数据卷。
- 应用根文件系统只读，`/tmp` 是有容量上限的临时空间，移除容器会丢失，不能存放长期图片。自定义存储路径必须额外挂载。
- 更新：备份数据，把 `.env` 中的 `ORIGIN_IMAGE` 改成新版本号，执行 `docker compose pull && docker compose up -d`。数据不在镜像里，不能只备份镜像。
- v0.4.7 起数据库结构版本一直为 9，可停服后改用二进制运行（见 [迁移指南](MIGRATION.md)），继续使用原配置与数据。不要同时运行两套工作器。对后续有数据库迁移的版本，不能假设换镜像即可回退。
- Docker 健康检查是存活检查，安装前也可 healthy。`/api/ready` 才表示已安装且数据库可连接；未安装或数据库故障时返回 503。启动失败应先看日志，不要删数据卷重试。

## 6. 源码构建与测试

在 `backend/` 目录执行：

```sh
docker buildx build --platform linux/amd64,linux/arm64 --tag yuki955/orimg:0.6.0 --provenance=false --load .
GO_BIN=/path/to/go node docker/smoke.cjs
node release/docker-package.cjs
```

Buildx 多架构镜像加载需要支持多架构的本地镜像存储，参见 [Docker 官方说明](https://docs.docker.com/build/building/multi-platform/)。可在旧 Docker 上分别单架构构建。默认构建依赖固定摘要的 Docker 官方镜像在 AWS Public ECR 的副本，不需要生产凭据；可通过 `GO_IMAGE`、`MYSQL_IMAGE` 构建参数切换自己审核过的镜像来源。

`docker/smoke.cjs` 只新建带唯一名称的内部网络、临时数据库和数据卷，关闭后台任务，验证两种架构的安装、上传、数据库备份及容器重建。测试完成后只删除自己创建的资源，不接触生产环境。应用镜像不会包含测试探针。
