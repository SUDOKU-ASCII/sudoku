package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SUDOKU-ASCII/sudoku/internal/tunnel"
)

// TestWAN200msLossyConcurrentUploadMuxStability exercises the exact failure
// mode users reported on real international links: several mux streams pushing
// uplink traffic at the same time over a 200ms RTT, lossy, jittery carrier.
// It fails if any single 8KiB round trip stalls beyond the production threshold.
func TestWAN200msLossyConcurrentUploadMuxStability(t *testing.T) {
	requireWANSimulation(t)

	duration := wanUploadDuration(t)
	harness := newWANMuxHarness(t, wanProfile{
		oneWayDelay:    100 * time.Millisecond,
		jitter:         20 * time.Millisecond,
		retransmitEach: 23,
		retransmitWait: 200 * time.Millisecond,
	})

	echoAddr := startWANTarget(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})

	clientCfg := newWANClientConfig(t, "auto", harness.proxy.addr())
	clientCfg.Multiplex = "on"
	clientCfg.HTTPMask.Multiplex = "on"
	if err := clientCfg.Finalize(); err != nil {
		t.Fatalf("finalize upload WAN client: %v", err)
	}
	dialer := &tunnel.MuxDialer{BaseDialer: tunnel.BaseDialer{
		Config: clientCfg,
		Tables: harness.tables,
	}}
	defer dialer.Close()

	warmCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := dialer.Warm(warmCtx); err != nil {
		t.Fatalf("warm mux: %v", err)
	}

	const (
		concurrency    = 16
		chunkSize      = 8 * 1024
		stallThreshold = 15 * time.Second
	)

	deadline := time.Now().Add(duration)
	var (
		wg          sync.WaitGroup
		totalBytes  atomic.Int64
		totalChunks atomic.Int64
		stalls      atomic.Int64
		maxStallUs  atomic.Int64
	)
	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			conn, err := dialer.Dial(echoAddr)
			if err != nil {
				errCh <- fmt.Errorf("worker %d dial: %w", idx, err)
				return
			}
			defer conn.Close()

			payload := make([]byte, chunkSize)
			for j := range payload {
				payload[j] = byte((idx*31 + j) % 251)
			}
			buf := make([]byte, chunkSize)

			for time.Now().Before(deadline) {
				start := time.Now()
				_ = conn.SetWriteDeadline(time.Now().Add(stallThreshold))
				if err := writeFull(conn, payload); err != nil {
					errCh <- fmt.Errorf("worker %d write: %w", idx, err)
					return
				}
				_ = conn.SetReadDeadline(time.Now().Add(stallThreshold))
				if _, err := io.ReadFull(conn, buf); err != nil {
					errCh <- fmt.Errorf("worker %d read: %w", idx, err)
					return
				}
				if !bytes.Equal(buf, payload) {
					errCh <- fmt.Errorf("worker %d echo mismatch", idx)
					return
				}
				elapsed := time.Since(start)
				totalBytes.Add(chunkSize)
				totalChunks.Add(1)
				if elapsed > stallThreshold {
					stalls.Add(1)
					us := elapsed.Microseconds()
					for {
						cur := maxStallUs.Load()
						if us <= cur || maxStallUs.CompareAndSwap(cur, us) {
							break
						}
					}
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("upload worker failed: %v", err)
	}
	if stalls.Load() > 0 {
		t.Fatalf("saw %d stalled round-trips (max stall=%v); production-visible stall detected",
			stalls.Load(), time.Duration(maxStallUs.Load())*time.Microsecond)
	}
	t.Logf("concurrent upload WAN stability: duration=%v workers=%d bytes=%d chunks=%d max_stall=%v",
		duration, concurrency, totalBytes.Load(), totalChunks.Load(),
		time.Duration(maxStallUs.Load())*time.Microsecond)
}

func wanUploadDuration(t testing.TB) time.Duration {
	t.Helper()
	const defaultDuration = 60 * time.Second
	raw := strings.TrimSpace(os.Getenv("SUDOKU_WAN_UPLOAD_DURATION"))
	if raw == "" {
		return defaultDuration
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		return time.Duration(seconds) * time.Second
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		t.Fatalf("invalid SUDOKU_WAN_UPLOAD_DURATION=%q", raw)
	}
	return duration
}
