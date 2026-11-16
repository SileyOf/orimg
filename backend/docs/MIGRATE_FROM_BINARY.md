# 从 v0.4.x 二进制部署迁到 Docker

适用于用 `zhuapic` 二进制 + `.env` 运行（例如在 aaPanel 中作为 Go 项目运行），数据库是同机 MySQL 的旧站点。迁移后数据库不动，图片目录复制一份给容器使用；切换只需要几分钟停机，随时可以切回原二进制。

## 思路

- **数据库原地复用。** v0.4.7 / v0.4.8 的数据库结构版本已是 9，与 v0.5.x 相同，切换不会改表；切回二进制也不需要恢复数据库。更早版本会先自动迁移到 9，这时回退需要用切换前的数据库备份。
- **旧 `.env` 原样使用。** 复制为 `legacy.env`，只读挂载到容器 `/app/.env`，由程序按二进制完全相同的规则解析。不要改成 Compose 的 `env_file`：它会展开密码或密钥里的 `$`。
- **主机网络。** `compose.legacy.yaml` 使用 `network_mode: host`，容器里的 `127.0.0.1` 仍然是服务器本机，所以 `DB_HOST`、MySQL 账号授权（通常只允许 localhost）、`TRUSTED_PROXIES` 和反向代理端口都不需要修改。
- **数据目录复制。** 镜像以 UID/GID `10001` 运行。复制一份 `data/` 并改属主，原目录保持不动作为回退。
- **后台任务先暂停。** 首次启动 `DISABLE_BACKGROUND_WORK=true`，确认无误后再开启，避免与旧进程重复执行副本同步、发信和清理。

## 1. 预检查（不改任何东西）

在服务器上建新目录（下文以 `/opt/origin` 为例），放入 `compose.legacy.yaml`、`.env.example`、`migrate-check.sh`，然后运行：

```sh
sudo bash migrate-check.sh /旧站点目录 /opt/origin
```

脚本只读取旧 `.env`、`data/` 和数据库设置，不显示密码和密钥。按输出处理所有 `[todo]`，重点是：

- 旧监听端口 → 新目录 `.env` 中的 `ORIGIN_PORT`。
- 数据库里保存的绝对路径（`local_upload_dir`、`thumb_dir`、`s3_cache_dir`、本地存储位置的目录）必须在 `compose.legacy.yaml` 中挂载到容器内相同路径。默认的 `data/...` 相对路径无需处理。
- 只在 `static/assets/` 里的自定义 Logo / 背景需要复制到 `data/site-assets/`（v0.4.7 起会自动搬过去，一般已存在）。
- 磁盘剩余空间要够复制一份 `data/`。

## 2. 准备新目录（旧站继续运行）

```sh
cd /opt/origin
cp .env.example .env                      # Compose 参数，不是旧应用配置
# 编辑 .env：ORIGIN_PORT=旧端口，ORIGIN_IMAGE 保持固定版本号
install -m 0600 /旧站点目录/.env legacy.env
rsync -a /旧站点目录/data/ ./data/        # 第一次全量复制，旧站仍在线
docker compose -f compose.legacy.yaml pull
```

## 3. 切换（短暂停机）

1. 在面板中停止旧的 Go 项目 / 进程，确认端口已释放。
2. 备份数据库（面板快照或 `mysqldump`）。
3. 增量同步第一次复制之后新增的文件，并调整权限：

   ```sh
   rsync -a --delete /旧站点目录/data/ ./data/
   chown -R 10001:10001 ./data legacy.env
   ```

4. 启动并查看日志：

   ```sh
   docker compose -f compose.legacy.yaml up -d
   docker compose -f compose.legacy.yaml logs -f origin
   ```

5. 检查：`curl -fsS http://127.0.0.1:旧端口/api/ready`、管理员登录、打开几张旧图片和缩略图、上传一张新图、后台各存储位置状态正常。
6. 都正常后，在 `.env` 中设 `DISABLE_BACKGROUND_WORK=false`，再执行一次 `docker compose -f compose.legacy.yaml up -d`。

反向代理（Nginx）配置不用改，仍然反代到 `127.0.0.1:旧端口`。

## 4. 回退

```sh
docker compose -f compose.legacy.yaml down     # 不要加 -v
```

然后在面板中重新启动旧二进制。数据库结构未变，可直接运行；但容器运行期间上传到 `/opt/origin/data` 的新图片需要同步回旧目录：

```sh
rsync -a /opt/origin/data/uploads/ /旧站点目录/data/uploads/
```

（缩略图、缓存同理；按实际目录同步。）

## 5. 以后的升级

把 `.env` 中的 `ORIGIN_IMAGE` 改成新版本号，然后：

```sh
docker compose -f compose.legacy.yaml pull
docker compose -f compose.legacy.yaml up -d
```

升级前照常备份数据库和 `data/`。每个版本的注意事项见 [Changelog](../../CHANGELOG.md) 与对应的 `docs/UPGRADE_v*.md`。

## 常见问题

- **容器起不来，日志提示数据库连接失败**：确认旧二进制已停、`legacy.env` 原样复制且属主为 10001。主机网络下 `DB_HOST=127.0.0.1` 指向本机 MySQL，与之前一致。
- **登录全部失效**：缺少 `data/installation.json.session-key`（短 JWT_SECRET 的站点才有）。从原目录复制回来并保持 0600；不要修改 `JWT_SECRET`，它还用于解密存储与邮件凭据。
- **数据库备份任务失败**：旧设置里的 `mysqldump_path` 指向主机路径时，v0.5.2 起会自动改用镜像内的 `/usr/bin/mysqldump`；也可在后台改为 `/usr/bin/mysqldump`。
- **想改回桥接网络**：需要让 MySQL 监听 Docker 网桥地址并为该网段授权，再把 `DB_HOST` 改为 `host.docker.internal`。主机网络更省事，安全加固（只读根文件系统、非 root、丢弃全部 capabilities）不受影响。
