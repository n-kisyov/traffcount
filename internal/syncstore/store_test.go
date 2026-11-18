package syncstore

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSyncStoreHostsAndFleetAggregation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sync-test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open syncstore: %v", err)
	}
	defer st.Close()

	// Create two hosts (Windows 11 and Raspberry Pi)
	h1, err := st.CreateHost(HostInput{
		Name:    "Gaming-PC",
		BaseURL: "192.168.1.50",
		Tags:    "windows,desktop",
	})
	if err != nil {
		t.Fatalf("create host 1: %v", err)
	}
	if h1.BaseURL != "http://192.168.1.50:9876" {
		t.Fatalf("expected normalized URL http://192.168.1.50:9876, got %s", h1.BaseURL)
	}

	h2, err := st.CreateHost(HostInput{
		Name:     "RPi-Router",
		BaseURL:  "http://192.168.1.2:9876/",
		APIToken: "secret-123",
	})
	if err != nil {
		t.Fatalf("create host 2: %v", err)
	}
	if h2.BaseURL != "http://192.168.1.2:9876" || !h2.HasToken {
		t.Fatalf("unexpected host 2 fields: %+v", h2)
	}

	now := time.Now()
	hr := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location()).Unix()

	_ = st.UpsertHostInterfaces(h1.ID, []HostInterfaceRecord{
		{IfaceID: "eth0", Name: "Ethernet", IPv4: "192.168.1.50", IsUp: true},
		{IfaceID: "lo", Name: "Loopback", IPv4: "127.0.0.1", IsUp: true, IsLoopback: true},
	})
	_ = st.UpsertHostInterfaces(h2.ID, []HostInterfaceRecord{
		{IfaceID: "eth0", Name: "eth0", IPv4: "192.168.1.2", IsUp: true},
	})

	// Insert + idempotently re-upsert hourly traffic (must replace, not add twice)
	_ = st.UpsertHostTrafficHourly(h1.ID, []HostTrafficRow{
		{IfaceID: "eth0", BucketTS: hr, RxBytes: 1000, TxBytes: 500},
		{IfaceID: "lo", BucketTS: hr, RxBytes: 999999, TxBytes: 999999},
	})
	_ = st.UpsertHostTrafficHourly(h1.ID, []HostTrafficRow{
		{IfaceID: "eth0", BucketTS: hr, RxBytes: 2000, TxBytes: 1000},
	})
	_ = st.UpsertHostTrafficHourly(h2.ID, []HostTrafficRow{
		{IfaceID: "eth0", BucketTS: hr, RxBytes: 4000, TxBytes: 1000},
	})

	_ = st.UpsertHostProcessHourly(h1.ID, []HostProcessRow{
		{ProcessName: "chrome.exe", ExePath: `C:\chrome.exe`, BucketTS: hr, RxBytes: 1800, TxBytes: 200},
	})

	rep, err := st.QueryFleetHistory("all", "all", hr-3600, hr+3600, "hour", true)
	if err != nil {
		t.Fatalf("QueryFleetHistory: %v", err)
	}
	if rep.TotalRxBytes != 6000 || rep.TotalTxBytes != 2000 {
		t.Fatalf("expected total Rx=6000 Tx=2000 (loopback hidden, idempotent upsert), got Rx=%d Tx=%d", rep.TotalRxBytes, rep.TotalTxBytes)
	}
	if len(rep.ByHost) != 2 || len(rep.ByProcess) != 1 {
		t.Fatalf("expected 2 hosts and 1 process in fleet report, got hosts=%d procs=%d", len(rep.ByHost), len(rep.ByProcess))
	}

	// Speed tests dedup by (host_id, ts)
	_ = st.UpsertHostSpeedTests(h2.ID, []HostSpeedTestRow{
		{Timestamp: hr, DownloadMbps: 250.5, UploadMbps: 42.1, UnloadedPingMs: 12, BufferbloatGrade: "A"},
	})
	_ = st.UpsertHostSpeedTests(h2.ID, []HostSpeedTestRow{
		{Timestamp: hr, DownloadMbps: 251.0, UploadMbps: 42.1, UnloadedPingMs: 12, BufferbloatGrade: "A+"},
	})
	tests, err := st.ListSpeedTests(h2.ID, 10)
	if err != nil || len(tests) != 1 || tests[0].BufferbloatGrade != "A+" {
		t.Fatalf("expected 1 deduped speed test with grade A+, got %+v err=%v", tests, err)
	}

	// Backup snapshot check
	bkp := filepath.Join(dir, "backup.db")
	if err := st.BackupTo(bkp); err != nil {
		t.Fatalf("BackupTo failed: %v", err)
	}
}
