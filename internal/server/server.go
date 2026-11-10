package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"traffcount/internal/collector"
	"traffcount/internal/service"
	"traffcount/internal/storage"
	"traffcount/internal/tray"
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
	miniMode     bool
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
	mux.HandleFunc("/favicon.ico", s.handleFavicon)
	mux.HandleFunc("/api/ping", s.handlePing)
	mux.HandleFunc("/api/realtime", s.handleRealtime)
	mux.HandleFunc("/api/connections", s.handleConnections)
	mux.HandleFunc("/api/connections/action", s.handleConnectionsAction)
	mux.HandleFunc("/api/firewall/block", s.handleFirewallBlock)
	mux.HandleFunc("/api/speedtest", s.handleSpeedTest)
	mux.HandleFunc("/api/db/backup", s.handleDBBackup)
	mux.HandleFunc("/api/db/reset", s.handleDBReset)
	mux.HandleFunc("/api/alerts/new-apps", s.handleAlertsNewApps)
	mux.HandleFunc("/api/interfaces", s.handleInterfaces)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/history/export", s.handleHistoryExport)
	mux.HandleFunc("/api/quota", s.handleQuota)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/window/topmost", s.handleWindowTopmost)
	mux.HandleFunc("/api/window/mini", s.handleWindowMini)
	mux.HandleFunc("/api/window/bounds", s.handleWindowBounds)
	mux.HandleFunc("/api/service", s.handleService)
	mux.HandleFunc("/api/server/release", s.handleServerRelease)
	mux.HandleFunc("/ws", s.handleWS)

	webRoot, _ := fs.Sub(embeddedWeb, "web")
	fileSrv := http.FileServer(http.FS(webRoot))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		fileSrv.ServeHTTP(w, r)
	})

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
	if err != nil && service.IsWindowsServiceProcess() {
		// If TraffCountService was started directly (e.g. via services.msc or sc start)
		// while the standalone app holds port 9876, ask the standalone app to release it.
		client := &http.Client{Timeout: 400 * time.Millisecond}
		if resp, rErr := client.Post("http://"+s.addr+"/api/server/release", "application/json", nil); rErr == nil {
			resp.Body.Close()
		}
		for i := 0; i < 25; i++ {
			time.Sleep(100 * time.Millisecond)
			ln, err = net.Listen("tcp", s.addr)
			if err == nil {
				break
			}
		}
	}
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

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	ico := tray.GenerateMultiSizeAppICO()
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(ico)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	src := "app"
	if service.IsWindowsServiceProcess() {
		src = "service"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"app":     "traffcount",
		"version": "1.1.0",
		"source":  src,
	})
}

func (s *Server) handleServerRelease(w http.ResponseWriter, r *http.Request) {
	if service.IsWindowsServiceProcess() {
		writeJSON(w, http.StatusOK, map[string]any{"released": false, "reason": "service"})
		return
	}
	s.collector.FlushToDB()
	writeJSON(w, http.StatusOK, map[string]any{"released": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		tray.NotifyTrayReleaseServer()
	}()
}

func (s *Server) handleQuota(w http.ResponseWriter, r *http.Request) {
	s.collector.FlushToDB()
	qs, err := s.store.QueryQuotaStatus()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, qs)
}

