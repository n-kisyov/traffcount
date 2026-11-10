//go:build windows

package tray

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"traffcount/internal/collector"
	"traffcount/internal/service"
	"traffcount/internal/winui"
)

const (
	startupRegKey = `Software\Microsoft\Windows\CurrentVersion\Run`
	startupRegVal = "TraffCountTray"

	WM_DESTROY       = 0x0002
	WM_CLOSE         = 0x0010
	WM_COMMAND       = 0x0111
	WM_USER          = 0x0400
	WM_TRAYICON      = WM_USER + 20
	WM_SHOWDASHBOARD = WM_USER + 21
	WM_QUIT_APP      = WM_USER + 22
	WM_SET_TOPMOST   = WM_USER + 23
	WM_SET_STARTUP   = WM_USER + 24
	WM_UPDATE_TIP    = WM_USER + 25
	WM_SHOW_BALLOON  = WM_USER + 26
	WM_LBUTTONUP     = 0x0202
	WM_LBUTTONDBLCLK = 0x0203
	WM_RBUTTONUP     = 0x0205
	WM_CONTEXTMENU   = 0x007B
	NIN_SELECT       = WM_USER + 0
	NIN_KEYSELECT    = WM_USER + 1

	NIM_ADD        = 0x00000000
	NIM_MODIFY     = 0x00000001
	NIM_DELETE     = 0x00000002
	NIM_SETVERSION = 0x00000004

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004
	NIF_INFO    = 0x00000010
	NIIF_INFO   = 0x00000001
	NIIF_WARN   = 0x00000002

	IDI_APPLICATION = 32512
	IMAGE_ICON      = 1
	LR_DEFAULTSIZE  = 0x00000040

	MF_STRING    = 0x00000000
	MF_SEPARATOR = 0x00000800
	MF_CHECKED   = 0x00000008
	MF_GRAYED    = 0x00000001

	TPM_BOTTOMALIGN = 0x0020
	TPM_LEFTALIGN   = 0x0000
	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100
	TPM_NONOTIFY    = 0x0080
	WM_NULL         = 0x0000

	cmdOpenDashboard  = 1001
	cmdStayOnTop      = 1002
	cmdStartupToggle  = 1003
	cmdSvcInstall     = 1004
	cmdSvcStart       = 1005
	cmdSvcStop        = 1006
	cmdSvcUninstall   = 1007
	cmdQuit           = 1008
)

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	shell32                  = windows.NewLazySystemDLL("shell32.dll")
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procFindWindowW          = user32.NewProc("FindWindowW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procCreatePopupMenu      = user32.NewProc("CreatePopupMenu")
	procDestroyMenu          = user32.NewProc("DestroyMenu")
	procAppendMenuW          = user32.NewProc("AppendMenuW")
	procTrackPopupMenu       = user32.NewProc("TrackPopupMenu")
	procGetCursorPos         = user32.NewProc("GetCursorPos")
	procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	procCreateIconFromResEx  = user32.NewProc("CreateIconFromResourceEx")
	procLoadIconW            = user32.NewProc("LoadIconW")
	procShellNotifyIconW     = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
)

type wndClassExW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type point struct {
	X, Y int32
}

