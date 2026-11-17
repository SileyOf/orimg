# v0.6.0

数据库结构仍为 9，没有新增迁移。从 v0.4.7 起的任何版本都可以直接升级，也可以直接回退。

## 升级

- **Docker**：把 Compose `.env` 中的 `ORIGIN_IMAGE` 改为 `yuki955/orimg:0.6.0`，执行 `docker compose pull && docker compose up -d`。
- **二进制**：停止服务，用发布包中的 `zhuapic` 和 `static/` 覆盖原文件（不要覆盖 `.env` 与 `data/`），再启动。详见 [二进制部署](BINARY.md#3-升级)。

升级后浏览器如果仍显示旧的后台页面，强制刷新一次（静态文件没有版本号）。

## 新功能

- 后台「存储空间 → 新增存储」可以选择 **WebDAV**，见 [存储说明](STORAGE.md#webdav-示例)。
- 邮件模板支持 `{{site_url}}`、`{{valid_for}}` 变量。已保存的自定义模板保持不变；使用默认模板的站点会自动得到新的样式和说明文字。

## 部署方式

从这个版本起，二进制发布包与 Docker 镜像是同一份构建。两种方式之间迁移见 [迁移指南](MIGRATION.md)。
