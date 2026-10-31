# 更新日志

## v0.5.2-01（2026-11-01）
- `fix/socks5`: 修复本地 SOCKS5 客户端 UDP ASSOCIATE 选中无关网卡地址的问题（#37）。loopback 客户端现在保留 loopback 中继，并按 UDP 请求的地址族选择 IPv4/IPv6；LAN 客户端仍使用实际接入的网关地址。补上相反地址族 direct 目标的代理回退，避免 udp6 中继直写 IPv4 失败时拆掉整个 UDP 关联；IPv6 不可用时回退到控制连接地址族。
- `security`: Linux 构建通过 `//go:debug multipathtcp=0` 关闭 MPTCP 监听，移除 CVE-2026-98432 已公开利用链所需的 MPTCP listener；完整修复仍需升级宿主内核。
- `docs`: 反向代理支持与 Sudoku 协议端口复用：`reverse.listen` 绑定回环地址，并把 `fallback_address` 指向该地址即可只公开一个端口。

## v0.5.2.0
一次不改协议的整体重构与性能优化：wire 格式与命令行参数均未变化，配置 JSON 与短链接只新增可选的节点名（`name` / `n`），与 v0.5.1 / v0.5.0 以及 mihomo、Anywhere 双向互通（`tests/interop_test.go` 以真实进程对照参考二进制验证，另有 v0.5.1 golden 向量固定解码行为）。

### 新功能
- `users`: 服务端新增 `users` 白名单。按握手中的用户哈希识别用户（`key` 可填客户端私钥或 8–16 位哈希前缀，按长度区分），哈希不在列表中的连接即使握手成功也会被拒绝；每个用户可配置 `alias`（日志显示 `[User:alias]`）、`bandwidth`（`[服务端→客户端, 客户端→服务端]`，单位 Mbps，限制解码后的负载速率）、`data`（每月 GB，按 wire 流量双向累计，用尽后断开并拒绝新连接，每个自然月自动清零；用量每分钟及退出时写入配置文件旁的 `<配置名>.usage.json`，重启后继续累计）与 `blocked`。不配置 `users` 时行为不变，协议无改动。
- `cli`: `-tui` 向导重做。分 节点 → 加密 → 确认 三页：只问公网地址、服务端端口（默认随机挑一个本机空闲的 30000 以上高位端口，输入时即检查能否监听）、节点名、AEAD 与 ASCII 模式，最后一页汇总确认后才写文件；Esc / Ctrl+C 取消且不写任何文件。其余项固定：padding 5–15%、纯 Sudoku 下行、不用自定义表、不开 mux，客户端 mixed 端口默认 10233。HTTP mask 预先配好 `auto` 模式与随机 path root（6–10 位）：服务端启用（同时接受裸 TCP），客户端配置与短链接带上同样的 mask 参数但 `disable: true`，以后只改这一项就能走 CDN。非终端环境仍退回逐行提问。
- `controller`: 新增 Clash/mihomo 语义的外部控制器（`external_controller` + `secret`，默认关闭），可接入 zashboard、metacubexd、yacd 等面板。客户端提供节点与分组（`PROXY` / `Balance` / `GLOBAL` 选择）、规则与规则提供者、实时连接（含 SOCKS5 UDP 各目的地址）及断开、流量与内存推流、日志推流、节点延迟测试，以及 `rule` / `global` / `direct` 模式实时切换；服务端提供只读 `/users`：用户数、活跃用户、每个用户的实时上下行速率与已用流量。未开启时不做任何连接统计，转发路径不变。接口文档见 [api.zh.md](./api.zh.md)。
- `config`: 新增可选的节点名，配置里是 `name`，短链接里是 `n`；`-export-link` 与 `-link` 双向保留，客户端启动日志里显示。旧短链接没有 `n` 也照常解析，其它实现会忽略这个字段。

### 性能
本机回环、同机对照 v0.5.1（`TestPerfCompare`，每场景交替运行 3 次取中位数，8 核；数值随机器而异，趋势更有参考价值）：
- 吞吐与 CPU：纯下行 / 上行 / 双向 / 8 路并发的吞吐提升 36%–41%，每 GiB 的 CPU 下降约 26%；AES-128-GCM 下行吞吐 +42%、CPU −28%；packed 下行吞吐持平、CPU −1%，packed 上行 +39%、双向 +9%；UDP（UoT）吞吐 +40%、每 GiB CPU −45%。
- 各 httpmask 传输：stream / poll / ws 的下行与上行吞吐提升 25%–42%，CPU 下降 22%–31%。
- 内存：1000 条空闲连接的 RSS 下降 52.3%（203 MB → 97 MB），mux 下 1000 条空闲 stream 下降 29.7%；stream / poll 下行的峰值 RSS 下降 13%–18%。
- 建连：单 worker 串行建连 +11.8%；每条连接的 CPU 在 1、16、64 并发下分别下降 1%、26%、23%，ws / stream mux / mux 下的建连同样下降 23%–26%。但回环压测中 16 与 64 并发的每秒新建连接数下降 13%–17%（见下面 `perf/memory`）。

