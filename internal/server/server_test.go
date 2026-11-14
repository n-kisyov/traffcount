package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"traffcount/internal/storage"
	"traffcount/internal/winui"
)

func TestHandleWindowMiniStateLifecycle(t *testing.T) {
	winMgr := winui.NewManager("http://127.0.0.1:9876", false)
	srv := New("127.0.0.1:0", nil, nil, winMgr, nil)

	// Cold start defaults to normal mode (mini == false).
	getReq := httptest.NewRequest(http.MethodGet, "/api/window/mini", nil)
	getRec := httptest.NewRecorder()
	srv.handleWindowMini(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/window/mini status = %d, want 200", getRec.Code)
	}
	var state struct {
		Mini bool `json:"mini"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if state.Mini {
		t.Fatalf("expected initial server miniMode == false")
	}

	// Enabling mini mode persists across subsequent reads (e.g. reopen after window close [X]).
	postReq := httptest.NewRequest(http.MethodPost, "/api/window/mini", strings.NewReader(`{"mini":true}`))
	postRec := httptest.NewRecorder()
	srv.handleWindowMini(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/window/mini status = %d, want 200", postRec.Code)
	}
	if !winMgr.IsMiniMode() {
		t.Fatalf("expected winMgr.IsMiniMode() == true after POST mini:true")
	}

	getRec2 := httptest.NewRecorder()
	srv.handleWindowMini(getRec2, httptest.NewRequest(http.MethodGet, "/api/window/mini", nil))
	if err := json.Unmarshal(getRec2.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode second GET response: %v", err)
	}
	if !state.Mini {
		t.Fatalf("expected server miniMode == true to persist before reopen")
	}

	// Resetting to normal mode (used on fresh cold start when background service stayed alive).
	resetReq := httptest.NewRequest(http.MethodPost, "/api/window/mini", strings.NewReader(`{"mini":false}`))
	resetRec := httptest.NewRecorder()
	srv.handleWindowMini(resetRec, resetReq)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("POST reset status = %d, want 200", resetRec.Code)
	}
	if winMgr.IsMiniMode() {
		t.Fatalf("expected winMgr.IsMiniMode() == false after reset")
	}

	// Concurrent reads/writes on /api/window/mini should remain race-free.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx%2 == 0 {
				rec := httptest.NewRecorder()
				srv.handleWindowMini(rec, httptest.NewRequest(http.MethodPost, "/api/window/mini", strings.NewReader(`{"mini":true}`)))
			} else {
				rec := httptest.NewRecorder()
				srv.handleWindowMini(rec, httptest.NewRequest(http.MethodGet, "/api/window/mini", nil))
			}
		}(i)
	}
	wg.Wait()
}

func TestHandleAppsSpeedTestBackupAndResetEndpoints(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "srv_test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	_, err = store.RecordProcessTrafficBatch([]storage.ProcessTrafficDelta{
		{ProcessName: "curl.exe", ExePath: `C:\Windows\System32\curl.exe`, RxBytes: 4096, TxBytes: 1024, Timestamp: time.Now()},
	})
	if err != nil {
		t.Fatalf("seed process: %v", err)
	}
	_ = store.RecordSpeedTest(storage.SpeedTestRecord{
		Timestamp: time.Now().Unix(), DownloadMbps: 250.5, UploadMbps: 50.2, BufferbloatGrade: "A+",
	})

	srv := New("127.0.0.1:0", store, nil, nil, nil)

	// /api/apps
	appsRec := httptest.NewRecorder()
	srv.handleApps(appsRec, httptest.NewRequest(http.MethodGet, "/api/apps", nil))
	if appsRec.Code != http.StatusOK || !strings.Contains(appsRec.Body.String(), "curl.exe") {
		t.Fatalf("unexpected /api/apps response: code=%d body=%s", appsRec.Code, appsRec.Body.String())
	}

	// /api/speedtest/history GET & DELETE
	stRec := httptest.NewRecorder()
	srv.handleSpeedTestHistory(stRec, httptest.NewRequest(http.MethodGet, "/api/speedtest/history", nil))
	if stRec.Code != http.StatusOK || !strings.Contains(stRec.Body.String(), "250.5") {
		t.Fatalf("unexpected /api/speedtest/history GET: code=%d body=%s", stRec.Code, stRec.Body.String())
	}
	delRec := httptest.NewRecorder()
	srv.handleSpeedTestHistory(delRec, httptest.NewRequest(http.MethodDelete, "/api/speedtest/history", nil))
	if delRec.Code != http.StatusOK {
		t.Fatalf("unexpected DELETE /api/speedtest/history code=%d", delRec.Code)
	}

	// /api/db/backup returns valid non-empty SQLite header ("SQLite format 3\x00")
	bakRec := httptest.NewRecorder()
	srv.handleDBBackup(bakRec, httptest.NewRequest(http.MethodGet, "/api/db/backup", nil))
	if bakRec.Code != http.StatusOK || !strings.HasPrefix(bakRec.Body.String(), "SQLite format 3") {
		t.Fatalf("unexpected /api/db/backup response: code=%d len=%d", bakRec.Code, bakRec.Body.Len())
	}

	// /api/db/reset
	rstRec := httptest.NewRecorder()
	srv.handleDBReset(rstRec, httptest.NewRequest(http.MethodPost, "/api/db/reset", nil))
	if rstRec.Code != http.StatusOK {
		t.Fatalf("unexpected /api/db/reset code=%d", rstRec.Code)
	}
}

