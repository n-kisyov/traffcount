//go:build windows

package winui

import (
	"encoding/json"
	"fmt"
	"net/url"
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
	user32                   = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows          = user32.NewProc("EnumWindows")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procSetWindowPos         = user32.NewProc("SetWindowPos")
	procGetWindowRect        = user32.NewProc("GetWindowRect")
	procGetWindowPlacement   = user32.NewProc("GetWindowPlacement")
	procSetWindowPlacement   = user32.NewProc("SetWindowPlacement")
	procSetForeground        = user32.NewProc("SetForegroundWindow")
	procShowWindow           = user32.NewProc("ShowWindow")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procIsWindowVisible      = user32.NewProc("IsWindowVisible")
	procIsIconic             = user32.NewProc("IsIconic")
	procIsZoomed             = user32.NewProc("IsZoomed")
	procGetWindowLongW       = user32.NewProc("GetWindowLongW")
	procSystemParametersInfo = user32.NewProc("SystemParametersInfoW")

	dwmapi                    = windows.NewLazySystemDLL("dwmapi.dll")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	WM_CLOSE         = 0x0010
	SWP_NOSIZE       = 0x0001
	SWP_NOMOVE       = 0x0002
	SWP_NOZORDER     = 0x0004
	SWP_NOACTIVATE   = 0x0010
	SWP_FRAMECHANGED = 0x0020
	SWP_SHOWWINDOW   = 0x0040
	SW_HIDE          = 0
	SW_SHOWNORMAL    = 1
	SW_MAXIMIZE      = 3
	SW_SHOWMAXIMIZED = 3
	SW_SHOW          = 5
	SW_RESTORE       = 9
	HWND_TOPMOST     = ^uintptr(0)     // -1
	HWND_NOTOPMOST   = ^uintptr(0) - 1 // -2

	GWL_EXSTYLE     = -20
	WS_EX_TOPMOST   = 0x00000008
	SPI_GETWORKAREA = 0x0030

	WPF_RESTORETOMAXIMIZED        = 0x0002
	WPF_ASYNCWINDOWPLACEMENT      = 0x0004
	DWMWA_TRANSITIONS_FORCEDISABLED = 3

	miniWindowWidth   = 380
	miniWindowHeight  = 230
	minimizeHideDelay = 200 * time.Millisecond
)

var enumMu sync.Mutex

type winPoint struct {
	X int32
	Y int32
}

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type windowPlacement struct {
	Length           uint32
	Flags            uint32
	ShowCmd          uint32
	PtMinPosition    winPoint
	PtMaxPosition    winPoint
	RcNormalPosition winRect
}

// Manager controls the GUI app window, its Always-On-Top ("Stay on Top") state, and saved window bounds.
type Manager struct {
	mu          sync.Mutex
	url         string
	alwaysOnTop bool
	miniMode    bool
	miniHasPos  bool
	miniX       int
	miniY       int
	bounds      storage.WindowBounds
	launching   bool
	iconicSince map[uintptr]time.Time
}