- `perf/uot`: UoT 每个数据报改为一次写入（原先 3 次写入、3 个 AEAD record），并新增批量写入队列：积压的数据报合并为大 record 发送，服务端与 SOCKS5 UDP 客户端共用。队列超过 256KB 时丢弃新数据报，行为与拥塞的 UDP 链路一致。客户端改用常驻 `UoTReader`，服务端缓存域名目的地址（最长 30 秒，仅缓存成功解析）。本机基准中 packed 下行 UoT/TCP 由约 17% 提升到约 66%，其余方向约 76%–80%。
- `perf/memory`: 空闲连接不再持有缓冲区。Sudoku 编解码连接空闲时只占 512 B 内联读缓冲，读满后才换用池化的 32 KiB 缓冲；record 帧改为从 `sync.Pool` 借用；中继缓冲在读满之前只用 2 KiB。本机 1000 条空闲连接的 RSS 下降 52.3%，每连接 CPU 下降 23%–26%，单 worker 串行建连速度提升 11.8%。代价是回环压测里 16 / 64 并发的每秒新建连接数下降 13%–17%：该场景是闭环压测，生成器自身接近占满一个核，数字受生成器影响；这是用建连速率换内存与每连接 CPU 的取舍（涉及提交 `b1fa9a5`、`e3ab57e`，如更看重建连速率可回退这两个提交）。
- `perf/codec`: Sudoku 表查找改为位集，解码按块处理，编码使用 wyrand 无分支路径；`apis` 跨连接共享已构建的表。解码输出与 v0.5.1 golden 向量逐字节一致。编码器随机数的抽取顺序有调整，wire 字节序列不同但解码等价（测试中的 `wire_compat` 摘要已重新生成）。
- `perf/mux`: 多条并发 stream 的小帧合并为一次写入；keepalive 统一为一套实现，不再为每帧写时间戳；每条 stream 的内存减半（内嵌 cond，共享占位地址）。
- `perf/httpmask`: 空闲 split session 不再每 5ms 唤醒；上传 body 读入按 Content-Length 取尺寸的池化缓冲；上传批次原地发送；下行 pull 的池化缓冲直接交给连接；请求头值与带序号的上传 URL 预先构造；空闲 session 由单个定时器回收而不是各占一个 goroutine；每个 session 只保留一个 pull 租约。
- `perf/handshake`: 每条 KIP 消息与 OpenTCP 各用一次写入（原为两次）；地址编解码零分配（`AppendAddress` / `DecodeAddress`）；探测读缓冲仅在需要再读时分配。
- `perf/app`: UDP 中继每个数据报零分配；路由规则每次决策只查一次；HTTP 代理请求读取器回收复用；SOCKS4 请求一次读取；`logx` 每行日志格式化进池化缓冲并一次写出，零分配。
- `perf/geodata`: 规则以不可变快照发布，域名集合写时复制；匹配不再分配；纯文本列表绕过 YAML 解析器；下载内容以 string 持有。
- `perf/dns`: 缓存渲染后的地址，合并并发的同名查询，并限制缓存大小。
- `perf/reverse`: 响应改写按 Content-Length 取缓冲，根绝对路径 URL 经单个惰性拼接器改写。

