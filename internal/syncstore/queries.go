package syncstore

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// HostRecord represents a managed remote TraffCount host.
type HostRecord struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	BaseURL        string `json:"baseUrl"`
	APIToken       string `json:"apiToken,omitempty"`
	HasToken       bool   `json:"hasToken"`
	Enabled        bool   `json:"enabled"`
	PollIntervalMs int    `json:"pollIntervalMs"`
	Tags           string `json:"tags"`
	Platform       string `json:"platform"`
	Version        string `json:"version"`
	SourceMode     string `json:"sourceMode"`
	Status         string `json:"status"` // "online", "offline", "auth_error", "paused", "pending"
	LastSeen       int64  `json:"lastSeen"`
	LastSyncTS     int64  `json:"lastSyncTs"`
	LastLatencyMs  int64  `json:"lastLatencyMs"`
	LastError      string `json:"lastError"`
	CreatedAt      int64  `json:"createdAt"`
}

// HostInput holds fields for creating or updating a remote host.
type HostInput struct {
	Name           string `json:"name"`
	BaseURL        string `json:"baseUrl"`
	APIToken       string `json:"apiToken"`
	KeepToken      bool   `json:"keepToken"`
	Enabled        *bool  `json:"enabled"`
	PollIntervalMs int    `json:"pollIntervalMs"`
	Tags           string `json:"tags"`
}

// HostInterfaceRecord caches interface metadata synced from a remote host.
type HostInterfaceRecord struct {
	HostID        string `json:"hostId"`
	IfaceID       string `json:"ifaceId"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	MAC           string `json:"mac"`
	IPv4          string `json:"ipv4"`
	IPv6          string `json:"ipv6"`
	LinkSpeedBits uint64 `json:"linkSpeedBits"`
	IsUp          bool   `json:"isUp"`
	IsLoopback    bool   `json:"isLoopback"`
	LastSeen      int64  `json:"lastSeen"`
}

// HostTrafficRow represents a per-host hourly interface rollup stored in the central DB.
type HostTrafficRow struct {
	IfaceID  string
	BucketTS int64
	RxBytes  uint64
	TxBytes  uint64
}

// HostProcessRow represents a per-host hourly application rollup stored in the central DB.
type HostProcessRow struct {
	ProcessName string
	ExePath     string
	BucketTS    int64
	RxBytes     uint64
	TxBytes     uint64
	Blocked     bool
}

// HostSpeedTestRow represents a speed & bufferbloat run imported from a remote host.
type HostSpeedTestRow struct {
	ID               int64   `json:"id"`
	HostID           string  `json:"hostId"`
	HostName         string  `json:"hostName,omitempty"`
	Timestamp        int64   `json:"timestamp"`
	UnloadedPingMs   float64 `json:"unloadedPingMs"`
	JitterMs         float64 `json:"jitterMs"`
	LoadedPingMs     float64 `json:"loadedPingMs"`
	DownloadMbps     float64 `json:"downloadMbps"`
	UploadMbps       float64 `json:"uploadMbps"`
	BufferbloatGrade string  `json:"bufferbloatGrade"`
	ServerLabel      string  `json:"serverLabel"`
	Error            string  `json:"error,omitempty"`
}

// BucketPoint represents aggregated Rx/Tx volume in an hour or day bucket.
type BucketPoint struct {
	Timestamp int64  `json:"timestamp"`
	Label     string `json:"label"`
	RxBytes   uint64 `json:"rxBytes"`
	TxBytes   uint64 `json:"txBytes"`
	Total     uint64 `json:"total"`
}

// HostTrafficSummary summarizes historical volume per host over a report range.
type HostTrafficSummary struct {
	HostID     string  `json:"hostId"`
	HostName   string  `json:"hostName"`
	Platform   string  `json:"platform"`
	Status     string  `json:"status"`
	RxBytes    uint64  `json:"rxBytes"`
	TxBytes    uint64  `json:"txBytes"`
	TotalBytes uint64  `json:"totalBytes"`
	SharePct   float64 `json:"sharePct"`
}

// FleetInterfaceSummary summarizes historical volume per (host, interface).
type FleetInterfaceSummary struct {
	HostID      string  `json:"hostId"`
	HostName    string  `json:"hostName"`
	IfaceID     string  `json:"ifaceId"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	MAC         string  `json:"mac"`
	RxBytes     uint64  `json:"rxBytes"`
	TxBytes     uint64  `json:"txBytes"`
	TotalBytes  uint64  `json:"totalBytes"`
	SharePct    float64 `json:"sharePct"`
}

