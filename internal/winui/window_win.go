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
	"traffcount/internal/storage"
)

const WindowTitlePrefix = "TraffCount — Network Monitor"

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows     = user32.NewProc("EnumWindows")
	procGetWindowTextW  = user32.NewProc("GetWindowTextW")
	procSetWindowPos    = user32.NewProc("SetWindowPos")
	procGetWindowRect   = user32.NewProc("GetWindowRect")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procShowWindow      = user32.NewProc("ShowWindow")
	procIsWindowVisible = user32.NewProc("IsWindowVisible")
	procIsIconic        = user32.NewProc("IsIconic")
	procIsZoomed        = user32.NewProc("IsZoomed")
	procGetWindowLongW  = user32.NewProc("GetWindowLongW")
)

const (
	SWP_NOSIZE       = 0x0001
	SWP_NOMOVE       = 0x0002
	SWP_NOZORDER     = 0x0004
	SWP_NOACTIVATE   = 0x0010
	SWP_FRAMECHANGED = 0x0020
	SWP_SHOWWINDOW   = 0x0040
	SW_HIDE          = 0
	SW_MAXIMIZE      = 3
	SW_SHOW          = 5
	SW_RESTORE       = 9
	HWND_TOPMOST     = ^uintptr(0)     // -1
	HWND_NOTOPMOST   = ^uintptr(0) - 1 // -2

	GWL_EXSTYLE      = -20
	WS_EX_TOPMOST    = 0x00000008
)

var enumMu sync.Mutex

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

// Manager controls the GUI app window, its Always-On-Top ("Stay on Top") state, and saved window bounds.
type Manager struct {
	mu          sync.Mutex
	url         string
	alwaysOnTop bool
	bounds      storage.WindowBounds
	launching   bool
}

// NewManager creates a window manager for the given local dashboard URL.
func NewManager(url string, initialTopmost bool) *Manager {
	return &Manager{
		url:         url,
		alwaysOnTop: initialTopmost,
		bounds:      storage.DefaultWindowBounds(),
	}
}

// SetSavedBounds updates the stored window position and size to use on next launch.
func (m *Manager) SetSavedBounds(b storage.WindowBounds) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b.Width >= 400 && b.Height >= 300 {
		m.bounds = b
	}
}

// GetSavedBounds returns the most recently tracked or loaded window bounds.
func (m *Manager) GetSavedBounds() storage.WindowBounds {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bounds
}