### 结构与可读性
- `refactor/httpmask`: 按职责拆分文件（服务端、会话存储、pull 处理、客户端协议/传输/请求）；`streamSplitConn` 与 `pollConn` 合并为带 codec 的 `splitConn`；stream 与 poll 共用一个 session handler；抽出 `sessionStore`；`DialTunnel` 拆为各模式的小函数；重试常量命名并展平循环。
- `refactor/tunnel`: `HandshakeAndUpgradeWithTablesMeta` 重命名为 `ServerHandshake`；stream 与 early 握手共用一套 hello 交换；连接包装集中到 `conns.go`，mux stream 到 `mux_stream.go`；`closeNoSend` 改名 `abort`；四个 `dialBase*` 变体与重复的密钥派生导出合并；仅包内使用的名字改为未导出。
- `refactor/app`: 路由动作改为 `routeAction` 类型而非字符串；用户白名单准入抽成 `rejectReason`；UDP 中继按数据报拆分；`client_socks5.go` 按职责拆分；客户端协议处理器改名为 `handleSocks5`、`handleSocks4`、`serveMixed`；原 `internal/handler` 并入 `internal/app/fallback.go`。
- `refactor/protocol`: UoT、SOCKS5 UDP 与 KIP 共用同一个 IP 地址编码器 `AppendAddrPort`。
- `refactor/geodata`: `manager.go` 按职责拆为解析、缓存、下载、匹配与地址段；`MatchCN` 更名为 `MatchRuleOrLocal`，LAN 地址段常量改为字节判断。
- `refactor/reverse`: `manager.go` 拆为 manager、router、proxy；响应改写移入 `rewrite` 子包；去掉未使用的 `userHash` 与 client id 参数。
- `refactor/config`: `Finalize` 拆为按配置段的小步骤；枚举规整统一走 `normalizeChoice`；HTTP mask 与 multiplex 访问器移到 `httpmask.go`；值规整函数移到 `normalize.go`；CLI 向导直接依赖 `Config.Finalize`。
- `refactor/misc`: `dnsutil.ResolveWithCache` 更名为 `Resolve`，`RecommendedClientResolver` 更名为 `ClientResolver`；`connutil.go` / `ed25519.go` 按内容改名为 `halfclose.go` / `keypair.go`；删除不可达分支、永不触发的 nil 守卫与只被使用一次的包装函数。

### 行为变化与修复
- `app`: `Accept` 失败时退避重试而不是空转；SOCKS4 请求超长时直接关闭连接；HTTP CONNECT 握手阶段读到的早到字节不再丢弃，会转发给目标。
- `geodata`: 规则下载限制为 32 MiB，并在下载结束后关闭该次下载的 transport；关键字匹配顺序固定为排序后的顺序；修复 srcset 索引 0 的处理。
- `config`: `Save` 现在会返回 `Close` 的错误。
- `mux`: `Write` 返回时帧可能尚在合并队列中，尚未写到线上。
- `dns`: 移除内置 DoT 支持，默认仍为 DoH。
- `httpmask`: 每个 session 保证只有一个 pull 租约，并移除 `downlinkMu`。
- `build`: Go 升级到 1.27.2（`go.mod` 与 Dockerfile）；依赖升级到最新兼容版本：huh v1.0.0、coder/websocket v1.8.15、x/crypto v0.58.0、x/net v0.61.0、x/sys v0.49.0、x/term v0.47.0 等。编解码与 table 生成不变，v0.5.1 golden 向量与互通测试通过。

### 测试
- `tests`: 新增 `users` 白名单在 no-httpmask、legacy、stream、poll、ws 下的端到端测试，以及 UoT 与 TCP 吞吐对比基准（`SUDOKU_UOT_BENCH=1`）。
- `tests/interop`: 新增进程级互通测试 `TestInteropWithReference`，以 `SUDOKU_REF_BIN` 指向发布版二进制，在传输 × 编码矩阵上双向（当前客户端→参考服务端、参考客户端→当前服务端）运行回显、域名目标、大流量、半关闭、并发、UDP、fallback 与 key update 等负载，以及 reverse 代理；失败用例会自动以参考版对参考版做对照。
- `tests/perfcmp`: 新增 `TestPerfCompare`，对照参考二进制报告吞吐、每单位工作量的 CPU、峰值与空闲 RSS，包含 1、16、64 并发建连场景。
- 精简重复与过度细碎的测试，删除对已移除内部名字的测试，并用 v0.5.1 golden 向量固定编解码；删除 `internal/cli` 的单测。

