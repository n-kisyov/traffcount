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
	Theme          string  `json:"theme"`          // "dark" (default), "antigravity", "nord", "dracula", "emerald", "light"
	AlwaysOnTop    bool    `json:"alwaysOnTop"`    // Keep GUI window on top
	StartOnBoot    bool    `json:"startOnBoot"`    // Start minimized to tray on system boot
	StartMinimized bool    `json:"startMinimized"` // Start minimized to tray when launched manually
	SpeedUnit      string  `json:"speedUnit"`      // "bits" (Mbps/Kbps) or "bytes" (MB/s, KB/s)
	HideLoopback   bool    `json:"hideLoopback"`   // Hide Loopback / tunnel adapters
	RetentionDays  int     `json:"retentionDays"`  // How many days of history to keep (default 365)
	RefreshRateMs  int     `json:"refreshRateMs"`  // Default 1000ms
	DataCapEnabled bool    `json:"dataCapEnabled"` // Enable monthly/billing-cycle data cap
	DataCapGB      float64 `json:"dataCapGB"`      // Monthly data cap in GB (e.g. 500)
	DataCapIface   string  `json:"dataCapIface"`   // "all" or specific NIC ID
	BillingDay     int     `json:"billingDay"`     // Day of month (1-28) when billing cycle resets
	SpikeAlertMbps float64 `json:"spikeAlertMbps"` // Alert when sustained speed exceeds this Mbps (0 = disabled)
}

// WindowBounds stores the last screen coordinates and size of the dashboard window.
type WindowBounds struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized"`
	HasValue  bool `json:"hasValue"`
}

// DefaultWindowBounds returns the default size before any user resize/move.
func DefaultWindowBounds() WindowBounds {
	return WindowBounds{
		X:      120,
		Y:      80,
		Width:  1180,
		Height: 780,
	}
}

// ValidThemes lists all supported UI theme identifiers.
var ValidThemes = map[string]bool{
	"dark":        true, // Windows 11 Dark (Default)
	"antigravity": true, // Antigravity IDE (Dark Modern)
	"nord":        true, // Nord Polar Dark
	"dracula":     true, // Dracula Dark
	"emerald":     true, // Matrix / Emerald Cyber Dark
	"light":       true, // Windows 11 Light
}

func isValidTheme(t string) bool {
	return ValidThemes[t]
}

// DefaultSettings returns default app settings (Dark mode default).
func DefaultSettings() Settings {
	return Settings{
		Theme:          "dark",
		AlwaysOnTop:    false,
		SpeedUnit:      "bits",
		HideLoopback:   true,
		RetentionDays:  365,
		RefreshRateMs:  1000,
		DataCapEnabled: false,
		DataCapGB:      500,
		DataCapIface:   "all",
		BillingDay:     1,
		SpikeAlertMbps: 0,
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
			dbFile := filepath.Join(dir, "traffcount.db")
			if f, err := os.OpenFile(dbFile, os.O_CREATE|os.O_RDWR, 0666); err == nil {
				f.Close()
				return dbFile
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
	if !isValidTheme(cfg.Theme) {
		cfg.Theme = "dark"
	}
	if cfg.SpeedUnit != "bits" && cfg.SpeedUnit != "bytes" {
		cfg.SpeedUnit = "bits"
	}
	if cfg.BillingDay < 1 || cfg.BillingDay > 28 {
		cfg.BillingDay = 1
	}
	if cfg.DataCapIface == "" {
		cfg.DataCapIface = "all"
	}
	if cfg.DataCapGB <= 0 {
		cfg.DataCapGB = 500
	}
	return cfg, nil
}

// SaveSettings persists application settings to SQLite.
func (s *Store) SaveSettings(cfg Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !isValidTheme(cfg.Theme) {
		cfg.Theme = "dark"
	}
	if cfg.SpeedUnit != "bits" && cfg.SpeedUnit != "bytes" {
		cfg.SpeedUnit = "bits"
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 365
	}
	if cfg.BillingDay < 1 || cfg.BillingDay > 28 {
		cfg.BillingDay = 1
	}
	if cfg.DataCapIface == "" {
		cfg.DataCapIface = "all"
	}
	if cfg.DataCapGB <= 0 {
		cfg.DataCapGB = 500
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

// GetWindowBounds retrieves saved window position and size from SQLite.
func (s *Store) GetWindowBounds() (WindowBounds, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b := DefaultWindowBounds()
	var raw string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'window_bounds'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	_ = json.Unmarshal([]byte(raw), &b)
	if b.Width < 400 {
		b.Width = 1180
	}
	if b.Height < 300 {
		b.Height = 780
	}
	return b, nil
}

// SaveWindowBounds persists window position and size to SQLite.
func (s *Store) SaveWindowBounds(b WindowBounds) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if b.Width < 400 || b.Height < 300 {
		return nil
	}
	b.HasValue = true
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO settings (key, value) VALUES ('window_bounds', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, string(data))
	return err
}

