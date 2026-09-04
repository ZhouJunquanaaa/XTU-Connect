# XTU Connect

湘潭大学（XTU）WebVPN 客户端的第三方开源实现。无需安装深信服 EasyConnect 客户端，在本地提供 SOCKS5 / HTTP 代理访问校内网络资源。

**默认不创建虚拟网卡、不修改路由表、不接管系统 DNS**，因此可以与 Clash / Surge / sing-box 等代理软件（包括 TUN 模式）完美共存，彻底解决 EasyConnect 与 Clash TUN 冲突导致无法上网的问题。

> 本项目基于 [zju-connect](https://github.com/Mythologyli/zju-connect)（浙江大学）裁剪适配而来，协议实现源自 [EasierConnect](https://github.com/TeamSUEP/EasierConnect) 的逆向研究成果。感谢原作者们的开源精神。
> 协议探测已确认 `vpn.xtu.edu.cn` 为深信服 EasyConnect 经典版服务端（M7.6.8），登录链路验证通过。

## 功能特性

- 深信服 EasyConnect 协议登录（账号密码 / TOTP / 证书 / TWFID 免密）
- 本地 SOCKS5 代理（默认 `:1080`），支持用户名密码认证
- 本地 HTTP 代理（默认 `:1081`）
- 隧道侧远程 DNS：内网域名（如 `lib.xtu.edu.cn`）通过校内 DNS 解析
- 自动保活与断线重连
- TCP/UDP 端口转发：把内网服务映射到本地端口
- 服务端资源列表自动解析，按校内规则分流（内网走 VPN，其余直连）
- 支持 Windows / macOS / Linux（含 ARM）
- 可选 TUN 模式与 TCP 隧道模式（实验性）

## 快速开始

### 1. 获取程序

从 Release 页面下载对应平台的可执行文件，或自行编译（见下文）。

### 2. 命令行运行

```bash
# 最简运行（服务器默认 vpn.xtu.edu.cn）
./xtu-connect -username 你的学号 -password 你的密码

# 指定代理端口 + 关闭除 1080 端口外的服务
./xtu-connect -username 你的学号 -password 你的密码 -socks-bind 127.0.0.1:1080 -http-bind ""
```

登录成功后，本地 `127.0.0.1:1080` 即为 SOCKS5 代理，`127.0.0.1:1081` 为 HTTP 代理。

### 3. 配置文件运行

```bash
cp config.toml.example config.toml
# 编辑 config.toml 填入账号密码
./xtu-connect -config config.toml
```

也支持环境变量（前缀 `XTU_CONNECT_`），详见 `--help`。

### 4. 使用代理访问校内资源

浏览器（推荐配 [SwitchyOmega](https://github.com/FelisCatus/SwitchyOmega)）或任意支持代理的程序指向 `127.0.0.1:1080` 即可。命令行程序示例：

```bash
# curl
curl -x socks5h://127.0.0.1:1080 http://内网地址/

# git
git config --global http.proxy socks5://127.0.0.1:1080

# SSH 访问内网服务器（配合端口转发，见下）
```

## 与 Clash 共存（推荐配置）

### 方式一：Clash 规则把内网流量指向 XTU-Connect（最优雅）

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

需要 Go 1.25+：

```bash
# 当前平台
go build -o xtu-connect .

# 交叉编译
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o xtu-connect.exe .
CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -o xtu-connect-linux .
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o xtu-connect-mac .

# 跑测试
go test ./...
```

发布时注入版本号：

```bash
go build -ldflags "-X main.xtuConnectVersion=0.1.0" -o xtu-connect .
```

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

全程不触碰系统网络栈（除非显式开启 `tun_mode`），这是与 EasyConnect 客户端的本质区别，也是不与代理软件冲突的原因。

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
