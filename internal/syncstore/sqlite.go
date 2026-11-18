package syncstore

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// SyncSettings holds persistent central server UI and polling preferences.
type SyncSettings struct {
	Theme          string `json:"theme"`          // "dark", "antigravity", "nord", "dracula", "emerald", "light"
	SpeedUnit      string `json:"speedUnit"`      // "bits" or "bytes"
	HideLoopback   bool   `json:"hideLoopback"`   // Hide loopback adapters in fleet/host charts
	RefreshRateMs  int    `json:"refreshRateMs"`  // UI redraw cadence: 500, 1000, 2000, 5000
	RetentionDays  int    `json:"retentionDays"`  // Fleet history retention in days (-1 = unlimited)
	DefaultPollMs  int    `json:"defaultPollMs"`  // Default live poll interval for new hosts (ms)
	HistorySyncSec int    `json:"historySyncSec"` // Periodic historical reconciliation interval (seconds)
}

var ValidThemes = map[string]bool{
	"dark":        true,
	"antigravity": true,
	"nord":        true,
	"dracula":     true,
	"emerald":     true,
	"light":       true,
}

// DefaultSyncSettings returns sensible defaults matching TraffCount's desktop theme/unit settings.
func DefaultSyncSettings() SyncSettings {
	return SyncSettings{
		Theme:          "dark",
		SpeedUnit:      "bits",
		HideLoopback:   true,
		RefreshRateMs:  1000,
		RetentionDays:  365,
		DefaultPollMs:  1000,
		HistorySyncSec: 60,
	}
}

func normalizeSyncSettings(cfg SyncSettings) SyncSettings {
	if !ValidThemes[cfg.Theme] {
		cfg.Theme = "dark"
	}
	if cfg.SpeedUnit != "bits" && cfg.SpeedUnit != "bytes" {
		cfg.SpeedUnit = "bits"
	}
	switch cfg.RefreshRateMs {
	case 500, 1000, 2000, 5000:
	default:
		cfg.RefreshRateMs = 1000
	}
	if cfg.RetentionDays != -1 && cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 365
	}
	if cfg.DefaultPollMs < 500 || cfg.DefaultPollMs > 15000 {
		cfg.DefaultPollMs = 1000
	}
	if cfg.HistorySyncSec < 15 || cfg.HistorySyncSec > 3600 {
		cfg.HistorySyncSec = 60
	}
	return cfg
}

// Store provides concurrency-safe SQLite persistence for TraffCount Sync Server.
type Store struct {
	mu     sync.RWMutex
	db     *sql.DB
	dbPath string
}

// Open opens or initializes the fleet SQLite database using WAL mode.
func Open(dbPath string) (*Store, error) {
	if strings.TrimSpace(dbPath) == "" {
		dbPath = DefaultDBPath()
	}
	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create sync db dir: %w", err)
		}
	}

	dsn := dbPath + "?_pragma=busy_timeout(8000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &Store{db: db, dbPath: dbPath}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// DBPath returns the filesystem path of the active database.
func (s *Store) DBPath() string {
	return s.dbPath
}

// Close closes the underlying SQLite connection.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS hosts (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		base_url TEXT NOT NULL UNIQUE,
		api_token TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		poll_interval_ms INTEGER NOT NULL DEFAULT 1000,
		tags TEXT NOT NULL DEFAULT '',
		platform TEXT NOT NULL DEFAULT '',
		version TEXT NOT NULL DEFAULT '',
		source_mode TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		last_seen INTEGER NOT NULL DEFAULT 0,
		last_sync_ts INTEGER NOT NULL DEFAULT 0,
		last_latency_ms INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS host_interfaces (
		host_id TEXT NOT NULL,
		iface_id TEXT NOT NULL,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		mac TEXT NOT NULL DEFAULT '',
		ipv4 TEXT NOT NULL DEFAULT '',
		ipv6 TEXT NOT NULL DEFAULT '',
		link_speed_bits INTEGER NOT NULL DEFAULT 0,
		is_up INTEGER NOT NULL DEFAULT 0,
		is_loopback INTEGER NOT NULL DEFAULT 0,
		last_seen INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (host_id, iface_id),
		FOREIGN KEY (host_id) REFERENCES hosts(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS host_traffic_hourly (
		host_id TEXT NOT NULL,
		iface_id TEXT NOT NULL,
		bucket_ts INTEGER NOT NULL,
		rx_bytes INTEGER NOT NULL DEFAULT 0,
		tx_bytes INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (host_id, iface_id, bucket_ts),
		FOREIGN KEY (host_id) REFERENCES hosts(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_sync_traffic_ts ON host_traffic_hourly(bucket_ts);
	CREATE INDEX IF NOT EXISTS idx_sync_traffic_host_ts ON host_traffic_hourly(host_id, bucket_ts);

	CREATE TABLE IF NOT EXISTS host_process_hourly (
		host_id TEXT NOT NULL,
		process_name TEXT NOT NULL,
		exe_path TEXT NOT NULL DEFAULT '',
		bucket_ts INTEGER NOT NULL,
		rx_bytes INTEGER NOT NULL DEFAULT 0,
		tx_bytes INTEGER NOT NULL DEFAULT 0,
		blocked INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (host_id, process_name, bucket_ts),
		FOREIGN KEY (host_id) REFERENCES hosts(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_sync_proc_ts ON host_process_hourly(bucket_ts);

	CREATE TABLE IF NOT EXISTS host_speed_tests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		host_id TEXT NOT NULL,
		ts INTEGER NOT NULL,
		unloaded_ping_ms REAL NOT NULL DEFAULT 0,
		jitter_ms REAL NOT NULL DEFAULT 0,
		loaded_ping_ms REAL NOT NULL DEFAULT 0,
		download_mbps REAL NOT NULL DEFAULT 0,
		upload_mbps REAL NOT NULL DEFAULT 0,
		bufferbloat_grade TEXT NOT NULL DEFAULT '',
		server_label TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		UNIQUE (host_id, ts),
		FOREIGN KEY (host_id) REFERENCES hosts(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_sync_speed_ts ON host_speed_tests(host_id, ts DESC);

	CREATE TABLE IF NOT EXISTS sync_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	`
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("migrate sync schema: %w", err)
	}
	return nil
}

// GetSettings retrieves persistent Sync Server UI & poller settings.
func (s *Store) GetSettings() (SyncSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := DefaultSyncSettings()
	var raw string
	err := s.db.QueryRow(`SELECT value FROM sync_settings WHERE key = 'sync_settings'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	_ = json.Unmarshal([]byte(raw), &cfg)
	return normalizeSyncSettings(cfg), nil
}

// SaveSettings persists Sync Server settings to SQLite.
func (s *Store) SaveSettings(cfg SyncSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg = normalizeSyncSettings(cfg)
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO sync_settings (key, value) VALUES ('sync_settings', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, string(data))
	return err
}
