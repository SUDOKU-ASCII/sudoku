# Sudoku API (Standard)

面向其他开发者开放的纯 Sudoku 协议 API：HTTP 伪装 + 数独 ASCII/Entropy 混淆 + AEAD 加密，支持自动下行提速与 UoT (UDP over TCP)。**旧版 hybrid/Mieru 分离逻辑已标记弃用，不再推荐接入。**

## 安装
- 推荐指定已有 tag：`go get github.com/saba-futai/sudoku@v0.1.0`
- 或者直接跟随最新提交：`go get github.com/saba-futai/sudoku`

## 配置要点
- 表格：`table := sudoku.NewTable("your-seed", "prefer_ascii"|"prefer_entropy")`（两端一致）。
- 密钥：任意字符串即可，需两端一致，可用 `./sudoku -keygen` 或 `crypto.GenerateMasterKey` 生成。
- AEAD：`chacha20-poly1305`（默认）或 `aes-128-gcm`，`none` 仅测试用。
- 填充：`PaddingMin`/`PaddingMax` 为 0-100 的概率百分比。
- 客户端：设置 `ServerAddress`、`TargetAddress`。
- 服务端：可设置 `HandshakeTimeoutSeconds` 限制握手耗时。
- 下行提速：`EnableDownlinkBoost` 默认开启，5 秒内真实下行超过 12MB 时自动协商切换高带宽下行编码。
- UDP：`DialUoT` / `ServerHandshakeWithUoT` 提供 UoT 支持。

## 客户端示例
```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/saba-futai/sudoku/apis"
	"github.com/saba-futai/sudoku/pkg/obfs/sudoku"
)

func main() {
	cfg := &apis.ProtocolConfig{
		ServerAddress: "1.2.3.4:8443",
		TargetAddress: "example.com:443",
		Key:           "shared-key-hex-or-plain",
		AEADMethod:    "chacha20-poly1305",
		Table:         sudoku.NewTable("seed-for-table", "prefer_ascii"),
		PaddingMin:    5,
		PaddingMax:    15,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := apis.Dial(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// conn 即已完成握手的隧道，可直接读写应用层数据
}
```

## 服务端示例
```go
package main

import (
	"io"
	"log"
	"net"

	"github.com/saba-futai/sudoku/apis"
	"github.com/saba-futai/sudoku/pkg/obfs/sudoku"
)

func main() {
	cfg := &apis.ProtocolConfig{
		Key:                     "shared-key-hex-or-plain",
		AEADMethod:              "chacha20-poly1305",
		Table:                   sudoku.NewTable("seed-for-table", "prefer_ascii"),
		PaddingMin:              5,
		PaddingMax:              15,
		HandshakeTimeoutSeconds: 5,
	}

	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal(err)
	}
	for {
		rawConn, err := ln.Accept()
		if err != nil {
			log.Println("accept:", err)
			continue
		}
		go func(c net.Conn) {
			defer c.Close()

			tunnel, target, err := apis.ServerHandshake(c, cfg)
			if err != nil {
				// 握手失败时可按需 fallback；HandshakeError 携带已读数据
				log.Println("handshake:", err)
				return
			}
			defer tunnel.Close()

			up, err := net.Dial("tcp", target)
			if err != nil {
				log.Println("dial target:", err)
				return
			}
			defer up.Close()

			go io.Copy(up, tunnel)
			io.Copy(tunnel, up)
		}(rawConn)
	}
}
```

## 说明
- `DefaultConfig()` 提供合理默认值，仍需设置 `Key`、`Table` 及对应的地址字段。
- 服务端如需回落（HTTP/原始 TCP），可从 `HandshakeError` 取出 `HTTPHeaderData` 与 `ReadData` 按顺序重放。
- 需要 hybrid/Mieru 上下行分离时请继续使用项目内置 CLI；该 API 仅覆盖标准单通道模式，且 Mieru 已进入弃用周期。
- UDP-over-TCP：使用 `DialUoT` 与 `ServerHandshakeWithUoT` 组合，可直接在隧道内传输 UDP 数据报。
