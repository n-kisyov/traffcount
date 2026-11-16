//go:build !windows

package winui

import (
	"sync"

	"traffcount/internal/storage"
)

// Manager tracks window-related state as no-ops on headless / web-only platforms.
type Manager struct {
	mu          sync.Mutex
	url         string
	alwaysOnTop bool
	miniMode    bool
	bounds      storage.WindowBounds
}

// NewManager creates a headless Manager stub on non-Windows platforms.
func NewManager(url string, initialTopmost bool) *Manager {
	return &Manager{url: url, alwaysOnTop: initialTopmost}
}

func (m *Manager) SetSavedBounds(b storage.WindowBounds) {
	m.mu.Lock()
	m.bounds = b
	m.mu.Unlock()
}

func (m *Manager) GetSavedBounds() storage.WindowBounds {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bounds
}

func (m *Manager) IsMiniMode() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.miniMode
}

func (m *Manager) SetMiniMode(mini bool) bool {
	m.mu.Lock()
	m.miniMode = mini
	m.mu.Unlock()
	return false
}

func (m *Manager) IsAlwaysOnTop() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alwaysOnTop
}

func (m *Manager) SetAlwaysOnTop(topmost bool) bool {
	m.mu.Lock()
	m.alwaysOnTop = topmost
	m.mu.Unlock()
	return false
}

func (m *Manager) EnforceAlwaysOnTop(bool) {}

func (m *Manager) OpenDashboard() error { return nil }
