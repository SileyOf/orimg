# Origin 部署入口

当前版本 v0.7.0，数据库结构版本 10。**从 v0.6 及更早版本升级会改写存储相关的表，升级前务必备份数据库**，见 [v0.7.0 升级说明](docs/UPGRADE_v0.7.0.md)。

| 我想… | 看这里 |
| --- | --- |
| 了解准备工作、选择部署方式、完成安装向导 | [安装指南](docs/INSTALLATION.md) |
| 用 Docker Compose 部署或升级 | [Docker 部署](docs/DOCKER.md) |
| 用二进制部署（含宝塔 / systemd）或升级 | [二进制部署](docs/BINARY.md) |
| 从源码编译 | [开发与编译](docs/DEVELOPMENT.md#编译) |
| 二进制 ⇄ Docker 互相迁移、换服务器 | [迁移指南](docs/MIGRATION.md) |
| 配置本地 / S3 / WebDAV 存储与备份 | [存储说明](docs/STORAGE.md) |
| 查看某个版本的升级注意事项 | [Changelog](../CHANGELOG.md)、`docs/UPGRADE_v*.md` |
| v0.4.7 密钥检查导致启动失败 | [v0.4.8 兼容修复](docs/UPGRADE_v0.4.8.md) |

## 升级前必须确认

- 备份数据库、原 `.env` / 安装文件、独立签名密钥、图片及自定义资源。
- 不要替换旧 `JWT_SECRET`；它仍用于解密存储和邮件凭据。新签名密钥不能替代它。
- 先停止旧实例与后台工作器，切勿同时运行两套会发信、同步或清理的实例。
- 换部署方式不是覆盖程序文件。核对数据库地址、原存储绝对路径、挂载与 UID/GID 权限，见 [迁移指南](docs/MIGRATION.md)。
- `DISABLE_BACKGROUND_WORK=true` 仅暂停自动任务，不是数据库只读模式；手动 API 仍可写入。
- `/api/health` 是进程存活检查；`/api/ready` 才反映安装与数据库就绪情况。

生产部署前应在隔离环境验证旧图访问和备份恢复。

此前逐次发布的完整说明保留在 [历史部署记录](README_DEPLOY_HISTORY.md)。历史记录中的路径、默认值和未完成事项只代表相应版本，发生冲突时以当前指南为准。
