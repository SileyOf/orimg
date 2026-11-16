# 第三方组件与许可

Origin 本身以 [AGPL-3.0](LICENSE) 授权。以下第三方组件按各自许可证分发。

| 组件 | 固定版本/位置 | 许可证 | 许可文本 |
| --- | --- | --- | --- |
| github.com/go-sql-driver/mysql | v1.8.1，`backend/go.mod` | MPL-2.0 | [上游许可证副本](licenses/go-sql-driver-mysql-LICENSE) |
| filippo.io/edwards25519 | v1.1.1，`backend/go.mod` | BSD-3-Clause | [上游许可证副本](licenses/edwards25519-LICENSE) |
| Lucide 图标 | `backend/static/assets/` | ISC | [许可文本](backend/static/assets/lucide-LICENSE) |

Go 模块源码不复制进仓库，依赖校验由 `go.sum` 保留。许可证副本取自相应固定版本的 Go 模块，未改写内容。

## Docker 镜像中的组件

`yuki955/orimg` 镜像除 Origin 程序外，还包含从固定摘要的官方 `mysql:8.4` 镜像复制的组件（见 `backend/Dockerfile` 与 `backend/docker/collect-runtime.sh`）：

- Oracle MySQL Community 8.4（当前 8.4.11）的 `mysqldump` 客户端，GPL-2.0 授权。对应源码可从 <https://github.com/mysql/mysql-server>（标签 `mysql-8.4.11`）或 <https://dev.mysql.com/downloads/mysql/>（Source Code）获取；许可文本随镜像保存在 `/usr/share/doc/mysql-community-server-minimal/LICENSE`。
- 其运行所需的系统库、CA 证书（`/etc/pki`）与时区数据，来自同一基础镜像的 Oracle Linux 软件包；各自许可证保存在镜像的 `/usr/share/licenses/`。

浏览器测试使用的 Playwright、Chrome 不包含在仓库或镜像中。