type msg struct {
	HWnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type notifyIconDataW struct {
	CbSize           uint32
	HWnd             windows.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}

// Options configures the System Tray runner.
type Options struct {
	DashboardURL string
	WinMgr       *winui.Manager
	AutoOpenUI   bool
	OnExit       func()
}

// Controller manages the Windows 11 System Tray icon and context menu.
type Controller struct {
	opts       Options
	hwnd       windows.Handle
	nid        notifyIconDataW
	hIconIdle  windows.Handle
	hIconRx    windows.Handle
	hIconTx    windows.Handle
	hIconBoth  windows.Handle
	curIcon    windows.Handle
	stayOnTop  bool
	svcState   string
	alerted80  bool
	alerted100 bool
}

var activeController *Controller

// NewController creates a new System Tray controller.
func NewController(opts Options) *Controller {
	top := opts.WinMgr != nil && opts.WinMgr.IsAlwaysOnTop()
	c := &Controller{
		opts:      opts,
		stayOnTop: top,
		svcState:  "Checking...",
	}
	activeController = c
	return c
}

// SyncStayOnTop updates the tray checkmark and applies the topmost style in the interactive user session.
func (c *Controller) SyncStayOnTop(topmost bool) {
	c.stayOnTop = topmost
	if c.opts.WinMgr != nil {
		c.opts.WinMgr.SetAlwaysOnTop(topmost)
	}
}

// NotifyTrayTopmost sends a Win32 message to any running TraffCount tray window in the interactive session
// to immediately apply HWND_TOPMOST / HWND_NOTOPMOST.
func NotifyTrayTopmost(topmost bool) bool {
	className, _ := syscall.UTF16PtrFromString("TraffCountTrayWindowClass")
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
	if hwnd != 0 {
		var wp uintptr
		if topmost {
			wp = 1
		}
		procPostMessageW.Call(hwnd, WM_SET_TOPMOST, wp, 0)
		return true
	}
	return false
}

// NotifyTrayStartup sends a Win32 message to the interactive tray window to update HKCU\...\Run.
func NotifyTrayStartup(enable bool) bool {
	className, _ := syscall.UTF16PtrFromString("TraffCountTrayWindowClass")
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
	if hwnd != 0 {
		var wp uintptr
		if enable {
			wp = 1
		}
		procPostMessageW.Call(hwnd, WM_SET_STARTUP, wp, 0)
		return true
	}
	return false
}

// SignalExistingTrayInstance finds an already-running TraffCount tray hidden window and tells it to open/focus the dashboard.
func SignalExistingTrayInstance() bool {
	className, _ := syscall.UTF16PtrFromString("TraffCountTrayWindowClass")
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, WM_SHOWDASHBOARD, 0, 0)
		return true
	}
	return false
}

// ShutdownExistingTrayInstance asks an already-running TraffCount tray instance to exit cleanly.
func ShutdownExistingTrayInstance() bool {
	className, _ := syscall.UTF16PtrFromString("TraffCountTrayWindowClass")
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, WM_QUIT_APP, 0, 0)
		return true
	}
	return false
}

// Run blocks on the native Win32 message loop for the system tray icon.
func (c *Controller) Run(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hInst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("TraffCountTrayWindowClass")

	wcx := wndClassExW{
		Size:      uint32(unsafe.Sizeof(wndClassExW{})),
		WndProc:   syscall.NewCallback(trayWndProc),
		Instance:  windows.Handle(hInst),
		ClassName: className,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcx)))

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0,
		0, 0, 0, 0,
		0, 0,
		hInst,
		0,
	)
	c.hwnd = windows.Handle(hwnd)

	c.hIconIdle = loadTrayHICONFromBytes(GenerateActivityIconICO(false, false))
	c.hIconRx = loadTrayHICONFromBytes(GenerateActivityIconICO(true, false))
	c.hIconTx = loadTrayHICONFromBytes(GenerateActivityIconICO(false, true))
	c.hIconBoth = loadTrayHICONFromBytes(GenerateActivityIconICO(true, true))
	c.curIcon = c.hIconBoth

	c.nid = notifyIconDataW{
		CbSize:           uint32(unsafe.Sizeof(notifyIconDataW{})),
		HWnd:             c.hwnd,
		UID:              1,
		UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
		UCallbackMessage: WM_TRAYICON,
		HIcon:            c.curIcon,
	}
	setTipText(&c.nid, "TraffCount — Network Traffic Monitor")
	procShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&c.nid)))

	defer func() {
		procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&c.nid)))
		if c.opts.OnExit != nil {
			c.opts.OnExit()
		}
	}()

	if c.opts.AutoOpenUI && c.opts.WinMgr != nil {
		go func() {
			time.Sleep(250 * time.Millisecond)
			_ = c.opts.WinMgr.OpenDashboard()
		}()
	}

	go c.statusUpdater(ctx)
	go c.windowMinimizeWatcher(ctx)

	go func() {
		<-ctx.Done()
		if c.hwnd != 0 {
			procPostMessageW.Call(uintptr(c.hwnd), WM_CLOSE, 0, 0)
		}
	}()

	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func trayWndProc(hwnd uintptr, uMsg uint32, wParam, lParam uintptr) uintptr {
	c := activeController
	switch uMsg {
	case WM_TRAYICON:
		low := uint32(lParam & 0xFFFF)
		switch low {
		case WM_LBUTTONUP, WM_LBUTTONDBLCLK, NIN_SELECT, NIN_KEYSELECT:
			if c != nil && c.opts.WinMgr != nil {
				go c.opts.WinMgr.OpenDashboard()
			}
			return 0
		case WM_RBUTTONUP, WM_CONTEXTMENU:
			if c != nil {
				c.showContextMenu(windows.Handle(hwnd))
			}
			return 0
		}

	case WM_UPDATE_TIP:
		if c != nil {
			c.nid.UFlags = NIF_TIP | NIF_ICON
			procShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&c.nid)))
		}
		return 0

	case WM_SHOW_BALLOON:
		if c != nil {
			c.nid.UFlags = NIF_INFO
			procShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&c.nid)))
		}
		return 0

	case WM_COMMAND:
		cmdID := uint32(wParam & 0xFFFF)
		if c != nil {
			c.handleCommand(cmdID)
		}
		return 0

	case WM_SHOWDASHBOARD:
		if c != nil && c.opts.WinMgr != nil {
			go c.opts.WinMgr.OpenDashboard()
		}
		return 0

	case WM_SET_TOPMOST:
		top := wParam != 0
		if c != nil {
			c.stayOnTop = top
			if c.opts.WinMgr != nil {
				c.opts.WinMgr.SetAlwaysOnTop(top)
			}
		}
		return 0

	case WM_SET_STARTUP:
		enable := wParam != 0
		_ = SetStartupEnabled(enable)
		return 0

	case WM_CLOSE, WM_QUIT_APP:
		procDestroyWindow.Call(hwnd)
		return 0

	case WM_DESTROY:
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(uMsg), wParam, lParam)
	return ret
}

