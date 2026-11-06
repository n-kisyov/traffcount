package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Settings holds persistent user preferences.
type Settings struct {
	Theme          string `json:"theme"`          // "dark" (default) or "light"
	AlwaysOnTop    bool   `json:"alwaysOnTop"`    // Keep GUI window on top
	SpeedUnit      string `json:"speedUnit"`      // "bits" (Mbps/Kbps) or "bytes" (MB/s, KB/s)
	HideLoopback   bool   `json:"hideLoopback"`   // Hide Loopback / tunnel adapters
	RetentionDays  int    `json:"retentionDays"`  // How many days of history to keep (default 365)
	RefreshRateMs  int    `json:"refreshRateMs"`  // Default 1000ms
}

// DefaultSettings returns default app settings (Dark mode default).
func DefaultSettings() Settings {
	return Settings{
		Theme:         "dark",
		AlwaysOnTop:   false,
		SpeedUnit:     "bits",
		HideLoopback:  true,
		RetentionDays: 365,
		RefreshRateMs: 1000,
	}
}

// InterfaceRecord represents a network interface stored in SQLite.
type InterfaceRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MAC         string `json:"mac"`
	FirstSeen   int64  `json:"firstSeen"`
	LastSeen    int64  `json:"lastSeen"`
}

// TrafficDelta holds accumulated bytes for an interface before flushing.
type TrafficDelta struct {
	IfaceID     string
	Name        string
	Description string
	MAC         string
	RxBytes     uint64
	TxBytes     uint64
	Timestamp   time.Time
}

// Store manages the SQLite database for traffic counters and settings.
type Store struct {
	mu     sync.RWMutex
	db     *sql.DB
	dbPath string
}

// DefaultDBPath returns a suitable path for the SQLite database.
func DefaultDBPath() string {
	// Prefer ProgramData\TraffCount if writable (shared between Windows Service & User Tray),
	// otherwise fall back to LocalAppData\TraffCount.
	progData := os.Getenv("ProgramData")
	if progData != "" {
		dir := filepath.Join(progData, "TraffCount")
		if err := os.MkdirAll(dir, 0777); err == nil {
			testFile := filepath.Join(dir, ".write_test")
			if f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0666); err == nil {
				f.Close()
				os.Remove(testFile)
				return filepath.Join(dir, "traffcount.db")
			}
		}
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		localAppData = "."
	}
	dir := filepath.Join(localAppData, "TraffCount")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "traffcount.db")
}

// Open initializes the SQLite database at dbPath and runs schema migrations.
func Open(dbPath string) (*Store, error) {
	if dbPath == "" {
		dbPath = DefaultDBPath()
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{
		db:     db,
		dbPath: dbPath,
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// DBPath returns the path to the SQLite database file.
func (s *Store) DBPath() string {
	return s.dbPath
}

// Close closes the underlying database connection.
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
	CREATE TABLE IF NOT EXISTS interfaces (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		mac TEXT NOT NULL DEFAULT '',
		first_seen INTEGER NOT NULL,
		last_seen INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS traffic_hourly (
		iface_id TEXT NOT NULL,
		bucket_ts INTEGER NOT NULL,
		rx_bytes INTEGER NOT NULL DEFAULT 0,
		tx_bytes INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (iface_id, bucket_ts)
	);

	CREATE INDEX IF NOT EXISTS idx_traffic_hourly_ts ON traffic_hourly(bucket_ts);

	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	`
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

// GetSettings retrieves application settings, or returns defaults if not yet saved.
func (s *Store) GetSettings() (Settings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := DefaultSettings()
	var raw string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'app_settings'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	_ = json.Unmarshal([]byte(raw), &cfg)
	if cfg.Theme != "light" && cfg.Theme != "dark" {
		cfg.Theme = "dark"
	}
	if cfg.SpeedUnit != "bits" && cfg.SpeedUnit != "bytes" {
		cfg.SpeedUnit = "bits"
	}
	return cfg, nil
}

// SaveSettings persists application settings to SQLite.
func (s *Store) SaveSettings(cfg Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cfg.Theme != "light" && cfg.Theme != "dark" {
		cfg.Theme = "dark"
	}
	if cfg.SpeedUnit != "bits" && cfg.SpeedUnit != "bytes" {
		cfg.SpeedUnit = "bits"
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 365
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO settings (key, value) VALUES ('app_settings', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, string(data))
	return err
}

