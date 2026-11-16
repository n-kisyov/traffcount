//go:build !windows

package tray

import "sync"

var (
	releaseMu sync.Mutex
	releaseFn func()
)

// RegisterReleaseHandler registers a callback invoked when /api/server/release is called
// on non-Windows headless/CLI instances (e.g. yielding the port to the systemd service).
func RegisterReleaseHandler(fn func()) {
	releaseMu.Lock()
	releaseFn = fn
	releaseMu.Unlock()
}

func NotifyTrayTopmost(bool) bool { return false }

func NotifyTrayStartup(bool) bool { return false }

func NotifyTrayMiniMode(bool) bool { return false }

func NotifyTrayReleaseServer() bool {
	releaseMu.Lock()
	fn := releaseFn
	releaseMu.Unlock()
	if fn != nil {
		go fn()
		return true
	}
	return false
}

func IsStartupEnabled() bool { return false }

func SetStartupEnabled(bool) error { return nil }
