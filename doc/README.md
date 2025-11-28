# Sudoku Tunnel Documentation / 使用手册

English | [中文](#zh)

Docs map:
- Beginner guide (EN): [doc/getting-started.en.md](./getting-started.en.md)
- 零基础上手指南（中文）：[doc/getting-started.zh.md](./getting-started.zh.md)
- 更新日志（中文）：[doc/CHANGELOG.zh.md](./CHANGELOG.zh.md)

## Overview
- **What it is**: TCP tunnel with HTTP masking, Sudoku ASCII/entropy obfuscation, and AEAD encryption; supports split uplink/downlink (Mieru).
- **Binaries**: single `sudoku` entry; configs in JSON; optional `sudoku://` short links for quick client bootstrap.
- **Ports**: server listens on `local_port`; client exposes mixed HTTP/SOCKS proxy on `local_port`; Mieru optional extra port.

## Usage
- Generate keys: `./sudoku -keygen` (prints master+split); or reuse a public key as the shared key.
- Run with config: `./sudoku -c config.json`
- Test config only: `./sudoku -c config.json -test`
- Start client from link: `./sudoku -link "sudoku://..."` (PAC mode)
- Export short link from config: `./sudoku -c config.json -export-link [-public-host your.ip]`
- Interactive setup (creates server/client configs + link, then starts server): `./sudoku -tui [-public-host your.ip]`

## Protocol (Layers & Principle)
- **HTTP mask**: random-looking HTTP request on connect.
- **Sudoku obfuscation**: bytes encoded as 4×4 Sudoku hints; `prefer_ascii` keeps output printable, `prefer_entropy` maximizes entropy.
- **AEAD**: `chacha20-poly1305` (default), `aes-128-gcm`, or `none` (test only); key hashed with SHA-256 to derive cipher key.
- **Handshake**: timestamp + nonce; optional split-key derivation when client provided private key.
- **Split (Mieru)**: optional downlink over dedicated transport; signaled by magic `0xFF` + UUID; server pairs via manager.

## Config Templates
Minimal Server (standard):
```json
{
  "mode": "server",
  "local_port": 8080,
  "fallback_address": "127.0.0.1:80",
  "key": "MASTER_OR_SPLIT_PRIVATE_KEY",
  "aead": "chacha20-poly1305",
  "suspicious_action": "fallback",
  "padding_min": 5,
  "padding_max": 15,
  "ascii": "prefer_entropy",
  "enable_mieru": false
}
```

Client (PAC, standard):
```json
{
  "mode": "client",
  "local_port": 1080,
  "server_address": "1.2.3.4:8080",
  "key": "SERVER_PUBLIC_KEY",
  "aead": "chacha20-poly1305",
  "padding_min": 5,
  "padding_max": 15,
  "ascii": "prefer_entropy",
  "proxy_mode": "pac",
  "rule_urls": []
}
```

Prefer ASCII traffic: set `"ascii": "prefer_ascii"` on both ends.

Enable Mieru (split):
- Add `"enable_mieru": true` and `"mieru_config": {"port":20123,"transport":"TCP","mtu":1400,"multiplexing":"HIGH"}` on server.
- Client mirrors `mieru_config.port` and sets `"enable_mieru": true`.

## Deployment & Persistence
- Build: `go build -o sudoku ./cmd/sudoku-tunnel`
- Systemd (example):
```ini
[Unit]
Description=Sudoku Tunnel Server
After=network.target

[Service]
ExecStart=/usr/local/bin/sudoku -c /etc/sudoku/server.json
Restart=on-failure
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```
- Adjust paths/ports; for client, run as user service if desired.

## System Proxy (Client)
- Mixed proxy listens on `local_port` (default 1080).
- Browser: set SOCKS5 proxy to `127.0.0.1:<local_port>` (SwitchyOmega etc.).
- OS-wide: configure system SOCKS5/HTTP proxy to the same port (PAC rules handled inside client).

## Short Link Format
- Scheme: `sudoku://<base64url(json)>`
- JSON payload fields:
  - `h` host (server IP/FQDN) **required**
  - `p` port (server port) **required**
  - `k` key (public/shared) **required**
  - `a` ascii mode: `ascii` or `entropy` (default entropy)
  - `e` AEAD: `chacha20-poly1305` (default) / `aes-128-gcm` / `none`
  - `m` client mixed proxy port (default 1080 if missing)
  - `mp` Mieru port (TCP; enables Mieru when present)
- Example: `sudoku://eyJoIjoiZXhhbXBsZS5jb20iLCJwIjo4MDgwLCJrIjoiYWJjZCIsImEiOiJhc2NpaSIsIm0iOjEwODAsIm1wIjoyMDEyM30`
- Client bootstrap: `./sudoku -link "<link>"` (starts PAC proxy).
- Export from config: `./sudoku -c client.json -export-link [-public-host host]`

---

<a name="zh"></a>

## 概览
- **功能**：HTTP 伪装 + 数独 ASCII/高熵混淆 + AEAD 加密，可选上下行分离（Mieru）。
- **形态**：单二进制 `sudoku`，JSON 配置；可用 `sudoku://` 短链接直接启动客户端。
- **端口**：服务端监听 `local_port`；客户端在 `local_port` 提供混合 HTTP/SOCKS 代理；Mieru 另开端口。

## 使用方式
- 生成密钥：`./sudoku -keygen`（输出主密钥与拆分密钥）
- 配置运行：`./sudoku -c config.json`
- 仅校验配置：`./sudoku -c config.json -test`
- 短链启动客户端：`./sudoku -link "sudoku://..."`（PAC 模式）
- 从配置导出短链：`./sudoku -c config.json -export-link [-public-host 服务器IP]`
- 交互式配置并启动服务端：`./sudoku -tui [-public-host 服务器IP]`

## 协议定义与原理
- **HTTP 伪装**：建立连接时先发随机化 HTTP 请求头。
- **数独混淆**：每字节编码为 4×4 数独提示；`prefer_ascii` 输出可打印字符，`prefer_entropy` 输出高熵字节。
- **AEAD 加密**：`chacha20-poly1305`（默认）/`aes-128-gcm`/`none`（仅测试）；密钥经 SHA-256 派生。
- **握手**：时间戳 + 随机/私钥派生 nonce；支持拆分私钥推导。
- **Mieru 分离**：魔数 `0xFF` + UUID 通知；服务端通过管理器配对上下行。

## 配置示例
服务端（标准）：
```json
{
  "mode": "server",
  "local_port": 8080,
  "fallback_address": "127.0.0.1:80",
  "key": "主或拆分私钥",
  "aead": "chacha20-poly1305",
  "suspicious_action": "fallback",
  "padding_min": 5,
  "padding_max": 15,
  "ascii": "prefer_entropy",
  "enable_mieru": false
}
```

客户端（PAC，标准）：
```json
{
  "mode": "client",
  "local_port": 1080,
  "server_address": "1.2.3.4:8080",
  "key": "服务器公钥",
  "aead": "chacha20-poly1305",
  "padding_min": 5,
  "padding_max": 15,
  "ascii": "prefer_entropy",
  "proxy_mode": "pac",
  "rule_urls": []
}
```

- ASCII 风格：`"ascii": "prefer_ascii"`（客户端/服务端一致）。
- 开启 Mieru：`"enable_mieru": true`，并在双方设置相同的 `mieru_config.port`（TCP 默认）。

## 部署与守护
- 构建：`go build -o sudoku ./cmd/sudoku-tunnel`
- Systemd 示例见上（修改路径/端口）；客户端可用用户级服务。
- 确保 `LimitNOFILE` 足够大；若用 Mieru，放行其端口。

## 系统代理指向客户端
- 混合代理监听 `local_port`（默认 1080）。
- 浏览器：SOCKS5 指向 `127.0.0.1:<端口>`（可用 SwitchyOmega）。
- 系统级：在网络设置中填入同样的 SOCKS5/HTTP 代理；PAC 逻辑由客户端内部处理。

## 短链接格式
- 形式：`sudoku://<base64url(json)>`
- 字段：
  - `h` 主机（必填），`p` 端口（必填），`k` 密钥（必填，公钥/共享密钥）
  - `a` ASCII 模式：`ascii` / `entropy`（默认 entropy）
  - `e` AEAD：`chacha20-poly1305`（默认）/`aes-128-gcm`/`none`
  - `m` 客户端混合代理端口（缺省 1080）
  - `mp` Mieru 端口（存在即启用，TCP）
- 启动：`./sudoku -link "<短链>"`；导出：`./sudoku -c client.json -export-link [-public-host]`
