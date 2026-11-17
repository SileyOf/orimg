# Origin

**Origin（起源图床）** 是一个自托管图床：Go 单文件程序 + MySQL，前端无需构建。适合个人或小团队在自己的服务器上存放、管理和分享图片。

[![Docker Hub](https://img.shields.io/docker/v/yuki955/orimg?sort=semver&label=docker)](https://hub.docker.com/r/yuki955/orimg)
[![Release](https://img.shields.io/github/v/release/SileyOf/orimg)](https://github.com/SileyOf/orimg/releases)
[![License: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue)](LICENSE)

当前版本 **v0.7.0**，变更见 [Changelog](CHANGELOG.md)。1.0 之前接口和部署方式仍可能调整，升级前请阅读对应版本的升级说明。

## 功能

- **上传**：拖拽、粘贴、批量上传，真实进度；一键复制 URL / Markdown / HTML / BBCode；支持 API Token。
- **存储**：本地 / NAS 目录、S3 兼容（AWS、R2、MinIO、B2 等）、**WebDAV**（Nextcloud、群晖、Alist、rclone 等）可以同时使用。每张图片可以有多份副本：设置默认上传位置、自动备份位置，按用户组开放选择。
- **可靠性**：SHA-256 校验、副本缺失或损坏时自动修复、离线存储自动熔断不影响其他存储、存储迁出后安全移除、7 天回收站、可选的 MySQL 定时备份。
- **用户**：邮箱注册与验证、找回密码、自定义用户组、配额、上传策略和头像。
- **外观**：自动 / 浅色 / 深色主题、强调色、自定义 Logo 与背景，邮件模板可编辑。
- **安全**：首次安装需一次性安装码、无默认密码；SVG 过滤、登录限流与验证码、多域名白名单、只信任显式配置的反向代理。

## 部署

需要 **MySQL 8**（5.7 未测试）和一个 HTTPS 反向代理（Nginx / Caddy 等）。三种方式任选其一，数据和配置互相兼容，之后也可以[互相迁移](backend/docs/MIGRATION.md)。

| 方式 | 适合 | 指南 |
| --- | --- | --- |
| **Docker Compose**（推荐） | 有 Docker 的服务器，升级只需 `pull` | [Docker 部署](backend/docs/DOCKER.md) |
| **二进制** | 宝塔 / aaPanel、systemd，不想装 Docker | [二进制部署](backend/docs/BINARY.md) |
| **源码编译** | 想自己构建或修改代码 | [开发与编译](backend/docs/DEVELOPMENT.md) |

### Docker 快速开始

```sh
mkdir origin && cd origin
curl -fsSLO https://raw.githubusercontent.com/SileyOf/orimg/main/backend/docker/compose.yaml
curl -fsSL -o .env https://raw.githubusercontent.com/SileyOf/orimg/main/backend/docker/.env.example
docker compose up -d
docker compose logs origin        # 日志里有一次性安装码
```

### 二进制快速开始

```sh
# 从 Releases 下载对应架构（x86_64 → amd64，aarch64 → arm64）
tar -xzf origin-v0.7.0-linux-amd64.tar.gz
cd origin-v0.7.0-linux-amd64
./zhuapic                         # 日志里有一次性安装码
```

两种方式默认都监听 `8080` 端口。用反向代理配置好 HTTPS 后访问 `https://你的域名/install`，按向导连接数据库、创建管理员即可。完整步骤（含数据库准备、反向代理、systemd）见 [安装指南](backend/docs/INSTALLATION.md)。

## 文档

| 文档 | 内容 |
| --- | --- |
| [安装指南](backend/docs/INSTALLATION.md) | 部署方式对比、安装向导、接入已有数据库、配置与密钥持久化 |
| [Docker 部署](backend/docs/DOCKER.md) / [二进制部署](backend/docs/BINARY.md) | 各自的安装、升级、回退 |
| [迁移指南](backend/docs/MIGRATION.md) | 二进制 ⇄ Docker 双向迁移、跨服务器搬迁 |
| [存储说明](backend/docs/STORAGE.md) | 本地 / S3 / WebDAV、默认位置、自动备份、数据库备份 |
| [开发与编译](backend/docs/DEVELOPMENT.md) | 源码结构、编译、测试、打包 |
| [Changelog](CHANGELOG.md) | 版本记录与各版升级说明 |

## 常用 API

上传需要登录会话或 API Token（个人中心 → API Token）：

```sh
curl -H "Authorization: Bearer zpic_xxx" -F file=@photo.jpg https://你的域名/api/upload
```

返回 JSON 中的 `url`、`markdown`、`html` 可直接使用。其他接口：

- `GET /i/:name`：原图；`GET /t/{small|medium|large}/:name`：缩略图。
- `GET /api/health`：进程存活；`GET /api/ready`：已安装且数据库可用。
- 管理接口（`/api/admin/*`）需要管理员登录，详见源码 `backend/routes/routes.go`。

## 参与

欢迎通过 [Issues](https://github.com/SileyOf/orimg/issues) 反馈问题或提交 Pull Request，约定见 [CONTRIBUTING.md](CONTRIBUTING.md)。安全问题请按 [SECURITY.md](SECURITY.md) 私下报告。

## 许可证

Origin 以 [GNU AGPL-3.0](LICENSE) 授权：可以自由使用、修改和部署；如果修改后以网络服务形式提供给他人使用，需要向用户提供对应源码。第三方组件许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