## v0.5.0（2026-09-05）
- `BREAKING/httpmask`: 非 WebSocket 的 `auto`、`stream`、`poll` 重构为 v0.5 session 上传序列协议。每个上传批次带单调递增序号；CDN 在服务端已收包后丢失 HTTP 响应时，客户端以同一序号和原始负载重试，服务端幂等确认而不重复写入。非 WS HTTPMask 的客户端和服务端必须同时升级到 v0.5.0；旧 v0.4.x 非 WS peer 会被明确拒绝。
- `httpmask`: 每个 split session 只允许一个下行 pull 读取后端连接。新 pull 会有序接管旧 pull；下行数据仅在 HTTP 响应成功 flush 后确认，CDN reset、超时或连接替换不会丢弃尚未确认的数据。`auto/stream/poll` 的短暂网络错误改为持续、受限退避重连，避免固定重试次数耗尽后长时间断流。
- `perf/httpmask`: 移除 mux 场景额外等待的 spare preconnect，只等待下行 pull 与首个上传 ready；在长 RTT 链路上减少一轮无效预热等待，使非 WS 首次可用时延保持与 HTTPMask 关闭路径同一量级。
- `mux`: 修复 stream 半关闭状态机。远端关闭写方向后，本地写方向不会因 `CloseRead` 被提前移除，避免合法帧被静默丢弃并造成 mux 会话长期卡死。
- `concurrency`: 为 HTTPMask 接收队列补齐并发读保护，并新增 mux 多 stream 有序回显、上传重复幂等与下行 pull 接管回归测试。
- `compatibility`: `mux off`、`httpmask disable` 与 `httpmask ws` 的协议及行为保持不变；fallback、rotate、customized 和 early handshake 继续沿用现有语义。

## v0.4.6（2026-05-25）
- `sudoku/downlink`: 删除服务端下行 MTU 边界触发的 1/13 概率纯 padding 小包注入；保留常规 Sudoku padding 与 packed AEAD Record 前缀扰动，避免 TCP 粘包下无效的额外写入和带宽浪费。
- `perf`: 优化下行热路径，RecordConn 复用读帧缓冲并原地解密，Sudoku/Packed 读取优先直接解码进调用方 buffer，同时减少无 padding 编码容量浪费并调大代理 pipe buffer 以降低大下载 record 开销。
- `perf`: 继续优化 packed 下行接收路径，按调用方需求限制小读解码窗口，避免 AEAD 长度头读取时一次性堆积大量 pending 数据；新增 packed 4:3 解码快路径与 96KiB 解码 scratch，降低客户端下载 CPU 开销。
- `perf`: 优化 packed 下行发送热路径，移除 group 编码中的接口随机数与多层函数调用开销，并让轻量 PRNG 复用 64bit 输出；保持 padding 概率与 wire 格式兼容，降低服务端 packed 写侧 CPU。

## v0.4.5（2026-05-22）
- `proxy/pac`: `rule_urls` 新增 `-https://...` / `_https://...` proxy 规则源语义；命中 proxy 规则的目标会优先走代理，可用于覆盖默认直连规则中的个别域名或网段。

## v0.4.1（2026-04-02）
- `proxy/pac`: `rule_urls` 新增 `!https://...` / `！https://...` reject 规则源语义；命中 reject 规则的域名会在客户端侧直接拒绝，不再发起 TCP/UDP 外连，用于默认去广告与显式拦截场景。
- `proxy/pac`: `global` / `direct` 模式现在会隐式加载 AWAvenue Ads Reject 规则作为默认去广告集，但不会把该规则显式写入配置；PAC 示例配置则保留一条可见的 `!gcore...AWAvenue...yaml` reject 规则示例。
- `proxy/pac`: 收窄 PAC 域名判定逻辑。域名若未命中直连规则，不再额外用本地 223 DoH 回查 IP 归属，而是直接走代理；仅在已命中直连域名规则后，继续使用现有内置 resolver 解析直连目标。
- `geodata`: 修复 YAML `payload` 中裸域名条目的解析缺口。像 AWAvenue Ads Rule 这类 `payload: - 'ad.qq.com'` 的 domain-set 规则现在会被正确装载，不再出现 reject 规则下载成功但统计仍为 `0 Domains, 0 Suffixes` 的情况。
- `config`: 默认 PAC 示例移除冗余的 `BiliBili` / `WeChat` 规则项，仅保留 `ChinaMaxNoIP + CN CIDR v4/v6`，避免与已有大中华规则集重复。
- `geodata`: 规则管理器从单全局实例收敛为按 URL 集合缓存的实例，允许直连规则集与 reject 规则集同时存在，降低耦合并避免后续扩展继续叠加状态隐患。
- `tests`: 补充 reject 路由、HTTP/SOCKS 拒绝反馈，以及“PAC 未命中域名直接代理、命中直连域名后再解析”的回归测试；同时通过 `go vet ./...` 与本地等价 release workflow 的 `go test ./... -count=1 -shuffle=on -v -race` 校验。