// NewManager creates a window manager for the given local dashboard URL.
func NewManager(url string, initialTopmost bool) *Manager {
	return &Manager{
		url:         url,
		alwaysOnTop: initialTopmost,
		bounds:      storage.DefaultWindowBounds(),
		iconicSince: make(map[uintptr]time.Time),
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
// on the desktop and updates m.bounds if found. When in mini-widget mode, it updates the remembered
// mini-widget position without overwriting normal window bounds.
func (m *Manager) CaptureLiveWindowBounds() (storage.WindowBounds, bool) {
	m.mu.Lock()
	inMini := m.miniMode
	m.mu.Unlock()

	hwnd := FindTraffCountWindow()
	if hwnd == 0 {
		return storage.WindowBounds{}, false
	}
	iconic, _, _ := procIsIconic.Call(hwnd)
	if iconic != 0 {
		// Don't overwrite window bounds while minimized to taskbar (-32000,-32000)
		return storage.WindowBounds{}, false
	}
	var r winRect
	ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ret == 0 {
		return storage.WindowBounds{}, false
	}
	w := int(r.Right - r.Left)
	h := int(r.Bottom - r.Top)

	if inMini {
		if w > 0 && w <= 440 && h > 0 && h <= 280 && r.Left > -3000 && r.Top > -3000 {
			m.mu.Lock()
			m.miniX = int(r.Left)
			m.miniY = int(r.Top)
			m.miniHasPos = true
			m.mu.Unlock()
		}
		return storage.WindowBounds{}, false
	}

	zoomed, _, _ := procIsZoomed.Call(hwnd)
	if w < 450 || h < 320 {
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

// IsMiniMode returns true if the window is currently in compact mini-widget overlay mode.
func (m *Manager) IsMiniMode() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.miniMode
}

// SetMiniMode resizes the open TraffCount window into a compact 380x230 always-on-top mini-widget
// or restores its normal saved bounds.
func (m *Manager) SetMiniMode(mini bool) bool {
	if mini {
		if !m.IsMiniMode() {
			m.CaptureLiveWindowBounds()
		}
	} else {
		// Preserve current mini-widget position before returning to normal view
		m.CaptureLiveWindowBounds()
	}

	m.mu.Lock()
	m.miniMode = mini
	miniHasPos := m.miniHasPos
	miniX := m.miniX
	miniY := m.miniY
	bounds := m.bounds
	topmost := m.alwaysOnTop
	m.mu.Unlock()

	hwlist := FindAllTraffCountWindows(true)
	applied := false
	for _, hwnd := range hwlist {
		iconic, _, _ := procIsIconic.Call(hwnd)
		if iconic != 0 {
			continue
		}
		if mini {
			zoomed, _, _ := procIsZoomed.Call(hwnd)
			if zoomed != 0 {
				procShowWindow.Call(hwnd, SW_RESTORE)
				time.Sleep(40 * time.Millisecond)
			}
			targetX, targetY := miniX, miniY
			if !miniHasPos {
				var r winRect
				procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
				targetX = int(r.Left)
				targetY = int(r.Top)
				if targetX < 0 || targetY < 0 {
					targetX = 80
					targetY = 80
				}
				m.mu.Lock()
				m.miniX = targetX
				m.miniY = targetY
				m.miniHasPos = true
				m.mu.Unlock()
				miniX, miniY, miniHasPos = targetX, targetY, true
			}
			procSetWindowPos.Call(
				hwnd,
				HWND_TOPMOST,
				uintptr(int32(targetX)),
				uintptr(int32(targetY)),
				uintptr(miniWindowWidth),
				uintptr(miniWindowHeight),
				SWP_FRAMECHANGED|SWP_NOACTIVATE,
			)
			applied = true
		} else {
			if bounds.Width < 450 {
				bounds.Width = 1180
			}
			if bounds.Height < 320 {
				bounds.Height = 780
			}
			if !bounds.HasValue || bounds.X < -10000 {
				bounds.X = 120
			}
			if !bounds.HasValue || bounds.Y < -10000 {
				bounds.Y = 80
			}
			bounds.HasValue = true
			applyWindowBounds(hwnd, bounds)
			applyTopmost(hwnd, topmost)
			applied = true
		}
	}
	return applied
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
	effectiveTop := topmost || m.miniMode
	m.mu.Unlock()

	return ApplyTopmostToAllWindows(effectiveTop)
}

// planMinimizeHides tracks when each visible window entered iconic (minimized) state and returns
// those that have finished the DWM minimize animation delay so they can be cleanly hidden with SW_HIDE.
func (m *Manager) planMinimizeHides(visibleIconic map[uintptr]bool, now time.Time) []uintptr {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.iconicSince == nil {
		m.iconicSince = make(map[uintptr]time.Time)
	}
	for hwnd := range m.iconicSince {
		if !visibleIconic[hwnd] {
			delete(m.iconicSince, hwnd)
		}
	}
	var toHide []uintptr
	for hwnd, isIconic := range visibleIconic {
		if !isIconic {
			delete(m.iconicSince, hwnd)
			continue
		}
		since, exists := m.iconicSince[hwnd]
		if !exists {
			m.iconicSince[hwnd] = now
			continue
		}
		if now.Sub(since) >= minimizeHideDelay {
			toHide = append(toHide, hwnd)
			delete(m.iconicSince, hwnd)
		}
	}
	return toHide
}

// InterceptMinimizeToTray waits for the native DWM minimize animation to complete on newly minimized
// TraffCount windows, then hides them (SW_HIDE only, without a flashing SW_RESTORE) to the System Tray.
func (m *Manager) InterceptMinimizeToTray() bool {
	hwlist := FindAllTraffCountWindows(true)
	if len(hwlist) == 0 {
		m.planMinimizeHides(nil, time.Now())
		return false
	}

	visibleIconic := make(map[uintptr]bool, len(hwlist))
	for _, hwnd := range hwlist {
		iconic, _, _ := procIsIconic.Call(hwnd)
		visibleIconic[hwnd] = iconic != 0
	}

	toHide := m.planMinimizeHides(visibleIconic, time.Now())
	if len(toHide) == 0 {
		return false
	}

	for _, hwnd := range toHide {
		// Record last restored mini-widget position before hiding while minimized.
		if m.IsMiniMode() {
			var wp windowPlacement
			wp.Length = uint32(unsafe.Sizeof(wp))
			if got, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp))); got != 0 {
				w := int(wp.RcNormalPosition.Right - wp.RcNormalPosition.Left)
				h := int(wp.RcNormalPosition.Bottom - wp.RcNormalPosition.Top)
				if w > 0 && w <= 440 && h > 0 && h <= 280 && wp.RcNormalPosition.Left > -3000 && wp.RcNormalPosition.Top > -3000 {
					m.mu.Lock()
					m.miniX = int(wp.RcNormalPosition.Left)
					m.miniY = int(wp.RcNormalPosition.Top)
					m.miniHasPos = true
					m.mu.Unlock()
				}
			}
		}
		// Hide without calling SW_RESTORE so DWM never snaps the window back mid-minimize.
		// Preserving WS_MINIMIZE allows SetWindowPlacement(SW_RESTORE) to animate smoothly on reopen.
		procShowWindow.Call(hwnd, SW_HIDE)
	}
	return true
}

