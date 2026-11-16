//go:build linux

package service

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const (
	ServiceName        = "traffcount"
	UnitFilePath       = "/etc/systemd/system/traffcount.service"
	ServiceDisplayName = "TraffCount Network Traffic Monitor"
	ServiceDescription = "Collects real-time and historical network interface traffic statistics for TraffCount on Raspberry Pi OS / Linux."
)

// StatusInfo represents the current state of the TraffCount systemd service.
type StatusInfo struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	StateText string `json:"stateText"`
	IsAdmin   bool   `json:"isAdmin"`
}

var runningAsService atomic.Bool

// SetServiceProcessMode marks whether the current process was started via `service run`.
func SetServiceProcessMode(v bool) {
	runningAsService.Store(v)
}

// IsServiceProcess reports whether the current process is running as a managed systemd background service.
func IsServiceProcess() bool {
	return runningAsService.Load() || os.Getenv("INVOCATION_ID") != ""
}

// IsWindowsServiceProcess is kept for cross-platform compatibility with shared callers.
func IsWindowsServiceProcess() bool {
	return IsServiceProcess()
}

// IsAdmin reports whether the current process has root privileges (euid == 0).
func IsAdmin() bool {
	return os.Geteuid() == 0
}

// QueryStatus checks whether traffcount.service is installed and running via systemd.
func QueryStatus() StatusInfo {
	info := StatusInfo{
		Installed: false,
		Running:   false,
		StateText: "Not Installed",
		IsAdmin:   IsAdmin(),
	}

	if _, err := os.Stat(UnitFilePath); err == nil {
		info.Installed = true
	}

	out, err := exec.Command("systemctl", "show", ServiceName+".service", "--property=LoadState,ActiveState").Output()
	if err != nil {
		if info.Installed {
			info.StateText = "Stopped"
		}
		return info
	}

	var loadState, activeState string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "LoadState=") {
			loadState = strings.TrimPrefix(line, "LoadState=")
		} else if strings.HasPrefix(line, "ActiveState=") {
			activeState = strings.TrimPrefix(line, "ActiveState=")
		}
	}
	if loadState == "loaded" {
		info.Installed = true
	}
	if !info.Installed {
		return info
	}

	switch activeState {
	case "active":
		info.Running = true
		info.StateText = "Running"
	case "activating":
		info.StateText = "Starting..."
	case "deactivating":
		info.StateText = "Stopping..."
	case "failed":
		info.StateText = "Failed"
	default:
		info.StateText = "Stopped"
	}
	return info
}

func runPrivilegedCmd(name string, args ...string) error {
	var cmd *exec.Cmd
	if IsAdmin() {
		cmd = exec.Command(name, args...)
	} else {
		// Try non-interactive sudo (e.g. passwordless sudoers on Raspberry Pi OS)
		sudoArgs := append([]string{"-n", name}, args...)
		cmd = exec.Command("sudo", sudoArgs...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if !IsAdmin() {
			if msg != "" {
				return fmt.Errorf("root privileges required (run with 'sudo traffcount service ...'): %s", msg)
			}
			return fmt.Errorf("root privileges required (run with 'sudo traffcount service ...')")
		}
		return fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), msg)
	}
	return nil
}

// InstallAndStart writes the systemd unit file and enables/starts traffcount.service.
func InstallAndStart() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	exePath, _ = filepath.Abs(exePath)

	unitContent := fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s service run
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`, ServiceDisplayName, exePath)

	if IsAdmin() {
		if err := os.WriteFile(UnitFilePath, []byte(unitContent), 0644); err != nil {
			return fmt.Errorf("write %s: %w", UnitFilePath, err)
		}
	} else {
		cmd := exec.Command("sudo", "-n", "tee", UnitFilePath)
		cmd.Stdin = strings.NewReader(unitContent)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("root privileges required to install systemd unit (run 'sudo traffcount service install'): %s", strings.TrimSpace(string(out)))
		}
	}

	_ = requestGracefulShutdown()
	if err := runPrivilegedCmd("systemctl", "daemon-reload"); err != nil {
		return err
	}
	return runPrivilegedCmd("systemctl", "enable", "--now", ServiceName+".service")
}

// StopAndUninstall stops, disables, and removes traffcount.service.
func StopAndUninstall() error {
	_ = runPrivilegedCmd("systemctl", "disable", "--now", ServiceName+".service")
	if IsAdmin() {
		_ = os.Remove(UnitFilePath)
	} else if err := runPrivilegedCmd("rm", "-f", UnitFilePath); err != nil {
		return err
	}
	return runPrivilegedCmd("systemctl", "daemon-reload")
}

// StartService starts traffcount.service after asking any standalone CLI instance to release port 9876.
func StartService() error {
	_ = requestGracefulShutdown()
	return runPrivilegedCmd("systemctl", "start", ServiceName+".service")
}

// StopService stops traffcount.service.
func StopService() error {
	return runPrivilegedCmd("systemctl", "stop", ServiceName+".service")
}

func requestGracefulShutdown() error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	if resp, err := client.Post("http://127.0.0.1:9876/api/server/release", "application/json", nil); err == nil {
		resp.Body.Close()
	}
	time.Sleep(350 * time.Millisecond)
	return nil
}
