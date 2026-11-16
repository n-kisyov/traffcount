//go:build !windows

package storage

import (
	"os"
	"path/filepath"
)

// DefaultDBPath returns a suitable path for the SQLite database on Linux / Raspberry Pi OS.
func DefaultDBPath() string {
	if override := os.Getenv("TRAFFCOUNT_DB"); override != "" {
		return override
	}
	// Prefer system-wide /var/lib/traffcount when running as root (systemd service) or if writable.
	sysDir := "/var/lib/traffcount"
	if os.Geteuid() == 0 {
		if err := os.MkdirAll(sysDir, 0755); err == nil {
			return filepath.Join(sysDir, "traffcount.db")
		}
	} else if info, err := os.Stat(sysDir); err == nil && info.IsDir() {
		dbFile := filepath.Join(sysDir, "traffcount.db")
		if f, err := os.OpenFile(dbFile, os.O_CREATE|os.O_RDWR, 0644); err == nil {
			f.Close()
			return dbFile
		}
	}

	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			dataHome = "."
		} else {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	dir := filepath.Join(dataHome, "traffcount")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "traffcount.db")
}
