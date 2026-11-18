package syncserver

import (
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"traffcount/internal/service"
	"traffcount/internal/syncpoller"
	"traffcount/internal/syncstore"
	"traffcount/internal/tray"
)

//go:embed web/*
var embeddedSyncWeb embed.FS

// Server exposes the TraffCount Sync Server HTTP API, WebSocket stream, and embedded dashboard UI.
type Server struct {
	addr      string
	store     *syncstore.Store
	poller    *syncpoller.Poller
	svcMgr    *service.Manager
	upgrader  websocket.Upgrader
	httpSrv   *http.Server
	relMu     sync.Mutex
	onRelease func()
}

// SetReleaseHandler registers a callback invoked when /api/server/release is requested
// in standalone console mode (e.g., yielding port 9880 to TraffCountSyncService).
func (s *Server) SetReleaseHandler(fn func()) {
	s.relMu.Lock()
	s.onRelease = fn
	s.relMu.Unlock()
}

// New creates a new Sync Server listening on addr (e.g. "0.0.0.0:9880").
func New(addr string, store *syncstore.Store, poller *syncpoller.Poller, svcMgr *service.Manager) *Server {
	if svcMgr == nil {
		svcMgr = service.NewManager(service.SyncConfig())
	}
	s := &Server{
		addr:   addr,
		store:  store,
		poller: poller,
		svcMgr: svcMgr,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/favicon.ico", s.handleFavicon)
	mux.HandleFunc("/api/ping", s.handlePing)
	mux.HandleFunc("/api/realtime", s.handleRealtime)
	mux.HandleFunc("/api/hosts/test", s.handleTestHost)
	mux.HandleFunc("/api/hosts/discover", s.handleDiscoverHosts)
	mux.HandleFunc("/api/hosts/", s.handleHostByID)
	mux.HandleFunc("/api/hosts", s.handleHosts)
	mux.HandleFunc("/api/speedtest/history", s.handleSpeedTestHistory)
	mux.HandleFunc("/api/interfaces", s.handleInterfaces)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/history/export", s.handleHistoryExport)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/db/backup", s.handleDBBackup)
	mux.HandleFunc("/api/db/reset", s.handleDBReset)
	mux.HandleFunc("/api/service", s.handleService)
	mux.HandleFunc("/api/server/release", s.handleServerRelease)
	mux.HandleFunc("/ws", s.handleWS)

	webRoot, _ := fs.Sub(embeddedSyncWeb, "web")
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

// Start begins serving HTTP/WS requests until ctx is canceled.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil && service.IsServiceProcess() {
		releaseHost := s.addr
		if h, p, splitErr := net.SplitHostPort(s.addr); splitErr == nil && (h == "" || h == "0.0.0.0" || h == "::") {
			releaseHost = net.JoinHostPort("127.0.0.1", p)
		}
		client := &http.Client{Timeout: 400 * time.Millisecond}
		if resp, rErr := client.Post("http://"+releaseHost+"/api/server/release", "application/json", nil); rErr == nil {
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
	src := "server"
	if service.IsServiceProcess() {
		src = "service"
	}
	hosts, _ := s.store.ListHosts()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"app":         "traffcount-sync",
		"version":     "1.0.0",
		"source":      src,
		"platform":    runtime.GOOS,
		"serviceName": s.svcMgr.Config().ServiceName,
		"hostCount":   len(hosts),
	})
}

