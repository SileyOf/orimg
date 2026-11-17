# Origin 二进制部署

适合不使用 Docker 的服务器，包括宝塔 / aaPanel 面板。发布包从 [GitHub Releases](https://github.com/SileyOf/orimg/releases) 下载，`x86_64` 选 `linux-amd64`，`aarch64` 选 `linux-arm64`。

发布包内容：

```text
zhuapic             程序（静态编译，无需 Go 环境；与 Docker 镜像中的程序相同）
static/             前端页面，必须与程序放在同一工作目录
.env.example        可选的环境变量示例（首次安装不需要）
origin.service      systemd 服务示例
UPGRADE.md          本版升级说明
SHA256SUMS          包内文件校验和
```

程序从**工作目录**读取 `.env`、`static/`，并把安装配置、图片、缓存写到 `data/`。

## 1. 安装

以 `/opt/origin`、专用用户 `origin` 为例：

```sh
sudo useradd --system --home /opt/origin --shell /usr/sbin/nologin origin
sudo mkdir -p /opt/origin
sudo tar -xzf origin-v0.7.0-linux-amd64.tar.gz -C /opt/origin --strip-components=1
cd /opt/origin && sha256sum -c SHA256SUMS
sudo install -d -o origin -g origin -m 0750 /opt/origin/data
```

安装为 systemd 服务并启动：

```sh
sudo cp origin.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now origin
journalctl -u origin -n 50       # 找到 One-time setup code
```

然后按 [安装指南](INSTALLATION.md#3-安装向导) 访问 `/install` 完成设置。服务示例启用了文件系统保护，只允许写 `/opt/origin/data`；如果在后台添加了其他本地存储目录或数据库备份目录，需要在服务文件里为每个目录加一行 `ReadWritePaths=`，再 `systemctl daemon-reload && systemctl restart origin`。

### 宝塔 / aaPanel

1. 在网站目录（例如 `/www/wwwroot/你的域名/`）上传并解压发布包，保持 `zhuapic` 与 `static/` 在同一目录。
2. 「网站 → Go 项目」添加项目：执行文件选 `zhuapic`，运行目录选该目录，端口填 `8080`，运行用户建议选 `www`。
3. 给运行用户 `data/` 目录的写权限，开启反向代理与 SSL，然后访问 `/install`。

### 数据库备份需要 mysqldump

后台的 MySQL 定时备份会调用 `mysqldump`。二进制部署需要服务器上有与 MySQL 版本匹配的客户端（宝塔 MySQL 自带，路径一般为 `/www/server/mysql/bin/mysqldump`），并在后台「存储 → MySQL 逻辑备份」填写其路径。Docker 镜像已内置，不需要这一步。

## 2. 反向代理

Nginx 站点配置（宝塔可在「反向代理」中设置同样的头）：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $remote_addr;
    client_max_body_size 210m;
    proxy_read_timeout 300s;
}
```

然后在 `.env` 中写 `TRUSTED_PROXIES=127.0.0.1/32,::1/128`（反代与程序在同一台机器时），重启服务。不要信任 `0.0.0.0/0` 或整个内网。

## 3. 升级

数据都在 `data/` 和数据库里，升级只替换程序与前端：

```sh
sudo systemctl stop origin
# 先备份数据库与 data/（至少 installation.json、installation.json.session-key）
cd /opt/origin
sudo cp zhuapic zhuapic.bak && sudo cp -a static static.bak
sudo tar -xzf origin-vX.Y.Z-linux-amd64.tar.gz --strip-components=1 \
  --wildcards '*/zhuapic' '*/static/*' '*/UPGRADE.md' '*/SHA256SUMS'
sudo systemctl start origin
curl -fsS http://127.0.0.1:8080/api/ready
```

宝塔用户：在 Go 项目里先停止，上传新包覆盖 `zhuapic` 和 `static/`（不要覆盖 `.env` 与 `data/`），再启动。

升级前阅读包内 `UPGRADE.md`。需要回退时，停止服务，换回 `zhuapic.bak` 与 `static.bak` 后启动；如果新版本做过数据库迁移（升级说明会写明），回退还需要恢复升级前的数据库备份。

## 4. 改用 Docker

二进制与 Docker 运行同一程序、读写同一数据库，可以随时切换，见 [迁移指南](MIGRATION.md)。

## 常见问题

- **访问 `/install` 提示需要 HTTPS**：先配置反向代理和证书，或用 SSH 端口转发 `ssh -L 8080:127.0.0.1:8080 服务器` 后访问 `http://localhost:8080/install`。
- **启动后页面空白或 404**：工作目录里缺少 `static/`，或服务的 `WorkingDirectory` 指向了别处。
- **`permission denied` 写入 data/**：运行用户对 `data/` 没有写权限；不要用 root 运行来绕过。
- **端口被占用**：在 `.env` 中设置 `PORT=其他端口`，并同步修改反向代理。
