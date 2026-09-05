# XTU Connect

湘潭大学（XTU）WebVPN 客户端的第三方开源实现。无需安装深信服 EasyConnect 客户端，在本地提供 SOCKS5 / HTTP 代理访问校内网络资源。

**默认不创建虚拟网卡、不修改路由表、不接管系统 DNS**，因此可以与 Clash / Surge / sing-box 等代理软件（包括 TUN 模式）完美共存，彻底解决 EasyConnect 与 Clash TUN 冲突导致无法上网的问题。

> 本项目基于 [zju-connect](https://github.com/Mythologyli/zju-connect)（浙江大学）裁剪适配而来，协议实现源自 [EasierConnect](https://github.com/TeamSUEP/EasierConnect) 的逆向研究成果。感谢原作者们的开源精神。
> 协议探测已确认 `vpn.xtu.edu.cn` 为深信服 EasyConnect 经典版服务端（M7.6.8），登录链路验证通过。

## 功能特性

- 深信服 EasyConnect 协议登录（账号密码 / TOTP / 证书 / TWFID 免密）
- 本地 SOCKS5 代理（默认 `127.0.0.1:1080`），支持用户名密码认证
- 本地 HTTP 代理（默认 `127.0.0.1:1081`）
- **桌面版连接后自动配置系统 PAC 分流：浏览器零配置直访校内网，断开自动还原；与 Clash TUN / 系统代理模式共存（系统代理被占用时自动链式接管）**
- 隧道侧远程 DNS：内网域名（如 `lib.xtu.edu.cn`）通过校内 DNS 解析
- 自动保活与断线重连
- TCP/UDP 端口转发：把内网服务映射到本地端口
- 服务端资源列表自动解析，按校内规则分流（内网走 VPN，其余直连）
- 支持 Windows / macOS / Linux（含 ARM）
- 命令行版可选 TUN 模式与 TCP 隧道模式（实验性，桌面版未启用）

## 快速开始

### 1. 获取程序

从 Release 页面下载对应平台的可执行文件，或自行编译（见下文）。

两种形态：

- **桌面版**（推荐普通用户）：macOS 下载 `XTU-Connect_<版本>_macos-arm64.zip`（Intel 机选 amd64），解压得到 `XTU-Connect.app` 拖入「应用程序」，双击运行后常驻菜单栏；Windows 下载 `xtu-connect-desktop_*.exe`（需与同目录的 `xtu-connect_*.exe` 配套，重命名为 `xtu-connect.exe`）
- **命令行版**：单个可执行文件，适合服务器 / 脚本 / Docker

### 2. 桌面版使用（GUI）

双击启动后常驻系统托盘（macOS 菜单栏），**启动即自动连接**：

- **系统代理分流（唯一模式，无需管理员权限）**：连接成功后自动把系统「自动代理（PAC）」指向内置分流脚本——浏览器访问 `*.xtu.edu.cn` 和 `10/8`、`172.16/12` 内网网段时走 XTU-Connect，其余流量直连或链到已有的系统代理，断开/退出自动还原。不创建虚拟网卡、不改路由、不动 DNS
- **SSH 自动分流（macOS）**：连接成功后自动在 `~/.ssh/config` 末尾写入带标记的托管块（内网网段走本地 SOCKS5 代理），断开/退出自动移除，不影响你的原有配置；终端 `ssh`、VS Code Remote-SSH 零配置直连内网。Linux/Windows 的 netcat 语法不统一，需按「一次性配置 SSH 直连内网」手动设置
- **多账号簿**：面板可保存多个校园网账号（密码 AES-GCM 加密存储），一键切换/删除；切换时自动断开旧会话并登录新账号（服务端单会话，旧登录自动下线）
- **与 Clash/Surge 等代理软件共存**：
  - 对方开 **TUN 模式**：互不接触，PAC 未命中的直连流量由 TUN 照常接管
  - 对方开 **系统代理模式**（手动代理，如 `127.0.0.1:7890`）：接管时自动把该代理**链入分流脚本**——校内走 XTU-Connect、外网继续走 Clash，互不覆盖；断开后对方设置原样保留
- 托盘菜单：状态显示（含内网 IP）、连接/断开、重新连接、浏览器分流开关、控制面板、查看日志、退出
- 控制面板（浏览器打开 `http://127.0.0.1:58081`）：状态卡片、连接控制、账号设置（首次运行会自动弹出）、实时日志
- 日志位置：`~/.config/xtu-connect/desktop.log`

> macOS 提示：应用未做开发者签名分发，若 Gatekeeper 拦截，右键点 App 选「打开」。首选项「登录时启动」可用系统设置里的「登录项」把 XTU-Connect.app 加入实现。
> 命令行工具（curl/git 等）不读系统 PAC：需要时用 `-x socks5h://127.0.0.1:1080` 或环境变量；SSH 已由 `~/.ssh/config` 自动处理（见下节）。

### 3. 命令行直接运行（推荐，零配置）

```bash
./xtu-connect
```

首次运行会交互式询问学号和密码（密码输入不回显），自动保存到 `~/.config/xtu-connect/config.toml`（权限 600）。**之后每次直接运行即可自动连接**，无需任何参数；删除该文件可重新配置。

连接就绪后，本地 `127.0.0.1:1080` 为 SOCKS5 代理，`127.0.0.1:1081` 为 HTTP 代理。

> 注意：学校 VPN 服务端为单会话策略，同一账号同时只能有一个在线客户端（包括浏览器 WebVPN 登录和官方 EasyConnect），重复登录会互相挤下线。

### 3. 一次性配置 SSH 直连内网

macOS 桌面版**自动完成**：连接成功后会在 `~/.ssh/config` 末尾写入带标记的托管块，断开时自动移除，无需手动操作（本节仅适用于命令行版、Linux/Windows 桌面版，或想永久保留规则的用户）。

在 `~/.ssh/config` 中加入（macOS/Linux 自带 nc 支持）：

```
Host 172.16.* 172.24.* 172.25.*
    ProxyCommand nc -X 5 -x 127.0.0.1:1080 %h %p
```

之后 `ssh user@172.16.x.x`、`scp`、VS Code Remote-SSH 全部自动走 VPN，无需其他操作。

### 4. 命令行运行（显式参数）

```bash
# 显式传入凭据（不保存，服务器默认 vpn.xtu.edu.cn）
./xtu-connect -username 你的学号 -password 你的密码

# 覆盖已保存的凭据/端口
./xtu-connect -username 你的学号 -password 新密码 -socks-bind 127.0.0.1:1080 -http-bind ""
```

### 5. 配置文件运行（进阶）

```bash
cp config.toml.example config.toml
# 编辑 config.toml 填入账号密码等
./xtu-connect -config config.toml
```

也支持环境变量（前缀 `XTU_CONNECT_`），详见 `--help`。未指定 `-config` 时会自动加载 `~/.config/xtu-connect/config.toml`。

### 6. 使用代理访问校内资源

浏览器（推荐配 [SwitchyOmega](https://github.com/FelisCatus/SwitchyOmega)）或任意支持代理的程序指向 `127.0.0.1:1080` 即可。命令行程序示例：

```bash
# curl
curl -x socks5h://127.0.0.1:1080 http://内网地址/

# git
git config --global http.proxy socks5://127.0.0.1:1080

# SSH 访问内网服务器（配合端口转发，见下）
```

## 与 Clash 共存

### 方式〇：什么都不做（桌面版默认）

桌面版连接成功后会自动把系统「自动代理」指向内置 PAC 脚本（`http://127.0.0.1:58081/proxy.pac`）：**只有** `*.xtu.edu.cn` 域名和 `10.0.0.0/8`、`172.16.0.0/12` 内网网段走校园网 VPN，其余流量直连或链到原有系统代理。断开或退出时自动还原系统设置。

- 浏览器（Safari/Chrome/Edge 等）零配置直接访问校内网
- **Clash TUN 模式**照常使用，互不干扰：TUN 不占用系统代理项，PAC 未命中的直连流量由 TUN 按你的规则接管
- **Clash 系统代理模式**（手动代理）也能共存：接管前会读出其代理地址（如 `127.0.0.1:7890`）写进分流脚本——校内流量走 XTU-Connect，外网流量继续走 Clash（面板会显示「链式接管」的地址）；Clash 的手动代理设置全程不被修改，断开后原样保留
- 唯一例外：若系统「自动代理」已被其他程序的 PAC URL 占用（PAC 无法嵌套 PAC），则不接管并在面板/托盘提示；此时把对方切换为手动系统代理即可自动链式共存
- 托盘菜单「浏览器分流（系统代理）」可随时手动开关

> 注意：命令行工具（curl/git 等）不读系统 PAC，需要时请用 `-x socks5h://127.0.0.1:1080` 或环境变量；SSH 已由 `~/.ssh/config` 自动处理。

### 方式一：Clash 规则把内网流量指向 XTU-Connect（备选）

在 Clash 配置中添加一个 proxies 节点和规则，内网网段自动走校园网 VPN，其余流量照常走 Clash：

```yaml
proxies:
  - name: "XTU-VPN"
    type: socks5
    server: 127.0.0.1
    port: 1080
    # udp: true

rules:
  # 常见校内网段（按实际情况增删）
  - IP-CIDR,10.0.0.0/8,XTU-VPN,no-resolve
  # 内网域名
  - DOMAIN-SUFFIX,xtu.edu.cn,XTU-VPN
  # 其余流量走你原本的规则/节点
  - MATCH,DIRECT   # 或你的代理组
```

Clash 的 TUN 模式随便开，两者互不干扰：XTU-Connect 只是本机一个监听 1080 端口的普通进程。

### 方式二：浏览器/终端双代理并存

Clash 保持系统代理或 TUN 不动，需要访问校内资源时把浏览器切换到 `127.0.0.1:1080`。两个代理同时在线，互不影响。

### 方式三：XTU-Connect 的非内网流量走 Clash

```toml
# config.toml
dial_direct_proxy = "http://127.0.0.1:7890"   # Clash 混合端口
```

## 端口转发示例

```toml
port_forwarding = [
    { network_type = "tcp", bind_address = "127.0.0.1:2222", remote_address = "10.1.2.3:22" },
]
```

之后 `ssh user@127.0.0.1 -p 2222` 即等于 SSH 到校内 `10.1.2.3`。

## TWFID 免密登录

先用浏览器登录 `https://vpn.xtu.edu.cn`，从 Cookie 中复制 `TWFID` 值：

```bash
./xtu-connect -twf-id 0123456789abcdef
```

## 作为系统服务运行

- Linux (systemd) 与 macOS (launchd) 的安装说明见 [docs/service.md](docs/service.md)
- Docker 运行见 [docs/docker.md](docs/docker.md)

## 编译

完整编译流程已固化为脚本（这是 Release 产物的唯一构建入口）：

```bash
# 一键全平台编译：CLI×5 + 桌面版×5 + macOS .app×2 + checksums，输出到 dist/
./scripts/build.sh              # 版本号自动取 git tag，无 tag 则用日期
./scripts/build.sh --version 0.2.0

# 等价入口
make build    # = scripts/build.sh
make test     # go test ./...
make clean    # 清理 dist/
```

编译目标矩阵：

| 产物 | 平台 | 说明 |
|---|---|---|
| xtu-connect | darwin/arm64+amd64, windows/amd64, linux/amd64+arm64 | 纯 Go，CGO 关闭，随处可交叉编译 |
| xtu-connect-desktop | 同上 + darwin 双架构 | Windows/Linux 纯 Go；**macOS 托盘依赖 Cocoa(cgo)，须在 macOS 本机编译** |
| XTU-Connect.app | macos arm64/amd64 | 含 GUI+CLI 双二进制、Info.plist、icns 图标、adhoc 签名，zip 打包 |

CI/发布流水线（`.github/workflows/`）：

- `ci.yml`：push/PR 时在 ubuntu/macos/windows 三平台跑 vet + test + build
- `release.yml`：推送 `v*` 标签时在 macOS runner 上执行 `scripts/build.sh` 全量构建并自动创建 GitHub Release（附全部产物与校验和）：

```bash
git tag v0.2.0 && git push origin v0.2.0   # 触发自动发布
```

手动构建单个目标：

```bash
go build -o xtu-connect .                                    # CLI（当前平台）
go build -o xtu-connect-desktop ./cmd/xtu-connect-desktop   # GUI（macOS 需本机）
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui" -o xtu-connect-desktop.exe ./cmd/xtu-connect-desktop
```

> 注意：macOS 应用包内 GUI 与 CLI 文件名分别为 `xtu-connect-desktop` 与 `xtu-connect`——macOS 文件系统大小写不敏感，不能命名为 `XTU-Connect`/`xtu-connect` 这种仅大小写差异的对，会互相覆盖。

## 工作原理

```
应用程序 ──SOCKS5/HTTP──▶ XTU-Connect
                            │  1. 按服务端下发的资源规则分流（内网走隧道，外网直连）
                            │  2. 内网域名经校内 DNS 解析（隧道侧 UDP 53）
                            ▼
                     gVisor 用户态 TCP/IP 栈
                            │  IP 包封装
                            ▼
                  特制 TLS 隧道（443 端口，L3IP 会话标识）
                            ▼
                   vpn.xtu.edu.cn 网关 ──▶ 校内网络
```

全程不触碰系统网络栈（除非在命令行版显式开启 `tun_mode`），这是与 EasyConnect 客户端的本质区别，也是不与代理软件冲突的原因。

## 已知限制

- 仅支持校内授权资源（与服务端资源列表一致，非全局 VPN）
- SOCKS5 模式下仅 TCP 走代理；UDP 应用请使用端口转发
- 若学校未来将网关升级为深信服 aTrust，需关注 aTrust 协议支持（上游 zju-connect 已实现，可同步）

## 免责声明

本项目为个人学习研究用途的第三方客户端，与湘潭大学官方及深信服公司无关。请遵守学校网络使用规定，账号密码仅在本机使用。协议实现源自社区公开的逆向研究，如官方要求停止使用，请立即停止。

## 许可证

[AGPL-3.0](LICENSE)（继承自 zju-connect / EasierConnect）

## 致谢

- [zju-connect](https://github.com/Mythologyli/zju-connect) 及其作者 Mythologyli
- [EasierConnect](https://github.com/TeamSUEP/EasierConnect) 及其作者 lyc8503
- 所有为深信服协议逆向做出贡献的社区开发者
