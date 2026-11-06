//go:build windows

package winui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const WindowTitlePrefix = "TraffCount — Network Monitor"

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows     = user32.NewProc("EnumWindows")
	procGetWindowTextW  = user32.NewProc("GetWindowTextW")
	procSetWindowPos    = user32.NewProc("SetWindowPos")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procShowWindow      = user32.NewProc("ShowWindow")
	procIsWindowVisible = user32.NewProc("IsWindowVisible")
	procIsIconic        = user32.NewProc("IsIconic")
)

const (
	SWP_NOSIZE       = 0x0001
	SWP_NOMOVE       = 0x0002
	SWP_NOACTIVATE   = 0x0010
	SWP_FRAMECHANGED = 0x0020
	SWP_SHOWWINDOW   = 0x0040
	SW_SHOW          = 5
	SW_RESTORE       = 9
	HWND_TOPMOST     = ^uintptr(0)     // -1
	HWND_NOTOPMOST   = ^uintptr(0) - 1 // -2

	GWL_EXSTYLE      = -20
	WS_EX_TOPMOST    = 0x00000008
)

var procGetWindowLongW = user32.NewProc("GetWindowLongW")

// Manager controls the GUI app window and its Always-On-Top ("Stay on Top") state.
type Manager struct {
	mu          sync.Mutex
	url         string
	alwaysOnTop bool
	launching   bool
}

// NewManager creates a window manager for the given local dashboard URL.
func NewManager(url string, initialTopmost bool) *Manager {
	return &Manager{
		url:         url,
		alwaysOnTop: initialTopmost,
	}
}

// IsAlwaysOnTop returns the current "Stay on Top" setting.
func (m *Manager) IsAlwaysOnTop() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alwaysOnTop
}

// SetAlwaysOnTop updates the "Stay on Top" flag and immediately applies it to all open TraffCount windows.
func (m *Manager) SetAlwaysOnTop(topmost bool) bool {
	m.mu.Lock()
	m.alwaysOnTop = topmost
	m.mu.Unlock()

	return ApplyTopmostToAllWindows(topmost)
}

// EnforceAlwaysOnTop checks open TraffCount windows and ensures their WS_EX_TOPMOST style matches topmost.
func (m *Manager) EnforceAlwaysOnTop(topmost bool) {
	m.mu.Lock()
	m.alwaysOnTop = topmost
	m.mu.Unlock()

	hwlist := FindAllTraffCountWindows()
	for _, hwnd := range hwlist {
		isTop := isWindowTopmost(hwnd)
		if isTop != topmost {
			applyTopmost(hwnd, topmost)
		}
	}
}

// OpenDashboard brings an existing TraffCount window to the foreground, or launches a new native-like app window.
func (m *Manager) OpenDashboard() error {
	m.mu.Lock()
	topmost := m.alwaysOnTop
	url := m.url
	if hwnd := FindTraffCountWindow(); hwnd != 0 {
		m.mu.Unlock()
		focusExistingWindow(hwnd, topmost)
		return nil
	}
	if m.launching {
		// Another click already initiated window launch and is waiting for it to appear
		m.mu.Unlock()
		return nil
	}
	m.launching = true
	m.mu.Unlock()

	edgePath := findEdgeBinary()
	if edgePath != "" {
		// Use a dedicated Edge profile directory so --app always opens as a standalone frameless app window
		// with its own taskbar icon and immediate window control.
		profileDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "TraffCount", "WebViewProfile")
		_ = os.MkdirAll(profileDir, 0755)

		cmd := exec.Command(
			edgePath,
			fmt.Sprintf("--app=%s", url),
			fmt.Sprintf("--user-data-dir=%s", profileDir),
			"--window-size=1180,780",
			"--no-first-run",
			"--disable-features=Translate",
		)
		if err := cmd.Start(); err == nil {
			go func() {
				defer func() {
					m.mu.Lock()
					m.launching = false
					m.mu.Unlock()
				}()
				// Poll briefly for the window to appear so we can apply Always-On-Top and focus
				for i := 0; i < 30; i++ {
					time.Sleep(100 * time.Millisecond)
					if hwnd := FindTraffCountWindow(); hwnd != 0 {
						applyTopmost(hwnd, m.IsAlwaysOnTop())
						procSetForeground.Call(hwnd)
						break
					}
				}
			}()
			return nil
		}
	}

	m.mu.Lock()
	m.launching = false
	m.mu.Unlock()

	// Fallback: open in default browser
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func focusExistingWindow(hwnd uintptr, topmost bool) {
	iconic, _, _ := procIsIconic.Call(hwnd)
	if iconic != 0 {
		procShowWindow.Call(hwnd, SW_RESTORE)
	} else {
		procShowWindow.Call(hwnd, SW_SHOW)
	}
	procSetForeground.Call(hwnd)
	applyTopmost(hwnd, topmost)
}

// FindTraffCountWindow searches top-level visible windows for one whose title contains WindowTitlePrefix.
func FindTraffCountWindow() uintptr {
	all := FindAllTraffCountWindows()
	if len(all) > 0 {
		return all[0]
	}
	return 0
}

// FindAllTraffCountWindows returns all visible top-level HWNDs matching WindowTitlePrefix.
func FindAllTraffCountWindows() []uintptr {
	var found []uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1 // continue
		}
		buf := make([]uint16, 256)
		ret, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if ret > 0 {
			title := windows.UTF16ToString(buf[:ret])
			if strings.Contains(title, WindowTitlePrefix) {
				found = append(found, hwnd)
			}
		}
		return 1 // continue
	})
	procEnumWindows.Call(cb, 0)
	return found
}

// ApplyTopmostToAllWindows sets HWND_TOPMOST or HWND_NOTOPMOST on all matching TraffCount windows.
func ApplyTopmostToAllWindows(topmost bool) bool {
	hwlist := FindAllTraffCountWindows()
	for _, hwnd := range hwlist {
		applyTopmost(hwnd, topmost)
	}
	return len(hwlist) > 0
}

func isWindowTopmost(hwnd uintptr) bool {
	idx := int32(GWL_EXSTYLE)
	style, _, _ := procGetWindowLongW.Call(hwnd, uintptr(idx))
	return (style & WS_EX_TOPMOST) != 0
}

func applyTopmost(hwnd uintptr, topmost bool) {
	insertAfter := HWND_NOTOPMOST
	if topmost {
		insertAfter = HWND_TOPMOST
	}
	procSetWindowPos.Call(
		hwnd,
		insertAfter,
		0, 0, 0, 0,
		SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE|SWP_FRAMECHANGED,
	)
}

func findEdgeBinary() string {
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "Application", "msedge.exe"),
	}
	for _, p := range candidates {
		if p != "" {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	if p, err := exec.LookPath("msedge.exe"); err == nil {
		return p
	}
	return ""
}

