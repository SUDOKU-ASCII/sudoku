# 配置说明（JSON）

[English Version](./README.md)

本项目使用 **两个** JSON 配置文件：
- 服务端模板：`configs/server.config.json`
- 客户端模板：`configs/client.config.json`

你可以把它们重命名为任意文件名，运行时通过 `-c` 指定即可：

```bash
./sudoku -c server.config.json
./sudoku -c client.config.json
```

## 快速开始

1. 生成密钥：
   ```bash
   ./sudoku -keygen
   ```
2. 将输出中的 **Master Public Key** 填入服务端配置的 `key`。
3. 将输出中的 **Available Private Key** 填入客户端配置的 `key`。

## 最小示例

服务端（基础 TCP）：
```json
{
  "mode": "server",
  "local_port": 8080,
  "fallback_address": "127.0.0.1:80",
  "key": "粘贴 Master Public Key",
  "aead": "chacha20-poly1305",
  "suspicious_action": "fallback",
  "padding_min": 5,
  "padding_max": 15,
  "ascii": "prefer_entropy",
  "enable_pure_downlink": true,
  "multiplex": "off",
  "httpmask": { "disable": false, "mode": "auto" }
}
```

客户端（基础 TCP）：
```json
{
  "mode": "client",
  "local_port": 1080,
  "server_address": "example.com:8080",
  "key": "粘贴 Available Private Key",
  "aead": "chacha20-poly1305",
  "padding_min": 5,
  "padding_max": 15,
  "ascii": "prefer_entropy",
  "enable_pure_downlink": true,
  "multiplex": "off",
  "httpmask": { "disable": false, "mode": "ws" },
  "rule_urls": ["global"]
}
```

## 字段含义

### 基础字段
- `mode`：`"server"` 或 `"client"`。
- `name`（可选）：节点名称；导出短链接时写入 `n`，客户端日志中显示。
- `local_port`：
  - 服务端：对外监听端口
  - 客户端：本地混合代理端口（HTTP + SOCKS）
- `server_address`（仅客户端）：服务端地址 `host:port`。
- `fallback_address`（仅服务端）：可疑流量回落的诱饵地址 `host:port`。
- `suspicious_action`（仅服务端）：
  - `"fallback"`：转发到诱饵地址
  - `"silent"`：静默不响应（保持连接）

### 密钥与加密
- `key`：
  - 服务端：填 **Master Public Key**
  - 客户端：填 **Available Private Key**
- `aead`：`"chacha20-poly1305"` / `"aes-128-gcm"` / `"none"`。

### Sudoku 编码与填充
- `ascii`：
  - `"prefer_entropy"`（推荐）：上下行都偏向低熵 Sudoku 字节布局
  - `"prefer_ascii"`：上下行都偏向可打印 ASCII
  - `"up_ascii_down_entropy"`：客户端上行偏向 ASCII，服务端下行偏向低熵布局
  - `"up_entropy_down_ascii"`：客户端上行偏向低熵布局，服务端下行偏向 ASCII
- `padding_min` / `padding_max`：0–100 的百分比；`padding_max` 必须 ≥ `padding_min`。
- `custom_table`：可选的布局字符串，例如 `"xpxvvpvv"`（2 个 `x`、2 个 `p`、4 个 `v`）。
- `custom_tables`：可选的布局列表；非空时会在每条连接中轮换布局。
- 方向模式补充说明：
  - `custom_table` 只会作用在 `ascii` 为 `entropy` / 低熵的那个方向上。
  - `custom_tables` 在方向模式中同样会按连接轮换；客户端会在握手中携带表提示，服务端据此选择对应布局。
- `enable_pure_downlink`：
  - `true`：上下行都用经典 Sudoku 编码
  - `false`：启用带宽优化下行（两端必须一致）

### Multiplex
`multiplex` 是单一的隧道级配置项。为了兼容旧配置，也可以继续从旧的 `httpmask.multiplex` 位置读取。

- `"off"`：关闭 session mux 和 HTTPMask 底层连接复用
- `"auto"`：仅在 HTTP 隧道模式下启用 HTTPMask 底层连接复用；不会启用 session mux
- `"on"`：启用 session mux，可用于 raw TCP 或 HTTPMask

### HTTP 伪装 / HTTP 隧道
HTTP 相关字段集中在 `httpmask` 对象中。

示例（更适合走 CDN/反代）：
```json
{
  "multiplex": "auto",
  "httpmask": {
    "disable": false,
    "mode": "auto",
    "tls": true,
    "host": "example.com",
    "path_root": "aabbcc"
  }
}
```