## v0.3.8（2026-03-28）
- `httpmask/fallback`: 修复启用 HTTPMask 时的链式回落缺口。外层在 `stream/poll` 的 `/session` early-handshake 拒绝后，现在会把已读 HTTP 前缀完整回放给内层，恢复 `ws/auto/poll` 场景下的 fallback 中转能力。
- `tests`: 将 HTTPMask 链式回落回归测试扩成模式矩阵，覆盖 `ws/auto/poll` 与 `auth reject/early reject` 组合，同时保留正常 HTTPMask 直连基线校验。
- `dns`: 为内置 resolver 增加 DoT 实现与测试，但默认策略保持阿里公共 DNS 的 DoH 作为首选，更贴合当前基于 `http.Client`/HTTP2 连接复用的实现形态与跨网络兼容性。
- `cleanup`: 收敛 DNS bootstrap 规范化/校验的重复逻辑，降低后续继续扩展 DoQ 等上游协议时的维护噪音。

## v0.3.7（2026-03-21）
- `sudoku/downlink`: 新增服务端下行随机 inter-packet padding 注入，按写入规模和低频概率偶发插入纯 padding 小包，兼容旧端直接忽略，并同时覆盖 pure / packed 下行路径。
- `sudoku/downlink`: 收敛服务端 pure / packed 下行装配逻辑，复用统一 writer 构造；补充 pure / packed 兼容回归测试，确认注入 padding 后解码结果不变。
- `cleanup`: 继续移除项目内生产路径不可达的旧包装与死代码，包括 HTTPMask token 包装、内部客户端便捷包装、未使用的 DNS/log 辅助接口，以及已被真实生产路径替代的 geodata / httpmask 旧入口。
- `tests`: 对齐当前生产实现，测试直接覆盖 `MatchCN`、`WriteRandomRequestHeaderWithPathRoot` 与当前下行路径，减少围绕死代码的过时测试。

## v0.3.6（2026-03-15）
- `client/lb`: 客户端新增多节点负载均衡：`-c` / `-link` 支持多值同时生效，mixed 监听端口固定取首个配置的 `local_port`，同站点目标按 Rendezvous Hashing 粘性选路，并在节点失败时按次优节点回退。
- `cli`: 交互式 setup wizard 与客户端配置装配逻辑迁移到独立 `internal/cli`，入口职责更清晰；新增 `internal/cliutil` 复用多值 flag 解析，减少 `cmd/sudoku-tunnel` 主入口样板代码。
- `tests`: 将 CLI flag、负载均衡与原 `cmd/matrix-smoke` 的 HTTPMask 冒烟场景统一迁入 `tests/`，减少重复测试代码并保持全量回归覆盖。

## v0.3.4（2026-03-12）
- `cleanup`: 删除仓库内已不再使用的旧 `AEADConn` 实现及其测试，统一收敛到当前记录层 `RecordConn` 路径，减少重复加密封装。
- `cleanup`: 移除内部未使用的握手包装函数、下行模式辅助函数与 HTTPMask 认证辅助死代码，收紧实现表面并降低维护噪音。
- `tests`: 清理未引用的中间人测试辅助逻辑，保持测试代码与当前覆盖路径一致。

## v0.3.3（2026-03-10）
- `httpmask`: 将 KIP 握手折叠进首个 HTTP/WS 往返，消除启用 `httpmask` 时相对关闭状态多出的 1 RTT，同时保持抓包仍为 HTTP/WS、认证与 CDN 兼容路径不变。
- `httpmask`: 新增统一的 early-handshake 构建逻辑，收敛 client/server 在 `apis`、CLI 与测试入口的重复实现，降低后续维护成本。
- `tests`: 本地 smoke 新增固定链路时延下的 RTT 对照测试，并补充启用/关闭 `httpmask` 的 RTT 一致性校验。
- `crypto`: 记录层 epoch/seq 轮换逻辑改为随机初始化与更稳健的接收侧推进，补充对应测试，降低长连接与重协商边界场景下的脆弱性。
- `sudoku/packed`: 为前缀字节增加受保护的 padding 扰动，并补充前缀回归测试，降低前缀模式过于固定的问题。

