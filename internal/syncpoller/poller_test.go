package syncpoller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"traffcount/internal/collector"
	"traffcount/internal/storage"
	"traffcount/internal/syncstore"
)

func makeMockAgent(t *testing.T, platform, token string, rxBps, txBps uint64, bucketTS int64) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	checkAuth := func(w http.ResponseWriter, r *http.Request) bool {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"app":      "traffcount",
			"version":  "1.1.0",
			"source":   "service",
			"platform": platform,
			"hostname": "mock-" + platform,
		})
	})
	mux.HandleFunc("/api/realtime", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(collector.RealtimePayload{
			Timestamp:  time.Now().Unix(),
			Source:     "service",
			TotalRxBps: rxBps,
			TotalTxBps: txBps,
			Interfaces: []collector.LiveInterfaceSnapshot{
				{ID: "eth0", Name: "eth0", IPv4: "10.0.0.10", IsUp: true, CurrentRxBps: rxBps, CurrentTxBps: txBps},
			},
		})
	})
	mux.HandleFunc("/api/sync/export", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(storage.SyncExportPayload{
			ServerTS: time.Now().Unix(),
			Interfaces: []storage.InterfaceRecord{
				{ID: "eth0", Name: "eth0", Description: "Mock NIC"},
			},
			Traffic: []storage.SyncTrafficRow{
				{IfaceID: "eth0", BucketTS: bucketTS, RxBytes: rxBps * 10, TxBytes: txBps * 10},
			},
			Processes: []storage.SyncProcessRow{
				{ProcessName: "curl", ExePath: "/usr/bin/curl", BucketTS: bucketTS, RxBytes: rxBps * 5, TxBytes: txBps * 2},
			},
		})
	})
	return httptest.NewServer(mux)
}

func TestPollerMultiHostLiveAndHistorySync(t *testing.T) {
	now := time.Now()
	hr := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location()).Unix()

	agentWin := makeMockAgent(t, "windows", "tok-win", 1000, 200, hr)
	defer agentWin.Close()

	agentPi := makeMockAgent(t, "linux", "", 2500, 500, hr)
	defer agentPi.Close()

	st, err := syncstore.Open(filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	poller := New(st)

	// Test probing with valid and invalid tokens
	badProbe := poller.TestHostConnection(context.Background(), agentWin.URL, "wrong")
	if badProbe.OK {
		t.Fatalf("expected auth probe failure, got OK")
	}
	goodProbe := poller.TestHostConnection(context.Background(), agentWin.URL, "tok-win")
	if !goodProbe.OK || goodProbe.Platform != "windows" {
		t.Fatalf("expected valid windows probe, got %+v", goodProbe)
	}

	hWin, err := st.CreateHost(syncstore.HostInput{Name: "WinHost", BaseURL: agentWin.URL, APIToken: "tok-win"})
	if err != nil {
		t.Fatal(err)
	}
	hPi, err := st.CreateHost(syncstore.HostInput{Name: "PiHost", BaseURL: agentPi.URL})
	if err != nil {
		t.Fatal(err)
	}

	if ok := poller.pollHostOnce(context.Background(), hWin); !ok {
		t.Fatalf("expected pollHostOnce for WinHost to succeed")
	}
	if ok := poller.pollHostOnce(context.Background(), hPi); !ok {
		t.Fatalf("expected pollHostOnce for PiHost to succeed")
	}

	poller.syncHostHistory(context.Background(), hWin)
	poller.syncHostHistory(context.Background(), hPi)

	cluster := poller.aggregateTick(now.Unix())
	if cluster.OnlineHosts != 2 || cluster.TotalRxBps != 3500 || cluster.TotalTxBps != 700 {
		t.Fatalf("unexpected cluster aggregate: %+v", cluster)
	}

	rep, err := st.QueryFleetHistory("all", "all", hr-3600, hr+3600, "hour", true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.TotalRxBytes != 35000 || rep.TotalTxBytes != 7000 {
		t.Fatalf("expected synced fleet totals Rx=35000 Tx=7000, got Rx=%d Tx=%d", rep.TotalRxBytes, rep.TotalTxBytes)
	}
}
