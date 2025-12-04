package tunnel

import (
	"errors"
	"net"

	"github.com/saba-futai/sudoku/pkg/obfs/sudoku"
)

// ManagedConn keeps a reference to the underlying Sudoku connection so callers
// can toggle advanced features (e.g., high-bandwidth downlink) without leaking
// transport details elsewhere.
type ManagedConn struct {
	net.Conn
	obfs *sudoku.Conn
}

func NewManagedConn(base net.Conn, obfs *sudoku.Conn) *ManagedConn {
	return &ManagedConn{
		Conn: base,
		obfs: obfs,
	}
}

// EnableBoost toggles high-bandwidth codec for either direction.
func (m *ManagedConn) EnableBoost(write, read bool, aesKey, iv []byte, isASCII bool) error {
	if m == nil || m.obfs == nil {
		return errors.New("boost not supported on this connection")
	}
	return m.obfs.EnableBoost(write, read, aesKey, iv, isASCII)
}

func (m *ManagedConn) BoostSupported() bool {
	return m != nil && m.obfs != nil
}

// ExtractManagedConn unwraps known wrappers to retrieve ManagedConn.
func ExtractManagedConn(conn net.Conn) (*ManagedConn, bool) {
	if mc, ok := conn.(*ManagedConn); ok {
		return mc, true
	}
	if pb, ok := conn.(*PreBufferedConn); ok {
		return ExtractManagedConn(pb.Conn)
	}
	return nil, false
}
