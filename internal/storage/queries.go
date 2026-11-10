package storage

import (
	"fmt"
	"sort"
	"time"
)

// BucketPoint represents aggregated traffic in a time bucket (hour or day).
type BucketPoint struct {
	Timestamp int64  `json:"timestamp"`
	Label     string `json:"label"`
	RxBytes   uint64 `json:"rxBytes"`
	TxBytes   uint64 `json:"txBytes"`
	Total     uint64 `json:"total"`
}

// InterfaceSummary represents traffic totals for a single NIC over a selected period.
type InterfaceSummary struct {
	IfaceID     string  `json:"ifaceId"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	MAC         string  `json:"mac"`
	RxBytes     uint64  `json:"rxBytes"`
	TxBytes     uint64  `json:"txBytes"`
	TotalBytes  uint64  `json:"totalBytes"`
	SharePct    float64 `json:"sharePct"`
}

// ProcessHistorySummary represents historical traffic totals for a single application process.
type ProcessHistorySummary struct {
	ProcessName string  `json:"processName"`
	ExePath     string  `json:"exePath"`
	RxBytes     uint64  `json:"rxBytes"`
	TxBytes     uint64  `json:"txBytes"`
	TotalBytes  uint64  `json:"totalBytes"`
	SharePct    float64 `json:"sharePct"`
	Blocked     bool    `json:"blocked"`
}

// HistoryReport is returned by QueryHistory for the dashboard.
type HistoryReport struct {
	StartTime     int64                   `json:"startTime"`
	EndTime       int64                   `json:"endTime"`
	Granularity   string                  `json:"granularity"` // "hour" or "day"
	SelectedIface string                  `json:"selectedIface"`
	TotalRxBytes  uint64                  `json:"totalRxBytes"`
	TotalTxBytes  uint64                  `json:"totalTxBytes"`
	CombinedBytes uint64                  `json:"combinedBytes"`
	AvgDailyBytes uint64                  `json:"avgDailyBytes"`
	Series        []BucketPoint           `json:"series"`
	ByInterface   []InterfaceSummary      `json:"byInterface"`
	ByProcess     []ProcessHistorySummary `json:"byProcess"`
}

// TruncateToHour truncates a timestamp to the start of its UTC/local hour.
func TruncateToHour(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, t.Hour(), 0, 0, 0, t.Location()).Unix()
}

// RecordTrafficBatch upserts interface metadata and increments hourly traffic buckets.
func (s *Store) RecordTrafficBatch(deltas []TrafficDelta) error {
	if len(deltas) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	ifaceStmt, err := tx.Prepare(`
		INSERT INTO interfaces (id, name, description, mac, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			description = CASE WHEN excluded.description != '' THEN excluded.description ELSE interfaces.description END,
			mac = CASE WHEN excluded.mac != '' THEN excluded.mac ELSE interfaces.mac END,
			last_seen = excluded.last_seen
	`)
	if err != nil {
		return err
	}
	defer ifaceStmt.Close()

	trafficStmt, err := tx.Prepare(`
		INSERT INTO traffic_hourly (iface_id, bucket_ts, rx_bytes, tx_bytes)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(iface_id, bucket_ts) DO UPDATE SET
			rx_bytes = traffic_hourly.rx_bytes + excluded.rx_bytes,
			tx_bytes = traffic_hourly.tx_bytes + excluded.tx_bytes
	`)
	if err != nil {
		return err
	}
	defer trafficStmt.Close()

	for _, d := range deltas {
		ts := d.Timestamp
		if ts.IsZero() {
			ts = time.Now()
		}
		unixNow := ts.Unix()
		bucketTS := TruncateToHour(ts)

		if _, err := ifaceStmt.Exec(d.IfaceID, d.Name, d.Description, d.MAC, unixNow, unixNow); err != nil {
			return fmt.Errorf("upsert interface %s: %w", d.IfaceID, err)
		}

		if d.RxBytes > 0 || d.TxBytes > 0 {
			if _, err := trafficStmt.Exec(d.IfaceID, bucketTS, d.RxBytes, d.TxBytes); err != nil {
				return fmt.Errorf("upsert traffic %s: %w", d.IfaceID, err)
			}
		}
	}

	return tx.Commit()
}

// RecordProcessTrafficBatch records hourly per-process traffic deltas and detects newly seen applications.
func (s *Store) RecordProcessTrafficBatch(deltas []ProcessTrafficDelta) ([]string, error) {
	if len(deltas) == 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var existingCount int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM known_apps`).Scan(&existingCount)
	isInitialSeed := existingCount == 0

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	checkStmt, err := tx.Prepare(`SELECT 1 FROM known_apps WHERE process_name = ?`)
	if err != nil {
		return nil, err
	}
	defer checkStmt.Close()

	appStmt, err := tx.Prepare(`
		INSERT INTO known_apps (process_name, exe_path, first_seen, last_seen, blocked)
		VALUES (?, ?, ?, ?, 0)
		ON CONFLICT(process_name) DO UPDATE SET
			exe_path = CASE WHEN excluded.exe_path != '' THEN excluded.exe_path ELSE known_apps.exe_path END,
			last_seen = excluded.last_seen
	`)
	if err != nil {
		return nil, err
	}
	defer appStmt.Close()

	trafficStmt, err := tx.Prepare(`
		INSERT INTO process_traffic_hourly (process_name, exe_path, bucket_ts, rx_bytes, tx_bytes)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(process_name, bucket_ts) DO UPDATE SET
			exe_path = CASE WHEN excluded.exe_path != '' THEN excluded.exe_path ELSE process_traffic_hourly.exe_path END,
			rx_bytes = process_traffic_hourly.rx_bytes + excluded.rx_bytes,
			tx_bytes = process_traffic_hourly.tx_bytes + excluded.tx_bytes
	`)
	if err != nil {
		return nil, err
	}
	defer trafficStmt.Close()

	var newApps []string
	for _, d := range deltas {
		if d.ProcessName == "" || d.ProcessName == "System Idle" {
			continue
		}
		ts := d.Timestamp
		if ts.IsZero() {
			ts = time.Now()
		}
		unixNow := ts.Unix()
		bucketTS := TruncateToHour(ts)

		var dummy int
		err := checkStmt.QueryRow(d.ProcessName).Scan(&dummy)
		if err != nil && !isInitialSeed {
			newApps = append(newApps, d.ProcessName)
		}

		_, _ = appStmt.Exec(d.ProcessName, d.ExePath, unixNow, unixNow)
		if d.RxBytes > 0 || d.TxBytes > 0 {
			_, _ = trafficStmt.Exec(d.ProcessName, d.ExePath, bucketTS, d.RxBytes, d.TxBytes)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return newApps, nil
}

// ListInterfaces returns all known interfaces from SQLite.
func (s *Store) ListInterfaces() ([]InterfaceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, name, description, mac, first_seen, last_seen
		FROM interfaces
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []InterfaceRecord
	for rows.Next() {
		var r InterfaceRecord
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.MAC, &r.FirstSeen, &r.LastSeen); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// QueryHistory aggregates historical traffic between start and end for a specific NIC or "all".
func (s *Store) QueryHistory(start, end time.Time, ifaceID string) (*HistoryReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if ifaceID == "" {
		ifaceID = "all"
	}

	startTS := TruncateToHour(start)
	endTS := end.Unix()

	duration := end.Sub(start)
	granularity := "day"
	if duration <= 48*time.Hour {
		granularity = "hour"
	}

	report := &HistoryReport{
		StartTime:     startTS,
		EndTime:       endTS,
		Granularity:   granularity,
		SelectedIface: ifaceID,
		Series:        []BucketPoint{},
		ByInterface:   []InterfaceSummary{},
	}

	// 1. Per-interface breakdown for the entire time window (always across all interfaces for comparison,
	// or filtered if requested, though showing all NICs in the breakdown table is super useful).
	ifaceRows, err := s.db.Query(`
		SELECT
			t.iface_id,
			COALESCE(i.name, t.iface_id) AS name,
			COALESCE(i.description, '') AS description,
			COALESCE(i.mac, '') AS mac,
			SUM(t.rx_bytes) AS rx,
			SUM(t.tx_bytes) AS tx
		FROM traffic_hourly t
		LEFT JOIN interfaces i ON i.id = t.iface_id
		WHERE t.bucket_ts >= ? AND t.bucket_ts <= ?
		GROUP BY t.iface_id
		ORDER BY (SUM(t.rx_bytes) + SUM(t.tx_bytes)) DESC
	`, startTS, endTS)
	if err != nil {
		return nil, fmt.Errorf("query interface breakdown: %w", err)
	}
	defer ifaceRows.Close()

	var grandTotalAllNICs uint64
	for ifaceRows.Next() {
		var item InterfaceSummary
		if err := ifaceRows.Scan(&item.IfaceID, &item.Name, &item.Description, &item.MAC, &item.RxBytes, &item.TxBytes); err != nil {
			return nil, err
		}
		item.TotalBytes = item.RxBytes + item.TxBytes
		grandTotalAllNICs += item.TotalBytes
		report.ByInterface = append(report.ByInterface, item)
	}
	if err := ifaceRows.Err(); err != nil {
		return nil, err
	}

	for idx := range report.ByInterface {
		if grandTotalAllNICs > 0 {
			report.ByInterface[idx].SharePct = (float64(report.ByInterface[idx].TotalBytes) / float64(grandTotalAllNICs)) * 100.0
		}
	}

	// 2. Query time-series buckets for the selected interface (or all)
	var seriesQuery string
	var args []any
	if ifaceID == "all" {
		seriesQuery = `
			SELECT bucket_ts, SUM(rx_bytes), SUM(tx_bytes)
			FROM traffic_hourly
			WHERE bucket_ts >= ? AND bucket_ts <= ?
			GROUP BY bucket_ts
			ORDER BY bucket_ts ASC
		`
		args = []any{startTS, endTS}
	} else {
		seriesQuery = `
			SELECT bucket_ts, SUM(rx_bytes), SUM(tx_bytes)
			FROM traffic_hourly
			WHERE bucket_ts >= ? AND bucket_ts <= ? AND iface_id = ?
			GROUP BY bucket_ts
			ORDER BY bucket_ts ASC
		`
		args = []any{startTS, endTS, ifaceID}
	}

	rows, err := s.db.Query(seriesQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query time series: %w", err)
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

		t := time.Unix(ts, 0).In(loc)
		var keyTS int64
		var label string
		if granularity == "hour" {
			keyTS = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc).Unix()
			label = t.Format("Jan 02 15:00")
		} else {
			keyTS = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).Unix()
			label = t.Format("Jan 02")
		}

		bp, exists := bucketMap[keyTS]
		if !exists {
			bp = &BucketPoint{
				Timestamp: keyTS,
				Label:     label,
			}
			bucketMap[keyTS] = bp
		}
		bp.RxBytes += rx
		bp.TxBytes += tx
		bp.Total = bp.RxBytes + bp.TxBytes
	}

	// Fill continuous timeline buckets so the chart has zero-filled gaps instead of missing dates
	if granularity == "hour" {
		cur := time.Unix(startTS, 0).In(loc)
		endLocal := end.In(loc)
		for !cur.After(endLocal) {
			keyTS := time.Date(cur.Year(), cur.Month(), cur.Day(), cur.Hour(), 0, 0, 0, loc).Unix()
			if _, ok := bucketMap[keyTS]; !ok {
				bucketMap[keyTS] = &BucketPoint{
					Timestamp: keyTS,
					Label:     cur.Format("Jan 02 15:00"),
				}
			}
			cur = cur.Add(time.Hour)
		}
	} else {
		sLocal := start.In(loc)
		cur := time.Date(sLocal.Year(), sLocal.Month(), sLocal.Day(), 0, 0, 0, 0, loc)
		endLocal := end.In(loc)
		for !cur.After(endLocal) {
			keyTS := cur.Unix()
			if _, ok := bucketMap[keyTS]; !ok {
				bucketMap[keyTS] = &BucketPoint{
					Timestamp: keyTS,
					Label:     cur.Format("Jan 02"),
				}
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
	days := duration.Hours() / 24.0
	if days < 1.0 {
		days = 1.0
	}
	report.AvgDailyBytes = uint64(float64(report.CombinedBytes) / days)

	// 3. Per-application historical traffic breakdown
	report.ByProcess = []ProcessHistorySummary{}
	procRows, err := s.db.Query(`
		SELECT
			p.process_name,
			MAX(COALESCE(NULLIF(p.exe_path, ''), COALESCE(k.exe_path, ''))) AS exe_path,
			SUM(p.rx_bytes) AS rx,
			SUM(p.tx_bytes) AS tx,
			COALESCE(MAX(k.blocked), 0) AS blocked
		FROM process_traffic_hourly p
		LEFT JOIN known_apps k ON k.process_name = p.process_name
		WHERE p.bucket_ts >= ? AND p.bucket_ts <= ?
		GROUP BY p.process_name
		HAVING (SUM(p.rx_bytes) + SUM(p.tx_bytes)) > 0
		ORDER BY (SUM(p.rx_bytes) + SUM(p.tx_bytes)) DESC
		LIMIT 50
	`, startTS, endTS)
	if err == nil {
		defer procRows.Close()
		var grandTotalProcs uint64
		for procRows.Next() {
			var item ProcessHistorySummary
			var blockedInt int
			if err := procRows.Scan(&item.ProcessName, &item.ExePath, &item.RxBytes, &item.TxBytes, &blockedInt); err == nil {
				item.TotalBytes = item.RxBytes + item.TxBytes
				item.Blocked = blockedInt != 0
				grandTotalProcs += item.TotalBytes
				report.ByProcess = append(report.ByProcess, item)
			}
		}
		for idx := range report.ByProcess {
			if grandTotalProcs > 0 {
				report.ByProcess[idx].SharePct = (float64(report.ByProcess[idx].TotalBytes) / float64(grandTotalProcs)) * 100.0
			}
		}
	}

	return report, nil
}

// QuotaStatus represents current billing-cycle data usage against a user-configured cap.
type QuotaStatus struct {
	Enabled       bool    `json:"enabled"`
	IfaceID       string  `json:"ifaceId"`
	BillingDay    int     `json:"billingDay"`
	CycleStart    int64   `json:"cycleStart"`
	CycleLabel    string  `json:"cycleLabel"`
	CapBytes      uint64  `json:"capBytes"`
	CapGB         float64 `json:"capGB"`
	CapGb         float64 `json:"capGb"`
	UsedRxBytes   uint64  `json:"usedRxBytes"`
	UsedTxBytes   uint64  `json:"usedTxBytes"`
	UsedTotal     uint64  `json:"usedTotal"`
	UsedBytes     uint64  `json:"usedBytes"`
	UsedPct       float64 `json:"usedPct"`
	Over80        bool    `json:"over80"`
	Over100       bool    `json:"over100"`
}

// QueryQuotaStatus computes usage since the current billing cycle start day.
func (s *Store) QueryQuotaStatus() (QuotaStatus, error) {
	cfg, err := s.GetSettings()
	if err != nil {
		return QuotaStatus{}, err
	}

	now := time.Now()
	loc := now.Location()
	bDay := cfg.BillingDay
	if bDay < 1 || bDay > 28 {
		bDay = 1
	}

	var cycleStart time.Time
	if now.Day() >= bDay {
		cycleStart = time.Date(now.Year(), now.Month(), bDay, 0, 0, 0, 0, loc)
	} else {
		prev := now.AddDate(0, -1, 0)
		cycleStart = time.Date(prev.Year(), prev.Month(), bDay, 0, 0, 0, 0, loc)
	}

	capBytes := uint64(cfg.DataCapGB * 1024 * 1024 * 1024)
	qs := QuotaStatus{
		Enabled:    cfg.DataCapEnabled,
		IfaceID:    cfg.DataCapIface,
		BillingDay: bDay,
		CycleStart: cycleStart.Unix(),
		CycleLabel: fmt.Sprintf("Since %s", cycleStart.Format("Jan 02")),
		CapBytes:   capBytes,
		CapGB:      cfg.DataCapGB,
		CapGb:      cfg.DataCapGB,
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var rx, tx uint64
	if cfg.DataCapIface == "" || cfg.DataCapIface == "all" {
		_ = s.db.QueryRow(`
			SELECT COALESCE(SUM(rx_bytes), 0), COALESCE(SUM(tx_bytes), 0)
			FROM traffic_hourly
			WHERE bucket_ts >= ?
		`, cycleStart.Unix()).Scan(&rx, &tx)
	} else {
		_ = s.db.QueryRow(`
			SELECT COALESCE(SUM(rx_bytes), 0), COALESCE(SUM(tx_bytes), 0)
			FROM traffic_hourly
			WHERE bucket_ts >= ? AND iface_id = ?
		`, cycleStart.Unix(), cfg.DataCapIface).Scan(&rx, &tx)
	}

	qs.UsedRxBytes = rx
	qs.UsedTxBytes = tx
	qs.UsedTotal = rx + tx
	qs.UsedBytes = rx + tx
	if capBytes > 0 {
		qs.UsedPct = (float64(qs.UsedTotal) / float64(capBytes)) * 100.0
		qs.Over80 = qs.UsedPct >= 80.0
		qs.Over100 = qs.UsedPct >= 100.0
	}
	return qs, nil
}

// SetAppBlocked records whether an application is blocked in Windows Firewall.
func (s *Store) SetAppBlocked(processName, exePath string, blocked bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	bVal := 0
	if blocked {
		bVal = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO known_apps (process_name, exe_path, first_seen, last_seen, blocked)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(process_name) DO UPDATE SET
			exe_path = CASE WHEN excluded.exe_path != '' THEN excluded.exe_path ELSE known_apps.exe_path END,
			last_seen = excluded.last_seen,
			blocked = excluded.blocked
	`, processName, exePath, now, now, bVal)
	return err
}

// GetBlockedApps returns a map of processName -> true for all blocked applications.
func (s *Store) GetBlockedApps() (map[string]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT process_name FROM known_apps WHERE blocked = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			res[name] = true
		}
	}
	return res, nil
}

// ResetTrafficHistory clears all historical hourly traffic and process traffic counters.
func (s *Store) ResetTrafficHistory() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		DELETE FROM traffic_hourly;
		DELETE FROM process_traffic_hourly;
	`)
	return err
}

// PruneOldData deletes traffic buckets older than retentionDays.
func (s *Store) PruneOldData(retentionDays int) error {
	if retentionDays <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().AddDate(0, 0, -retentionDays).Unix()
	_, _ = s.db.Exec(`DELETE FROM process_traffic_hourly WHERE bucket_ts < ?`, cutoff)
	_, err := s.db.Exec(`DELETE FROM traffic_hourly WHERE bucket_ts < ?`, cutoff)
	return err
}

