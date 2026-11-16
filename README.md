# Origin

Origin（起源图床）v0.5.0，基于 Go + MySQL，当前站点品牌为 Origin。支持命名存储、多地副本、相册、用户组及邮件账号流程。首版 Docker 提供 x86_64 / ARM64 离线部署包；公共镜像仓库尚未发布。

Docker 部署、旧站点迁入和持久化说明见 [Docker 指南](backend/docs/DOCKER.md)。

## 文档导航

- [Changelog](CHANGELOG.md)：版本变更与本次源码整理记录。
- [部署入口](backend/README_DEPLOY.md)：当前安装、升级与旧站点迁入。
- [开发指南](backend/docs/DEVELOPMENT.md)：源码目录、构建、隔离测试与打包。
- [安全说明](SECURITY.md)、[贡献约定](CONTRIBUTING.md)、[第三方许可](THIRD_PARTY_NOTICES.md)。

本次整理保持应用版本 v0.5.0，仅收录源码与文档，不包含生产配置、用户数据或预编译镜像。项目级开源许可证尚未选定，不能将此包视为已完成公开开源发行。

## 主要功能

- 单一 Classic 界面，支持拖拽、批量选择和粘贴上传、自动/浅色/深色外观及强调色。
- 单图快捷上传、批量上传、真实上传进度、URL/Markdown/HTML/BBCode 复制。
- 邮箱注册、验证和独立找回密码页，HTML/纯文本邮件及可编辑模板。
- 自定义用户组、配额、头像和组级存储选择权限。
- 多个 Local/NAS、S3 兼容存储位置，默认上传位置与自动备份分开管理。
- S3 读取缓存与 30 天默认清理策略。
- 永久 Local + S3 双副本、异步同步、失败退避重试和回源自修复。
- 可恢复的 S3/Local 迁移任务和 SHA-256 完整性校验。
- 7 天回收站，支持恢复与多副本永久删除。
- 存储容量总览和默认 85% 本地磁盘安全线。
- 可选的 MySQL 压缩逻辑备份到 Local/NAS 或 S3。
- 多域名白名单，复制链接采用当前访问域名。
- SVG 安全过滤、动图识别、多尺寸缩略图、暗色模式和图片查看器。

## 存储语义

```text
Local   -> 永久本地原图
S3      -> S3 原图 + 可过期本地读取缓存
Mirror  -> 永久本地原图 + S3 异步副本
```

Mirror 上传先写本地并返回图片链接，后台随后同步 S3。访问时优先本地；本地副本缺失时从 S3 恢复。迁移任务只补齐目标副本，不自动删除来源。

## 本地运行

```bash
cd backend
go run .
```

首次部署不要设置数据库环境变量。准备好空数据库后，访问安装页并输入启动日志中的一次性安装码：

```text
http://localhost:8080/install
```

安装器保存私有 `data/installation.json`，安装后入口关闭，无默认管理员密码。已有环境变量部署继续支持 `.env`。接入旧库必须保留原密钥、数据库和图片目录，不会重置旧账号。详细步骤见 [安装与旧库接入](backend/docs/INSTALLATION.md)。

新站点默认关闭游客上传和邮箱注册。数据库断开或配置损坏不会自动重新开启安装页。`/api/health` 检查进程存活，`/api/ready` 检查安装与数据库就绪状态。

## 关键 API

- `POST /api/upload`：上传图片。
- `GET /i/:name`：公开图片。
- `GET /t/:size/:name`：缩略图。
- `POST /api/auth/login`：邮箱登录。
- `GET /api/admin/storage`：存储状态和任务。
- `POST /api/admin/storage/jobs`：创建迁移或校验任务。
- `POST /api/admin/storage/backup`：创建 MySQL S3 备份任务。
- `GET /api/admin/trash`：回收站。

生产升级步骤和 aaPanel 文件清单见 [backend/README_DEPLOY.md](backend/README_DEPLOY.md)。