- `disable`：设为 `true` 表示关闭所有 HTTP 伪装/隧道。
- `mode`：
  - `"legacy"`：先写一个伪造 HTTP/1.1 头再进入原始流（不适合 CDN）
  - `"stream"` / `"poll"` / `"auto"`：HTTP 隧道模式（更适合 CDN）
  - `"ws"`：WebSocket 隧道（更适合 CDN/反代）
- `tls`（仅客户端、且隧道模式）：设为 `true` 表示入口使用 HTTPS。
- `host`（仅客户端、且隧道模式）：覆盖 HTTP Host / SNI（可选）。
- `path_root`（可选）：双方约定的一级路径前缀。
### 反向代理（把客户端服务暴露到服务端）
反向代理相关字段在 `reverse` 对象中。

服务端入口：
```json
{ "reverse": { "listen": ":8081" } }
```

客户端声明要暴露的服务：
```json
{
  "reverse": {
    "client_id": "client-1",
    "routes": [
      { "path": "/gitea", "target": "127.0.0.1:3000", "strip_prefix": true },
      { "path": "/intra", "target": "gitea.intra.example.com:3000" },
      { "path": "/ssh", "target": "127.0.0.1:22" }
    ]
  }
}
```

- 服务端配置只需要设置 `reverse.listen` 来开启入口端口；要暴露哪些服务永远由客户端 `routes` 决定。
- 端口复用（可选）：无需额外对外开放 `reverse.listen`。将它绑定到回环地址（如 `127.0.0.1:8081`），把 `fallback_address` 设为同一地址并将 `suspicious_action` 设为 `"fallback"`，即可只公开 Sudoku 的协议端口（`local_port`，如 8080）；无法完成 Sudoku 握手的流量会回落到反代入口，反代 URL 也使用该端口，例如 `http://<server>:8080/gitea/`。
- 反向代理会默认把客户端上行切到 `packed`，以降低 reverse-proxy 场景的客户端上行带宽压力；普通正向代理流量仍保持 `pure` 上行。
- reverse 会话现在要求使用 `packed` 上行；注册反向代理时应使用内置的 reverse helper，而不是 `DialBase`。
- `listen`（服务端）：对外暴露的入口地址。
- `client_id`（客户端）：可选的标识，用于多客户端的区分/管理。
- `routes`（客户端）：要暴露的服务列表。
  - `path`：HTTP 的路径前缀；或 TCP-over-WebSocket 的精确路径。
  - `target`：客户端侧的 `host:port`，其中 host 可以是 IP，也可以是域名。
  - `strip_prefix`：是否去掉前缀后再转发（默认 `true`）。
  - `host_header`：可选，覆盖转发时的 `Host`；如果不填且 `target` 使用域名 host，则默认把该目标 host 透传给上游。

TCP-over-WebSocket（本地转发器）：
```bash
./sudoku -rev-dial wss://example.com:8081/ssh -rev-listen 127.0.0.1:2222
ssh -p 2222 127.0.0.1
```
注意：
- 隧道入口是 **精确路径** `/ssh`（不要带尾斜杠）。
- 内置转发器会协商 WebSocket 子协议 `sudoku-tcp-v1`。

纯 TCP 转发（每个入口仅支持 1 条）：
```json
{
  "reverse": {
    "client_id": "client-1",
    "routes": [{ "target": "10.0.0.1:25565" }]
  }
}
```

## 分流规则（客户端）
`rule_urls` 用于控制客户端分流（PAC/规则下载）：
- `["global"]`：全局代理
- `["direct"]`：全直连
- URL 列表：下载规则文件并按规则分流
- `-https://...` / `_https://...`：proxy 规则源，命中域名即使也命中直连规则也会走代理
- `!https://...` / `！https://...`：reject 规则源，命中域名时会在发起任何外连前直接拒绝
- `["global"]` 和 `["direct"]` 也会默认隐式启用 `https://fastly.jsdelivr.net/gh/TG-Twilight/AWAvenue-Ads-Rule@main/Filters/AWAvenue-Ads-Rule-Clash.yaml` 这份 reject 规则

中国大陆适用举例：
- 模板 `configs/client.config.json` 默认给了一组 PAC 规则 URL（ChinaMaxNoIP + CN CIDR v4/v6），并额外演示了 `!https://gcore.jsdelivr.net/gh/TG-Twilight/AWAvenue-Ads-Rule@main/Filters/AWAvenue-Ads-Rule-Clash.yaml` 这种 reject 规则写法。

## Users(服务端白名单)

