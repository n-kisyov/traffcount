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

	// 4. Proxy endpoints against a live mock agent server
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/apps":
			_, _ = w.Write([]byte(`{"apps":[{"processName":"nginx","blocked":false}]}`))
		case "/api/firewall/block":
			_, _ = w.Write([]byte(`{"ok":true,"blocked":true}`))
		case "/api/connections/action":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockAgent.Close()

	hMock, err := st.CreateHost(syncstore.HostInput{Name: "ProxyHost", BaseURL: mockAgent.URL})
	if err != nil {
		t.Fatal(err)
	}

	recApps := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(recApps, httptest.NewRequest(http.MethodGet, "/api/hosts/"+hMock.ID+"/apps", nil))
	if recApps.Code != http.StatusOK || !strings.Contains(recApps.Body.String(), `"nginx"`) {
		t.Fatalf("expected proxied /apps 200 OK, got %d: %s", recApps.Code, recApps.Body.String())
	}

	recFw := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(recFw, httptest.NewRequest(http.MethodPost, "/api/hosts/"+hMock.ID+"/firewall/block", bytes.NewBufferString(`{"processName":"nginx","blocked":true}`)))
	if recFw.Code != http.StatusOK || !strings.Contains(recFw.Body.String(), `"blocked":true`) {
		t.Fatalf("expected proxied /firewall/block 200 OK, got %d: %s", recFw.Code, recFw.Body.String())
	}

	recAct := httptest.NewRecorder()
	srv.httpSrv.Handler.ServeHTTP(recAct, httptest.NewRequest(http.MethodPost, "/api/hosts/"+hMock.ID+"/connections/action", bytes.NewBufferString(`{"action":"kill_process","pid":999}`)))
	if recAct.Code != http.StatusOK || !strings.Contains(recAct.Body.String(), `"ok":true`) {
		t.Fatalf("expected proxied /connections/action 200 OK, got %d: %s", recAct.Code, recAct.Body.String())
	}
}
