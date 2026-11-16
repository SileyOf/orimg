# v0.5.2

开源与 Docker Hub 发布版。数据库结构仍为 9，没有新增迁移，与 v0.5.0 / v0.5.1 及 v0.4.7 / v0.4.8 二进制可以互相切换。

## 从 v0.5.0 / v0.5.1 容器升级

镜像名从本地的 `origin:X` 改为 Docker Hub 的 `yuki955/orimg:X`。在 Compose `.env` 中设置：

```sh
ORIGIN_IMAGE=yuki955/orimg:0.5.2
```

然后 `docker compose pull && docker compose up -d`。数据卷、安装配置和密钥不变。

新的 `compose.yaml` 用 `pull_policy: missing` 取代了 `never`：本机已有该镜像（包括离线包载入的）时直接使用，否则从 Docker Hub 拉取。

## 从二进制旧站迁入

见 [迁移指南](MIGRATE_FROM_BINARY.md)。

## 行为变化

- 镜像不再设置 `INSTALL_CONFIG`、`UPLOAD_DIR`、`CACHE_DIR`、`ASSET_DIR` 环境变量。默认值相同（相对 `/app` 的 `data/...`），已安装的站点读取 `installation.json` 中保存的路径，不受影响。若你曾在 Compose 中显式设置这些变量，设置仍然有效。
- 未保存过站点名称或邮件发件人名称的新站点，默认显示 “Origin”。
- `mysqldump_path` 指向容器内不存在的路径时，自动使用镜像内的 `/usr/bin/mysqldump`。
