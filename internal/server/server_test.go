package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
}