## v0.3.2（2026-03-07）
- `dns`: 内置客户端侧硬编码 DNS 能力：DoH（阿里/腾讯）、缓存、过期兜底与 bogus IP（`198.18.0.0/15`）过滤；不新增配置字段，保持内核级默认策略。
- `dns`: DNS 查询保持 `IPv4 only`，并仅用于客户端目标 / PAC / DIRECT 解析；代理服务器引导路径（`server_address` / tunnel bootstrap）不走这套解析链，避免自循环与引导污染。
- `proxy/pac`: 在 FakeIP / OpenClash 一类环境下，PAC 与直连目标解析改用内置 resolver，避免系统 DNS 返回 `198.18.0.0/15` 导致误分流或误直连。
- `proxy/tun`: 内核新增跨平台主动出站 bypass 能力，消费 `SUDOKU_OUTBOUND_*` 环境变量；规则下载、DoH、DIRECT、服务端 TCP 拨号与 HTTPMask 主动出站在 TUN 场景下可绕开系统默认隧道路由，避免自循环导致的个别 PAC 规则源下载失败。
- `socks5/uot`: 修复 `UDP ASSOCIATE` 在 loopback / 多网卡 / point-to-point 接口场景下的本机地址通告与客户端地址学习逻辑，提升本地与虚拟网卡环境下的健壮性。
- `tests`: 补充解析与 `UDP ASSOCIATE` 相关单测、集成测试，并让测试地址选择跳过 point-to-point 接口，避免 `utun` 等本机特殊地址导致误报。

## v0.2.6（2026-02-23）
- `refactor`: 大规模模块化重构（不改协议/配置语义）：拆分 HTTPMask tunnel client/server 逻辑、拆分客户端 mixed proxy（SOCKS4/5/HTTP/路由/拨号）、拆分 reverse 重写逻辑并补单测，整体显著降低单文件复杂度与重复代码，便于后续维护与定位问题。
- `httpmask`: masker 的 User-Agent/paths/content-types 列表改为 embed 资源，代码更精简且便于后续按需调整指纹数据集。
- `sudoku/table`: `NewTable` 构建失败不再 `panic`，改为记录错误并返回 `nil`（仅影响作为 Go 库被调用的异常分支；正常配置不会触发）。

## v0.2.3（2026-02-14）
- `proxy/pac`: 修复 PAC 路由中的 DNS 缓存过期实现，移除 `time.AfterFunc` 删除策略导致的“旧定时器误删新值”与高并发定时器膨胀风险；改为按过期时间惰性回收，提升稳定性与可维护性。
- `proxy/pac`: PAC 的回查 DNS 从 `ip4` 扩展为 `ip`，允许同时利用 A/AAAA 结果进行规则判断。
- `geodata`: 重构规则解析路径，统一为构建态解析模型；在保持行为兼容的前提下收敛参数传递、减少耦合，使解析流程更易读、更易扩展。
- `geodata`: 新增原生 IPv6 规则区间存储与匹配（`IP-CIDR6`/IPv6 单 IP），并避免 IPv6 规则污染 IPv4 匹配集合，修复 PAC 在 IPv6 场景的潜在误分流。
- `proxy/http`: 修复 HTTP/CONNECT 请求中 `Host: [IPv6]` 无端口时的默认端口补全与路由 IP 提取逻辑，避免该场景下的错误路由/连接失败。
- `tests`: 新增/完善回归测试覆盖 DNS 缓存刷新、IPv6 规则匹配与 IPv6 Host 无端口解析，确保重构后行为稳定。

## v0.2.2（2026-02-05）
- `reverse`: 子路径反代彻底可用：修复资源 404、SVG/图片无法加载、WebSocket 无法建立等问题；增强 `Location`/`Set-Cookie Path` 重写，并支持在 `Content-Type` 缺失时按路径后缀推断是否可重写。
- `reverse`: 新增反代前缀 Cookie 回写与路由回退：当浏览器发起根路径请求（如 `/api/*`、`/ws`）但无 `Referer` 时，仍能正确路由到最近访问的子路径应用，彻底解决“资源 404 / WS 连不上”类问题。
- `reverse`: 避免对子路径场景的 JS/HTML 重写误伤正则字面量导致页面卡死/报错（如 `Invalid regular expression flags`）：重写时自动跳过 `<script>` 内联脚本内容（不再尝试改写脚本）。
- `reverse`: 避免将纯分隔符 `"/"` 误当作 URL 重写（修复 Navidrome 等应用出现 `#/navi//navi/...` 这类路径爆炸）；并在 `Content-Type` 不准确时按 `.js/.mjs` 后缀识别为 JS，从而禁用文本重写以避免破坏 bundle。
- `reverse`: 新增 TCP-over-WebSocket（CDN/反代友好）：在每个路由的精确路径 `/<route>`（无尾斜杠）上协商子协议 `sudoku-tcp-v1` 并转发任意 TCP 到客户端 `target`。
- `reverse`: 反向代理支持纯 TCP 转发：当 `reverse.routes[].path` 为空时，`reverse.listen` 上的非 HTTP 入站连接会被当作原始 TCP 流量转发到客户端目标（每个入口仅支持 1 条 TCP 路由）。
- `cli`: 新增内置本地端口转发器：`-rev-dial` / `-rev-listen` / `-rev-insecure`（用于把本地 TCP 通过 TCP-over-WebSocket 隧道转发）。
- `tunnel`: 修复双向 Pipe 在对端不支持 half-close 时的关闭行为，避免隧道连接“卡住不退出”，使内置转发器（含 SSH）稳定可用。
- `tunnel`: 修复 mux 会话在对端异常断开（`ECONNRESET`）时被误判为错误导致的偶发失败，提升 CI 与压力测试稳定性。
- `tests`: 新增 BDD 场景测试，模拟 TLS Edge/CDN、内置转发器与 HTTPMask 隧道，验证 HTTP 子路径与 TCP-over-WS 可用性。
- `deps`: 替换弃用的 `nhooyr.io/websocket` 为 `github.com/coder/websocket`。
- `ci`: release workflow 在构建发布产物前先跑严格测试，避免未验证版本被发布。
- `docs`: 补充反向代理与 TCP-over-WebSocket 的使用说明（含配置与命令示例）。

