package tunnel

import "time"

type bandwidthSample struct {
	t   time.Time
	len int
}

// BandwidthMonitor tracks rolling throughput and signals when a connection
// should be upgraded to the high-bandwidth codec.
type BandwidthMonitor struct {
	window      []bandwidthSample
	total       int64
	threshold   int64
	windowDur   time.Duration
	triggered   bool
	pendingTick bool
}

func NewBandwidthMonitor(thresholdBytes int64, window time.Duration) *BandwidthMonitor {
	return &BandwidthMonitor{
		window:    make([]bandwidthSample, 0, 16),
		threshold: thresholdBytes,
		windowDur: window,
	}
}

// Add records a newly delivered payload size and returns true when an upgrade
// should be initiated (only once).
func (m *BandwidthMonitor) Add(n int) bool {
	if n <= 0 {
		return false
	}
	now := time.Now()
	m.window = append(m.window, bandwidthSample{t: now, len: n})
	m.total += int64(n)

	// Trim stale samples
	cutoff := now.Add(-m.windowDur)
	trim := 0
	for trim < len(m.window) && m.window[trim].t.Before(cutoff) {
		m.total -= int64(m.window[trim].len)
		trim++
	}
	if trim > 0 {
		m.window = m.window[trim:]
	}

	if m.triggered {
		return false
	}

	if m.total >= m.threshold {
		if m.pendingTick {
			m.triggered = true
			return true
		}
		m.pendingTick = true
	} else {
		m.pendingTick = false
	}

	return false
}
