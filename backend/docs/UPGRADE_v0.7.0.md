# v0.7.0

这个版本重构了存储、副本、用户组策略和后台任务，数据库结构从 9 升到 10。升级时自动转换，转换后**旧版本无法再读取这个数据库**。

## 升级前

1. **备份数据库**（必须）：`mysqldump --single-transaction --routines --triggers --no-tablespaces -u 用户 -p 库名 > before-0.7.sql`。这是回退到旧版本的唯一途径。
2. 备份 `data/`（至少 `installation.json` 和 `installation.json.session-key`）。
3. 可选：在旧版本后台把「副本异常」处理干净；不处理也可以，转换后会继续重试。

## 升级

- Docker：`.env` 中 `ORIGIN_IMAGE=yuki955/orimg:0.7.0`，然后 `docker compose pull && docker compose up -d`。
- 二进制：停止服务，覆盖 `zhuapic` 与 `static/`，启动。

首次启动会执行转换（图片不多时几秒内完成），日志中没有报错、`/api/ready` 返回 200 即完成。图片文件不会移动或复制。

## 自动转换的内容

| 旧配置 | 转换为 |
| --- | --- |
| 原「Local」兼容存储（站点设置中的本地目录） | 一个本地存储，目录不变 |
| 原「S3」兼容存储（站点设置中的 S3 配置） | 一个 S3 存储；如果从未配置也未使用，则不创建 |
| 命名存储（含 WebDAV） | 同名存储，ID 不变；"已暂停"保持暂停 |
| 站点存储模式 mirror（Local + S3 双副本） | 本地存储为默认上传位置，S3 存储开启自动备份 |
| 默认上传位置 | 不变 |
| 存储的"上传可选范围" + 用户组"允许自选开放存储" | 用户组的"成员可选的存储"：登录用户组获得原先对登录用户开放的存储，游客组获得原先对游客开放的存储 |
| 用户组指定的存储 | 用户组的"默认上传位置" |
| 两张副本表的全部记录 | 统一的副本记录；状态保留（失败的副本会继续自动重试） |
| 数据库备份目标 | 不变；原"兼容 S3"目标转换为对应的 S3 存储 |
| 历史后台任务记录 | 不保留 |

转换后请到后台检查：存储空间（每个存储的开关）、用户组（默认位置与可选存储）、运行设置中的数据库备份目标。

## 处理离线的存储

原来删不掉的离线节点：打开它的编辑窗口 →「移除存储」。如果所有图片在其他存储都有副本，直接确认即可；如果有图片只存在于它，选择「强制移除」，这些图片会进入回收站。

## 时区修复

数据库会话现在使用程序所在的时区（Docker 默认 `TZ=Asia/Shanghai`）。如果你的 MySQL 与程序时区不同，升级后新记录的时间会恢复正常；升级前由数据库生成的时间（例如上传时间）可能仍有固定偏差，不影响图片访问。程序与 MySQL 在同一台机器、同一时区时没有变化。

## 回退

停止 v0.7，恢复升级前的数据库备份，再启动旧版本。v0.7 运行期间新上传的图片文件仍在存储里，但旧版本的数据库中没有它们的记录。

## API 变化

- 上传：新增 `storage` 字段（存储 ID）；`storage_mode=target:ID` 继续可用，`local`/`s3`/`mirror` 视为默认位置。
- 管理接口：`/api/admin/storages`（列表、新增）、`PUT /api/admin/storages/{id}`、`POST /api/admin/storages/{id}/actions`（probe / default / pause / resume / retry / verify）、`GET|POST /api/admin/storages/{id}/removal`、`GET /api/admin/operations`、`PUT /api/admin/operations/{id}`（cancel）。旧的 `/api/admin/storage/targets*`、`/api/admin/storage/jobs*` 已移除。
- 图片对象新增 `storages` 数组；`storage_mode` 变为存储名称组成的显示文字。
