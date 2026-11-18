//go:build !windows

package syncstore

import (
	"os"
	"path/filepath"
)

// DefaultDBPath resolves the SQLite database location for TraffCount Sync Server on Linux / Raspberry Pi OS.
func DefaultDBPath() string {
	if p := os.Getenv("TRAFFCOUNT_SYNC_DB"); p != "" {
		return p
	}
	sysDir := "/var/lib/traffcount-sync"
	if os.Geteuid() == 0 {
		if err := os.MkdirAll(sysDir, 0o755); err == nil {
			return filepath.Join(sysDir, "traffcount-sync.db")
		}
	} else if st, err := os.Stat(sysDir); err == nil && st.IsDir() {
		f, err := os.OpenFile(filepath.Join(sysDir, ".write_test"), os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			_ = os.Remove(filepath.Join(sysDir, ".write_test"))
			return filepath.Join(sysDir, "traffcount-sync.db")
		}
	}

	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			dataHome = os.TempDir()
		} else {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	appDir := filepath.Join(dataHome, "traffcount-sync")
	_ = os.MkdirAll(appDir, 0o755)
	return filepath.Join(appDir, "traffcount-sync.db")
}
