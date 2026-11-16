//go:build windows

package storage

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

var aclGrantOnce sync.Once

// DefaultDBPath returns a suitable path for the SQLite database on Windows.
func DefaultDBPath() string {
	if override := os.Getenv("TRAFFCOUNT_DB"); override != "" {
		return override
	}
	// Prefer ProgramData\TraffCount if writable (shared between Windows Service & User Tray),
	// otherwise fall back to LocalAppData\TraffCount.
	progData := os.Getenv("ProgramData")
	if progData != "" {
		dir := filepath.Join(progData, "TraffCount")
		if err := os.MkdirAll(dir, 0777); err == nil {
			aclGrantOnce.Do(func() {
				// Ensure the Windows Users group (*S-1-5-32-545) has Modify access on ProgramData\TraffCount
				// so both TraffCountService (SYSTEM) and standalone user mode share the same DB file cleanly.
				_ = exec.Command("icacls", dir, "/grant", "*S-1-5-32-545:(OI)(CI)M", "/T", "/Q").Run()
			})
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
