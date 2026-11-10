//go:build windows

package service

import (
	"fmt"
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

// StatusInfo represents the current state of the TraffCount Windows Service.
type StatusInfo struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	StateText string `json:"stateText"`
	IsAdmin   bool   `json:"isAdmin"`
}

// IsWindowsServiceProcess reports whether the current process is executing as a Windows Service.
func IsWindowsServiceProcess() bool {
	inService, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return inService
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
func QueryStatus() StatusInfo {
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

	svcNamePtr, _ := syscall.UTF16PtrFromString(ServiceName)
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
		// Wait briefly (up to 3s) for non-interactive completion
		_, _ = windows.WaitForSingleObject(sei.HProcess, 3000)
	}
	return nil
}

// InstallAndStart installs TraffCountService and starts it (requires Administrator privileges).
func InstallAndStart() error {
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

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()

	// Ask any running TraffCount tray instance to release its in-process HTTP server on port 9876
	_ = requestGracefulShutdown()

	s, err := m.OpenService(ServiceName)
	if err == nil {
		// Already installed; make sure it's started
		defer s.Close()
		_ = s.Start()
		return nil
	}

	s, err = m.CreateService(
		ServiceName,
		exePath,
		mgr.Config{
			DisplayName: ServiceDisplayName,
			Description: ServiceDescription,
			StartType:   mgr.StartAutomatic,
		},
		"service", "run",
	)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	_ = eventlog.InstallAsEventCreate(ServiceName, eventlog.Error|eventlog.Warning|eventlog.Info)

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

// StopAndUninstall stops and removes TraffCountService (requires Administrator privileges).
func StopAndUninstall() error {
	if !IsAdmin() {
		return RunElevated("service", "uninstall")
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("service %s is not installed", ServiceName)
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
	_ = eventlog.Remove(ServiceName)
	return nil
}

// StartService starts an already-installed TraffCountService.
func StartService() error {
	if !IsAdmin() {
		return RunElevated("service", "start")
	}
	_ = requestGracefulShutdown()
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Start()
}

// StopService stops a running TraffCountService and waits until it reaches Stopped state.
func StopService() error {
	if !IsAdmin() {
		return RunElevated("service", "stop")
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
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

func requestGracefulShutdown() error {
	// Ask any running TraffCount tray instance to release its in-process HTTP server on port 9876
	// (WITHOUT closing the system tray icon itself!) so TraffCountService can bind port 9876 cleanly.
	user32 := windows.NewLazySystemDLL("user32.dll")
	procFindWindowW := user32.NewProc("FindWindowW")
	procPostMessageW := user32.NewProc("PostMessageW")
	className, _ := syscall.UTF16PtrFromString("TraffCountTrayWindowClass")
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
	if hwnd != 0 {
		const WM_RELEASE_SERVER = 0x0400 + 28
		procPostMessageW.Call(hwnd, WM_RELEASE_SERVER, 0, 0)
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}