在**服务端**配置里加入 `users` 即启用白名单:握手携带的用户哈希不在列表中的连接会被拒绝(即使握手本身成功)。

```json
"users": [
  { "alias": "alice@example.com", "key": "客户端私钥HEX", "bandwidth": [20, 5], "data": 100 },
  { "key": "8531a778fe7c" },
  { "alias": "bob", "key": "0123456789abcdef", "blocked": true }
]
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `key` | 是 | 客户端私钥(hex,64 或 128 字符),或握手中用户哈希 `sha256(私钥字节)[:8]` 的前缀(8–16 个 hex 字符),按长度区分。 |
| `alias` | 否 | 日志中显示为 `[User:alias]`;不填则为 16 位用户哈希(不会使用私钥)。 |
| `bandwidth` | 否 | `[a, b]`,单位 Mbps,`0` 不限速。`a` 为服务端→客户端(即客户端下行),`b` 为客户端→服务端(即客户端上行)。限制的是解码后的实际负载速率(客户端可感知带宽),不是 Sudoku wire 速率;同一用户的所有连接共享额度。 |
| `data` | 否 | 每月流量,单位 GB(1 GB = 10^9 字节),`0` 不限制。按 wire 字节双向累计、该用户所有连接共用;用尽后现有连接被断开、新连接被拒绝,直到下个自然月(服务端本地时间)自动清零。用量重启不丢:按用户哈希每分钟及退出时写入配置文件旁的 `<配置名>.usage.json`(如 `server.config.usage.json`);删除对应条目(或整个文件)即可提前清零。 |
| `blocked` | 否 | `true` 则拒绝该用户。 |

不配置 `users` 时行为不变(日志仍显示原始哈希)。

## 外部控制器（Web 面板）

设置 `external_controller` 即开启 Clash/mihomo 语义的 RESTful API，[zashboard](https://github.com/Zephyruso/zashboard)、metacubexd、yacd 等面板可直接接入。为空则关闭。全部接口的参数、返回值与 curl/websocket 调用示例见 [doc/api.zh.md](../doc/api.zh.md)。

```json
"external_controller": "127.0.0.1:9090",
"secret": "change-me"
```

- `secret` 是面板使用的 Bearer token。只有监听回环地址时才建议留空；监听其它地址而不设 secret，任何能连到端口的人都能查看和操控节点（启动时会打警告）。
- 面板里添加后端 `http://127.0.0.1:9090` 并填 secret。API 允许任意来源（CORS `*`），靠 secret 保护。

**客户端**——路由、JSON 结构与状态码均与 mihomo 一致：
- 节点与分组：`PROXY`（Selector，成员为 `Balance` 与各节点）、`Balance`（LoadBalance，两个及以上节点时才有）、`GLOBAL`（全局模式使用的 Selector，默认跟随 `PROXY`）、各节点（类型 `Sudoku`）、`DIRECT`、`REJECT`。切换只影响新连接，重启后恢复默认。
- 模式：`rule`（即 sudoku 的 PAC）、`global`、`direct`，经 `PATCH /configs` 实时切换，影响新连接。没有配置 `rule_urls` 时切到 `rule` 会加载默认 PAC 规则。
- 规则：每类规则源是一个规则提供者，名为 `reject`、`proxy`、`direct`，按匹配顺序列出，最后是 `Match`；`PUT /providers/rules/{name}` 重新下载。
- 延迟测试（`/proxies/{name}/delay`、`/group/{name}/delay`）与真实流量走同一条拨号路径。
- `/connections` 列出所有 TCP 连接，以及 SOCKS5 UDP 关联里的每个目的地址（关闭其中一条会结束整个 UDP 关联）；`/traffic`、`/memory`、`/logs` 的推流方式与 mihomo 相同。
- 开启后每条连接约多占 0.6 KB 用于统计，转发仍走原有的拷贝路径；关闭时没有任何额外开销。

**服务端**——只读的 `/users`（另有 `/traffic`、`/memory`、`/logs`、`/version`）：

```json
{ "count": 2, "active": 1, "up": 1000, "down": 3000, "users": [
  { "name": "alice", "blocked": false, "exceeded": false, "conns": 1,
    "up": 1000, "down": 3000, "upTotal": 3000, "downTotal": 10000, "used": 13000, "limit": 100000000000 } ] }
```

`up`/`down` 为来自/发往该用户的每秒字节数，`upTotal`/`downTotal` 为启动以来累计，`used`/`limit` 为本月计入配额的字节数（`limit` 为 0 表示不限）。以 websocket 连接时每秒推送一次。服务端的 `/traffic` 为所有用户之和。
