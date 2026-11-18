//go:build windows

package syncstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
)

var aclGrantOnce sync.Once

// DefaultDBPath resolves the SQLite database location for TraffCount Sync Server on Windows.
// Shared across LocalSystem (TraffCountSyncService) and desktop console sessions via %ProgramData%.
func DefaultDBPath() string {
	if p := os.Getenv("TRAFFCOUNT_SYNC_DB"); p != "" {
		return p
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		dir := filepath.Join(pd, "TraffCountSync")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			aclGrantOnce.Do(func() {
				// Grant local interactive Users group Modify rights on %ProgramData%\TraffCountSync
				// so console CLI inspection and service sessions share the same DB cleanly.
				cmd := exec.Command("icacls", dir, "/grant", "*S-1-5-32-545:(OI)(CI)M", "/T", "/Q")
				cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true}
				_ = cmd.Run()
			})
			return filepath.Join(dir, "traffcount-sync.db")
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	appDir := filepath.Join(dir, "TraffCountSync")
	_ = os.MkdirAll(appDir, 0o755)
	return filepath.Join(appDir, "traffcount-sync.db")
}
