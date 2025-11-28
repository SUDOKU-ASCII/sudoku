/*
Copyright (C) 2025 by ふたい <contact me via issue>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
*/
package apis

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/saba-futai/sudoku/internal/protocol"
	"github.com/saba-futai/sudoku/pkg/crypto"
	"github.com/saba-futai/sudoku/pkg/obfs/httpmask"
	"github.com/saba-futai/sudoku/pkg/obfs/sudoku"
)

// bufferedConn 这是一个内部辅助结构，用于将 bufio 多读的数据传递给后续层
// 必须实现 net.Conn
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (bc *bufferedConn) Read(p []byte) (int, error) {
	return bc.r.Read(p)
}

// ServerHandshake 执行 Sudoku 服务端握手逻辑
// 输入: rawConn (刚 Accept 的 TCP 连接)
// 输出:
//  1. tunnelConn: 解密后的透明连接，可直接用于应用层数据传输
//  2. targetAddr: 客户端想要访问的目标地址
//  3. error: 如果是 *HandshakeError 类型，包含了用于 Fallback 的完整数据
//
// 握手过程分为多个层次：
//  1. HTTP 伪装层：验证 HTTP POST 请求头
//  2. Sudoku 混淆层：解码 Sudoku 谜题
//  3. AEAD 加密层：解密并验证数据
//  4. 协议层：验证时间戳握手、读取目标地址
//
// 任何层次失败都会返回 HandshakeError，其中包含该层及之前所有层读取的数据
func ServerHandshake(rawConn net.Conn, cfg *ProtocolConfig) (net.Conn, string, error) {
	if cfg == nil {
		return nil, "", fmt.Errorf("config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, "", fmt.Errorf("invalid config: %w", err)
	}

	// 设置握手总超时，防止慢速攻击占用资源
	deadline := time.Now().Add(time.Duration(cfg.HandshakeTimeoutSeconds) * time.Second)
	rawConn.SetReadDeadline(deadline)

	// 0. HTTP 头处理层 (读取并丢弃伪装头，同时记录字节)
	bufReader := bufio.NewReader(rawConn)

	// 自动检测逻辑：
	// 1. 如果 DisableHTTPMask = true，则直接跳过检测
	// 2. 如果 DisableHTTPMask = false，则 Peek 前 4 字节
	//    - 如果是 "POST"，则认为是 HTTP 伪装，进行 ConsumeHeader
	//    - 否则认为是无伪装模式，跳过 ConsumeHeader

	shouldConsumeMask := false
	var httpHeaderData []byte

	if !cfg.DisableHTTPMask {
		peekBytes, err := bufReader.Peek(4)
		if err == nil && string(peekBytes) == "POST" {
			shouldConsumeMask = true
		}
		// 如果 Peek 失败（比如数据不足），这里不处理，留给后续 Read 处理或者超时
		// 但通常 TCP 连接建立后应该能读到数据
	}

	if shouldConsumeMask {
		var err error
		httpHeaderData, err = httpmask.ConsumeHeader(bufReader)
		if err != nil {
			// HTTP 头都不对，直接返回错误，此时还没进入 Sudoku 层
			// 这里的错误通常意味着非 HTTP 流量或格式错误
			rawConn.SetReadDeadline(time.Time{})
			return nil, "", &HandshakeError{
				Err:            fmt.Errorf("invalid http header: %w", err),
				RawConn:        rawConn,
				HTTPHeaderData: httpHeaderData,
				ReadData:       nil,
			}
		}
	}

	// 构造 BufferedConn，防止 bufReader 预读的数据丢失
	bConn := &bufferedConn{
		Conn: rawConn,
		r:    bufReader,
	}

	// 1. Sudoku 层 (开启记录模式，以便握手失败时能提取原始数据用于 Fallback)
	sConn := sudoku.NewConn(bConn, cfg.Table, cfg.PaddingMin, cfg.PaddingMax, true)

	// 定义一个清理函数，用于在失败时关闭连接并返回特定错误
	fail := func(originalErr error) (net.Conn, string, error) {
		rawConn.SetReadDeadline(time.Time{})
		badData := sConn.GetBufferedAndRecorded() // 获取所有已读取的字节
		return nil, "", &HandshakeError{
			Err:            originalErr,
			RawConn:        rawConn,
			HTTPHeaderData: httpHeaderData,
			ReadData:       badData,
		}
	}

	// 2. 加密层
	cConn, err := crypto.NewAEADConn(sConn, cfg.Key, cfg.AEADMethod)
	if err != nil {
		return fail(fmt.Errorf("crypto setup failed: %w", err))
	}

	// 3. 验证内部握手 (Timestamp)
	handshakeBuf := make([]byte, 16)
	if _, err := io.ReadFull(cConn, handshakeBuf); err != nil {
		// 如果解密失败或读取不足，这里会报错
		cConn.Close()
		return fail(fmt.Errorf("read handshake failed: %w", err))
	}

	ts := int64(binary.BigEndian.Uint64(handshakeBuf[:8]))
	now := time.Now().Unix()

	// 允许 60 秒的时间偏差
	if abs(now-ts) > 60 {
		cConn.Close()
		return fail(fmt.Errorf("timestamp skew/replay detected: server_time=%d client_time=%d", now, ts))
	}

	// 握手成功，停止录制数据，释放内存
	sConn.StopRecording()

	// 4. 读取目标地址
	targetAddr, _, _, err := protocol.ReadAddress(cConn)
	if err != nil {
		cConn.Close()
		return fail(fmt.Errorf("read target address failed: %w", err))
	}

	// 握手全部完成，取消超时限制
	rawConn.SetReadDeadline(time.Time{})

	return cConn, targetAddr, nil
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
