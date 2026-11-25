# LxGate

**Unified Xray + VPNGate dual-engine proxy gateway.**
LxGate merges [Lx-ui](https://github.com/junchengluo9-del/Lx-ui) (a Go + React 19 + Ant Design 6 panel around Xray-core) with the AimiliVPN VPNGate engine (a zero-dependency Python sidecar driving OpenVPN), and dresses both in one interface: **Liquid Glass 2.0**, light and dark.

> Bilingual: [English](#english) · [中文](#中文)

---

<a name="english"></a>

## English

### What it is

Two proxy engines, one panel:

| Engine | Role | How it runs |
| --- | --- | --- |
| **Xray-core** | Your inbound protocols (VLESS, VMess, Trojan, Shadowsocks, Hysteria2, WireGuard, TUIC, …), routing, subscription output | Managed process, as in Lx-ui |
| **VPNGate** | Free public VPN exits, used as an *upstream* for Xray traffic | Embedded Python sidecar + OpenVPN tunnel on `tun0` |

Xray can route any inbound, client or domain through a VPNGate exit by pointing a routing rule at the outbound tag **`vpngate-out`**. Switching exit nodes never restarts Xray, because the tag always points at the same loopback proxy port.

### Highlights

- **Liquid Glass 2.0 UI** — one design language for both themes: `#FAFBFC` canvas with translucent white cards in light mode, `#090B10` canvas with `#12151E` surfaces in dark mode, real frosted glass (`backdrop-filter: blur(18px) saturate(160%)`) on the sidebar, status capsule, drawers and modals, crisp surfaces on dense tables, unified 8/12/16 px radii, and `tabular-nums` for every traffic and latency number.
- **Top status capsule** — Xray state, live VPNGate exit country + latency, update badge and the light/dark toggle on every page.
- **VPNGate Exit page** — routing mode (auto / fixed country / fixed node / favourites), node table with latency, score and session count, connect & disconnect, live structured log, and a one-click "Register `vpngate-out`".
- **System & Update page** — version vs. the latest GitHub release, changelog, native self-update with the database and `vpngate_data/` preserved, or the exact `docker compose` command when containerised.

### Install

Native, on a Linux VPS (root):

```bash
bash <(curl -Ls https://raw.githubusercontent.com/junchengluo9-del/LxGate/main/install.sh)
```

The installer adds the panel *and* the VPNGate runtime (`python3`, `openvpn`, `iproute2`, `iptables`) best-effort: if those packages are unavailable the panel still installs and the VPNGate page simply reports the engine as unavailable.

Docker:

```bash
git clone https://github.com/junchengluo9-del/LxGate.git
cd LxGate
docker compose up -d --build
```

The compose file grants `NET_ADMIN`, `NET_RAW` and `/dev/net/tun` to the container and persists `/etc/lxgate` (panel database) and `/opt/lxgate/vpngate_data` (engine state) on volumes.

### Security model (read this)

The VPNGate engine is a **sidecar, never a public service**:

- Internal engine API: `127.0.0.1:18787` (`LXGATE_VPNGATE_API_PORT`).
- SOCKS5/HTTP proxy: `127.0.0.1:7928` (`LOCAL_PROXY_PORT`).
- The engine only answers requests carrying the shared `X-LxGate-Token`, which LxGate generates per install and keeps in `vpngate_data/.api_token` (mode 0600).
- The engine's own web UI and login are disabled in embedded mode.
- Every panel call goes through `/panel/api/vpngate/*`, i.e. through LxGate's session, 2FA and CSRF middleware.

OpenVPN is started with `--route-nopull` plus policy routing on `tun0`, so a VPNGate tunnel can never take over the VPS default gateway and lock you out of SSH.

### Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `LXGATE_VPNGATE_ENABLED` | `1` | Disable the sidecar entirely (VPNGate page shows "unavailable"). |
| `LXGATE_VPNGATE_API_PORT` | `18787` | Loopback port of the engine API. |
| `LOCAL_PROXY_PORT` | `7928` | Loopback SOCKS5/HTTP proxy port Xray dials. |
| `LXGATE_VPNGATE_DATA_DIR` | `/opt/lxgate/vpngate_data` | Node cache, tunnel configs, logs. |
| `LXGATE_VPNGATE_SCRIPT` | auto-detected | Path to `vpngate/vpngate_manager.py`. |
| `LXGATE_VPNGATE_TOKEN` | generated | Override the shared engine token. |
| `LXGATE_DOCKER` | unset | Force Docker mode for the update endpoints. |
| `LXGATE_VPNGATE_INSECURE_TLS` | `0` | Allow node-list fetches to retry without certificate checks / over plain HTTP. Off by default: a forged node list can steer traffic to an attacker-run server. Only enable behind a TLS-intercepting proxy you trust. |

The panel's own `LXGATE_*` / legacy `XUI_*` variables keep working exactly as before (see `.env.example`).

### CLI

```bash
lxgate status          # panel status
lxgate vpngate         # VPNGate engine: runtime, tun0, last state
lxgate restart         # restart the panel (and the sidecar)
lxgate update          # self-update from GitHub releases
```

### Build from source

```bash
# Frontend (Node 26+, npm 11+)
cd frontend && npm ci && npm run build

# Backend (Go 1.27, CGO enabled for SQLite)
go build -o lxgate .
```

Useful checks: `go build ./...`, `go vet ./...`, `go test ./...`, and in `frontend/`: `npm run typecheck && npm test && npm run build`. The Python engine carries its own suite: `cd vpngate && python3 -m unittest discover -s tests -p "*_test.py"`.

### Credits & license

LxGate is GPL-3.0. It is built on:

- [3x-ui](https://github.com/MHSanaei/3x-ui) / [Lx-ui](https://github.com/junchengluo9-del/Lx-ui) — panel, Xray integration, node federation.
- [Xray-core](https://github.com/XTLS/Xray-core) — the proxy core.
- [AimiliVPN](https://github.com/junchengluo9-del/aimilivpn) — the VPNGate engine.
- [VPNGate](https://www.vpngate.net/) — the public VPN relay network.

Upstream issue links, license headers and notices are preserved; see `LICENSE`, `NOTICE` and `docs/LX_CHANGES.md`.

---

<a name="中文"></a>

## 中文

### 这是什么

两个代理引擎，一个面板：

| 引擎 | 作用 | 运行方式 |
| --- | --- | --- |
| **Xray-core** | 入站协议（VLESS、VMess、Trojan、Shadowsocks、Hysteria2、WireGuard、TUIC 等）、路由、订阅输出 | 与 Lx-ui 相同的托管进程 |
| **VPNGate** | 免费公共 VPN 出口，作为 Xray 的**上游**使用 | 嵌入式 Python 边车 + `tun0` 上的 OpenVPN 隧道 |

把路由规则的出站指向 **`vpngate-out`**，即可让任意入站、用户或域名走 VPNGate 出口。切换出口节点不需要重启 Xray —— 该 tag 始终指向同一个本地代理端口。

### 主要特性

- **Liquid Glass 2.0 界面**：浅色 `#FAFBFC` 画布 + 半透明白卡，深色 `#090B10` 画布 + `#12151E` 表面；侧边栏、状态胶囊、抽屉与弹窗使用真正的毛玻璃（`backdrop-filter: blur(18px) saturate(160%)`），密集表格保持清晰实心表面；统一 8/12/16px 圆角；流量与延迟数字全部启用 `tabular-nums`。
- **顶部状态胶囊**：每页可见 Xray 状态、VPNGate 实时出口国家与延迟、更新角标、明暗切换。
- **VPNGate 出口页**：路由模式（智能自动 / 固定国家 / 固定节点 / 收藏）、带延迟与评分与会话数的节点表、连接与断开、实时结构化日志、一键「注册 vpngate-out」。
- **系统与更新页**：当前版本对照 GitHub 最新发布、更新日志、原生在线更新（保留数据库与 `vpngate_data/`），Docker 部署则直接给出 `docker compose` 命令。

### 安装

Linux VPS 原生安装（root）：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/junchengluo9-del/LxGate/main/install.sh)
```

安装脚本会顺带安装 VPNGate 运行时依赖（`python3`、`openvpn`、`iproute2`、`iptables`）；若系统源不可用，面板仍会正常安装，只是 VPNGate 页面会提示引擎不可用。

Docker：

```bash
git clone https://github.com/junchengluo9-del/LxGate.git
cd LxGate
docker compose up -d --build
```

compose 已为容器授予 `NET_ADMIN`、`NET_RAW` 与 `/dev/net/tun`，并把 `/etc/lxgate`（面板数据库）与 `/opt/lxgate/vpngate_data`（引擎数据）持久化到卷。

### 安全模型（重要）

VPNGate 引擎是**边车，绝不对外提供服务**：

- 引擎内部 API：`127.0.0.1:18787`（`LXGATE_VPNGATE_API_PORT`）。
- SOCKS5/HTTP 代理：`127.0.0.1:7928`（`LOCAL_PROXY_PORT`）。
- 引擎只接受携带共享令牌 `X-LxGate-Token` 的请求；令牌由 LxGate 安装时生成，存放于 `vpngate_data/.api_token`（0600）。
- 嵌入式模式下引擎自带的网页与登录入口被关闭。
- 面板侧所有调用都走 `/panel/api/vpngate/*`，即经过 LxGate 的会话、两步验证与 CSRF 中间件。

OpenVPN 以 `--route-nopull` 启动并在 `tun0` 上做策略路由，因此 VPNGate 隧道**永远不会**劫持 VPS 默认网关、把你锁在 SSH 之外。

### 常用配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `LXGATE_VPNGATE_ENABLED` | `1` | 设为 `0` 完全关闭边车（页面显示不可用）。 |
| `LXGATE_VPNGATE_API_PORT` | `18787` | 引擎 API 的回环端口。 |
| `LOCAL_PROXY_PORT` | `7928` | Xray 连接的回环 SOCKS5/HTTP 端口。 |
| `LXGATE_VPNGATE_DATA_DIR` | `/opt/lxgate/vpngate_data` | 节点缓存、隧道配置与日志。 |
| `LXGATE_VPNGATE_SCRIPT` | 自动探测 | `vpngate/vpngate_manager.py` 路径。 |
| `LXGATE_VPNGATE_TOKEN` | 自动生成 | 覆盖引擎共享令牌。 |
| `LXGATE_DOCKER` | 未设置 | 强制更新接口使用 Docker 模式。 |
| `LXGATE_VPNGATE_INSECURE_TLS` | `0` | 允许节点列表在证书校验失败时以不校验证书 / 明文 HTTP 重试。默认关闭：伪造的节点列表可把流量引向攻击者的服务器，仅在可信的 TLS 中间代理环境下开启。 |

面板原有 `LXGATE_*` / 兼容 `XUI_*` 变量完全照旧可用（见 `.env.example`）。

### 命令行

```bash
lxgate status          # 面板状态
lxgate vpngate         # VPNGate 引擎：依赖、tun0、最近状态
lxgate restart         # 重启面板（含边车）
lxgate update          # 从 GitHub Releases 在线更新
```

### 从源码构建

```bash
cd frontend && npm ci && npm run build   # Node 26+ / npm 11+
go build -o lxgate .                     # Go 1.27，SQLite 需要 CGO
```

自检命令：`go build ./...`、`go vet ./...`、`go test ./...`；前端 `npm run typecheck && npm test && npm run build`；Python 引擎 `cd vpngate && python3 -m unittest discover -s tests -p "*_test.py"`。

### 致谢与许可

LxGate 采用 GPL-3.0。基于以下项目：

- [3x-ui](https://github.com/MHSanaei/3x-ui) / [Lx-ui](https://github.com/junchengluo9-del/Lx-ui)：面板、Xray 集成与节点联邦。
- [Xray-core](https://github.com/XTLS/Xray-core)：代理内核。
- [AimiliVPN](https://github.com/junchengluo9-del/aimilivpn)：VPNGate 引擎。
- [VPNGate](https://www.vpngate.net/)：公共 VPN 中继网络。

上游议题链接、许可证头与声明均已保留，详见 `LICENSE`、`NOTICE` 与 `docs/LX_CHANGES.md`。
