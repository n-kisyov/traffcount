package collector

import "testing"

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