func (c *Controller) showContextMenu(hwnd windows.Handle) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	appendMenuItem(hMenu, MF_STRING, cmdOpenDashboard, "Open TraffCount Dashboard")
	appendMenuItem(hMenu, MF_SEPARATOR, 0, "")

	topFlags := uint32(MF_STRING)
	if c.stayOnTop {
		topFlags |= MF_CHECKED
	}
	appendMenuItem(hMenu, topFlags, cmdStayOnTop, "Stay on Top (Always on Top)")

	startupFlags := uint32(MF_STRING)
	if IsStartupEnabled() {
		startupFlags |= MF_CHECKED
	}
	appendMenuItem(hMenu, startupFlags, cmdStartupToggle, "Start on System Boot (Minimized to Tray)")

	appendMenuItem(hMenu, MF_SEPARATOR, 0, "")
	appendMenuItem(hMenu, MF_STRING|MF_GRAYED, 0, fmt.Sprintf("Windows Service: %s", c.svcState))
	appendMenuItem(hMenu, MF_STRING, cmdSvcInstall, "Install & Start Service (Admin)")
	appendMenuItem(hMenu, MF_STRING, cmdSvcStop, "Stop Service (Admin)")
	appendMenuItem(hMenu, MF_STRING, cmdSvcUninstall, "Uninstall Service (Admin)")

	appendMenuItem(hMenu, MF_SEPARATOR, 0, "")
	appendMenuItem(hMenu, MF_STRING, cmdQuit, "Quit TraffCount")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(uintptr(hwnd))
	cmdID, _, _ := procTrackPopupMenu.Call(
		hMenu,
		TPM_BOTTOMALIGN|TPM_LEFTALIGN|TPM_RIGHTBUTTON|TPM_RETURNCMD|TPM_NONOTIFY,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		uintptr(hwnd),
		0,
	)
	// Required by Win32 Shell_NotifyIcon popup menu docs so the menu dismisses cleanly
	// and subsequent tray clicks are never swallowed
	procPostMessageW.Call(uintptr(hwnd), WM_NULL, 0, 0)
	if cmdID != 0 {
		c.handleCommand(uint32(cmdID))
	}
}