func (s *Server) handleServerRelease(w http.ResponseWriter, r *http.Request) {
	if service.IsServiceProcess() {
		writeJSON(w, http.StatusOK, map[string]any{"released": false, "reason": "service"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"released": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(25 * time.Millisecond)
		s.relMu.Lock()
		fn := s.onRelease
		s.relMu.Unlock()
		if fn != nil {
			fn()
		} else {
			tray.NotifyTrayReleaseServer()
		}
	}()
}

func (s *Server) handleRealtime(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.poller.LatestSnapshot())
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		hosts, err := s.store.ListHosts()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for i := range hosts {
			hosts[i].APIToken = ""
		}
		writeJSON(w, http.StatusOK, hosts)

	case http.MethodPost:
		var in syncstore.HostInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		h, err := s.store.CreateHost(in)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.poller.ReloadHosts()
		h.APIToken = ""
		writeJSON(w, http.StatusCreated, h)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTestHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		BaseURL  string `json:"baseUrl"`
		APIToken string `json:"apiToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	res := s.poller.TestHostConnection(r.Context(), req.BaseURL, req.APIToken)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleDiscoverHosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	found := s.poller.DiscoverLANHosts(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"hosts": found})
}

func (s *Server) handleHostByID(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/hosts/")
	rel = strings.Trim(rel, "/")
	if rel == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	parts := strings.SplitN(rel, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	switch action {
	case "":
		s.handleHostCRUD(w, r, id)
	case "sync":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := s.poller.TriggerSync(id); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "connections":
		ifaceIP := r.URL.Query().Get("ip")
		if ifaceIP == "" {
			ifaceIP = r.URL.Query().Get("ifaceIp")
		}
		includeUDP := r.URL.Query().Get("udp") == "1" || r.URL.Query().Get("udp") == "true" || r.URL.Query().Get("includeUdp") == "true"
		body, code, err := s.poller.ProxyConnections(r.Context(), id, ifaceIP, includeUDP)
		if err != nil {
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeRawJSON(w, code, body)
	case "connections/action":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		reqBody, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		body, code, err := s.poller.ProxyConnectionsAction(r.Context(), id, reqBody)
		if err != nil {
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeRawJSON(w, code, body)
	case "apps":
		body, code, err := s.poller.ProxyApps(r.Context(), id)
		if err != nil {
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeRawJSON(w, code, body)
	case "firewall/block":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		reqBody, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		body, code, err := s.poller.ProxyFirewallBlock(r.Context(), id, reqBody)
		if err != nil {
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeRawJSON(w, code, body)
	case "quota":
		body, code, err := s.poller.ProxyQuota(r.Context(), id)
		if err != nil {
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeRawJSON(w, code, body)
	case "speedtest":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, code, err := s.poller.TriggerHostSpeedTest(r.Context(), id)
		if err != nil {
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeRawJSON(w, code, body)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) handleHostCRUD(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h, err := s.store.GetHost(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "host not found"})
			return
		}
		h.APIToken = ""
		writeJSON(w, http.StatusOK, h)

	case http.MethodPut, http.MethodPatch:
		var in syncstore.HostInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		h, err := s.store.UpdateHost(id, in)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.poller.ReloadHosts()
		h.APIToken = ""
		writeJSON(w, http.StatusOK, h)

	case http.MethodDelete:
		if err := s.store.DeleteHost(id); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "host not found"})
			return
		}
		s.poller.ReloadHosts()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSpeedTestHistory(w http.ResponseWriter, r *http.Request) {
	hostID := r.URL.Query().Get("hostId")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	list, err := s.store.ListSpeedTests(hostID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleInterfaces(w http.ResponseWriter, r *http.Request) {
	hostID := r.URL.Query().Get("hostId")
	ifaces, err := s.store.ListHostInterfaces(hostID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ifaces)
}

func (s *Server) parseHistoryRange(r *http.Request) (hostID, ifaceID string, startTS, endTS int64, gran string, hideLoopback bool) {
	q := r.URL.Query()
	hostID = q.Get("hostId")
	if hostID == "" {
		hostID = "all"
	}
	ifaceID = q.Get("iface")
	if ifaceID == "" {
		ifaceID = "all"
	}
	gran = q.Get("granularity")
	now := time.Now()
	loc := now.Location()
	endTS = now.Unix()
	startTS = now.AddDate(0, 0, -13).Unix()

	switch q.Get("period") {
	case "today":
		startTS = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Unix()
		if gran == "" {
			gran = "hour"
		}
	case "24h":
		startTS = now.Add(-23 * time.Hour).Unix()
		if gran == "" {
			gran = "hour"
		}
	default:
		if daysStr := q.Get("days"); daysStr != "" {
			if d, err := strconv.Atoi(daysStr); err == nil && d > 0 {
				t0 := now.AddDate(0, 0, -(d - 1))
				startTS = time.Date(t0.Year(), t0.Month(), t0.Day(), 0, 0, 0, 0, loc).Unix()
			}
		} else {
			if stStr := q.Get("start"); stStr != "" {
				if v, err := strconv.ParseInt(stStr, 10, 64); err == nil {
					startTS = v
				} else if t, err := time.ParseInLocation("2006-01-02", stStr, loc); err == nil {
					startTS = t.Unix()
				}
			}
			if enStr := q.Get("end"); enStr != "" {
				if v, err := strconv.ParseInt(enStr, 10, 64); err == nil {
					endTS = v
				} else if t, err := time.ParseInLocation("2006-01-02", enStr, loc); err == nil {
					endTS = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, loc).Unix()
				}
			}
		}
	}
	if gran == "" {
		if endTS-startTS <= 72*3600 {
			gran = "hour"
		} else {
			gran = "day"
		}
	}
	hideLoopback = true
	if cfg, err := s.store.GetSettings(); err == nil {
		hideLoopback = cfg.HideLoopback
	}
	return hostID, ifaceID, startTS, endTS, gran, hideLoopback
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	hostID, ifaceID, startTS, endTS, gran, hideLoopback := s.parseHistoryRange(r)
	rep, err := s.store.QueryFleetHistory(hostID, ifaceID, startTS, endTS, gran, hideLoopback)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleHistoryExport(w http.ResponseWriter, r *http.Request) {
	hostID, ifaceID, startTS, endTS, gran, hideLoopback := s.parseHistoryRange(r)
	rep, err := s.store.QueryFleetHistory(hostID, ifaceID, startTS, endTS, gran, hideLoopback)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "json" {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="traffcount-sync-history-%s.json"`, time.Now().Format("20060102-150405")))
		writeJSON(w, http.StatusOK, rep)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="traffcount-sync-history-%s.csv"`, time.Now().Format("20060102-150405")))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Section", "TimeOrHost", "LabelOrApp", "RxBytes", "TxBytes", "TotalBytes"})
	for _, b := range rep.Series {
		_ = cw.Write([]string{"Timeline", strconv.FormatInt(b.Timestamp, 10), b.Label, strconv.FormatUint(b.RxBytes, 10), strconv.FormatUint(b.TxBytes, 10), strconv.FormatUint(b.Total, 10)})
	}
	for _, h := range rep.ByHost {
		_ = cw.Write([]string{"ByHost", h.HostName, h.Platform, strconv.FormatUint(h.RxBytes, 10), strconv.FormatUint(h.TxBytes, 10), strconv.FormatUint(h.TotalBytes, 10)})
	}
	for _, p := range rep.ByProcess {
		_ = cw.Write([]string{"TopApp", p.HostName, p.ProcessName, strconv.FormatUint(p.RxBytes, 10), strconv.FormatUint(p.TxBytes, 10), strconv.FormatUint(p.TotalBytes, 10)})
	}
	cw.Flush()
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
		var cfg syncstore.SyncSettings
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if err := s.store.SaveSettings(cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if cfg.RetentionDays > 0 {
			go func(days int) { _ = s.store.PruneOldData(days) }(cfg.RetentionDays)
		}
		writeJSON(w, http.StatusOK, cfg)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDBBackup(w http.ResponseWriter, r *http.Request) {
	tmp, err := os.CreateTemp("", "traffcount-sync-backup-*.db")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	if err := s.store.BackupTo(tmpPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="traffcount-sync-backup-%s.db"`, time.Now().Format("20060102-150405")))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleDBReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := s.store.ResetHistory(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.svcMgr.QueryStatus())

	case http.MethodPost:
		var req struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}

		if service.IsServiceProcess() && (req.Action == "stop" || req.Action == "uninstall") {
			st := s.svcMgr.QueryStatus()
			if req.Action == "uninstall" {
				st.Installed = false
				st.StateText = "Not Installed"
			} else {
				st.StateText = "Stopped"
			}
			st.Running = false
			writeJSON(w, http.StatusOK, st)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			go func(act string) {
				time.Sleep(80 * time.Millisecond)
				if act == "uninstall" {
					_ = s.svcMgr.StopAndUninstall()
				} else {
					_ = s.svcMgr.StopService()
				}
			}(req.Action)
			return
		}

		if !service.IsServiceProcess() && (req.Action == "start" || req.Action == "install") {
			st := s.svcMgr.QueryStatus()
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
					_ = s.svcMgr.InstallAndStart()
				} else {
					_ = s.svcMgr.StartService()
				}
			}(req.Action)
			return
		}

		var err error
		switch req.Action {
		case "install":
			err = s.svcMgr.InstallAndStart()
		case "uninstall":
			err = s.svcMgr.StopAndUninstall()
		case "start":
			err = s.svcMgr.StartService()
		case "stop":
			err = s.svcMgr.StopService()
		case "restart":
			_ = s.svcMgr.StopService()
			time.Sleep(500 * time.Millisecond)
			err = s.svcMgr.StartService()
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		time.Sleep(350 * time.Millisecond)
		writeJSON(w, http.StatusOK, s.svcMgr.QueryStatus())

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

	_ = conn.WriteJSON(s.poller.LatestSnapshot())

	ch, unsubscribe := s.poller.Subscribe()
	defer unsubscribe()

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

func writeRawJSON(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
