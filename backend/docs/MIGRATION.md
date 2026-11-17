# 迁移指南：二进制 ⇄ Docker、换服务器

二进制和 Docker 运行的是同一个程序，数据库结构和 `data/` 目录布局完全一致。迁移的实质只有三件事：

1. **数据库**：同一台服务器上原地复用，不需要导出导入；换服务器时用 `mysqldump` 导出再导入。
2. **`data/` 目录**：图片原文件、缩略图、自定义 Logo/背景、安装配置 `installation.json` 和签名密钥 `installation.json.session-key`。缓存 `data/cache` 可以不带。
3. **配置**：站点用哪种方式保存数据库连接，决定了迁移时带哪个文件：

| 配置方式 | 判断方法 | 迁移时带上 |
| --- | --- | --- |
| 环境变量（旧站常见） | 工作目录有 `.env`，里面有 `DB_HOST`、`JWT_SECRET` | `.env` 原文件（**不要修改 `JWT_SECRET`**） |
| 安装向导 | `data/installation.json` 存在 | `data/` 里已经包含 |

**通用规则**

- 迁移前停止旧实例，**不要让两套程序同时运行**：两边都会执行副本同步、发信和清理。
- 先备份数据库和 `data/`。
- 新实例首次启动时设置 `DISABLE_BACKGROUND_WORK=true`，检查无误后再开启。
- 数据库里保存的绝对路径（自定义本地存储目录、NAS 挂载、`thumb_dir` 等）在新环境必须仍然有效。Docker 中需要把这些路径挂载到容器内的相同位置。

## 一、二进制 → Docker（同一台服务器）

### 1. 预检查（只读）

在服务器上新建目录（下文以 `/opt/origin-docker` 为例），放入仓库 `backend/docker/` 下的 `compose.legacy.yaml`、`.env.example`、`migrate-check.sh`，运行：

```sh
sudo bash migrate-check.sh /旧站点目录 /opt/origin-docker
```

脚本只读取旧 `.env`、`data/` 和数据库设置，不显示密码和密钥，不修改任何东西。按输出处理所有 `[todo]`：

- 旧监听端口 → 新目录 `.env` 中的 `ORIGIN_PORT`。
- 数据库里保存的绝对路径 → 在 `compose.legacy.yaml` 的 `volumes` 里挂载到相同路径。默认的 `data/...` 相对路径无需处理。
- 磁盘剩余空间是否够复制一份 `data/`。

### 2. 为什么用 `compose.legacy.yaml`

- **主机网络**：容器里的 `127.0.0.1` 仍然是服务器本机，所以数据库地址、MySQL 账号授权（通常只允许 localhost）、`TRUSTED_PROXIES` 和反向代理端口都不用改。
- **旧 `.env` 原样只读挂载为 `/app/.env`**：由程序按二进制完全相同的规则解析。不要改用 Compose 的 `env_file`，它会展开密码或密钥里的 `$`。
- **后台任务默认暂停**（`DISABLE_BACKGROUND_WORK=true`）。

安装向导方式的站点没有 `.env`：创建一个空文件 `touch legacy.env` 即可，配置会从 `data/installation.json` 读取。

### 3. 准备（旧站继续运行）

```sh
cd /opt/origin-docker
cp .env.example .env                      # Compose 参数：ORIGIN_PORT=旧端口，ORIGIN_IMAGE 固定版本号
install -m 0600 /旧站点目录/.env legacy.env   # 安装向导站点改为：touch legacy.env
rsync -a /旧站点目录/data/ ./data/        # 先全量复制一次
docker compose -f compose.legacy.yaml pull
```

### 4. 切换（停机几分钟）

```sh
# 1) 停止旧程序（systemctl stop origin，或在面板中停止 Go 项目），并备份数据库
# 2) 同步第一次复制之后新增的文件，调整属主（镜像以 UID/GID 10001 运行）
rsync -a --delete /旧站点目录/data/ ./data/
chown -R 10001:10001 ./data legacy.env
# 3) 启动
docker compose -f compose.legacy.yaml up -d
docker compose -f compose.legacy.yaml logs -f origin
```

检查：`curl -fsS http://127.0.0.1:端口/api/ready`、管理员登录、打开旧图片和缩略图、上传新图片、后台存储状态。全部正常后在 `.env` 中设置 `DISABLE_BACKGROUND_WORK=false`，再执行一次 `docker compose -f compose.legacy.yaml up -d`。

反向代理配置不用改。数据库备份如果之前填的是主机上的 `mysqldump` 路径，容器里会自动改用镜像自带的 `/usr/bin/mysqldump`。

### 5. 回退到二进制

```sh
docker compose -f compose.legacy.yaml down          # 不要加 -v
rsync -a /opt/origin-docker/data/ /旧站点目录/data/   # 带回容器运行期间的新图片
```

再启动旧程序即可。两边版本相同时数据库无需恢复；如果新容器比旧程序版本新、并且做过数据库迁移（例如从 v0.4.x / v0.6 二进制迁到 v0.7 Docker，结构 9 → 10），旧程序无法读取新结构，必须先恢复切换前备份的数据库，再启动旧程序。

