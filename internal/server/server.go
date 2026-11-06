package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"traffcount/internal/collector"
	"traffcount/internal/service"
	"traffcount/internal/storage"
	"traffcount/internal/winui"
)

//go:embed web/*
var embeddedWeb embed.FS

// Server serves the local HTTP API, WebSocket real-time stream, and embedded web dashboard.
type Server struct {
	addr         string
	store        *storage.Store
	collector    *collector.Collector
	winMgr       *winui.Manager
	onTopChanged func(bool)
	upgrader     websocket.Upgrader
	httpSrv      *http.Server
}

// New creates a new HTTP/WebSocket server on addr (e.g. "127.0.0.1:9876").
func New(addr string, store *storage.Store, coll *collector.Collector, winMgr *winui.Manager, onTopChanged func(bool)) *Server {
	s := &Server{
		addr:         addr,
		store:        store,
		collector:    coll,
		winMgr:       winMgr,
		onTopChanged: onTopChanged,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", s.handlePing)
	mux.HandleFunc("/api/realtime", s.handleRealtime)
	mux.HandleFunc("/api/connections", s.handleConnections)
	mux.HandleFunc("/api/interfaces", s.handleInterfaces)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/window/topmost", s.handleWindowTopmost)
	mux.HandleFunc("/api/service", s.handleService)
	mux.HandleFunc("/ws", s.handleWS)

	webRoot, _ := fs.Sub(embeddedWeb, "web")
	mux.Handle("/", http.FileServer(http.FS(webRoot)))

	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Start begins listening and serving until ctx is canceled.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
	}()
	err = s.httpSrv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"app":     "traffcount",
		"version": "1.0.0",
	})
}

func (s *Server) handleRealtime(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.collector.LatestSnapshot())
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filterIP := q.Get("ip")
	includeUDP := q.Get("udp") == "1" || q.Get("udp") == "true"
	snap := collector.GetActiveConnections(filterIP, includeUDP)
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleInterfaces(w http.ResponseWriter, r *http.Request) {
	dbIfaces, err := s.store.ListInterfaces()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"interfaces": dbIfaces,
		"live":       s.collector.LatestSnapshot().Interfaces,
	})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	// Ensure latest in-memory deltas are flushed before querying historical totals
	s.collector.FlushToDB()

	q := r.URL.Query()
	ifaceID := q.Get("iface")
	if ifaceID == "" {
		ifaceID = "all"
	}

	now := time.Now()
	var start, end time.Time
	end = now

	if startStr := q.Get("start"); startStr != "" {
		if ts, err := strconv.ParseInt(startStr, 10, 64); err == nil {
			start = time.Unix(ts, 0)
		}
	}
	if endStr := q.Get("end"); endStr != "" {
		if ts, err := strconv.ParseInt(endStr, 10, 64); err == nil {
			end = time.Unix(ts, 0)
		}
	}

	if start.IsZero() {
		days := 14
		if dStr := q.Get("days"); dStr != "" {
			if d, err := strconv.Atoi(dStr); err == nil && d > 0 {
				days = d
			}
		}
		if q.Get("period") == "today" {
			y, m, d := now.Date()
			start = time.Date(y, m, d, 0, 0, 0, 0, now.Location())
		} else if q.Get("period") == "24h" {
			start = now.Add(-24 * time.Hour)
		} else {
			y, m, d := now.AddDate(0, 0, -(days - 1)).Date()
			start = time.Date(y, m, d, 0, 0, 0, 0, now.Location())
		}
	}

	report, err := s.store.QueryHistory(start, end, ifaceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := s.store.GetSettings()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, cfg)

	case http.MethodPost, http.MethodPut:
		var cfg storage.Settings
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if err := s.store.SaveSettings(cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if s.winMgr != nil {
			s.winMgr.SetAlwaysOnTop(cfg.AlwaysOnTop)
		}
		if s.onTopChanged != nil {
			s.onTopChanged(cfg.AlwaysOnTop)
		}
		writeJSON(w, http.StatusOK, cfg)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWindowTopmost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		AlwaysOnTop bool `json:"alwaysOnTop"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	cfg, _ := s.store.GetSettings()
	cfg.AlwaysOnTop = req.AlwaysOnTop
	_ = s.store.SaveSettings(cfg)

	applied := false
	if s.winMgr != nil {
		applied = s.winMgr.SetAlwaysOnTop(req.AlwaysOnTop)
	}
	if s.onTopChanged != nil {
		s.onTopChanged(req.AlwaysOnTop)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"alwaysOnTop": req.AlwaysOnTop,
		"applied":     applied,
	})
}

func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, service.QueryStatus())

	case http.MethodPost:
		var req struct {
			Action string `json:"action"` // "install", "uninstall", "start", "stop"
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		var err error
		switch req.Action {
		case "install":
			err = service.InstallAndStart()
		case "uninstall":
			err = service.StopAndUninstall()
		case "start":
			err = service.StartService()
		case "stop":
			err = service.StopService()
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// Give Windows SCM a brief moment to update state
		time.Sleep(400 * time.Millisecond)
		writeJSON(w, http.StatusOK, service.QueryStatus())

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Send immediate snapshot on connect
	_ = conn.WriteJSON(s.collector.LatestSnapshot())

	ch, unsubscribe := s.collector.Subscribe()
	defer unsubscribe()

	// Read loop in goroutine to detect client disconnect
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-closed:
			return
		case payload, ok := <-ch:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if err := conn.WriteJSON(payload); err != nil {
				return
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

