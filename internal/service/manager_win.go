//go:build windows

package service

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	ServiceName        = "TraffCountService"
	ServiceDisplayName = "TraffCount Network Traffic Monitor"
	ServiceDescription = "Collects real-time and historical network interface traffic statistics for TraffCount on Windows 11."
)

var defaultMgr = NewManager(AgentConfig())

// IsWindowsServiceProcess reports whether the current process is executing as a Windows Service.
func IsWindowsServiceProcess() bool {
	inService, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return inService
}

// IsServiceProcess reports whether the current process is running as a managed background service.
func IsServiceProcess() bool {
	return IsWindowsServiceProcess()
}

// IsAdmin reports whether the current process has elevated administrator privileges.
func IsAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	token := windows.Token(0)
	member, err := token.IsMember(sid)
	if err != nil {
		return false
	}
	return member
}

// QueryStatus checks whether TraffCountService is installed and running.
func QueryStatus() StatusInfo { return defaultMgr.QueryStatus() }

// QueryStatus checks whether the configured service is installed and running.
func (m *Manager) QueryStatus() StatusInfo {
	info := StatusInfo{
		Installed: false,
		Running:   false,
		StateText: "Not Installed",
		IsAdmin:   IsAdmin(),
	}

	// Open Service Control Manager with read-only access so non-admin processes can check status
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return info
	}
	defer windows.CloseServiceHandle(h)

	svcNamePtr, _ := syscall.UTF16PtrFromString(m.cfg.ServiceName)
	sHandle, err := windows.OpenService(h, svcNamePtr, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return info
	}
	defer windows.CloseServiceHandle(sHandle)

	info.Installed = true
	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(sHandle, &status); err == nil {
		switch svc.State(status.CurrentState) {
		case svc.Running:
			info.Running = true
			info.StateText = "Running"
		case svc.StartPending:
			info.StateText = "Starting..."
		case svc.StopPending:
			info.StateText = "Stopping..."
		case svc.Stopped:
			info.StateText = "Stopped"
		default:
			info.StateText = fmt.Sprintf("State (%d)", status.CurrentState)
		}
	}
	return info
}

var (
	shell32             = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = shell32.NewProc("ShellExecuteExW")
)

type shellExecuteInfoW struct {
	CbSize       uint32
	FMask        uint32
	Hwnd         uintptr
	LpVerb       *uint16
	LpFile       *uint16
	LpParameters *uint16
	LpDirectory  *uint16
	NShow        int32
	HInstApp     uintptr
	LpIDList     uintptr
	LpClass      *uint16
	HkeyClass    uintptr
	DwHotKey     uint32
	HIcon        uintptr
	HProcess     windows.Handle
}

// RunElevated executes the current binary with UAC Administrator elevation ("runas") and waits for it to finish.
func RunElevated(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)

	verbPtr, _ := syscall.UTF16PtrFromString("runas")
	exePtr, _ := syscall.UTF16PtrFromString(exe)
	argStr := strings.Join(args, " ")
	argPtr, _ := syscall.UTF16PtrFromString(argStr)
	cwd, _ := os.Getwd()
	cwdPtr, _ := syscall.UTF16PtrFromString(cwd)

	const SEE_MASK_NOCLOSEPROCESS = 0x00000040
	sei := shellExecuteInfoW{
		CbSize:       uint32(unsafe.Sizeof(shellExecuteInfoW{})),
		FMask:        SEE_MASK_NOCLOSEPROCESS,
		LpVerb:       verbPtr,
		LpFile:       exePtr,
		LpParameters: argPtr,
		LpDirectory:  cwdPtr,
		NShow:        windows.SW_HIDE,
	}

	ret, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&sei)))
	if ret == 0 {
		return fmt.Errorf("ShellExecuteExW failed: %w", callErr)
	}
	if sei.HProcess != 0 {
		defer windows.CloseHandle(sei.HProcess)
		_, _ = windows.WaitForSingleObject(sei.HProcess, 3000)
	}
	return nil
}

