// pkg/obfs/httpmask/masker.go
package httpmask

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"time"
)

var (
	userAgents = []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/119.0",
	}
	accepts = []string{
		"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"application/json, text/plain, */*",
		"application/octet-stream",
		"*/*",
	}
	paths = []string{
		"/api/v1/upload",
		"/data/sync",
		"/uploads/raw",
		"/api/report",
		"/feed/update",
	}
	contentTypes = []string{
		"application/octet-stream",
		"application/x-protobuf",
	}
)

var (
	rngPool = sync.Pool{
		New: func() interface{} {
			return rand.New(rand.NewSource(time.Now().UnixNano()))
		},
	}
	headerBufPool = sync.Pool{
		New: func() interface{} {
			b := make([]byte, 0, 1024)
			return &b
		},
	}
)

// WriteRandomRequestHeader 向 writer 写入一个伪造的 HTTP POST 请求头
func WriteRandomRequestHeader(w io.Writer, host string) error {
	// Get RNG from pool
	r := rngPool.Get().(*rand.Rand)
	defer rngPool.Put(r)

	path := paths[r.Intn(len(paths))]
	ua := userAgents[r.Intn(len(userAgents))]
	ctype := contentTypes[r.Intn(len(contentTypes))]

	// 随机 Content-Length
	contentLength := 1024*1024*1024 + r.Int63n(1024*1024*1024)

	// Use buffer pool
	bufPtr := headerBufPool.Get().(*[]byte)
	buf := *bufPtr
	buf = buf[:0]
	defer func() {
		if cap(buf) <= 4096 {
			*bufPtr = buf
			headerBufPool.Put(bufPtr)
		}
	}()

	// Append directly to buffer
	buf = append(buf, "POST "...)
	buf = append(buf, path...)
	buf = append(buf, " HTTP/1.1\r\nHost: "...)
	buf = append(buf, host...)
	buf = append(buf, "\r\nUser-Agent: "...)
	buf = append(buf, ua...)
	buf = append(buf, "\r\nContent-Type: "...)
	buf = append(buf, ctype...)
	buf = append(buf, "\r\nContent-Length: "...)
	buf = fmt.Appendf(buf, "%d", contentLength) // Go 1.19+
	buf = append(buf, "\r\nConnection: keep-alive\r\nCache-Control: no-cache\r\n\r\n"...)

	_, err := w.Write(buf)
	return err
}

// ConsumeHeader 读取并消耗 HTTP 头部，返回消耗的数据和剩余的 reader 数据
// 如果不是 POST 请求或格式严重错误，返回 error
func ConsumeHeader(r *bufio.Reader) ([]byte, error) {
	var consumed bytes.Buffer

	// 1. 读取请求行
	// Use ReadSlice to avoid allocation if line fits in buffer
	line, err := r.ReadSlice('\n')
	if err != nil {
		return nil, err
	}
	consumed.Write(line)

	// 简单校验方法，必须是 POST
	if len(line) < 4 || !bytes.Equal(line[:4], []byte("POST")) {
		return consumed.Bytes(), fmt.Errorf("invalid method or garbage: %s", string(line))
	}

	// 2. 循环读取头部，直到遇到空行
	for {
		line, err = r.ReadSlice('\n')
		if err != nil {
			return consumed.Bytes(), err
		}
		consumed.Write(line)

		// Check for empty line (\r\n or \n)
		// ReadSlice includes the delimiter
		n := len(line)
		if n == 2 && line[0] == '\r' && line[1] == '\n' {
			return consumed.Bytes(), nil
		}
		if n == 1 && line[0] == '\n' {
			return consumed.Bytes(), nil
		}
	}
}
