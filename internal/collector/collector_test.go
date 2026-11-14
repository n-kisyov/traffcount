package collector

import (
	"testing"
	"time"

	"traffcount/internal/storage"
)

func TestComputeDelta(t *testing.T) {
	// Normal increment
	if got := ComputeDelta(1000, 1500); got != 500 {
		t.Errorf("expected 500, got %d", got)
	}
	// No change
	if got := ComputeDelta(1000, 1000); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
	// Reboot / adapter reset where current < previous
	if got := ComputeDelta(500000, 1200); got != 1200 {
		t.Errorf("expected 1200 on counter reset, got %d", got)
	}
}

func TestMakeInterfaceID(t *testing.T) {
	id := MakeInterfaceID("Ethernet", "AA:BB:CC:DD:EE:FF")
	if id != "Ethernet_AABBCCDDEEFF" {
		t.Errorf("unexpected id: %s", id)
	}
}

func TestCollectorResetBuffers(t *testing.T) {
	c := New(nil)
	c.counters["eth0"] = &rawCounter{sessionRx: 5000, sessionTx: 1200}
	c.pendingDB["eth0"] = &storage.TrafficDelta{
		IfaceID:   "eth0",
		RxBytes:   5000,
		TxBytes:   1200,
		Timestamp: time.Now(),
	}
	c.ResetBuffers()

	if len(c.pendingDB) != 0 {
		t.Errorf("expected pendingDB cleared after ResetBuffers, got %d", len(c.pendingDB))
	}
	if c.counters["eth0"].sessionRx != 0 || c.counters["eth0"].sessionTx != 0 {
		t.Errorf("expected session counters zeroed, got rx=%d tx=%d", c.counters["eth0"].sessionRx, c.counters["eth0"].sessionTx)
	}
}

