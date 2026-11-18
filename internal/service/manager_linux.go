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

var (
	runningAsService atomic.Bool
	defaultMgr       = NewManager(AgentConfig())
)

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

func (m *Manager) unitName() string {
	return m.cfg.UnitName + ".service"
}

func (m *Manager) unitFilePath() string {
	return "/etc/systemd/system/" + m.unitName()
}

// QueryStatus checks whether traffcount.service is installed and running via systemd.
func QueryStatus() StatusInfo { return defaultMgr.QueryStatus() }

// QueryStatus checks whether the configured systemd unit is installed and running.
func (m *Manager) QueryStatus() StatusInfo {
	info := StatusInfo{
		Installed: false,
		Running:   false,
		StateText: "Not Installed",
		IsAdmin:   IsAdmin(),
	}

	if _, err := os.Stat(m.unitFilePath()); err == nil {
		info.Installed = true
	}

	out, err := exec.Command("systemctl", "show", m.unitName(), "--property=LoadState,ActiveState").Output()
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
		sudoArgs := append([]string{"-n", name}, args...)
		cmd = exec.Command("sudo", sudoArgs...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if !IsAdmin() {
			if msg != "" {
				return fmt.Errorf("root privileges required (run with 'sudo ...'): %s", msg)
			}
			return fmt.Errorf("root privileges required (run with 'sudo ...')")
		}
		return fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), msg)
	}
	return nil
}

// InstallAndStart writes the systemd unit file and enables/starts traffcount.service.
func InstallAndStart() error { return defaultMgr.InstallAndStart() }

// InstallAndStart writes the configured systemd unit file and enables/starts it.
func (m *Manager) InstallAndStart() error {
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
`, m.cfg.DisplayName, exePath)

	unitPath := m.unitFilePath()
	if IsAdmin() {
		if err := os.WriteFile(unitPath, []byte(unitContent), 0644); err != nil {
			return fmt.Errorf("write %s: %w", unitPath, err)
		}
	} else {
		cmd := exec.Command("sudo", "-n", "tee", unitPath)
		cmd.Stdin = strings.NewReader(unitContent)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("root privileges required to install systemd unit: %s", strings.TrimSpace(string(out)))
		}
	}

	_ = m.requestGracefulShutdown()
	if err := runPrivilegedCmd("systemctl", "daemon-reload"); err != nil {
		return err
	}
	return runPrivilegedCmd("systemctl", "enable", "--now", m.unitName())
}

// StopAndUninstall stops, disables, and removes traffcount.service.
func StopAndUninstall() error { return defaultMgr.StopAndUninstall() }

// StopAndUninstall stops, disables, and removes the configured systemd unit.
func (m *Manager) StopAndUninstall() error {
	_ = runPrivilegedCmd("systemctl", "disable", "--now", m.unitName())
	unitPath := m.unitFilePath()
	if IsAdmin() {
		_ = os.Remove(unitPath)
	} else if err := runPrivilegedCmd("rm", "-f", unitPath); err != nil {
		return err
	}
	return runPrivilegedCmd("systemctl", "daemon-reload")
}

// StartService starts traffcount.service after asking any standalone CLI instance to release its port.
func StartService() error { return defaultMgr.StartService() }

// StartService starts the configured systemd unit.
func (m *Manager) StartService() error {
	_ = m.requestGracefulShutdown()
	return runPrivilegedCmd("systemctl", "start", m.unitName())
}

// StopService stops traffcount.service.
func StopService() error { return defaultMgr.StopService() }

// StopService stops the configured systemd unit.
func (m *Manager) StopService() error {
	return runPrivilegedCmd("systemctl", "stop", m.unitName())
}

func (m *Manager) requestGracefulShutdown() error {
	if m.cfg.ReleaseURL != "" {
		client := &http.Client{Timeout: 500 * time.Millisecond}
		if resp, err := client.Post(m.cfg.ReleaseURL, "application/json", nil); err == nil {
			resp.Body.Close()
		}
	}
	time.Sleep(400 * time.Millisecond)
	return nil
}