func (c *Controller) handleCommand(cmdID uint32) {
	switch cmdID {
	case cmdOpenDashboard:
		if c.opts.WinMgr != nil {
			go c.opts.WinMgr.OpenDashboard()
		}
	case cmdStayOnTop:
		next := !c.stayOnTop
		c.stayOnTop = next
		if c.opts.WinMgr != nil {
			c.opts.WinMgr.SetAlwaysOnTop(next)
		}
		go c.postTopmostToServer(next)
	case cmdStartupToggle:
		next := !IsStartupEnabled()
		_ = SetStartupEnabled(next)
		go c.postStartOnBootToServer(next)
	case cmdSvcInstall:
		go service.InstallAndStart()
	case cmdSvcStart:
		go service.StartService()
	case cmdSvcStop:
		go service.StopService()
	case cmdSvcUninstall:
		go service.StopAndUninstall()
	case cmdQuit:
		procPostMessageW.Call(uintptr(c.hwnd), WM_CLOSE, 0, 0)
	}
}

func appendMenuItem(hMenu uintptr, flags uint32, id uintptr, label string) {
	var ptr *uint16
	if label != "" {
		ptr, _ = syscall.UTF16PtrFromString(label)
	}
	procAppendMenuW.Call(hMenu, uintptr(flags), id, uintptr(unsafe.Pointer(ptr)))
}

func loadTrayHICON() windows.Handle {
	return loadTrayHICONFromBytes(GenerateIconICO())
}

func loadTrayHICONFromBytes(ico []byte) windows.Handle {
	// Offset 22 is start of BITMAPINFOHEADER in our single-entry ICO
	if len(ico) > 22 {
		dib := ico[22:]
		h, _, _ := procCreateIconFromResEx.Call(
			uintptr(unsafe.Pointer(&dib[0])),
			uintptr(len(dib)),
			1, // TRUE = Icon
			0x00030000,
			32,
			32,
			LR_DEFAULTSIZE,
		)
		if h != 0 {
			return windows.Handle(h)
		}
	}
	h, _, _ := procLoadIconW.Call(0, uintptr(IDI_APPLICATION))
	return windows.Handle(h)
}

// ShowBalloonNotification triggers a Windows 11 system tray toast/balloon alert.
func (c *Controller) ShowBalloonNotification(title, message string, isWarning bool) {
	if c == nil || c.hwnd == 0 {
		return
	}
	uTitle, _ := syscall.UTF16FromString(title)
	for i := range c.nid.SzInfoTitle {
		c.nid.SzInfoTitle[i] = 0
	}
	copy(c.nid.SzInfoTitle[:len(c.nid.SzInfoTitle)-1], uTitle)

	uMsg, _ := syscall.UTF16FromString(message)
	for i := range c.nid.SzInfo {
		c.nid.SzInfo[i] = 0
	}
	copy(c.nid.SzInfo[:len(c.nid.SzInfo)-1], uMsg)

	if isWarning {
		c.nid.DwInfoFlags = NIIF_WARN
	} else {
		c.nid.DwInfoFlags = NIIF_INFO
	}
	procPostMessageW.Call(uintptr(c.hwnd), WM_SHOW_BALLOON, 0, 0)
}

func setTipText(nid *notifyIconDataW, text string) {
	u16, _ := syscall.UTF16FromString(text)
	for i := range nid.SzTip {
		nid.SzTip[i] = 0
	}
	maxLen := len(nid.SzTip) - 1
	if len(u16) < maxLen {
		maxLen = len(u16)
	}
	copy(nid.SzTip[:maxLen], u16[:maxLen])
}

func (c *Controller) windowMinimizeWatcher(ctx context.Context) {
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if c.opts.WinMgr != nil {
				c.opts.WinMgr.InterceptMinimizeToTray()
			}
		}
	}
}

