package tunnel

import (
	"testing"
	"time"
)

func TestBandwidthMonitorTrigger(t *testing.T) {
	monitor := NewBandwidthMonitor(12*1024*1024, 5*time.Second)

	if monitor.Add(6 * 1024 * 1024) {
		t.Fatalf("should not trigger on first chunk")
	}
	if monitor.Add(7 * 1024 * 1024) {
		t.Fatalf("should wait for additional traffic after crossing threshold")
	}
	if !monitor.Add(1) {
		t.Fatalf("expected trigger after additional bytes")
	}
	if monitor.Add(1) {
		t.Fatalf("trigger should fire only once")
	}
}
