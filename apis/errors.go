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
	"fmt"
	"net"
)

// HandshakeError 包装了握手过程中的错误
// 如果发生此错误，调用者可以通过 RawConn、HTTPHeaderData 和 ReadData 进行回落处理
//
// 字段说明：
//   - Err: 原始错误，说明握手失败的具体原因
//   - RawConn: 原始 TCP 连接，可用于回落处理
//   - HTTPHeaderData: HTTP 伪装层读取的头部字节（在 ConsumeHeader 阶段收集）
//   - ReadData: Sudoku 层读取并记录的字节（在 Sudoku 解码阶段收集）
//
// 回落处理时的数据重放顺序：
//  1. 首先写入 HTTPHeaderData（如果非空）
//  2. 然后写入 ReadData（如果非空）
//  3. 最后转发 RawConn 中的剩余数据
//
// 示例用法：
//
//	conn, target, err := apis.ServerHandshake(rawConn, cfg)
//	if err != nil {
//	    var hsErr *apis.HandshakeError
//	    if errors.As(err, &hsErr) {
//	        // 可以进行回落处理
//	        fallbackConn, _ := net.Dial("tcp", fallbackAddr)
//	        fallbackConn.Write(hsErr.HTTPHeaderData)
//	        fallbackConn.Write(hsErr.ReadData)
//	        io.Copy(fallbackConn, hsErr.RawConn)
//	        io.Copy(hsErr.RawConn, fallbackConn)
//	    }
//	    return
//	}
type HandshakeError struct {
	Err            error
	RawConn        net.Conn
	HTTPHeaderData []byte // HTTP 伪装层头部数据
	ReadData       []byte // Sudoku 层已读取的数据
}

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("sudoku handshake failed: %v", e.Err)
}

func (e *HandshakeError) Unwrap() error {
	return e.Err
}