// FleetProcessSummary summarizes historical application volume tagged by host.
type FleetProcessSummary struct {
	HostID      string  `json:"hostId"`
	HostName    string  `json:"hostName"`
	ProcessName string  `json:"processName"`
	ExePath     string  `json:"exePath"`
	RxBytes     uint64  `json:"rxBytes"`
	TxBytes     uint64  `json:"txBytes"`
	TotalBytes  uint64  `json:"totalBytes"`
	SharePct    float64 `json:"sharePct"`
	Blocked     bool    `json:"blocked"`
}

// FleetHistoryReport is returned by /api/history for multi-host or single-host analytics.
type FleetHistoryReport struct {
	StartTime     int64                   `json:"startTime"`
	EndTime       int64                   `json:"endTime"`
	Granularity   string                  `json:"granularity"`
	SelectedHost  string                  `json:"selectedHost"`
	SelectedIface string                  `json:"selectedIface"`
	TotalRxBytes  uint64                  `json:"totalRxBytes"`
	TotalTxBytes  uint64                  `json:"totalTxBytes"`
	CombinedBytes uint64                  `json:"combinedBytes"`
	AvgDailyBytes uint64                  `json:"avgDailyBytes"`
	Series        []BucketPoint           `json:"series"`
	ByHost        []HostTrafficSummary    `json:"byHost"`
	ByInterface   []FleetInterfaceSummary `json:"byInterface"`
	ByProcess     []FleetProcessSummary   `json:"byProcess"`
}

// NormalizeHostBaseURL validates and normalizes a remote TraffCount URL (e.g. "http://192.168.1.50:9876").
func NormalizeHostBaseURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("baseUrl is required")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid baseUrl: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q (expected http or https)", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("baseUrl missing host")
	}
	if u.Port() == "" {
		u.Host = u.Host + ":9876"
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func generateHostID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "host_" + hex.EncodeToString(b[:])
}