func (c *Controller) statusUpdater(ctx context.Context) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st := service.QueryStatus()
			c.svcState = st.StateText

			// Sync settings (including AlwaysOnTop toggled from the Web UI when served by Windows Service)
			// and continuously enforce HWND_TOPMOST and capture live window bounds on the interactive desktop window.
			if c.opts.WinMgr != nil {
				if b, ok := c.opts.WinMgr.CaptureLiveWindowBounds(); ok {
					c.postBoundsToServer(b)
				}
			}

			sResp, sErr := client.Get(c.opts.DashboardURL + "/api/settings")
			if sErr == nil && sResp.StatusCode == http.StatusOK {
				var cfg struct {
					AlwaysOnTop bool `json:"alwaysOnTop"`
					StartOnBoot bool `json:"startOnBoot"`
				}
				if json.NewDecoder(sResp.Body).Decode(&cfg) == nil {
					if c.stayOnTop != cfg.AlwaysOnTop {
						c.stayOnTop = cfg.AlwaysOnTop
						if c.opts.WinMgr != nil {
							c.opts.WinMgr.SetAlwaysOnTop(cfg.AlwaysOnTop)
						}
					} else if c.opts.WinMgr != nil {
						c.opts.WinMgr.EnforceAlwaysOnTop(c.stayOnTop)
					}
					if IsStartupEnabled() != cfg.StartOnBoot {
						_ = SetStartupEnabled(cfg.StartOnBoot)
					}
				}
				sResp.Body.Close()
			} else if c.opts.WinMgr != nil {
				c.opts.WinMgr.EnforceAlwaysOnTop(c.stayOnTop)
			}

			resp, err := client.Get(c.opts.DashboardURL + "/api/realtime")
			if err == nil && resp.StatusCode == http.StatusOK {
				var snap collector.RealtimePayload
				if json.NewDecoder(resp.Body).Decode(&snap) == nil {
					tip := fmt.Sprintf("TraffCount\nDown: %s\nUp: %s",
						formatSpeedBits(snap.TotalRxBps),
						formatSpeedBits(snap.TotalTxBps),
					)
					setTipText(&c.nid, tip)

					// Dynamic activity icon state (> 1 KB/s threshold)
					rxActive := snap.TotalRxBps > 1024
					txActive := snap.TotalTxBps > 1024
					switch {
					case rxActive && txActive:
						c.nid.HIcon = c.hIconBoth
					case rxActive:
						c.nid.HIcon = c.hIconRx
					case txActive:
						c.nid.HIcon = c.hIconTx
					default:
						c.nid.HIcon = c.hIconIdle
					}

					if c.hwnd != 0 {
						procPostMessageW.Call(uintptr(c.hwnd), WM_UPDATE_TIP, 0, 0)
					}
				}
				resp.Body.Close()
			}
		}
	}
}

func (c *Controller) postTopmostToServer(topmost bool) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	body, _ := json.Marshal(map[string]bool{"alwaysOnTop": topmost})
	resp, err := client.Post(c.opts.DashboardURL+"/api/window/topmost", "application/json", bytes.NewReader(body))
	if err == nil {
		resp.Body.Close()
	}
}

func (c *Controller) postStartOnBootToServer(enabled bool) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	sResp, err := client.Get(c.opts.DashboardURL + "/api/settings")
	if err != nil {
		return
	}
	var cfg map[string]any
	_ = json.NewDecoder(sResp.Body).Decode(&cfg)
	sResp.Body.Close()
	if cfg == nil {
		return
	}
	cfg["startOnBoot"] = enabled
	body, _ := json.Marshal(cfg)
	resp, err := client.Post(c.opts.DashboardURL+"/api/settings", "application/json", bytes.NewReader(body))
	if err == nil {
		resp.Body.Close()
	}
}

func (c *Controller) postBoundsToServer(b any) {
	client := &http.Client{Timeout: 1000 * time.Millisecond}
	body, _ := json.Marshal(b)
	resp, err := client.Post(c.opts.DashboardURL+"/api/window/bounds", "application/json", bytes.NewReader(body))
	if err == nil {
		resp.Body.Close()
	}
}

func formatSpeedBits(bytesPerSec uint64) string {
	bits := float64(bytesPerSec) * 8.0
	switch {
	case bits >= 1e9:
		return fmt.Sprintf("%.2f Gbps", bits/1e9)
	case bits >= 1e6:
		return fmt.Sprintf("%.1f Mbps", bits/1e6)
	case bits >= 1e3:
		return fmt.Sprintf("%.0f Kbps", bits/1e3)
	default:
		return fmt.Sprintf("%.0f bps", bits)
	}
}

// IsStartupEnabled checks whether TraffCount is registered in HKCU\...\Run to start on system boot.
func IsStartupEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, startupRegKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetStringValue(startupRegVal)
	return err == nil && val != ""
}

// SetStartupEnabled configures or removes the HKCU\...\Run entry to launch TraffCount minimized to tray (--tray-only) on boot.
func SetStartupEnabled(enable bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, startupRegKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if !enable {
		_ = k.DeleteValue(startupRegVal)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	return k.SetStringValue(startupRegVal, fmt.Sprintf(`"%s" --tray-only`, exe))
}
