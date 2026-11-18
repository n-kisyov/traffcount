package service

// Config parameterizes background service registration across binaries and platforms.
type Config struct {
	ServiceName   string // Windows SCM service name (e.g. "TraffCountService")
	UnitName      string // Linux systemd unit base name (e.g. "traffcount")
	DisplayName   string
	Description   string
	ReleaseURL    string // Local HTTP release endpoint before service startup
	TrayWindowCls string // Optional Win32 tray class for WM_RELEASE_SERVER
}

// AgentConfig returns the default service configuration for the host collector agent.
func AgentConfig() Config {
	return Config{
		ServiceName:   ServiceName,
		UnitName:      "traffcount",
		DisplayName:   ServiceDisplayName,
		Description:   ServiceDescription,
		ReleaseURL:    "http://127.0.0.1:9876/api/server/release",
		TrayWindowCls: "TraffCountTrayWindowClass",
	}
}

// SyncConfig returns the service configuration for the central TraffCount Sync Server.
func SyncConfig() Config {
	return Config{
		ServiceName: "TraffCountSyncService",
		UnitName:    "traffcount-sync",
		DisplayName: "TraffCount Sync Server",
		Description: "Aggregates and stores multi-host real-time and historical network telemetry for TraffCount Sync Server.",
		ReleaseURL:  "http://127.0.0.1:9880/api/server/release",
	}
}

// StatusInfo represents the current state of a managed TraffCount background service.
type StatusInfo struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	StateText string `json:"stateText"`
	IsAdmin   bool   `json:"isAdmin"`
}

// Manager controls lifecycle and status checks for a specific Config.
type Manager struct {
	cfg Config
}

// NewManager creates a service Manager bound to cfg.
func NewManager(cfg Config) *Manager {
	if cfg.ServiceName == "" {
		cfg.ServiceName = ServiceName
	}
	if cfg.UnitName == "" {
		cfg.UnitName = "traffcount"
	}
	return &Manager{cfg: cfg}
}

// Config returns a copy of the Manager's service configuration.
func (m *Manager) Config() Config {
	return m.cfg
}