// CreateHost inserts a new remote host configuration.
func (s *Store) CreateHost(in HostInput) (HostRecord, error) {
	normURL, err := NormalizeHostBaseURL(in.BaseURL)
	if err != nil {
		return HostRecord{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		if u, uErr := url.Parse(normURL); uErr == nil {
			name = u.Hostname()
		}
		if name == "" {
			name = normURL
		}
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	pollMs := in.PollIntervalMs
	if pollMs < 500 || pollMs > 30000 {
		pollMs = 1000
	}
	status := "pending"
	if !enabled {
		status = "paused"
	}
	now := time.Now().Unix()
	id := generateHostID()

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err = s.db.Exec(`
		INSERT INTO hosts (id, name, base_url, api_token, enabled, poll_interval_ms, tags, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, name, normURL, strings.TrimSpace(in.APIToken), boolToInt(enabled), pollMs, strings.TrimSpace(in.Tags), status, now)
	if err != nil {
		return HostRecord{}, fmt.Errorf("create host: %w", err)
	}
	return s.getHostLocked(id)
}

// UpdateHost updates mutable configuration for an existing host.
func (s *Store) UpdateHost(id string, in HostInput) (HostRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := s.getHostLocked(id)
	if err != nil {
		return HostRecord{}, err
	}

	normURL := existing.BaseURL
	if strings.TrimSpace(in.BaseURL) != "" {
		normURL, err = NormalizeHostBaseURL(in.BaseURL)
		if err != nil {
			return HostRecord{}, err
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = existing.Name
	}
	token := strings.TrimSpace(in.APIToken)
	if in.KeepToken && token == "" {
		token = existing.APIToken
	}
	enabled := existing.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	pollMs := existing.PollIntervalMs
	if in.PollIntervalMs >= 500 && in.PollIntervalMs <= 30000 {
		pollMs = in.PollIntervalMs
	}
	tags := strings.TrimSpace(in.Tags)

	status := existing.Status
	if !enabled {
		status = "paused"
	} else if status == "paused" {
		status = "pending"
	}

	_, err = s.db.Exec(`
		UPDATE hosts
		SET name = ?, base_url = ?, api_token = ?, enabled = ?, poll_interval_ms = ?, tags = ?, status = ?
		WHERE id = ?
	`, name, normURL, token, boolToInt(enabled), pollMs, tags, status, id)
	if err != nil {
		return HostRecord{}, fmt.Errorf("update host: %w", err)
	}
	return s.getHostLocked(id)
}

// DeleteHost removes a host (cascade-deleting stored fleet history when purgeData or cascade applies).
func (s *Store) DeleteHost(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM hosts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetHost fetches a single host by ID.
func (s *Store) GetHost(id string) (HostRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getHostLocked(id)
}

func (s *Store) getHostLocked(id string) (HostRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, name, base_url, api_token, enabled, poll_interval_ms, tags,
		       platform, version, source_mode, status, last_seen, last_sync_ts, last_latency_ms, last_error, created_at
		FROM hosts WHERE id = ?
	`, id)
	return scanHostRow(row)
}

// ListHosts returns all configured remote hosts ordered by creation date / name.
func (s *Store) ListHosts() ([]HostRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, name, base_url, api_token, enabled, poll_interval_ms, tags,
		       platform, version, source_mode, status, last_seen, last_sync_ts, last_latency_ms, last_error, created_at
		FROM hosts
		ORDER BY created_at ASC, name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]HostRecord, 0)
	for rows.Next() {
		h, err := scanHostRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanHostRow(sc rowScanner) (HostRecord, error) {
	var h HostRecord
	var enInt int
	err := sc.Scan(
		&h.ID, &h.Name, &h.BaseURL, &h.APIToken, &enInt, &h.PollIntervalMs, &h.Tags,
		&h.Platform, &h.Version, &h.SourceMode, &h.Status, &h.LastSeen, &h.LastSyncTS,
		&h.LastLatencyMs, &h.LastError, &h.CreatedAt,
	)
	if err != nil {
		return h, err
	}
	h.Enabled = enInt != 0
	h.HasToken = h.APIToken != ""
	return h, nil
}

// UpdateHostRuntime persists latest poll connectivity and agent identity metadata.
func (s *Store) UpdateHostRuntime(id, status, platform, version, sourceMode, lastErr string, latencyMs, seenTS int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE hosts
		SET status = ?,
		    platform = CASE WHEN ? != '' THEN ? ELSE platform END,
		    version = CASE WHEN ? != '' THEN ? ELSE version END,
		    source_mode = CASE WHEN ? != '' THEN ? ELSE source_mode END,
		    last_error = ?,
		    last_latency_ms = CASE WHEN ? > 0 THEN ? ELSE last_latency_ms END,
		    last_seen = CASE WHEN ? > 0 THEN ? ELSE last_seen END
		WHERE id = ?
	`, status, platform, platform, version, version, sourceMode, sourceMode, lastErr, latencyMs, latencyMs, seenTS, seenTS, id)
	return err
}

// UpdateHostLastSync records the latest successful historical sync timestamp.
func (s *Store) UpdateHostLastSync(id string, syncTS int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE hosts SET last_sync_ts = ? WHERE id = ?`, syncTS, id)
	return err
}

// UpsertHostInterfaces stores or refreshes cached interface metadata for a host.
func (s *Store) UpsertHostInterfaces(hostID string, ifaces []HostInterfaceRecord) error {
	if len(ifaces) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO host_interfaces (host_id, iface_id, name, description, mac, ipv4, ipv6, link_speed_bits, is_up, is_loopback, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host_id, iface_id) DO UPDATE SET
			name = excluded.name,
			description = CASE WHEN excluded.description != '' THEN excluded.description ELSE host_interfaces.description END,
			mac = CASE WHEN excluded.mac != '' THEN excluded.mac ELSE host_interfaces.mac END,
			ipv4 = CASE WHEN excluded.ipv4 != '' THEN excluded.ipv4 ELSE host_interfaces.ipv4 END,
			ipv6 = CASE WHEN excluded.ipv6 != '' THEN excluded.ipv6 ELSE host_interfaces.ipv6 END,
			link_speed_bits = CASE WHEN excluded.link_speed_bits > 0 THEN excluded.link_speed_bits ELSE host_interfaces.link_speed_bits END,
			is_up = excluded.is_up,
			is_loopback = excluded.is_loopback,
			last_seen = MAX(host_interfaces.last_seen, excluded.last_seen)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, r := range ifaces {
		ls := r.LastSeen
		if ls == 0 {
			ls = now
		}
		if _, err := stmt.Exec(hostID, r.IfaceID, r.Name, r.Description, r.MAC, r.IPv4, r.IPv6, r.LinkSpeedBits, boolToInt(r.IsUp), boolToInt(r.IsLoopback), ls); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListHostInterfaces returns cached interfaces for one host or all hosts (hostID == "" or "all").
func (s *Store) ListHostInterfaces(hostID string) ([]HostInterfaceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows *sql.Rows
	var err error
	if hostID == "" || hostID == "all" {
		rows, err = s.db.Query(`
			SELECT host_id, iface_id, name, description, mac, ipv4, ipv6, link_speed_bits, is_up, is_loopback, last_seen
			FROM host_interfaces
			ORDER BY host_id ASC, name ASC
		`)
	} else {
		rows, err = s.db.Query(`
			SELECT host_id, iface_id, name, description, mac, ipv4, ipv6, link_speed_bits, is_up, is_loopback, last_seen
			FROM host_interfaces
			WHERE host_id = ?
			ORDER BY name ASC
		`, hostID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]HostInterfaceRecord, 0)
	for rows.Next() {
		var r HostInterfaceRecord
		var isUp, isLoop int
		if err := rows.Scan(&r.HostID, &r.IfaceID, &r.Name, &r.Description, &r.MAC, &r.IPv4, &r.IPv6, &r.LinkSpeedBits, &isUp, &isLoop, &r.LastSeen); err != nil {
			return nil, err
		}
		r.IsUp = isUp != 0
		r.IsLoopback = isLoop != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertHostTrafficHourly performs idempotent replacement of exact hourly interface totals synced from host.
func (s *Store) UpsertHostTrafficHourly(hostID string, rows []HostTrafficRow) error {
	if len(rows) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO host_traffic_hourly (host_id, iface_id, bucket_ts, rx_bytes, tx_bytes)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(host_id, iface_id, bucket_ts) DO UPDATE SET
			rx_bytes = excluded.rx_bytes,
			tx_bytes = excluded.tx_bytes
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range rows {
		if _, err := stmt.Exec(hostID, r.IfaceID, r.BucketTS, r.RxBytes, r.TxBytes); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertHostProcessHourly performs idempotent replacement of exact hourly app totals synced from host.
func (s *Store) UpsertHostProcessHourly(hostID string, rows []HostProcessRow) error {
	if len(rows) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO host_process_hourly (host_id, process_name, exe_path, bucket_ts, rx_bytes, tx_bytes, blocked)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host_id, process_name, bucket_ts) DO UPDATE SET
			exe_path = CASE WHEN excluded.exe_path != '' THEN excluded.exe_path ELSE host_process_hourly.exe_path END,
			rx_bytes = excluded.rx_bytes,
			tx_bytes = excluded.tx_bytes,
			blocked = excluded.blocked
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range rows {
		if _, err := stmt.Exec(hostID, r.ProcessName, r.ExePath, r.BucketTS, r.RxBytes, r.TxBytes, boolToInt(r.Blocked)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertHostSpeedTests deduplicates and persists speed tests synced from a remote host.
func (s *Store) UpsertHostSpeedTests(hostID string, rows []HostSpeedTestRow) error {
	if len(rows) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO host_speed_tests (host_id, ts, unloaded_ping_ms, jitter_ms, loaded_ping_ms, download_mbps, upload_mbps, bufferbloat_grade, server_label, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host_id, ts) DO UPDATE SET
			unloaded_ping_ms = excluded.unloaded_ping_ms,
			jitter_ms = excluded.jitter_ms,
			loaded_ping_ms = excluded.loaded_ping_ms,
			download_mbps = excluded.download_mbps,
			upload_mbps = excluded.upload_mbps,
			bufferbloat_grade = excluded.bufferbloat_grade,
			server_label = excluded.server_label,
			error = excluded.error
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range rows {
		if _, err := stmt.Exec(hostID, r.Timestamp, r.UnloadedPingMs, r.JitterMs, r.LoadedPingMs, r.DownloadMbps, r.UploadMbps, r.BufferbloatGrade, r.ServerLabel, r.Error); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListSpeedTests returns recent speed test runs across a host or all hosts.
func (s *Store) ListSpeedTests(hostID string, limit int) ([]HostSpeedTestRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT st.id, st.host_id, COALESCE(h.name, st.host_id), st.ts, st.unloaded_ping_ms, st.jitter_ms,
		       st.loaded_ping_ms, st.download_mbps, st.upload_mbps, st.bufferbloat_grade, st.server_label, st.error
		FROM host_speed_tests st
		LEFT JOIN hosts h ON h.id = st.host_id
	`
	var args []any
	if hostID != "" && hostID != "all" {
		query += ` WHERE st.host_id = ?`
		args = append(args, hostID)
	}
	query += ` ORDER BY st.ts DESC, st.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]HostSpeedTestRow, 0)
	for rows.Next() {
		var r HostSpeedTestRow
		if err := rows.Scan(&r.ID, &r.HostID, &r.HostName, &r.Timestamp, &r.UnloadedPingMs, &r.JitterMs, &r.LoadedPingMs, &r.DownloadMbps, &r.UploadMbps, &r.BufferbloatGrade, &r.ServerLabel, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryFleetHistory aggregates multi-host traffic across a time range and optional host/interface filter.
func (s *Store) QueryFleetHistory(hostID, ifaceID string, startTS, endTS int64, granularity string, hideLoopback bool) (*FleetHistoryReport, error) {
	if granularity != "hour" && granularity != "day" {
		granularity = "day"
	}
	if hostID == "" {
		hostID = "all"
	}
	if ifaceID == "" {
		ifaceID = "all"
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	report := &FleetHistoryReport{
		StartTime:     startTS,
		EndTime:       endTS,
		Granularity:   granularity,
		SelectedHost:  hostID,
		SelectedIface: ifaceID,
		Series:        []BucketPoint{},
		ByHost:        []HostTrafficSummary{},
		ByInterface:   []FleetInterfaceSummary{},
		ByProcess:     []FleetProcessSummary{},
	}

	where := []string{"t.bucket_ts >= ?", "t.bucket_ts <= ?"}
	args := []any{startTS, endTS}
	if hostID != "all" {
		where = append(where, "t.host_id = ?")
		args = append(args, hostID)
	}
	if ifaceID != "all" {
		where = append(where, "t.iface_id = ?")
		args = append(args, ifaceID)
	} else if hideLoopback {
		where = append(where, "COALESCE(hi.is_loopback, 0) = 0")
	}

	whereSQL := strings.Join(where, " AND ")

	// 1. Time series query
	seriesSQL := fmt.Sprintf(`
		SELECT t.bucket_ts, SUM(t.rx_bytes), SUM(t.tx_bytes)
		FROM host_traffic_hourly t
		LEFT JOIN host_interfaces hi ON hi.host_id = t.host_id AND hi.iface_id = t.iface_id
		WHERE %s
		GROUP BY t.bucket_ts
		ORDER BY t.bucket_ts ASC
	`, whereSQL)

	rows, err := s.db.Query(seriesSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("query fleet series: %w", err)
	}
	defer rows.Close()

	bucketMap := make(map[int64]*BucketPoint)
	loc := time.Local

	for rows.Next() {
		var ts int64
		var rx, tx uint64
		if err := rows.Scan(&ts, &rx, &tx); err != nil {
			return nil, err
		}
		report.TotalRxBytes += rx
		report.TotalTxBytes += tx

		tm := time.Unix(ts, 0).In(loc)
		var keyTS int64
		var label string
		if granularity == "hour" {
			keyTS = time.Date(tm.Year(), tm.Month(), tm.Day(), tm.Hour(), 0, 0, 0, loc).Unix()
			label = tm.Format("Jan 02 15:00")
		} else {
			keyTS = time.Date(tm.Year(), tm.Month(), tm.Day(), 0, 0, 0, 0, loc).Unix()
			label = tm.Format("Jan 02")
		}
		bp, ok := bucketMap[keyTS]
		if !ok {
			bp = &BucketPoint{Timestamp: keyTS, Label: label}
			bucketMap[keyTS] = bp
		}
		bp.RxBytes += rx
		bp.TxBytes += tx
		bp.Total = bp.RxBytes + bp.TxBytes
	}

	// Zero-fill continuous timeline
	start := time.Unix(startTS, 0).In(loc)
	end := time.Unix(endTS, 0).In(loc)
	if granularity == "hour" {
		cur := time.Date(start.Year(), start.Month(), start.Day(), start.Hour(), 0, 0, 0, loc)
		for !cur.After(end) {
			keyTS := cur.Unix()
			if _, ok := bucketMap[keyTS]; !ok {
				bucketMap[keyTS] = &BucketPoint{Timestamp: keyTS, Label: cur.Format("Jan 02 15:00")}
			}
			cur = cur.Add(time.Hour)
		}
	} else {
		cur := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
		for !cur.After(end) {
			keyTS := cur.Unix()
			if _, ok := bucketMap[keyTS]; !ok {
				bucketMap[keyTS] = &BucketPoint{Timestamp: keyTS, Label: cur.Format("Jan 02")}
			}
			cur = cur.AddDate(0, 0, 1)
		}
	}

	keys := make([]int64, 0, len(bucketMap))
	for k := range bucketMap {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		report.Series = append(report.Series, *bucketMap[k])
	}

	report.CombinedBytes = report.TotalRxBytes + report.TotalTxBytes
	spanDays := int64(end.Sub(start).Hours()/24) + 1
	if spanDays < 1 {
		spanDays = 1
	}
	report.AvgDailyBytes = report.CombinedBytes / uint64(spanDays)

	// 2. Per-Host Breakdown
	hostWhere := []string{"t.bucket_ts >= ?", "t.bucket_ts <= ?"}
	hostArgs := []any{startTS, endTS}
	if hostID != "all" {
		hostWhere = append(hostWhere, "t.host_id = ?")
		hostArgs = append(hostArgs, hostID)
	}
	if ifaceID != "all" {
		hostWhere = append(hostWhere, "t.iface_id = ?")
		hostArgs = append(hostArgs, ifaceID)
	} else if hideLoopback {
		hostWhere = append(hostWhere, "COALESCE(hi.is_loopback, 0) = 0")
	}

	byHostSQL := fmt.Sprintf(`
		SELECT t.host_id, COALESCE(h.name, t.host_id), COALESCE(h.platform, ''), COALESCE(h.status, ''),
		       SUM(t.rx_bytes), SUM(t.tx_bytes)
		FROM host_traffic_hourly t
		LEFT JOIN hosts h ON h.id = t.host_id
		LEFT JOIN host_interfaces hi ON hi.host_id = t.host_id AND hi.iface_id = t.iface_id
		WHERE %s
		GROUP BY t.host_id
		ORDER BY (SUM(t.rx_bytes) + SUM(t.tx_bytes)) DESC
	`, strings.Join(hostWhere, " AND "))

	if hRows, err := s.db.Query(byHostSQL, hostArgs...); err == nil {
		for hRows.Next() {
			var hs HostTrafficSummary
			if err := hRows.Scan(&hs.HostID, &hs.HostName, &hs.Platform, &hs.Status, &hs.RxBytes, &hs.TxBytes); err == nil {
				hs.TotalBytes = hs.RxBytes + hs.TxBytes
				if report.CombinedBytes > 0 {
					hs.SharePct = (float64(hs.TotalBytes) / float64(report.CombinedBytes)) * 100.0
				}
				report.ByHost = append(report.ByHost, hs)
			}
		}
		_ = hRows.Close()
	}

	// 3. Per-Interface Breakdown
	byIfaceWhere := []string{"t.bucket_ts >= ?", "t.bucket_ts <= ?"}
	byIfaceArgs := []any{startTS, endTS}
	if hostID != "all" {
		byIfaceWhere = append(byIfaceWhere, "t.host_id = ?")
		byIfaceArgs = append(byIfaceArgs, hostID)
	}
	if hideLoopback {
		byIfaceWhere = append(byIfaceWhere, "COALESCE(hi.is_loopback, 0) = 0")
	}
	byIfaceSQL := fmt.Sprintf(`
		SELECT t.host_id, COALESCE(h.name, t.host_id), t.iface_id,
		       COALESCE(NULLIF(hi.name, ''), t.iface_id), COALESCE(hi.description, ''), COALESCE(hi.mac, ''),
		       SUM(t.rx_bytes), SUM(t.tx_bytes)
		FROM host_traffic_hourly t
		LEFT JOIN hosts h ON h.id = t.host_id
		LEFT JOIN host_interfaces hi ON hi.host_id = t.host_id AND hi.iface_id = t.iface_id
		WHERE %s
		GROUP BY t.host_id, t.iface_id
		ORDER BY (SUM(t.rx_bytes) + SUM(t.tx_bytes)) DESC
		LIMIT 40
	`, strings.Join(byIfaceWhere, " AND "))

	if iRows, err := s.db.Query(byIfaceSQL, byIfaceArgs...); err == nil {
		for iRows.Next() {
			var is FleetInterfaceSummary
			if err := iRows.Scan(&is.HostID, &is.HostName, &is.IfaceID, &is.Name, &is.Description, &is.MAC, &is.RxBytes, &is.TxBytes); err == nil {
				is.TotalBytes = is.RxBytes + is.TxBytes
				if report.CombinedBytes > 0 {
					is.SharePct = (float64(is.TotalBytes) / float64(report.CombinedBytes)) * 100.0
				}
				report.ByInterface = append(report.ByInterface, is)
			}
		}
		_ = iRows.Close()
	}

	// 4. Top Applications across Host(s)
	procWhere := []string{"p.bucket_ts >= ?", "p.bucket_ts <= ?"}
	procArgs := []any{startTS, endTS}
	if hostID != "all" {
		procWhere = append(procWhere, "p.host_id = ?")
		procArgs = append(procArgs, hostID)
	}
	procSQL := fmt.Sprintf(`
		SELECT p.host_id, COALESCE(h.name, p.host_id), p.process_name, MAX(p.exe_path),
		       SUM(p.rx_bytes), SUM(p.tx_bytes), MAX(p.blocked)
		FROM host_process_hourly p
		LEFT JOIN hosts h ON h.id = p.host_id
		WHERE %s
		GROUP BY p.host_id, p.process_name
		HAVING (SUM(p.rx_bytes) + SUM(p.tx_bytes)) > 0
		ORDER BY (SUM(p.rx_bytes) + SUM(p.tx_bytes)) DESC
		LIMIT 35
	`, strings.Join(procWhere, " AND "))

	if pRows, err := s.db.Query(procSQL, procArgs...); err == nil {
		var procSum uint64
		for pRows.Next() {
			var ps FleetProcessSummary
			var blk int
			if err := pRows.Scan(&ps.HostID, &ps.HostName, &ps.ProcessName, &ps.ExePath, &ps.RxBytes, &ps.TxBytes, &blk); err == nil {
				ps.TotalBytes = ps.RxBytes + ps.TxBytes
				ps.Blocked = blk != 0
				procSum += ps.TotalBytes
				report.ByProcess = append(report.ByProcess, ps)
			}
		}
		_ = pRows.Close()
		denom := procSum
		if report.CombinedBytes > denom {
			denom = report.CombinedBytes
		}
		if denom > 0 {
			for i := range report.ByProcess {
				report.ByProcess[i].SharePct = (float64(report.ByProcess[i].TotalBytes) / float64(denom)) * 100.0
			}
		}
	}

	return report, nil
}

// BackupTo creates an atomic WAL-checkpointed snapshot at dstPath.
func (s *Store) BackupTo(dstPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.Remove(dstPath)
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	_, err := s.db.Exec(`VACUUM INTO ?`, dstPath)
	return err
}

// ResetHistory wipes collected fleet telemetry while keeping registered hosts and settings.
func (s *Store) ResetHistory() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(`DELETE FROM host_process_hourly`)
	_, _ = s.db.Exec(`DELETE FROM host_traffic_hourly`)
	_, _ = s.db.Exec(`DELETE FROM host_speed_tests`)
	_, err := s.db.Exec(`UPDATE hosts SET last_sync_ts = 0`)
	return err
}

// PruneOldData deletes telemetry older than retentionDays.
func (s *Store) PruneOldData(retentionDays int) error {
	if retentionDays <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().AddDate(0, 0, -retentionDays).Unix()
	_, _ = s.db.Exec(`DELETE FROM host_process_hourly WHERE bucket_ts < ?`, cutoff)
	_, err := s.db.Exec(`DELETE FROM host_traffic_hourly WHERE bucket_ts < ?`, cutoff)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