## v0.2.1（2026-01-30）
- `reverse`: 修复子路径挂载场景（如 `/gitea`）下静态资源/跳转路径丢前缀的问题：在 `strip_prefix=true` 时自动回写 `Location`、`Set-Cookie Path` 并重写 HTML/CSS/JS 中的根路径引用（如 `"/assets"`、`url(/assets)`）。

## v0.2.0（2026-01-29）
- `config`: `config.json` 新增 `httpmask` 对象统一管理 HTTP 相关字段，并保持对旧字段的向后兼容。
- `proxy`: 新增链式代理（多跳嵌套隧道）。
- `reverse`: 新增反向代理（服务端按路径前缀访问客户端本地 HTTP 服务）。
- `httpmask`: 过 CDN 默认使用 split-stream；移除 stream-one 路径。
- `perf`: Sudoku padding 概率逻辑移除 float；新增 `make bench` 与 `make pprof-sudoku`。
- `docker`: 新增服务端 Docker 镜像构建（Dockerfile）并在 tag 发布时推送到 GHCR。
- `apis`: 新增链式代理配置与更严格的校验测试。

## 版本概览
- v0.2.0：链式代理、反向代理、配置结构化（httpmask）、CDN 默认 split-stream、性能/benchmark 工具、Docker 镜像、API 对齐增强。
- v0.0.7：移除旧的分离下行实现，新增 `enable_pure_downlink` 开关（默认纯数独下行，可关闭以启用 6bit 拆分下行并提升带宽）；API/CLI 同步支持 UoT；改进 HTTP 伪装与回落。
- v0.0.6：初版 Sudoku 混淆 + AEAD 加密 + HTTP 伪装，支持 PAC/HTTP/SOCKS 混合代理。
- **v0.0.5**：新增 UoT（UDP over TCP）与 SOCKS5 UDP 支持，完善极端场景测试与 PR 自动化验证。
- **v old）**：优化数独连接性能与资源管理（ab6f00b）；补充文档入口（5890267）。
- **v0.1.3（2025-11-25）**：CLI 增强支持拆分密钥生成；握手新增 SHA-256 鉴权与错误细分；Ed25519 推导与拆分；配置描述/指引优化。
- **v0.1.2（2025-11-24）**：默认 Mieru 配置与修复初始化；连接缓冲/回放能力增强；HTTP 头处理与性能优化；SOCKS4、DNS 缓存、配置默认值加载与更多健壮性修复；新增标准模式测试。
- **v0.1.1（2025-11-24）**：新增协议 API；修复缓冲接口的空指针风险。
- **v0.0.ι（2025-11-24）**：HTTP 伪装与分离隧道支持；Mieru 下行隧道实现。
- **v0.0.γ（2025-11-23）**：Mieru 分离隧道初版，完善文档。
- **v0.0.α（2025-11-22）**：发布流程拆分；PAC 调试；YAML 规则、代理模式默认值与配置清理。
- **v0.0.9 / v0.0.8 / v0.0.7 / v0.0.5 / v0.0.4 / v0.0.3 / v0.0.2 / v0.0.1**：核心 Sudoku ASCII 协议、SOCKS5+PAC，逐步加入 ASCII 模式、多协议混合代理与规则下载。

