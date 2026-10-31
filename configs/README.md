# Configuration Guide (JSON)

[中文说明](./README.zh_CN.md)

This project uses **two** JSON configuration files:
- Server template: `configs/server.config.json`
- Client template: `configs/client.config.json`

You can rename them to any filename; start the program with `-c`.

```bash
./sudoku -c server.config.json
./sudoku -c client.config.json
```

## Quick Start

1. Generate keys:
   ```bash
   ./sudoku -keygen
   ```
2. Put **Master Public Key** into the server config `key`.
3. Put **Available Private Key** into the client config `key`.

## Minimal Examples

Server (basic TCP):
```json
{
  "mode": "server",
  "local_port": 8080,
  "fallback_address": "127.0.0.1:80",
  "key": "MASTER_PUBLIC_KEY_HEX",
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

Client (basic TCP):
```json
{
  "mode": "client",
  "local_port": 1080,
  "server_address": "example.com:8080",
  "key": "AVAILABLE_PRIVATE_KEY_HEX",
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

## Field Guide

### Basics
- `mode`: `"server"` or `"client"`.
- `name` (optional): node name; exported into short links as `n` and shown in client logs.
- `local_port`:
  - server: public listening port
  - client: local mixed proxy port (HTTP + SOCKS)
- `server_address` (client only): server `host:port`.
- `fallback_address` (server only): decoy `host:port` for suspicious traffic.
- `suspicious_action` (server only):
  - `"fallback"`: forward to the decoy
  - `"silent"`: keep the connection but do not respond

### Keys & AEAD
- `key`:
  - server: **Master Public Key**
  - client: **Available Private Key**
- `aead`: `"chacha20-poly1305"` / `"aes-128-gcm"` / `"none"`.

### Sudoku encoding & padding
- `ascii`:
  - `"prefer_entropy"` (recommended): both directions prefer the low-entropy Sudoku byte layout
  - `"prefer_ascii"`: both directions prefer printable ASCII
  - `"up_ascii_down_entropy"`: client uplink prefers ASCII, server downlink prefers the low-entropy layout
  - `"up_entropy_down_ascii"`: client uplink prefers the low-entropy layout, server downlink prefers ASCII
- `padding_min` / `padding_max`: 0–100 percentage; `padding_max` must be ≥ `padding_min`.
- `custom_table`: optional layout string like `"xpxvvpvv"` (2×`x`, 2×`p`, 4×`v`).
- `custom_tables`: optional array of layouts; if non-empty it rotates layouts per connection.
- Directional mode note:
  - `custom_table` applies only on the direction whose `ascii` side is `entropy` / low-entropy.
  - `custom_tables` also rotates in directional modes; the client sends a table hint during the handshake so the server can select the matching layout.
- `enable_pure_downlink`:
  - `true`: both directions use classic Sudoku encoding
  - `false`: packed downlink mode to reduce overhead (both ends must match)

### Multiplex
`multiplex` is a single tunnel-level setting. For compatibility with old configs, it can also still be read from the legacy `httpmask.multiplex` location.

- `"off"`: disable session mux and HTTPMask transport reuse
- `"auto"`: enable HTTPMask transport reuse when HTTP tunnel modes are used; does not enable session mux
- `"on"`: enable session mux over raw TCP or HTTPMask

### HTTP masking / HTTP tunnel
HTTP-related fields live under the `httpmask` object.

Example (CDN/proxy friendly mode):
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

- `disable`: set `true` to turn off all HTTP masking/tunneling.
- `mode`:
  - `"legacy"`: send one fake HTTP/1.1 header then switch to raw stream (not CDN-friendly)
  - `"stream"` / `"poll"` / `"auto"`: HTTP tunnel modes (CDN-friendly)
  - `"ws"`: WebSocket tunnel mode (CDN/reverse-proxy friendly)
- `tls` (client only, tunnel modes): set `true` to use HTTPS to the entry.
- `host` (client only, tunnel modes): override HTTP Host / SNI (optional).
- `path_root` (optional): first-level path prefix shared by both ends.
### Reverse proxy (expose client services)
Reverse proxy settings live under the `reverse` object.

Server entry:
```json
{ "reverse": { "listen": ":8081" } }
```

Client routes:
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

- Server config only needs `reverse.listen` to open the entry port. Routes are always defined on the client.
- Port reuse (optional): `reverse.listen` does not need to be exposed separately. Bind it to a loopback address (for example `127.0.0.1:8081`), set `fallback_address` to the same address and `suspicious_action` to `"fallback"`, and expose only Sudoku's protocol port (`local_port`, e.g. 8080). Traffic that cannot complete the Sudoku handshake falls back to the reverse entry, and reverse-proxy URLs use that same port, for example `http://<server>:8080/gitea/`.
- Reverse sessions now default to `packed` uplink on the client side to reduce reverse-proxy upload overhead. Normal forward proxy traffic still keeps `pure` uplink.
- Reverse sessions now require `packed` uplink. Use the built-in reverse helpers instead of `DialBase` for reverse registration.
- `listen` (server): the public reverse entry address.
- `client_id` (client): optional identifier for multi-client routing/management.
- `routes` (client): array of services to expose.
  - `path`: URL path prefix (for HTTP) or exact path (for TCP-over-WebSocket).
  - `target`: client-side `host:port`; the host may be an IP literal or a domain name.
  - `strip_prefix`: whether to remove the prefix before proxying (default `true`).
  - `host_header`: optional override for upstream `Host`. If omitted and `target` uses a domain host, that target host is forwarded by default.

TCP-over-WebSocket (client-side local forwarder):
```bash
./sudoku -rev-dial wss://example.com:8081/ssh -rev-listen 127.0.0.1:2222
ssh -p 2222 127.0.0.1
```
Notes:
- The tunnel endpoint is the **exact path** `/ssh` (no trailing slash).
- The WebSocket subprotocol is `sudoku-tcp-v1` (handled by the built-in forwarder).

Raw TCP forwarding (one per server entry):
```json
{
  "reverse": {
    "client_id": "client-1",
    "routes": [{ "target": "10.0.0.1:25565" }]
  }
}
```

## Routing rules (client)
`rule_urls` controls proxy routing (PAC/rule download):
- `["global"]`: proxy everything
- `["direct"]`: proxy nothing
- URL list: download rule files and route accordingly
- `-https://...` / `_https://...`: proxy rule source; matched domains use the proxy even if they also match a direct rule
- `!https://...` / `！https://...`: reject rule source; matched domains are blocked before any outbound connection is made
- `["global"]` and `["direct"]` still apply the built-in hidden reject list via `https://fastly.jsdelivr.net/gh/TG-Twilight/AWAvenue-Ads-Rule@main/Filters/AWAvenue-Ads-Rule-Clash.yaml`

Mainland China example:
- The default `configs/client.config.json` uses `ChinaMaxNoIP + CN CIDR v4/v6`, plus `!https://gcore.jsdelivr.net/gh/TG-Twilight/AWAvenue-Ads-Rule@main/Filters/AWAvenue-Ads-Rule-Clash.yaml` as a visible reject-rule example.

## Users (server whitelist)

Add `users` to the **server** config to switch to whitelist mode: a connection whose handshake user hash is not listed is rejected, even if the handshake itself succeeds.

```json
"users": [
  { "alias": "alice@example.com", "key": "CLIENT_PRIVATE_KEY_HEX", "bandwidth": [20, 5], "data": 100 },
  { "key": "8531a778fe7c" },
  { "alias": "bob", "key": "0123456789abcdef", "blocked": true }
]
```

| Field | Required | Meaning |
|---|---|---|
| `key` | yes | Either a client private key (hex, 64 or 128 chars), or a prefix (8–16 hex chars) of the user hash `sha256(privateKey bytes)[:8]` the client sends in the handshake. The two are told apart by length. |
| `alias` | no | Name shown in logs as `[User:alias]`. Defaults to the 16-char user hash (never the private key). |
| `bandwidth` | no | `[a, b]` in Mbps, `0` = unlimited. `a` is server→client (the client's download), `b` is client→server (the client's upload). Applied to the decoded payload, i.e. what the client perceives, not the Sudoku wire rate. Shared by all connections of the user. |
| `data` | no | Monthly quota in GB (1 GB = 10^9 bytes), `0` = unlimited. Counts wire bytes in both directions across all of the user's connections; once exhausted, existing connections are closed and new ones rejected until the next calendar month (server local time), when usage resets automatically. Usage survives restarts: it is saved to `<config name>.usage.json` next to the config file (e.g. `server.config.usage.json`) every minute and on shutdown, keyed by user hash. Delete an entry (or the file) to reset usage early. |
| `blocked` | no | `true` rejects this user. |

Without `users`, behaviour is unchanged (logs show the raw hash).

## External controller (web panels)

Set `external_controller` to serve the Clash/mihomo RESTful API, so panels such as [zashboard](https://github.com/Zephyruso/zashboard), metacubexd and yacd can watch and steer sudoku. Off when empty. Every endpoint, with parameters, responses and curl/websocket examples, is documented in [doc/api.en.md](../doc/api.en.md).

```json
"external_controller": "127.0.0.1:9090",
"secret": "change-me"
```

- `secret` is the panel's bearer token. Leave it empty only on a loopback address; on any other address an empty secret lets anyone who can reach the port watch and steer the node (sudoku logs a warning).
- In the panel, add backend `http://127.0.0.1:9090` with the secret. Any origin may call the API (CORS `*`); the secret is what guards it.

**Client** — routes, shapes and status codes follow mihomo:
- Proxies: `PROXY` (Selector over `Balance` and every node), `Balance` (LoadBalance, only with two or more nodes), `GLOBAL` (Selector used in global mode, `PROXY` by default), each node (type `Sudoku`), `DIRECT`, `REJECT`. Picks take effect for new connections and are not kept across restarts.
- Modes: `rule` (sudoku's PAC), `global`, `direct`; switching via `PATCH /configs` applies to new connections. Switching to `rule` without `rule_urls` loads the default PAC rules.
- Rules: each rule source is a rule provider named `reject`, `proxy` or `direct`, listed in match order followed by the final `Match`; `PUT /providers/rules/{name}` re-downloads it.
- Latency tests (`/proxies/{name}/delay`, `/group/{name}/delay`) dial through the node exactly like traffic does.
- `/connections` lists every TCP connection and every UDP destination of a SOCKS5 UDP association (closing one of those closes the association); `/traffic`, `/memory`, `/logs` stream as in mihomo.
- While the controller is on, each connection carries about 0.6 KB of tracking state; relays keep their usual copy paths. Off, it costs nothing.

**Server** — `/users` (plus `/traffic`, `/memory`, `/logs`, `/version`), read-only:

```json
{ "count": 2, "active": 1, "up": 1000, "down": 3000, "users": [
  { "name": "alice", "blocked": false, "exceeded": false, "conns": 1,
    "up": 1000, "down": 3000, "upTotal": 3000, "downTotal": 10000, "used": 13000, "limit": 100000000000 } ] }
```

`up`/`down` are bytes per second from/to the user, `upTotal`/`downTotal` count since start, `used`/`limit` are this month's quota bytes (`limit` 0 = unlimited). Over a websocket it pushes once per second. The server's `/traffic` is the sum of its users.