// EnforceAlwaysOnTop checks open TraffCount windows and ensures their WS_EX_TOPMOST style matches topmost.
func (m *Manager) EnforceAlwaysOnTop(topmost bool) {
	m.mu.Lock()
	m.alwaysOnTop = topmost
	inMini := m.miniMode
	effectiveTop := topmost || inMini
	m.mu.Unlock()

	hwlist := FindAllTraffCountWindows(true)
	for _, hwnd := range hwlist {
		iconic, _, _ := procIsIconic.Call(hwnd)
		if iconic != 0 {
			continue
		}
		if inMini {
			var r winRect
			ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
			if ret != 0 && ((r.Right-r.Left) > 420 || (r.Bottom-r.Top) > 280) {
				// Window is supposed to be in mini-mode but hasn't been resized yet
				m.SetMiniMode(true)
				return
			}
		}
		isTop := isWindowTopmost(hwnd)
		if isTop != effectiveTop {
			applyTopmost(hwnd, effectiveTop)
		}
	}
}

// OpenDashboard brings an existing TraffCount window to the foreground, or launches a new native-like app window.
func (m *Manager) OpenDashboard() error {
	m.mu.Lock()
	topmost := m.alwaysOnTop
	inMini := m.miniMode
	miniHasPos := m.miniHasPos
	miniX := m.miniX
	miniY := m.miniY
	url := m.url
	bounds := m.bounds
	if hwnd := FindAnyTraffCountWindow(); hwnd != 0 {
		delete(m.iconicSince, hwnd)
		m.mu.Unlock()
		m.focusExistingWindow(hwnd, topmost, bounds, inMini, miniHasPos, miniX, miniY)
		time.Sleep(60 * time.Millisecond)
		if vis, _, _ := procIsWindowVisible.Call(hwnd); vis != 0 {
			return nil
		}
		m.mu.Lock()
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
		// Isolate Mini-Widget into its own Edge user-data-dir so a lingering normal-mode browser
		// ProcessSingleton (e.g. 1920x1032) never hijacks widget launches with full-screen dimensions.
		profileName := "WebViewProfile"
		if inMini {
			profileName = "WebViewProfileMini"
		}
		profileDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "TraffCount", profileName)
		_ = os.MkdirAll(profileDir, 0755)

		var w, h, x, y int
		var hasPos bool
		if inMini {
			w = miniWindowWidth
			h = miniWindowHeight
			if miniHasPos {
				x, y, hasPos = miniX, miniY, true
			} else if bounds.HasValue && bounds.X >= 0 && bounds.Y >= 0 {
				x, y, hasPos = bounds.X, bounds.Y, true
			} else {
				wa := getWorkAreaRect()
				x = int(wa.Right) - w - 32
				y = int(wa.Bottom) - h - 32
				if x < int(wa.Left) {
					x = 120
				}
				if y < int(wa.Top) {
					y = 80
				}
				hasPos = true
			}
			_ = ensureMiniProfilePlacement(profileDir, url, x, y, w, h)
		} else {
			w = bounds.Width
			h = bounds.Height
			if w < 450 {
				w = 1180
			}
			if h < 320 {
				h = 780
			}
			if bounds.HasValue {
				x, y, hasPos = bounds.X, bounds.Y, true
			}
		}

		args := []string{
			fmt.Sprintf("--app=%s", url),
			fmt.Sprintf("--user-data-dir=%s", profileDir),
			fmt.Sprintf("--window-size=%d,%d", w, h),
			"--no-first-run",
			"--disable-background-mode",
			"--disable-session-crashed-bubble",
			"--disable-features=Translate,msEdgeStartupBoost",
		}
		if hasPos {
			args = append(args, fmt.Sprintf("--window-position=%d,%d", x, y))
		}

		cmd := exec.Command(edgePath, args...)
		if err := cmd.Start(); err == nil {
			go func() {
				defer func() {
					m.mu.Lock()
					m.launching = false
					m.mu.Unlock()
				}()
				// Poll quickly for the window to appear so saved bounds, Always-On-Top, and focus apply immediately.
				for i := 0; i < 175; i++ {
					time.Sleep(20 * time.Millisecond)
					if i == 50 && FindAnyTraffCountWindow() == 0 {
						// Retry launch once in case previous Edge instance was still releasing its profile lock
						_ = exec.Command(edgePath, args...).Start()
					}
					if hwnd := FindTraffCountWindow(); hwnd != 0 {
						if m.IsMiniMode() {
							m.applyMiniToWindow(hwnd)
						} else {
							b := m.GetSavedBounds()
							if b.HasValue {
								applyWindowBounds(hwnd, b)
							}
							applyTopmost(hwnd, m.IsAlwaysOnTop())
						}
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

func (m *Manager) applyMiniToWindow(hwnd uintptr) {
	if zoomed, _, _ := procIsZoomed.Call(hwnd); zoomed != 0 {
		procShowWindow.Call(hwnd, SW_RESTORE)
	}
	m.mu.Lock()
	miniHasPos := m.miniHasPos
	miniX := m.miniX
	miniY := m.miniY
	m.mu.Unlock()

	flags := uintptr(SWP_FRAMECHANGED | SWP_NOACTIVATE)
	if !miniHasPos {
		flags |= SWP_NOMOVE
	}
	procSetWindowPos.Call(
		hwnd,
		HWND_TOPMOST,
		uintptr(int32(miniX)),
		uintptr(int32(miniY)),
		uintptr(miniWindowWidth),
		uintptr(miniWindowHeight),
		flags,
	)
}

func getWorkAreaRect() winRect {
	var r winRect
	if ret, _, _ := procSystemParametersInfo.Call(SPI_GETWORKAREA, 0, uintptr(unsafe.Pointer(&r)), 0); ret != 0 && (r.Right-r.Left) > 200 && (r.Bottom-r.Top) > 150 {
		return r
	}
	return winRect{Left: 0, Top: 0, Right: 1920, Bottom: 1032}
}

// appPlacementPathParts splits Chromium's dotted app_window_placement key ("host_path")
// the same way Chromium's SetByDottedPath splits IP addresses such as "127.0.0.1_/".
func appPlacementPathParts(rawURL string) []string {
	host := "127.0.0.1"
	reqPath := "/"
	if u, err := url.Parse(rawURL); err == nil {
		if h := u.Hostname(); h != "" {
			host = h
		}
		if p := u.EscapedPath(); p != "" {
			reqPath = p
		}
	}
	return strings.Split(host+"_"+reqPath, ".")
}

// ensureMiniProfilePlacement seeds Edge's Default/Preferences before launch so a fresh or
// previously closed WebViewProfileMini window starts un-maximized at the exact mini-widget bounds.
func ensureMiniProfilePlacement(profileDir, appURL string, x, y, w, h int) error {
	defDir := filepath.Join(profileDir, "Default")
	if err := os.MkdirAll(defDir, 0755); err != nil {
		return err
	}
	prefsPath := filepath.Join(defDir, "Preferences")
	prefs := make(map[string]any)
	if raw, err := os.ReadFile(prefsPath); err == nil && len(raw) > 0 {
		var parsed map[string]any
		if json.Unmarshal(raw, &parsed) == nil && parsed != nil {
			prefs = parsed
		}
	}

	prefs["background_mode"] = map[string]any{
		"enabled": false,
	}

	profMap, _ := prefs["profile"].(map[string]any)
	if profMap == nil {
		profMap = make(map[string]any)
	}
	profMap["exit_type"] = "Normal"
	profMap["exited_cleanly"] = true
	prefs["profile"] = profMap

	browserMap, _ := prefs["browser"].(map[string]any)
	if browserMap == nil {
		browserMap = make(map[string]any)
	}
	delete(browserMap, "window_placement")

	appMap, _ := browserMap["app_window_placement"].(map[string]any)
	if appMap == nil {
		appMap = make(map[string]any)
	}

	parts := appPlacementPathParts(appURL)
	cur := appMap
	for i := 0; i < len(parts)-1; i++ {
		next, _ := cur[parts[i]].(map[string]any)
		if next == nil {
			next = make(map[string]any)
			cur[parts[i]] = next
		}
		cur = next
	}

	wa := getWorkAreaRect()
	cur[parts[len(parts)-1]] = map[string]any{
		"left":             x,
		"top":              y,
		"right":            x + w,
		"bottom":           y + h,
		"maximized":        false,
		"work_area_left":   int(wa.Left),
		"work_area_top":    int(wa.Top),
		"work_area_right":  int(wa.Right),
		"work_area_bottom": int(wa.Bottom),
	}
	browserMap["app_window_placement"] = appMap
	prefs["browser"] = browserMap

	encoded, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	return os.WriteFile(prefsPath, encoded, 0644)
}

func applyWindowBounds(hwnd uintptr, b storage.WindowBounds) {
	if b.Width >= 450 && b.Height >= 320 {
		var wp windowPlacement
		wp.Length = uint32(unsafe.Sizeof(wp))
		if b.Maximized {
			if got, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp))); got != 0 {
				wp.RcNormalPosition = winRect{
					Left:   int32(b.X),
					Top:    int32(b.Y),
					Right:  int32(b.X + b.Width),
					Bottom: int32(b.Y + b.Height),
				}
				wp.Flags |= WPF_RESTORETOMAXIMIZED
				wp.ShowCmd = SW_SHOWMAXIMIZED
				procSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
				return
			}
		}
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
		procShowWindow.Call(hwnd, SW_SHOWMAXIMIZED)
	}
}

func ensureTransitionsEnabled(hwnd uintptr) {
	var disable int32 // FALSE = allow DWM open/minimize/restore transitions
	procDwmSetWindowAttribute.Call(
		hwnd,
		DWMWA_TRANSITIONS_FORCEDISABLED,
		uintptr(unsafe.Pointer(&disable)),
		unsafe.Sizeof(disable),
	)
}

func (m *Manager) focusExistingWindow(hwnd uintptr, topmost bool, bounds storage.WindowBounds, inMini bool, miniHasPos bool, miniX int, miniY int) {
	vis, _, _ := procIsWindowVisible.Call(hwnd)
	iconic, _, _ := procIsIconic.Call(hwnd)
	effectiveTop := topmost || inMini

	// Already visible and un-minimized: bring to front cleanly without re-running show animations.
	if vis != 0 && iconic == 0 {
		if isWindowTopmost(hwnd) != effectiveTop {
			applyTopmost(hwnd, effectiveTop)
		}
		if inMini {
			var r winRect
			if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret != 0 {
				if (r.Right-r.Left) > 420 || (r.Bottom-r.Top) > 280 {
					m.applyMiniToWindow(hwnd)
				}
			}
		}
		procSetForeground.Call(hwnd)
		return
	}

	// Ensure DWM transitions are enabled and apply Z-order while still hidden so restore animates in a single step.
	ensureTransitionsEnabled(hwnd)
	if vis == 0 {
		applyTopmost(hwnd, effectiveTop)
	}

	var wp windowPlacement
	wp.Length = uint32(unsafe.Sizeof(wp))
	if got, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp))); got != 0 {
		if inMini {
			mx, my := miniX, miniY
			if !miniHasPos {
				rw := int(wp.RcNormalPosition.Right - wp.RcNormalPosition.Left)
				rh := int(wp.RcNormalPosition.Bottom - wp.RcNormalPosition.Top)
				if rw > 0 && rw <= 440 && rh > 0 && rh <= 280 && wp.RcNormalPosition.Left > -3000 && wp.RcNormalPosition.Top > -3000 {
					mx = int(wp.RcNormalPosition.Left)
					my = int(wp.RcNormalPosition.Top)
				} else {
					mx, my = 80, 80
				}
			}
			wp.RcNormalPosition = winRect{
				Left:   int32(mx),
				Top:    int32(my),
				Right:  int32(mx + miniWindowWidth),
				Bottom: int32(my + miniWindowHeight),
			}
			wp.Flags &^= WPF_RESTORETOMAXIMIZED
			wp.ShowCmd = SW_RESTORE
		} else {
			if bounds.HasValue && bounds.Width >= 450 && bounds.Height >= 320 {
				wp.RcNormalPosition = winRect{
					Left:   int32(bounds.X),
					Top:    int32(bounds.Y),
					Right:  int32(bounds.X + bounds.Width),
					Bottom: int32(bounds.Y + bounds.Height),
				}
			}
			if bounds.HasValue && bounds.Maximized {
				wp.Flags |= WPF_RESTORETOMAXIMIZED
				wp.ShowCmd = SW_SHOWMAXIMIZED
			} else {
				wp.Flags &^= WPF_RESTORETOMAXIMIZED
				if iconic != 0 {
					wp.ShowCmd = SW_RESTORE
				} else {
					wp.ShowCmd = SW_SHOWNORMAL
				}
			}
		}
		procSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	} else {
		procShowWindow.Call(hwnd, SW_RESTORE)
	}

	if isWindowTopmost(hwnd) != effectiveTop {
		applyTopmost(hwnd, effectiveTop)
	}
	procSetForeground.Call(hwnd)
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
		var r winRect
		retRect, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		iconic, _, _ := procIsIconic.Call(hwnd)
		if iconic == 0 && (retRect == 0 || (r.Right-r.Left) < 200 || (r.Bottom-r.Top) < 150) {
			// Skip helper/zero-size internal Chromium message windows
			return 1
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

