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

	// Verify custom themes (antigravity, nord, dracula, emerald) persist across reloads
	for _, th := range []string{"antigravity", "nord", "dracula", "emerald", "dark"} {
		cfg2.Theme = th
		if err := store.SaveSettings(cfg2); err != nil {
			t.Fatalf("save theme %s: %v", th, err)
		}
		gotCfg, err := store.GetSettings()
		if err != nil || gotCfg.Theme != th {
			t.Errorf("expected persisted theme %q, got %q (err=%v)", th, gotCfg.Theme, err)
		}
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

	// 7. Test Per-Process Traffic Recording, New App Detection, and Firewall Blocked Persistence
	newApps, err := store.RecordProcessTrafficBatch([]ProcessTrafficDelta{
		{
			ProcessName: "chrome.exe",
			ExePath:     `C:\Program Files\Google\Chrome\Application\chrome.exe`,
			RxBytes:     250 * 1024 * 1024,
			TxBytes:     25 * 1024 * 1024,
			Timestamp:   day1Ago,
		},
		{
			ProcessName: "code.exe",
			ExePath:     `C:\Users\dev\AppData\Local\Programs\Microsoft VS Code\Code.exe`,
			RxBytes:     120 * 1024 * 1024,
			TxBytes:     15 * 1024 * 1024,
			Timestamp:   day1Ago,
		},
	})
	if err != nil {
		t.Fatalf("record process traffic: %v", err)
	}
	if len(newApps) != 0 {
		t.Errorf("expected initial seed batch to return 0 alerts, got %v", newApps)
	}

	// Recording again with an existing app (chrome.exe) and a brand-new app (firefox.exe) should flag only firefox.exe
	newApps2, err := store.RecordProcessTrafficBatch([]ProcessTrafficDelta{
		{
			ProcessName: "chrome.exe",
			ExePath:     `C:\Program Files\Google\Chrome\Application\chrome.exe`,
			RxBytes:     50 * 1024 * 1024,
			TxBytes:     5 * 1024 * 1024,
			Timestamp:   now,
		},
		{
			ProcessName: "firefox.exe",
			ExePath:     `C:\Program Files\Mozilla Firefox\firefox.exe`,
			RxBytes:     10 * 1024 * 1024,
			TxBytes:     2 * 1024 * 1024,
			Timestamp:   now,
		},
	})
	if err != nil {
		t.Fatalf("record process traffic 2: %v", err)
	}
	if len(newApps2) != 1 || newApps2[0] != "firefox.exe" {
		t.Errorf("expected [firefox.exe] newly discovered on second batch, got %v", newApps2)
	}

	if err := store.SetAppBlocked("code.exe", `C:\Users\dev\AppData\Local\Programs\Microsoft VS Code\Code.exe`, true); err != nil {
		t.Fatalf("set app blocked: %v", err)
	}
	blockedMap, err := store.GetBlockedApps()
	if err != nil || !blockedMap["code.exe"] {
		t.Errorf("expected code.exe to be blocked, got map=%v err=%v", blockedMap, err)
	}

	repWithProcs, err := store.QueryHistory(start14, now, "all")
	if err != nil {
		t.Fatalf("query history with procs: %v", err)
	}
	if len(repWithProcs.ByProcess) != 3 || repWithProcs.ByProcess[0].ProcessName != "chrome.exe" {
		t.Errorf("unexpected ByProcess breakdown: %+v", repWithProcs.ByProcess)
	}

	// 8. Test ListKnownApps (including blocked + byte sums)
	apps, err := store.ListKnownApps()
	if err != nil {
		t.Fatalf("list known apps: %v", err)
	}
	if len(apps) != 3 || !apps[0].Blocked || apps[0].ProcessName != "code.exe" {
		t.Errorf("expected code.exe first and blocked in ListKnownApps, got %+v", apps)
	}

	// 9. Test SpeedTestRecord persistence
	if err := store.RecordSpeedTest(SpeedTestRecord{
		Timestamp:        now.Unix(),
		UnloadedPingMs:   12.3,
		JitterMs:         1.4,
		LoadedPingMs:     18.6,
		DownloadMbps:     450.5,
		UploadMbps:       120.2,
		BufferbloatGrade: "A",
		ServerLabel:      "Test",
	}); err != nil {
		t.Fatalf("record speed test: %v", err)
	}
	stList, err := store.ListSpeedTests(10)
	if err != nil || len(stList) != 1 || stList[0].DownloadMbps != 450.5 {
		t.Errorf("unexpected speed tests list: %+v err=%v", stList, err)
	}

	// 10. Test QuotaStatus projection math
	cfg2.DataCapEnabled = true
	cfg2.DataCapGB = 10
	cfg2.BillingDay = 1
	cfg2.RetentionDays = 30
	cfg2.RefreshRateMs = 500
	if err := store.SaveSettings(cfg2); err != nil {
		t.Fatalf("save settings quota: %v", err)
	}
	qs, err := store.QueryQuotaStatus()
	if err != nil {
		t.Fatalf("query quota status: %v", err)
	}
	if !qs.Enabled || qs.TotalCycleDays < 28 || qs.DaysElapsed < 1 || qs.ProjectedTotalBytes == 0 {
		t.Errorf("unexpected quota projection: %+v", qs)
	}

	// 11. Test BackupTo (WAL-safe VACUUM INTO) and PruneOldData
	backupPath := filepath.Join(tmpDir, "backup_snapshot.db")
	if err := store.BackupTo(backupPath); err != nil {
		t.Fatalf("BackupTo failed: %v", err)
	}
	bStore, err := Open(backupPath)
	if err != nil {
		t.Fatalf("open backup store: %v", err)
	}
	bApps, err := bStore.ListKnownApps()
	bStore.Close()
	if err != nil || len(bApps) != 3 {
		t.Errorf("expected 3 known apps inside backup snapshot, got %d (err=%v)", len(bApps), err)
	}

	// Prune 10 days -> removes day20Ago bucket while preserving day5Ago & day1Ago
	if err := store.PruneOldData(10); err != nil {
		t.Fatalf("prune old data: %v", err)
	}
	repAfterPrune, err := store.QueryHistory(start30, now, "all")
	if err != nil {
		t.Fatalf("query after prune: %v", err)
	}
	if repAfterPrune.TotalRxBytes >= want30Rx {
		t.Errorf("expected 20-day old bucket pruned (< %d), got %d", want30Rx, repAfterPrune.TotalRxBytes)
	}

	// 12. Test ResetTrafficHistory and ClearSpeedTests
	if err := store.ResetTrafficHistory(); err != nil {
		t.Fatalf("reset traffic history: %v", err)
	}
	repAfterReset, err := store.QueryHistory(start30, now, "all")
	if err != nil {
		t.Fatalf("query after reset: %v", err)
	}
	if repAfterReset.CombinedBytes != 0 || len(repAfterReset.ByProcess) != 0 {
		t.Errorf("expected 0 traffic after reset, got combined=%d procs=%d", repAfterReset.CombinedBytes, len(repAfterReset.ByProcess))
	}
	if err := store.ClearSpeedTests(); err != nil {
		t.Fatalf("clear speed tests: %v", err)
	}
	if stAfter, _ := store.ListSpeedTests(10); len(stAfter) != 0 {
		t.Errorf("expected 0 speed tests after clear, got %d", len(stAfter))
	}
}

