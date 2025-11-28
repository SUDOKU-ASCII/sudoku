package app

import (
	"io"
	"net"
	"sync"
)

// copyBufferPool reuses buffers for bidirectional piping to reduce GC churn.
var copyBufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 32*1024)
	},
}

func pipeConn(a, b net.Conn) {
	var once sync.Once

	closeBoth := func() {
		_ = a.Close()
		_ = b.Close()
	}

	go func() {
		copyOneWay(a, b)
		once.Do(closeBoth)
	}()

	copyOneWay(b, a)
	once.Do(closeBoth)
}

func copyOneWay(dst io.Writer, src io.Reader) {
	buf := copyBufferPool.Get().([]byte)
	defer copyBufferPool.Put(buf)
	_, _ = io.CopyBuffer(dst, src, buf)
}
