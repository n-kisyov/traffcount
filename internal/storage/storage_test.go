package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreTrafficAndHistory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_traffcount.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	// 1. Verify Default Settings (Dark mode by default)
	cfg, err := store.GetSettings()
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if cfg.Theme != "dark" {
		t.Errorf("expected default theme 'dark', got %q", cfg.Theme)
	}
	if cfg.AlwaysOnTop {
		t.Errorf("expected default AlwaysOnTop false")
	}

	// 2. Save updated settings (Light mode + AlwaysOnTop)
	cfg.Theme = "light"
	cfg.AlwaysOnTop = true
	cfg.StartOnBoot = true
	cfg.SpeedUnit = "bytes"
	if err := store.SaveSettings(cfg); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	cfg2, err := store.GetSettings()
	if err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	if cfg2.Theme != "light" || !cfg2.AlwaysOnTop || !cfg2.StartOnBoot || cfg2.SpeedUnit != "bytes" {
		t.Errorf("unexpected reloaded settings: %+v", cfg2)
	}

	// Verify WindowBounds persistence
	wantBounds := WindowBounds{
		X:         240,
		Y:         160,
		Width:     1320,
		Height:    840,
		Maximized: false,
	}
	if err := store.SaveWindowBounds(wantBounds); err != nil {
		t.Fatalf("save window bounds: %v", err)
	}
	gotBounds, err := store.GetWindowBounds()
	if err != nil {
		t.Fatalf("get window bounds: %v", err)
	}
	if gotBounds.X != 240 || gotBounds.Y != 160 || gotBounds.Width != 1320 || gotBounds.Height != 840 || !gotBounds.HasValue {
		t.Errorf("unexpected window bounds: %+v", gotBounds)
	}

	// 3. Seed historical traffic across 14 days and 30 days for two NICs
	now := time.Now()
	day20Ago := now.AddDate(0, 0, -20)
	day5Ago := now.AddDate(0, 0, -5)
	day1Ago := now.AddDate(0, 0, -1)

	batch := []TrafficDelta{
		{
			IfaceID:     "Ethernet_AABBCCDDEEFF",
			Name:        "Ethernet",
			Description: "Intel I225-V",
			MAC:         "AA:BB:CC:DD:EE:FF",
			RxBytes:     1000 * 1024 * 1024, // 1000 MB 20 days ago
			TxBytes:     200 * 1024 * 1024,  // 200 MB 20 days ago
			Timestamp:   day20Ago,
		},
		{
			IfaceID:     "Ethernet_AABBCCDDEEFF",
			Name:        "Ethernet",
			Description: "Intel I225-V",
			MAC:         "AA:BB:CC:DD:EE:FF",
			RxBytes:     500 * 1024 * 1024, // 500 MB 5 days ago
			TxBytes:     100 * 1024 * 1024, // 100 MB 5 days ago
			Timestamp:   day5Ago,
		},
		{
			IfaceID:     "Wi-Fi_112233445566",
			Name:        "Wi-Fi",
			Description: "Intel Wi-Fi 6E AX211",
			MAC:         "11:22:33:44:55:66",
			RxBytes:     300 * 1024 * 1024, // 300 MB 1 day ago
			TxBytes:     50 * 1024 * 1024,  // 50 MB 1 day ago
			Timestamp:   day1Ago,
		},
	}

	if err := store.RecordTrafficBatch(batch); err != nil {
		t.Fatalf("record batch: %v", err)
	}

	// 4. Query Past 14 Days for ALL NICs (should include 5d ago + 1d ago, excluding 20d ago)
	start14 := now.AddDate(0, 0, -13)
	rep14, err := store.QueryHistory(start14, now, "all")
	if err != nil {
		t.Fatalf("query 14d: %v", err)
	}

	want14Rx := uint64((500 + 300) * 1024 * 1024)
	want14Tx := uint64((100 + 50) * 1024 * 1024)
	if rep14.TotalRxBytes != want14Rx || rep14.TotalTxBytes != want14Tx {
		t.Errorf("14d all-NIC totals mismatch: got rx=%d tx=%d, want rx=%d tx=%d",
			rep14.TotalRxBytes, rep14.TotalTxBytes, want14Rx, want14Tx)
	}

	// 5. Query Past 30 Days for ALL NICs (should include 20d + 5d + 1d ago)
	start30 := now.AddDate(0, 0, -29)
	rep30, err := store.QueryHistory(start30, now, "all")
	if err != nil {
		t.Fatalf("query 30d: %v", err)
	}
	want30Rx := uint64((1000 + 500 + 300) * 1024 * 1024)
	if rep30.TotalRxBytes != want30Rx {
		t.Errorf("30d total rx mismatch: got %d, want %d", rep30.TotalRxBytes, want30Rx)
	}

	// 6. Query Past 30 Days filtered to Ethernet only
	repEth, err := store.QueryHistory(start30, now, "Ethernet_AABBCCDDEEFF")
	if err != nil {
		t.Fatalf("query ethernet: %v", err)
	}
	wantEthRx := uint64((1000 + 500) * 1024 * 1024)
	if repEth.TotalRxBytes != wantEthRx {
		t.Errorf("30d Ethernet rx mismatch: got %d, want %d", repEth.TotalRxBytes, wantEthRx)
	}
}

