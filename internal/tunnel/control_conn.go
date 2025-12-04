package tunnel

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net"
	"sync"
)

const (
	controlVersion byte = 0x01
	hmacLen             = 16
	readChunkSize       = 32 * 1024
)

const (
	ControlCmdBoostRequest byte = 0x01
	ControlCmdBoostAck     byte = 0x02
)

var controlMagic = []byte{0xF7, 'H', 'B', 'C'}

// ControlConn filters control frames out of the data stream and exposes a
// handler for them. Normal reads return only payload data.
type ControlConn struct {
	net.Conn
	hmacKey []byte
	handler func(cmd byte, payload []byte)
	onData  func(int)

	readBuf []byte
	dataBuf []byte
	wMu     sync.Mutex
}

func DeriveControlKey(seed string) []byte {
	sum := sha256.Sum256([]byte(seed + "|hb-control"))
	return sum[:]
}

func NewControlConn(base net.Conn, hmacKey []byte, handler func(cmd byte, payload []byte), onData func(int)) *ControlConn {
	return &ControlConn{
		Conn:    base,
		hmacKey: hmacKey,
		handler: handler,
		onData:  onData,
		readBuf: make([]byte, 0, 4096),
		dataBuf: make([]byte, 0, 4096),
	}
}

func (c *ControlConn) Write(p []byte) (int, error) {
	c.wMu.Lock()
	defer c.wMu.Unlock()
	return c.Conn.Write(p)
}

func (c *ControlConn) SendControl(cmd byte, payload []byte) error {
	if len(payload) > 0xFFFF {
		return errors.New("control payload too large")
	}
	header := make([]byte, 0, len(controlMagic)+1+1+2+hmacLen+len(payload))
	header = append(header, controlMagic...)
	header = append(header, controlVersion, cmd)

	lenField := make([]byte, 2)
	binary.BigEndian.PutUint16(lenField, uint16(len(payload)))
	header = append(header, lenField...)
	header = append(header, payload...)

	mac := hmac.New(sha256.New, c.hmacKey)
	mac.Write(header[len(controlMagic):]) // exclude magic
	fullMAC := mac.Sum(nil)
	header = append(header, fullMAC[:hmacLen]...)

	c.wMu.Lock()
	defer c.wMu.Unlock()
	_, err := c.Conn.Write(header)
	return err
}

func (c *ControlConn) Read(p []byte) (int, error) {
	for len(c.dataBuf) == 0 {
		buf := make([]byte, readChunkSize)
		n, err := c.Conn.Read(buf)
		if n > 0 {
			c.readBuf = append(c.readBuf, buf[:n]...)
			c.processBuffer()
		}
		if len(c.dataBuf) > 0 {
			break
		}
		if err != nil {
			return 0, err
		}
	}

	n := copy(p, c.dataBuf)
	if n == len(c.dataBuf) {
		c.dataBuf = c.dataBuf[:0]
	} else {
		c.dataBuf = c.dataBuf[n:]
	}
	return n, nil
}

func (c *ControlConn) processBuffer() {
	for {
		idx := bytes.Index(c.readBuf, controlMagic)
		if idx == -1 {
			if len(c.readBuf) > 0 {
				c.appendData(c.readBuf)
			}
			c.readBuf = c.readBuf[:0]
			return
		}

		if idx > 0 {
			c.appendData(c.readBuf[:idx])
			c.readBuf = c.readBuf[idx:]
		}

		if len(c.readBuf) < len(controlMagic)+1+1+2+hmacLen {
			// Wait for more bytes
			return
		}

		if !bytes.Equal(c.readBuf[:len(controlMagic)], controlMagic) {
			c.appendData(c.readBuf[:1])
			c.readBuf = c.readBuf[1:]
			continue
		}

		version := c.readBuf[len(controlMagic)]
		cmd := c.readBuf[len(controlMagic)+1]
		payloadLen := binary.BigEndian.Uint16(c.readBuf[len(controlMagic)+2 : len(controlMagic)+4])

		total := len(controlMagic) + 1 + 1 + 2 + int(payloadLen) + hmacLen
		if len(c.readBuf) < total {
			return
		}

		frame := c.readBuf[:total]
		payload := frame[len(controlMagic)+4 : len(controlMagic)+4+int(payloadLen)]
		macBytes := frame[total-hmacLen:]

		ok := version == controlVersion && c.verifyMAC(frame[len(controlMagic):total-hmacLen], macBytes)
		if ok {
			if c.handler != nil {
				c.handler(cmd, payload)
			}
			c.readBuf = c.readBuf[total:]
			continue
		}

		// Not a valid control frame, emit first byte as data to progress.
		c.appendData(c.readBuf[:1])
		c.readBuf = c.readBuf[1:]
	}
}

func (c *ControlConn) verifyMAC(body, macBytes []byte) bool {
	mac := hmac.New(sha256.New, c.hmacKey)
	mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(expected[:hmacLen], macBytes)
}

func (c *ControlConn) appendData(b []byte) {
	if len(b) == 0 {
		return
	}
	c.dataBuf = append(c.dataBuf, b...)
	if c.onData != nil {
		c.onData(len(b))
	}
}
