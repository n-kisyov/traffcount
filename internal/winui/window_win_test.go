//go:build windows

package winui

import (
	"testing"
	"time"
	"unsafe"

	"traffcount/internal/storage"
)

func TestWindowPlacementStructLayout(t *testing.T) {
	if got := unsafe.Sizeof(windowPlacement{}); got != 44 {
		t.Fatalf("windowPlacement size = %d, want 44 (Win32 WINDOWPLACEMENT ABI)", got)
	}
	var wp windowPlacement
	if off := unsafe.Offsetof(wp.RcNormalPosition); off != 28 {
		t.Fatalf("RcNormalPosition offset = %d, want 28", off)
	}
}

func TestManagerDefaultsAndMiniBoundsPreservation(t *testing.T) {
	m := NewManager("http://127.0.0.1:9876", false)
	if m.IsMiniMode() {
		t.Fatalf("expected new Manager to start in normal mode (IsMiniMode() == false)")
	}

	normal := storage.WindowBounds{
		X:         240,
		Y:         120,
		Width:     1280,
		Height:    820,
		Maximized: false,
		HasValue:  true,
	}
	m.SetSavedBounds(normal)

	m.SetMiniMode(true)
	if !m.IsMiniMode() {
		t.Fatalf("expected IsMiniMode() == true after SetMiniMode(true)")
	}
	if got := m.GetSavedBounds(); got != normal {
		t.Fatalf("normal bounds overwritten in mini mode: got %+v, want %+v", got, normal)
	}

	m.SetMiniMode(false)
	if m.IsMiniMode() {
		t.Fatalf("expected IsMiniMode() == false after SetMiniMode(false)")
	}
	if got := m.GetSavedBounds(); got != normal {
		t.Fatalf("normal bounds lost after leaving mini mode: got %+v, want %+v", got, normal)
	}
}

func TestPlanMinimizeHidesWaitsForAnimationAndCleansUp(t *testing.T) {
	m := NewManager("http://127.0.0.1:9876", false)
	const hwnd1 uintptr = 0x1001
	const hwnd2 uintptr = 0x2002
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	// First observation when IsIconic becomes true must NOT immediately hide (let DWM animate).
	if hide := m.planMinimizeHides(map[uintptr]bool{hwnd1: true, hwnd2: false}, t0); len(hide) != 0 {
		t.Fatalf("expected no immediate hide at t0, got %v", hide)
	}

	// Still before minimizeHideDelay: must not hide yet.
	if hide := m.planMinimizeHides(map[uintptr]bool{hwnd1: true}, t0.Add(minimizeHideDelay/2)); len(hide) != 0 {
		t.Fatalf("expected no hide before delay elapsed, got %v", hide)
	}

	// If window restores before delay finishes, timer must reset.
	if hide := m.planMinimizeHides(map[uintptr]bool{hwnd1: false}, t0.Add(minimizeHideDelay/2+10*time.Millisecond)); len(hide) != 0 {
		t.Fatalf("expected no hide when un-minimized, got %v", hide)
	}
	if hide := m.planMinimizeHides(map[uintptr]bool{hwnd1: true}, t0.Add(minimizeHideDelay)); len(hide) != 0 {
		t.Fatalf("expected fresh timer after un-minimize, got %v", hide)
	}

	// After full delay elapses while remaining iconic, hwnd1 should be returned once and removed.
	hide := m.planMinimizeHides(map[uintptr]bool{hwnd1: true}, t0.Add(2*minimizeHideDelay))
	if len(hide) != 1 || hide[0] != hwnd1 {
		t.Fatalf("expected [%v] after delay, got %v", hwnd1, hide)
	}
	if _, stillTracked := m.iconicSince[hwnd1]; stillTracked {
		t.Fatalf("expected iconicSince entry for %v to be cleared once hidden", hwnd1)
	}

	// Closing/hiding removes stale entries from iconicSince.
	_ = m.planMinimizeHides(map[uintptr]bool{hwnd2: true}, t0)
	_ = m.planMinimizeHides(nil, t0.Add(time.Millisecond))
	if len(m.iconicSince) != 0 {
		t.Fatalf("expected stale iconicSince entries to be pruned, got %d remaining", len(m.iconicSince))
	}
}

