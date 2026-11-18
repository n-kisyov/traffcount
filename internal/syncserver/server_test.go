package syncserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"traffcount/internal/service"
	"traffcount/internal/syncpoller"
	"traffcount/internal/syncstore"
)

func TestSyncServerAPIRoutes(t *testing.T) {
	st, err := syncstore.Open(filepath.Join(t.TempDir(), "sync-http.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	poller := syncpoller.New(st)
	srv := New("127.0.0.1:0", st, poller, service.NewManager(service.SyncConfig()))

	// 1. /api/ping
	rec := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"traffcount-sync"`) {
		t.Fatalf("unexpected /api/ping response %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Embedded index.html
	recWeb := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(recWeb, httptest.NewRequest(http.MethodGet, "/", nil))
	if recWeb.Code != http.StatusOK || !strings.Contains(recWeb.Body.String(), "TraffCount Sync") {
		t.Fatalf("unexpected / HTML response %d", recWeb.Code)
	}

	// 3. Create Host + verify APIToken is masked in GET /api/hosts
	body := `{"name":"LabPi","baseUrl":"192.168.1.40","apiToken":"top-secret"}`
	recPost := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(recPost, httptest.NewRequest(http.MethodPost, "/api/hosts", bytes.NewBufferString(body)))
	if recPost.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", recPost.Code, recPost.Body.String())
	}

	recList := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(recList, httptest.NewRequest(http.MethodGet, "/api/hosts", nil))
	if recList.Code != http.StatusOK {
		t.Fatalf("GET /api/hosts failed: %d", recList.Code)
	}
	var hosts []syncstore.HostRecord
	if err := json.Unmarshal(recList.Body.Bytes(), &hosts); err != nil || len(hosts) != 1 {
		t.Fatalf("unexpected hosts list: %s (%v)", recList.Body.String(), err)
	}
	if hosts[0].APIToken != "" || !hosts[0].HasToken {
		t.Fatalf("expected APIToken masked in list response while HasToken=true, got %+v", hosts[0])
	}
}
