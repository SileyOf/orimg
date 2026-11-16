# 第三方组件与许可

项目本身尚未选定开源许可证，本文件不是对 Origin 代码或 Origin 品牌素材的授权声明。

| 组件 | 固定版本/位置 | 许可文本 |
| --- | --- | --- |
| github.com/go-sql-driver/mysql | v1.8.1，`backend/go.mod` | [上游许可证副本](licenses/go-sql-driver-mysql-LICENSE) |
| filippo.io/edwards25519 | v1.1.1，`backend/go.mod` | [上游许可证副本](licenses/edwards25519-LICENSE) |
| Lucide 图标 | `backend/static/assets/` | [已有许可文本](backend/static/assets/lucide-LICENSE) |

Go 模块源码不复制进本包，依赖校验由 `go.sum` 保留。许可证副本取自相应固定版本的本地 Go 模块缓存，未改写内容。

Docker 构建依赖与摘要见 `backend/Dockerfile`。运行镜像中的 MySQL 客户端、运行库、证书及时区来自固定的官方基础镜像；收集脚本同时保留其许可目录。本源码包不包含这些二进制。浏览器测试使用的 Playwright、Chrome 不包含在源码包中。

公开分发源码、镜像或品牌资产之前，仍需分别确认项目授权、第三方再分发要求及素材权属。