## 完整提交时间线
- 2025-11-26 5890267 docs: add initial project README.
- 2025-11-26 ab6f00b refactor(sudoku): 重构数独连接以提高性能和资源管理
- 2025-11-25 7177bf1 (v0.1.3) feat(cli): enhance key generation with split key support
- 2025-11-25 ba07aed feat(security): enhance handshake authentication with SHA-256 hashing
- 2025-11-25 1677cb6 feat(config): update key generation instructions and improve mieru integration
- 2025-11-25 a27b3d9 feat(crypto): implement Ed25519 key derivation and splitting
- 2025-11-25 3b2c7c7 feat(api): enhance Sudoku protocol handshake with detailed error handling
- 2025-11-25 fe8915e refactor(config): clarify ASCII mode description and optimize logic
- 2025-11-24 7fec754 (v0.1.2) fix(config): correct mieru config initialization logic
- 2025-11-24 ab3a69d feat(config): implement default mieru config when enabled but not set
- 2025-11-24 5ff9eb4 feat(obfs): improve http header consumption and fallback handling
- 2025-11-24 db26ba8 feat(tunnel): enhance BufferedConn with data recording and retrieval
- 2025-11-24 7dee241 refactor(obfs/sudoku): reimplement connection management and buffering
- 2025-11-24 ace9e6b fix(obfs/sudoku): add nil pointer checks to prevent panics
- 2025-11-24 ee0e103 fix: Enhance connection safety and prevent panics with nil and type assertion checks across various connection types.
- 2025-11-24 c7a28d7 perf: improve obfuscation performance by reducing allocations and adding benchmarks.
- 2025-11-24 8e1c4cf feat: introduce configuration loading with default value application and remove specific HTTP masker content types.
- 2025-11-24 6c19c88 feat: Add SOCKS4 proxy support, implement DNS caching, and include unit tests for protocol handlers.
- 2025-11-24 716ac89 test: add `SudokuTunnel_Standard` test case for standard mode operation.
- 2025-11-24 8bd57ec feat: Abstract client proxy connection logic with a new `tunnel.Dialer` interface, improve hybrid manager's connection
- 2025-11-24 0cfc93a Antigravaty changed
- 2025-11-24 b7d9b0b (v0.1.1) fix(obfs): handle nil pointer in GetBufferedAndRecorded method
- 2025-11-24 c61b38e feat(api): implement Sudoku protocol client and server APIs
- 2025-11-24 57b783e (v0.0.ι) feat(proxy): implement HTTP masking and split tunneling support
- 2025-11-23 843b040 feat(hybrid): implement mieru-based downlink tunneling
- 2025-11-23 9686484 (v0.0.γ) feat(hybrid): implement split tunneling with Mieru integration
- 2025-11-22 806011e docs(readme): add link to Chinese documentation
- 2025-11-22 45f5f07 docs(readme): refine documentation and clarify protocol features
- 2025-11-22 b5a2c25 (v0.0.α) chore(release): split build and release workflows
- 2025-11-22 2c831b1 debug(config): add pac proxy mode support
- 2025-11-22 f61506f (v0.0.9) feat(geodata): support YAML format for rule parsing
- 2025-11-22 14ee4d6 refactor(config): remove legacy geoip_url and update default proxy mode
- 2025-11-22 65bc6a0 (v0.0.8) feat(client): implement mixed protocol proxy with HTTP/SOCKS5 support
- 2025-11-22 45c5e81 feat(client): implement mixed protocol proxy with HTTP/SOCKS5 support
- 2025-11-21 1f2130e docs(readme): translate and restructure documentation content
- 2025-11-21 a87cf9e (v0.0.7) feat(obfs): implement ASCII mode for Sudoku obfuscation
- 2025-11-21 9d3ac27 feat(obfs): implement ASCII mode for Sudoku obfuscation
- 2025-11-21 fec2ad4 (v0.0.5) feat(obfs): implement ASCII mode for Sudoku obfuscation
- 2025-11-21 8cb8d3a docs(readme): update README with badges, TODO section, and running instructions
- 2025-11-21 5d40e57 (v0.0.4, v0.0.3) feat(proxy): implement SOCKS5 proxy with PAC routing support
- 2025-11-20 aee2734 (v0.0.2, v0.0.1) feat(core): implement sudoku ascii traffic obfuscation protocol
- 2025-11-20 067240f Initial commit
