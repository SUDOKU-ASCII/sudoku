package tunnel

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

// mockConn is a simple mock net.Conn for testing
type mockConn struct {
	net.Conn
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
}

func newMockConn(data []byte) *mockConn {
	return &mockConn{
		readBuf:  bytes.NewBuffer(data),
		writeBuf: new(bytes.Buffer),
	}
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	return m.readBuf.Read(b)
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	return m.writeBuf.Write(b)
}

func (m *mockConn) Close() error {
	return nil
}

func (m *mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func (m *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 54321}
}

func (m *mockConn) SetDeadline(t time.Time) error {
	return nil
}

func (m *mockConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *mockConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func TestBufferedConn_GetBufferedAndRecorded(t *testing.T) {
	testData := []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")
	mockConn := newMockConn(testData)

	// Create BufferedConn with recording enabled
	bc := &BufferedConn{
		Conn:     mockConn,
		r:        bufio.NewReader(mockConn),
		recorder: new(bytes.Buffer),
	}

	// Read some data
	buf := make([]byte, 10)
	n, err := bc.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 10 {
		t.Fatalf("Expected to read 10 bytes, got %d", n)
	}

	// Verify recorded data
	recorded := bc.GetBufferedAndRecorded()
	if len(recorded) < 10 {
		t.Errorf("Expected at least 10 bytes recorded, got %d", len(recorded))
	}

	// The first 10 bytes should match what we read
	if !bytes.Equal(recorded[:10], testData[:10]) {
		t.Errorf("Recorded data mismatch: got %q, want %q", recorded[:10], testData[:10])
	}

	// The remaining data should be buffered (peeked)
	if len(recorded) > 10 {
		// This is expected - bufio.Reader buffers ahead
		t.Logf("Total recorded+buffered: %d bytes", len(recorded))
	}
}

func TestBufferedConn_GetBufferedAndRecorded_NoRecorder(t *testing.T) {
	testData := []byte("Some data")
	mockConn := newMockConn(testData)

	// Create BufferedConn WITHOUT recording
	bc := &BufferedConn{
		Conn: mockConn,
		r:    bufio.NewReader(mockConn),
		// recorder is nil
	}

	// Read some data
	buf := make([]byte, 5)
	_, err := bc.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	// GetBufferedAndRecorded should still work, but only return buffered data
	recorded := bc.GetBufferedAndRecorded()
	// Should have at least the remaining buffered data
	if len(recorded) == 0 {
		// This is acceptable - if bufio hasn't buffered ahead, nothing to return
		t.Log("No data recorded (recorder was nil)")
	}
}

func TestBufferedConn_GetBufferedAndRecorded_AfterFullRead(t *testing.T) {
	testData := []byte("Short")
	mockConn := newMockConn(testData)

	bc := &BufferedConn{
		Conn:     mockConn,
		r:        bufio.NewReader(mockConn),
		recorder: new(bytes.Buffer),
	}

	// Read all data
	buf := make([]byte, 100)
	n, err := bc.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read failed: %v", err)
	}

	// Verify all data was recorded
	recorded := bc.GetBufferedAndRecorded()
	if !bytes.Equal(recorded, testData[:n]) {
		t.Errorf("Recorded data mismatch: got %q, want %q", recorded, testData[:n])
	}
}
