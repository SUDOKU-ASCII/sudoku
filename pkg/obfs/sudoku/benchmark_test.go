package sudoku

import (
	"io"
	"math/rand"
	"net"
	"testing"
	"time"
)

// MockConn implements net.Conn for benchmarking
type MockConn struct {
	readBuf  []byte
	writeBuf []byte
}

func (m *MockConn) Read(b []byte) (n int, err error) {
	if len(m.readBuf) == 0 {
		return 0, io.EOF
	}
	n = copy(b, m.readBuf)
	m.readBuf = m.readBuf[n:]
	return n, nil
}

func (m *MockConn) Write(b []byte) (n int, err error) {
	m.writeBuf = append(m.writeBuf, b...)
	return len(b), nil
}

func (m *MockConn) Close() error                       { return nil }
func (m *MockConn) LocalAddr() net.Addr                { return nil }
func (m *MockConn) RemoteAddr() net.Addr               { return nil }
func (m *MockConn) SetDeadline(t time.Time) error      { return nil }
func (m *MockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *MockConn) SetWriteDeadline(t time.Time) error { return nil }

func BenchmarkSudokuWrite(b *testing.B) {
	key := "benchmark-key"
	table := NewTable(key, "prefer_ascii")
	mock := &MockConn{}
	conn := NewConn(mock, table, 10, 20, false)

	data := make([]byte, 1024)
	rand.Read(data)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mock.writeBuf = mock.writeBuf[:0] // Reset buffer
		conn.Write(data)
	}
}

func BenchmarkSudokuRead(b *testing.B) {
	key := "benchmark-key"
	table := NewTable(key, "prefer_ascii")

	// Pre-generate encoded data
	mock := &MockConn{}
	writerConn := NewConn(mock, table, 10, 20, false)
	data := make([]byte, 1024)
	rand.Read(data)
	writerConn.Write(data)
	encodedData := mock.writeBuf

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Reset reader state
		mock.readBuf = encodedData
		readerConn := NewConn(mock, table, 10, 20, false)
		buf := make([]byte, 1024)
		io.ReadFull(readerConn, buf)
	}
}
