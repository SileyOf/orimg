# v0.5.0

首版 Docker 发行提供 `origin:0.5.0` 的 amd64 / arm64 离线部署包。切换容器请遵循 [Docker 指南](DOCKER.md)，不是直接覆盖旧程序目录。

本版数据库结构仍为 9，没有新增迁移，保留 v0.4.8 的原加密密钥兼容逻辑。不要修改原 JWT_SECRET；配置、签名密钥、原图、备份与站点资源应单独备份并持久化。

应用新增 `-version` 与 `-healthcheck` 运维命令；健康检查不读取数据库凭据或触发安装。MySQL 逻辑备份使用 `--no-tablespaces` 避免额外 PROCESS 权限，使用 `--set-gtid-purged=OFF` 避免备份导入时修改全局 GTID 状态；它是应用数据库逻辑备份，不是复制集重建或全实例物理备份。