**建议**：先在旧版本的范围内完成迁移（例如 v0.6 二进制 → v0.6 Docker），确认正常后再单独升级版本。这样每一步都可以独立回退。

## 二、Docker → 二进制（同一台服务器）

### 1. 准备二进制目录

按 [二进制部署](BINARY.md#1-安装) 解压发布包到 `/opt/origin`（**先不要启动服务**）。

### 2. 停止容器并取出数据

```sh
cd 你的 compose 目录
docker compose stop origin                  # compose.legacy.yaml 部署请加 -f compose.legacy.yaml
container=$(docker compose ps -a -q origin)
sudo docker cp "$container":/app/data/. /opt/origin/data/   # 命名卷与目录挂载都适用
sudo chown -R origin:origin /opt/origin/data
```

使用 `compose.legacy.yaml`（`./data` 目录挂载）时也可以直接 `rsync -a ./data/ /opt/origin/data/`。

### 3. 配置

- **环境变量站点**（`compose.legacy.yaml`）：`sudo install -o origin -m 0600 legacy.env /opt/origin/.env`。
- **安装向导站点**：配置已经在 `data/installation.json` 中。检查两处：
  1. 数据库地址 `"DBHost"`：如果是 `db`（附带的 MySQL 容器），二进制无法解析这个名字。可以把 MySQL 容器端口发布到 `127.0.0.1:3306` 并把 `DBHost` 改为 `127.0.0.1`，或者把数据库导出导入到主机上的 MySQL（见第三节）。
  2. v0.5.0 / v0.5.1 镜像安装的站点会把 `/app/data/...` 绝对路径写入配置和数据库。检查：

     ```sh
     sudo grep -o '"/app/data[^"]*"' /opt/origin/data/installation.json
     mysql -e "SELECT \`key\`,value FROM site_settings WHERE value LIKE '/app/%'; SELECT id,name,config_json FROM storage_targets WHERE config_json LIKE '%/app/%'" 你的库名
     ```

     有结果时，最简单的做法是让同一路径在主机上也存在：`sudo mkdir -p /app && sudo ln -s /opt/origin/data /app/data`。

### 4. 启动并检查

先在 `/opt/origin/.env` 中加入（或把已有的一行改成）`DISABLE_BACKGROUND_WORK=true`。同一变量出现多次时只有第一行生效，所以不要只在文件末尾追加。然后：

```sh
sudo systemctl enable --now origin
curl -fsS http://127.0.0.1:8080/api/ready
```

确认登录、旧图片、上传正常后，把这一行改为 `false` 并 `systemctl restart origin`。反向代理改为指向二进制监听的端口。

二进制部署没有内置 `mysqldump`；如果使用数据库备份，安装 MySQL 客户端并在后台填写路径。

## 三、换服务器

在新服务器上按目标方式（Docker 或二进制）准备好程序，但先不要启动：

```sh
# 旧服务器：停止程序后导出数据库
mysqldump --single-transaction --routines --triggers --no-tablespaces -u 用户 -p 库名 | gzip > origin.sql.gz
# 新服务器：建库建账号（见安装指南），导入
gunzip -c origin.sql.gz | mysql -u 用户 -p 库名
# 复制 data/（以及 .env，如有）
rsync -a 旧服务器:/opt/origin/data/ /opt/origin/data/
```

然后按前两节检查数据库地址、绝对路径、属主，设置 `DISABLE_BACKGROUND_WORK=true` 启动，验证后开启后台任务并切换 DNS。S3 / WebDAV 上的图片不需要搬，新服务器会按需回源到本地缓存。

## 四、版本升级

升级与迁移互相独立，可以先迁移再升级，也可以反过来，但不要在同一步里同时做两件事。

- Docker：修改 `.env` 中的 `ORIGIN_IMAGE` 版本号 → `docker compose pull && docker compose up -d`，见 [Docker 部署](DOCKER.md#5-持久化更新与回退)。
- 二进制：替换 `zhuapic` 与 `static/` 后重启，见 [二进制部署](BINARY.md#3-升级)。

每个版本的注意事项见 [Changelog](../../CHANGELOG.md) 与 `docs/UPGRADE_v*.md`。没有数据库迁移的版本之间可以直接互相切换；有迁移时，回退需要恢复升级前的数据库备份。

## 常见问题

- **容器起不来，提示数据库连接失败**：确认旧程序已停止、`legacy.env` 原样复制且属主为 10001。主机网络下 `DB_HOST=127.0.0.1` 指向本机 MySQL，与之前一致。
- **所有登录都失效**：缺少 `data/installation.json.session-key`（短 `JWT_SECRET` 的旧站才有）。从原目录复制回来并保持 0600。不要修改 `JWT_SECRET`，它还用于解密存储和邮件凭据。
- **旧图片 404**：数据库中保存的绝对路径在新环境不存在。用 `migrate-check.sh` 或上面的 SQL 找出路径，挂载或建立软链接。
- **想让 Docker 改用桥接网络**：需要让 MySQL 监听 Docker 网桥地址、为该网段授权，并把 `DB_HOST` 改为 `host.docker.internal`。主机网络更省事，只读根文件系统、非 root、丢弃全部 capabilities 等加固不受影响。