// CaptureLiveWindowBounds inspects any currently open normal (non-minimized) TraffCount window
// on the desktop and updates m.bounds if found.
func (m *Manager) CaptureLiveWindowBounds() (storage.WindowBounds, bool) {
	hwnd := FindTraffCountWindow()
	if hwnd == 0 {
		return storage.WindowBounds{}, false
	}
	iconic, _, _ := procIsIconic.Call(hwnd)
	if iconic != 0 {
		// Don't overwrite normal window bounds while minimized to taskbar (-32000,-32000)
		return storage.WindowBounds{}, false
	}
	zoomed, _, _ := procIsZoomed.Call(hwnd)
	var r winRect
	ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ret == 0 {
		return storage.WindowBounds{}, false
	}
	w := int(r.Right - r.Left)
	h := int(r.Bottom - r.Top)
	if w < 400 || h < 300 {
		return storage.WindowBounds{}, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if zoomed != 0 {
		m.bounds.Maximized = true
		m.bounds.HasValue = true
	} else {
		m.bounds = storage.WindowBounds{
			X:         int(r.Left),
			Y:         int(r.Top),
			Width:     w,
			Height:    h,
			Maximized: false,
			HasValue:  true,
		}
	}
	return m.bounds, true
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

// InterceptMinimizeToTray checks if any TraffCount window has been minimized (IsIconic)
// and immediately hides it from the taskbar (SW_HIDE) so minimizing sends it to the System Tray only.
func (m *Manager) InterceptMinimizeToTray() bool {
	hwlist := FindAllTraffCountWindows(false)
	hidAny := false
	for _, hwnd := range hwlist {
		iconic, _, _ := procIsIconic.Call(hwnd)
		if iconic != 0 {
			vis, _, _ := procIsWindowVisible.Call(hwnd)
			if vis != 0 {
				procShowWindow.Call(hwnd, SW_HIDE)
				hidAny = true
			}
		}
	}
	return hidAny
}

// EnforceAlwaysOnTop checks open TraffCount windows and ensures their WS_EX_TOPMOST style matches topmost.
func (m *Manager) EnforceAlwaysOnTop(topmost bool) {
	m.mu.Lock()
	m.alwaysOnTop = topmost
	m.mu.Unlock()

	hwlist := FindAllTraffCountWindows(true)
	for _, hwnd := range hwlist {
		isTop := isWindowTopmost(hwnd)
		if isTop != topmost {
			applyTopmost(hwnd, topmost)
		}
	}
}

// OpenDashboard brings an existing TraffCount window to the foreground (even if hidden in tray), or launches a new native-like app window.
func (m *Manager) OpenDashboard() error {
	m.mu.Lock()
	topmost := m.alwaysOnTop
	url := m.url
	bounds := m.bounds
	if hwnd := FindAnyTraffCountWindow(); hwnd != 0 {
		m.mu.Unlock()
		focusExistingWindow(hwnd, topmost, bounds)
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

		w := bounds.Width
		h := bounds.Height
		if w < 400 {
			w = 1180
		}
		if h < 300 {
			h = 780
		}

		args := []string{
			fmt.Sprintf("--app=%s", url),
			fmt.Sprintf("--user-data-dir=%s", profileDir),
			fmt.Sprintf("--window-size=%d,%d", w, h),
			"--no-first-run",
			"--disable-features=Translate",
		}
		if bounds.HasValue {
			args = append(args, fmt.Sprintf("--window-position=%d,%d", bounds.X, bounds.Y))
		}

		cmd := exec.Command(edgePath, args...)
		if err := cmd.Start(); err == nil {
			go func() {
				defer func() {
					m.mu.Lock()
					m.launching = false
					m.mu.Unlock()
				}()
				// Poll briefly for the window to appear so we can apply saved bounds, Always-On-Top, and focus
				for i := 0; i < 30; i++ {
					time.Sleep(100 * time.Millisecond)
					if hwnd := FindTraffCountWindow(); hwnd != 0 {
						b := m.GetSavedBounds()
						if b.HasValue {
							applyWindowBounds(hwnd, b)
						}
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

func applyWindowBounds(hwnd uintptr, b storage.WindowBounds) {
	if b.Width >= 400 && b.Height >= 300 {
		procSetWindowPos.Call(
			hwnd,
			0,
			uintptr(int32(b.X)),
			uintptr(int32(b.Y)),
			uintptr(int32(b.Width)),
			uintptr(int32(b.Height)),
			SWP_NOZORDER|SWP_NOACTIVATE,
		)
	}
	if b.Maximized {
		procShowWindow.Call(hwnd, SW_MAXIMIZE)
	}
}

func focusExistingWindow(hwnd uintptr, topmost bool, bounds storage.WindowBounds) {
	vis, _, _ := procIsWindowVisible.Call(hwnd)
	iconic, _, _ := procIsIconic.Call(hwnd)
	if vis == 0 {
		procShowWindow.Call(hwnd, SW_SHOW)
	}
	if iconic != 0 {
		procShowWindow.Call(hwnd, SW_RESTORE)
	}
	if bounds.HasValue {
		applyWindowBounds(hwnd, bounds)
	}
	procSetForeground.Call(hwnd)
	applyTopmost(hwnd, topmost)
}

// FindTraffCountWindow searches top-level visible windows for one whose title contains WindowTitlePrefix.
func FindTraffCountWindow() uintptr {
	all := FindAllTraffCountWindows(true)
	if len(all) > 0 {
		return all[0]
	}
	return 0
}

// FindAnyTraffCountWindow searches top-level windows (both visible and SW_HIDE hidden) for WindowTitlePrefix.
func FindAnyTraffCountWindow() uintptr {
	all := FindAllTraffCountWindows(false)
	if len(all) > 0 {
		return all[0]
	}
	return 0
}

var (
	enumTargetVisibleOnly bool
	enumFoundList         []uintptr
	sharedEnumCallback    = syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		if enumTargetVisibleOnly {
			vis, _, _ := procIsWindowVisible.Call(hwnd)
			if vis == 0 {
				return 1
			}
		}
		buf := make([]uint16, 256)
		ret, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if ret > 0 {
			title := windows.UTF16ToString(buf[:ret])
			if strings.Contains(title, WindowTitlePrefix) {
				enumFoundList = append(enumFoundList, hwnd)
			}
		}
		return 1
	})
)

// FindAllTraffCountWindows returns top-level HWNDs matching WindowTitlePrefix.
func FindAllTraffCountWindows(visibleOnly bool) []uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumTargetVisibleOnly = visibleOnly
	enumFoundList = nil
	procEnumWindows.Call(sharedEnumCallback, 0)
	res := make([]uintptr, len(enumFoundList))
	copy(res, enumFoundList)
	return res
}

// ApplyTopmostToAllWindows sets HWND_TOPMOST or HWND_NOTOPMOST on all matching TraffCount windows.
func ApplyTopmostToAllWindows(topmost bool) bool {
	hwlist := FindAllTraffCountWindows(false)
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

