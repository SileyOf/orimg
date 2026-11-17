# 开发与编译

## 源码结构

```text
README.md / CHANGELOG.md          项目入口与版本历史
CONTRIBUTING.md / SECURITY.md     贡献约定与安全边界
THIRD_PARTY_NOTICES.md            第三方许可索引
backend/
  main.go / healthcheck.go        启动、退出与运维命令
  config/                        配置、安装检查点与签名密钥
  database/                      数据库初始化、迁移与旧库校验
  installer/                     安装接口及隔离安装测试
  handlers/ routes/ middleware/   HTTP 接口、路由与鉴权
  models/                        业务模型
  storage/                       存储适配、缓存、副本任务与备份
  mailer/                        SMTP、模板与邮件队列
  utils/                         密码、JWT、图片及代理等公共函数
  internal/testsupport/          集成测试的本机数据库保护
  static/                        无构建步骤的 HTML/CSS/JavaScript
  tests/                         Playwright 模拟 API 回归
  docker/                        Compose、迁移检查脚本、运行库收集与容器回归
  deploy/                        systemd 服务示例
  release/                       二进制、镜像、源码打包脚本
  docs/                          当前指南、设计与历史发布记录
```

源码目录暂未改名或迁移 Go module，内部模块名仍为 `zhuapic`，避免纯整理引入业务变化。

## 依赖与本地开发

- Go 依赖由 `go.mod` / `go.sum` 固定；module 的最低声明为 Go 1.23.0。发布构建（镜像和二进制包）固定使用 Go 1.27.1，具体摘要见 Dockerfile。部署前应另行审核工具链和依赖的更新情况。
- 普通运行需要 MySQL；首次启动通过安装向导初始化，无默认管理员密码。
- 前端无需 npm 构建。Node.js 用于 `.cjs` 测试与打包；浏览器测试另需 Playwright 和对应浏览器。目前还没有锁定的 Node 依赖清单，不应声称完全可复现的浏览器工具链。
- MySQL 备份需要兼容的 `mysqldump`；Docker 镜像已包含，本地运行则需自行安装。

```sh
cd backend
go mod download
go run .
```

请在干净的开发目录执行，不要复制生产 `.env` 或生产数据。首次安装见 [安装指南](INSTALLATION.md)。

## 编译

### 编译二进制

需要 Go 1.23 或更新版本（推荐与发布一致的 1.27），不需要 CGO：

```sh
git clone https://github.com/SileyOf/orimg.git
cd orimg/backend
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o zhuapic .
# 交叉编译：GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o zhuapic .
./zhuapic -version
```

运行时程序从工作目录读取 `static/`、`.env` 并写入 `data/`。可以直接在 `backend/` 目录运行，也可以把 `zhuapic` 和 `static/` 一起复制到部署目录，之后的步骤与 [二进制部署](BINARY.md) 相同。

### 构建 Docker 镜像

```sh
cd orimg/backend
docker build -t origin:dev .                  # 当前架构
docker buildx build --platform linux/amd64,linux/arm64 -t origin:dev --load .   # 双架构
```

然后把 Compose `.env` 中的 `ORIGIN_IMAGE` 改为 `origin:dev`。Dockerfile 中的基础镜像固定了摘要，可以用 `--build-arg GO_IMAGE=...`、`--build-arg MYSQL_IMAGE=...` 换成自己审核过的来源。

## 普通测试

```sh
cd backend
env -u ORIMG_TEST_DSN -u ORIMG_INSTALL_TEST_PORT -u ORIMG_ALLOW_DB_TESTS go test -race ./...
go vet ./...
```

默认跳过数据库集成测试。部分测试使用临时本机 HTTP 监听端口，受限沙箱可能需要明确授权。测试通过不等于真实 SMTP、云存储、恢复演练或负载测试通过。

## 数据库与容器测试

- 升级回归：`GO_BIN=/path/to/go node docker/upgrade-smoke.cjs` 用上一版镜像（默认 `yuki955/orimg:0.6.0`）建立含本地、WebDAV、离线 S3 与用户组策略的站点，再原地升级到当前镜像，核对迁移结果、图片访问、移除离线存储和重启幂等。需要本地有 `rclone/rclone:1.68.2` 与 `mysql:8.4` 镜像。
- 推荐 `GO_BIN=/path/to/go node docker/smoke.cjs`：创建内部网络、一次性 MySQL 与卷，关闭后台任务，验证两种架构的完整容器流程，结束后清理自己创建的资源。先按 [Docker 指南](DOCKER.md) 构建镜像。
- `installer/database_integration_test.go` 只接受 `ORIMG_INSTALL_TEST_PORT` 指定的回环端口，要求全新空库 `orimg_install_test`，还会创建 `orimg_install_resume`。测试专用密码写在夹具中，不是部署默认密码。
- 较早的 `ORIMG_TEST_DSN` 测试现已去掉旧服务器地址，必须同时设置 `ORIMG_ALLOW_DB_TESTS=1`，使用字面回环 IP、非特权 TCP 端口和库名 `zhuapictest`。库须预先具备匹配结构及各测试要求的空表；这些测试会写入并清理夹具，不能指向真实数据，不能混用并发共享库。
- 上述保护不能识别“回环端口实际转发到生产库”的情况。禁止使用 SSH 隧道或代理将生产库伪装成本地测试库。

本次源码整理只运行默认本地测试，不运行数据库套件。

## 浏览器测试

`tests/*.cjs` 使用模拟 API，不应换成生产地址。部分脚本使用系统 Chrome，并将截图写入 `/tmp/orimg-*-qa`。邮件回归需要先生成固定的测试邮件预览：

```sh
cd backend
ORIMG_MAIL_PREVIEW=/tmp/orimg-email-preview.html go test ./mailer -run TestMailPreviewArtifact -count=1
node tests/mail-ui.cjs
```

在独立工具目录安装 Playwright 并通过 `NODE_PATH` 提供依赖即可，避免把 `node_modules` 打入源码包。安装向导浏览器测试另需 `ORIMG_QA_BINARY`；可选 `ORIMG_QA_BROWSER_CHANNEL` 指定浏览器通道。浏览器测试不属于本次源码整理的重跑范围。

## 打包

在 `backend/` 下运行：

```sh
node release/source-package.cjs
```

脚本根据代码版本和打包日期生成源码 ZIP、包级 SHA-256 文件，以及包内 `SOURCE_MANIFEST.json` / `SHA256SUMS`。只收集维护中的源码与文档目录，不包含 `.env`、安装/签名密钥、图片数据、数据库导出、缓存、历史 dist 或测试截图。运行前仍应人工审核新增文档和测试夹具，有限规则扫描不等于完整秘密审计。

发布包：先用 `docker buildx build --platform linux/amd64,linux/arm64 --tag yuki955/orimg:X.Y.Z --provenance=false --load .` 构建镜像，再运行：

- `node release/package.cjs`：二进制发布包 `origin-vX.Y.Z-linux-{amd64,arm64}.tar.gz`。程序直接取自上述镜像，保证两种发行方式是同一份构建。
- `node release/docker-package.cjs`：离线镜像包，供无法访问 Docker Hub 的服务器 `docker load`。