func (s *Server) handleHistoryExport(w http.ResponseWriter, r *http.Request) {
	s.collector.FlushToDB()

	q := r.URL.Query()
	ifaceID := q.Get("iface")
	if ifaceID == "" {
		ifaceID = "all"
	}
	format := q.Get("format")
	if format == "" {
		format = "csv"
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

	if format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="traffcount-history.json"`)
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return
	}

	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"Section", "Label_Or_Adapter", "Download_Bytes", "Upload_Bytes", "Total_Bytes", "Download_GB", "Upload_GB", "Total_GB"})
	for _, b := range report.Series {
		tot := b.RxBytes + b.TxBytes
		_ = cw.Write([]string{
			"TimeBucket",
			b.Label,
			strconv.FormatUint(b.RxBytes, 10),
			strconv.FormatUint(b.TxBytes, 10),
			strconv.FormatUint(tot, 10),
			fmt.Sprintf("%.4f", float64(b.RxBytes)/1e9),
			fmt.Sprintf("%.4f", float64(b.TxBytes)/1e9),
			fmt.Sprintf("%.4f", float64(tot)/1e9),
		})
	}
	for _, a := range report.ByInterface {
		_ = cw.Write([]string{
			"ByAdapter",
			a.Name,
			strconv.FormatUint(a.RxBytes, 10),
			strconv.FormatUint(a.TxBytes, 10),
			strconv.FormatUint(a.TotalBytes, 10),
			fmt.Sprintf("%.4f", float64(a.RxBytes)/1e9),
			fmt.Sprintf("%.4f", float64(a.TxBytes)/1e9),
			fmt.Sprintf("%.4f", float64(a.TotalBytes)/1e9),
		})
	}
	for _, p := range report.ByProcess {
		_ = cw.Write([]string{
			"ByApplication",
			p.ProcessName,
			strconv.FormatUint(p.RxBytes, 10),
			strconv.FormatUint(p.TxBytes, 10),
			strconv.FormatUint(p.TotalBytes, 10),
			fmt.Sprintf("%.4f", float64(p.RxBytes)/1e9),
			fmt.Sprintf("%.4f", float64(p.TxBytes)/1e9),
			fmt.Sprintf("%.4f", float64(p.TotalBytes)/1e9),
		})
	}
	cw.Flush()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="traffcount-history.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) handleWindowMini(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"mini": s.miniMode,
		})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Mini bool `json:"mini"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	s.miniMode = req.Mini
	applied := false
	if s.winMgr != nil {
		applied = s.winMgr.SetMiniMode(req.Mini)
	}
	if tray.NotifyTrayMiniMode(req.Mini) {
		applied = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mini":    req.Mini,
		"applied": applied,
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

func (s *Server) handleConnectionsAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Action     string `json:"action"` // "close_socket" or "kill_process"
		LocalIP    string `json:"localIP"`
		LocalPort  uint32 `json:"localPort"`
		RemoteIP   string `json:"remoteIP"`
		RemotePort uint32 `json:"remotePort"`
		PID        int32  `json:"pid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	var err error
	switch req.Action {
	case "close_socket":
		err = collector.CloseTCPConnectionV4(req.LocalIP, req.LocalPort, req.RemoteIP, req.RemotePort)
	case "kill_process":
		err = collector.TerminateProcessByPID(req.PID)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleFirewallBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ProcessName string `json:"processName"`
		ExePath     string `json:"exePath"`
		Blocked     bool   `json:"blocked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if err := collector.SetFirewallBlockRule(req.ProcessName, req.ExePath, req.Blocked); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.SetAppBlocked(req.ProcessName, req.ExePath, req.Blocked); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if blockedMap, err := s.store.GetBlockedApps(); err == nil {
		collector.SetBlockedAppsCache(blockedMap)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"processName": req.ProcessName,
		"blocked":     req.Blocked,
	})
}

func (s *Server) handleSpeedTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	res := collector.RunSpeedTest(r.Context())
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleDBBackup(w http.ResponseWriter, r *http.Request) {
	s.collector.FlushToDB()
	dbPath := s.store.DBPath()
	data, err := os.ReadFile(dbPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="traffcount-backup-%s.db"`, time.Now().Format("20060102-150405")))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleDBReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := s.store.ResetTrafficHistory(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAlertsNewApps(w http.ResponseWriter, r *http.Request) {
	apps := s.collector.PopNewAppAlerts()
	writeJSON(w, http.StatusOK, map[string]any{
		"newApps": apps,
	})
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
		_ = tray.SetStartupEnabled(cfg.StartOnBoot)
		tray.NotifyTrayStartup(cfg.StartOnBoot)
		tray.NotifyTrayTopmost(cfg.AlwaysOnTop)
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
	if tray.NotifyTrayTopmost(req.AlwaysOnTop) {
		applied = true
	}
	if s.onTopChanged != nil {
		s.onTopChanged(req.AlwaysOnTop)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"alwaysOnTop": req.AlwaysOnTop,
		"applied":     applied,
	})
}

func (s *Server) handleWindowBounds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		b, err := s.store.GetWindowBounds()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, b)

	case http.MethodPost, http.MethodPut:
		var b storage.WindowBounds
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if b.Width >= 400 && b.Height >= 300 {
			b.HasValue = true
			_ = s.store.SaveWindowBounds(b)
			if s.winMgr != nil {
				s.winMgr.SetSavedBounds(b)
			}
		}
		writeJSON(w, http.StatusOK, b)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
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

		// If the current process is the Windows Service and the action is stop/uninstall,
		// or if the current process is the standalone server and the action is start/install,
		// write the HTTP response BEFORE triggering the port-handover / self-stop so the browser
		// never gets a connection reset.
		if service.IsWindowsServiceProcess() && (req.Action == "stop" || req.Action == "uninstall") {
			s.collector.FlushToDB()
			st := service.QueryStatus()
			if req.Action == "uninstall" {
				st.Installed = false
				st.Running = false
				st.StateText = "Not Installed"
			} else {
				st.Running = false
				st.StateText = "Stopped"
			}
			writeJSON(w, http.StatusOK, st)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			go func(act string) {
				time.Sleep(80 * time.Millisecond)
				if act == "uninstall" {
					_ = service.StopAndUninstall()
				} else {
					_ = service.StopService()
				}
			}(req.Action)
			return
		}

		if !service.IsWindowsServiceProcess() && (req.Action == "start" || req.Action == "install") {
			s.collector.FlushToDB()
			st := service.QueryStatus()
			st.Installed = true
			st.Running = true
			st.StateText = "Starting..."
			writeJSON(w, http.StatusOK, st)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			go func(act string) {
				time.Sleep(80 * time.Millisecond)
				if act == "install" {
					_ = service.InstallAndStart()
				} else {
					_ = service.StartService()
				}
			}(req.Action)
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
		case "restart":
			_ = service.StopService()
			time.Sleep(800 * time.Millisecond)
			err = service.StartService()
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

