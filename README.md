# DD@Home · Go

用一个原生小程序给 DD@Home 贡献空闲资源。按 [DDatHome-nodejs](https://github.com/dd-center/DDatHome-nodejs) 的调度、HTTP 采集和直播转发协议重写，使用 **Go 1.27.1**，无需 Node.js。

支持 Windows、Linux、macOS；自动生成并保存昵称和 UUID，断网后自动重连，Ctrl+C 或停止系统服务会取消请求、关闭连接。

## 直接运行

从本次重写分支的 [Actions](https://github.com/dd-center/DDatHome-go/actions/workflows/go.yml) 下载 `DDatHome-go` 构建产物，选择对应系统、架构的程序。历史 Releases / `imlonghao/ddathome-go` 镜像不会自动包含这次重写。

- Windows：把 `.exe` 放在有写入权限的文件夹，双击运行。
- Linux / macOS：给可执行文件添加权限后运行，例如 Linux x64：

```sh
chmod +x DDatHome-go-linux-amd64
./DDatHome-go-linux-amd64
```

第一次运行会在**可执行文件旁边**生成 `config.json`，以后继续使用同一身份。已有配置可以直接沿用；旧配置缺少的新选项会使用默认值。一个配置文件只供一个运行实例使用，不同机器不要共用 UUID。

也可以明确指定配置位置（父目录不存在时自动创建）：

```sh
./DDatHome-go-linux-amd64 --config ./data/config.json
./DDatHome-go-linux-amd64 --config ./data/config.json --check-config
./DDatHome-go-linux-amd64 --version
```

`--check-config` 检查配置并保存缺少的身份后退出，不连接调度服务器。配置写入失败会报错，避免每次重启换一个 UUID。损坏的配置不会被覆盖。

## 可选配置

不需要手工创建配置。要调整时，停止程序、编辑自动生成的文件，再启动。格式见 [config.example.json](config.example.json)，标准 JSON 不允许注释。

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `NickName` | 自动生成 | 节点昵称，例如 `DD-Go-linux-arm64-xxxxxxxx` |
| `UUID` | 自动生成并保存 | 迁移时可使用旧 UUID |
| `Interval` | `1280` | 拉取任务间隔，毫秒；100–60000 |
| `UpstreamURL` | `wss://cluster.vtbs.moe/` | 调度服务器 |
| `HidePlatformInfo` | `false` | 隐藏操作系统、架构、运行时及程序版本 |
| `HTTPConcurrency` | `2` | 同时执行的 HTTP 请求上限；1–16 |
| `HTTPTimeoutMS` | `10000` | 包含 DNS、连接、响应头和响应体的总超时；100–12000 |
| `RoomLimit` | `5` | 直播房间连接上限；`0` 只做 HTTP，最大 100 |
| `StatusAddress` | `127.0.0.1:9465` | 本机状态接口；空字符串关闭，只接受回环 IP |
| `Verbose` | `false` | 记录每个 HTTP 任务；错误始终记录 |

旧 Go 版没有直播采集。新版默认最多 5 个房间；想保留原来只做 HTTP 的资源用量，设置 `RoomLimit: 0`。未提供登录 Cookie 的匿名直播连接可能被 B 站拒绝，新版会记录原因并退避重试。

HTTP 任务只接受 `api.bilibili.com` 和 `api.live.bilibili.com` 的 HTTP(S) 地址，不跟随重定向。普通错误上报兼容服务端的 `code: 233`；B 站非零 API 结果保留原文。HTTP 403/412/429、API -352/-412/-509 会触发 1–15 分钟冷却，冷却跨重连保留。

## 看看是否正常工作

前台运行时打开 [本机状态](http://127.0.0.1:9465/status)。也会每分钟输出一条状态日志。

- `connected`：WebSocket 已连接；`ready`：服务端 `online` 业务查询也正常。
- `valid`：本地收到 HTTP 成功、API `code: 0` 并已提交的数量，**不是服务端确认数**。
- `failed` / `lastFailure`：请求失败原因；`cooldownUntil`：限流冷却截止时间。
- `rooms` / `live`：已分配房间 / 已认证连接；`relayFailures` / `lastRelayFailure`：直播错误。
- `/healthz`：调度是否就绪；`/livez`：进程是否能响应。

没有任务不等于掉线。新版独立检查 WebSocket pong 和业务查询，避免空闲时不断重连。直播转发包含 LIVE、PREPARING、ROUND、heartbeat、ROOM_CHANGE、DANMU_MSG、SEND_GIFT 和 GUARD_BUY，字段与去重 token 对齐 Node.js 版。

## 系统服务

Linux 需要 root，Windows 需要管理员终端。先把程序放到长期保留的路径，再安装：

```sh
sudo ./DDatHome-go-linux-amd64 install --config /var/lib/ddathome/config.json
sudo ./DDatHome-go-linux-amd64 start
sudo ./DDatHome-go-linux-amd64 stop
sudo ./DDatHome-go-linux-amd64 restart
sudo ./DDatHome-go-linux-amd64 uninstall
```

Windows 使用相同的 `install` / `start` / `stop` / `restart` / `uninstall` 命令，程序名换成 `.exe`，去掉 `sudo`。状态接口同样可用。安装后不自动启动。

安装时会将**绝对配置路径**和工作目录写入服务定义，修复 [#9](https://github.com/dd-center/DDatHome-go/issues/9)。旧服务需要先停止、卸载后重新安装，才能更新启动参数；先备份旧 `config.json`（旧 Linux 服务可能写到了 `/config.json`），再把它复制到新路径。不要同时运行新旧实例。

Linux 上可通过 `systemctl status DDatHome-go`、`journalctl -u DDatHome-go -f` 查看服务状态和日志。Windows 系统服务的终端输出不可见，使用本机状态接口；排错时先停止服务，再以前台方式运行同一配置。

## Docker

在包含本次重写的仓库目录运行：

```sh
docker compose up -d --build
docker compose logs -f --tail=30
docker compose stop
```

多阶段构建使用 Go 1.27.1，最终镜像只含静态程序和 CA 证书，以非 root 用户运行。身份保存在命名卷；重建镜像不会丢失。`docker compose down -v` 会删除身份卷。

调整配置可使用 `docker compose cp ddathome:/data/config.json ./config.json` 导出，停止服务后编辑，保留已生成的 UUID 和昵称。然后创建 `compose.override.yaml`：

```yaml
services:
  ddathome:
    volumes:
      - ./config.json:/data/config.json:ro
```

Linux/macOS 设置 `chmod 644 config.json` 使容器用户可以读取，再执行 `docker compose up -d`。此时配置和身份由宿主机文件保存；只读文件必须已有非空 UUID 和昵称，程序才无需生成并写入。

状态接口在容器的回环地址上，Compose 不发布它，宿主机查看日志即可。默认内存上限 192 MiB、CPU 上限 1，日志轮转最多约 30 MB。这些是资源限制，实际占用取决于任务及直播负载；进程退出由 Docker 重启，客户端内部负责网络恢复。

## 从源码构建 / 测试

安装 [Go 1.27.1 或更新的兼容版本](https://go.dev/dl/)，在仓库目录：

```sh
go mod download
go test -race ./...
go vet ./...
go build -trimpath -ldflags "-s -w" -o dist/ddathome .
./dist/ddathome --config ./data/config.json
```

必须使用 `go build .` 构建整个包，旧的 `go build main.go` 不再适用。Windows 成品用 `-o dist/ddathome.exe`。如果使用 `go run .`，请明确 `--config` 路径，避免配置落入临时构建目录。

`sh tools/build.sh`（或 Windows `tools\build.bat`、Fish `tools/build.fish`）交叉编译 11 个目标；Linux `arm` 为 ARMv7。支持的具体系统最低版本以 Go 1.27 的要求为准，不再承诺旧 Go 1.16 支持的所有系统。

额外验证只访问本机模拟服务：

```sh
DDATHOME_SOAK=2m go test -race -run '^TestFaultSoak$' -count=1 -timeout=3m -v
go test -run '^$' -fuzz FuzzDecodePackets -fuzztime=30s
```

CI 在 Linux AMD64 / ARM64、macOS 和 Windows 上运行竞态测试，并构建全部目标和双架构 Docker 镜像。故障测试覆盖握手卡住、HTTP 头/体超时、断线取消、坏消息、限流、并发上限、直播认证/心跳/压缩帧，以及系统服务配置路径。测试通过不等于已经完成数周生产验收，详见 [验证记录](docs/verification.md)。

协议参考代码的版权声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