// InstallAndStart installs TraffCountService and starts it (requires Administrator privileges).
func InstallAndStart() error { return defaultMgr.InstallAndStart() }

// InstallAndStart installs the configured Windows service and starts it.
func (m *Manager) InstallAndStart() error {
	if !IsAdmin() {
		return RunElevated("service", "install")
	}

	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return err
	}

	cm, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer cm.Disconnect()

	_ = m.requestGracefulShutdown()

	s, err := cm.OpenService(m.cfg.ServiceName)
	if err == nil {
		defer s.Close()
		_ = s.Start()
		return nil
	}

	s, err = cm.CreateService(
		m.cfg.ServiceName,
		exePath,
		mgr.Config{
			DisplayName: m.cfg.DisplayName,
			Description: m.cfg.Description,
			StartType:   mgr.StartAutomatic,
		},
		"service", "run",
	)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	_ = eventlog.InstallAsEventCreate(m.cfg.ServiceName, eventlog.Error|eventlog.Warning|eventlog.Info)

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

// StopAndUninstall stops and removes TraffCountService (requires Administrator privileges).
func StopAndUninstall() error { return defaultMgr.StopAndUninstall() }

// StopAndUninstall stops and removes the configured Windows service.
func (m *Manager) StopAndUninstall() error {
	if !IsAdmin() {
		return RunElevated("service", "uninstall")
	}

	cm, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer cm.Disconnect()

	s, err := cm.OpenService(m.cfg.ServiceName)
	if err != nil {
		return fmt.Errorf("service %s is not installed", m.cfg.ServiceName)
	}
	defer s.Close()

	status, err := s.Control(svc.Stop)
	if err == nil {
		deadline := time.Now().Add(10 * time.Second)
		for status.State != svc.Stopped && time.Now().Before(deadline) {
			time.Sleep(300 * time.Millisecond)
			status, err = s.Query()
			if err != nil {
				break
			}
		}
	}

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	_ = eventlog.Remove(m.cfg.ServiceName)
	return nil
}

// StartService starts an already-installed TraffCountService.
func StartService() error { return defaultMgr.StartService() }

// StartService starts the already-installed configured service.
func (m *Manager) StartService() error {
	if !IsAdmin() {
		return RunElevated("service", "start")
	}
	_ = m.requestGracefulShutdown()
	cm, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer cm.Disconnect()
	s, err := cm.OpenService(m.cfg.ServiceName)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Start()
}

// StopService stops a running TraffCountService and waits until it reaches Stopped state.
func StopService() error { return defaultMgr.StopService() }

// StopService stops the running configured service and waits until it reaches Stopped state.
func (m *Manager) StopService() error {
	if !IsAdmin() {
		return RunElevated("service", "stop")
	}
	cm, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer cm.Disconnect()
	s, err := cm.OpenService(m.cfg.ServiceName)
	if err != nil {
		return err
	}
	defer s.Close()
	status, err := s.Control(svc.Stop)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for status.State != svc.Stopped && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		status, err = s.Query()
		if err != nil {
			break
		}
	}
	return nil
}

func (m *Manager) requestGracefulShutdown() error {
	if m.cfg.ReleaseURL != "" {
		client := &http.Client{Timeout: 400 * time.Millisecond}
		if resp, err := client.Post(m.cfg.ReleaseURL, "application/json", nil); err == nil {
			resp.Body.Close()
		}
	}

	if m.cfg.TrayWindowCls != "" {
		user32 := windows.NewLazySystemDLL("user32.dll")
		procFindWindowW := user32.NewProc("FindWindowW")
		procPostMessageW := user32.NewProc("PostMessageW")
		className, _ := syscall.UTF16PtrFromString(m.cfg.TrayWindowCls)
		hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
		if hwnd != 0 {
			const WM_RELEASE_SERVER = 0x0400 + 28
			procPostMessageW.Call(hwnd, WM_RELEASE_SERVER, 0, 0)
		}
	}
	time.Sleep(400 * time.Millisecond)
	return nil
}
