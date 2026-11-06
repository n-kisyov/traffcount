//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"traffcount/internal/collector"
	"traffcount/internal/server"
	"traffcount/internal/service"
	"traffcount/internal/storage"
	"traffcount/internal/tray"
	"traffcount/internal/winui"
)

const defaultAddr = "127.0.0.1:9876"

func main() {
	// 1. Check if launched by Windows Service Control Manager (Session 0)
	if service.IsWindowsServiceProcess() {
		_ = service.RunService(func(ctx context.Context) error {
			return runDaemon(ctx, defaultAddr, nil, nil)
		})
		return
	}

	// 2. Check CLI subcommands (`traffcount service install|uninstall|start|stop|status|run`)
	if len(os.Args) > 1 && os.Args[1] == "service" {
		handleServiceCLI(os.Args[2:])
		return
	}

	// 3. Parse standard flags for interactive / tray execution
	addr := flag.String("addr", defaultAddr, "Local HTTP bind address")
	trayOnly := flag.Bool("tray-only", false, "Start in system tray without opening the dashboard window immediately")
	headless := flag.Bool("headless", false, "Run collector and HTTP server without system tray or GUI")
	flag.Parse()

	dashboardURL := "http://" + *addr

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *headless {
		fmt.Printf("TraffCount running in headless mode on %s\n", dashboardURL)
		if err := runDaemon(ctx, *addr, nil, nil); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Enforce single interactive tray/GUI instance (separate from Windows Service in Session 0).
	mutexHandle, alreadyExists := acquireSingleInstanceMutex()
	if alreadyExists {
		// Another desktop instance is already running: ask it to open/focus the dashboard window and exit immediately.
		if !*trayOnly {
			if !tray.SignalExistingTrayInstance() {
				winui.NewManager(dashboardURL, false).OpenDashboard()
			}
		}
		return
	}
	defer windows.CloseHandle(mutexHandle)

	// Check if the TraffCount Windows Service is already listening on dashboardURL
	alreadyRunning := isServerAlive(dashboardURL)

	// Load initial settings to know AlwaysOnTop state
	var initialTopmost bool
	if st, err := storage.Open(""); err == nil {
		if cfg, err := st.GetSettings(); err == nil {
			initialTopmost = cfg.AlwaysOnTop
		}
		st.Close()
	}

	winMgr := winui.NewManager(dashboardURL, initialTopmost)
	var trayCtrl *tray.Controller

	trayCtrl = tray.NewController(tray.Options{
		DashboardURL: dashboardURL,
		WinMgr:       winMgr,
		AutoOpenUI:   !*trayOnly,
		OnExit: func() {
			stop()
		},
	})

	// If the background service is not already serving the API, run the collector & HTTP server in-process!
	if !alreadyRunning {
		go func() {
			_ = runDaemon(ctx, *addr, winMgr, func(topmost bool) {
				if trayCtrl != nil {
					trayCtrl.SyncStayOnTop(topmost)
				}
			})
		}()
		// Wait briefly for in-process HTTP server to come up before opening UI
		waitForServer(dashboardURL, 2*time.Second)
	}

	// Run System Tray loop on the main thread
	trayCtrl.Run(ctx)
}

func runDaemon(ctx context.Context, addr string, winMgr *winui.Manager, onTopChanged func(bool)) error {
	store, err := storage.Open("")
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer store.Close()

	if winMgr == nil {
		cfg, _ := store.GetSettings()
		winMgr = winui.NewManager("http://"+addr, cfg.AlwaysOnTop)
	}

	coll := collector.New(store)
	go coll.Run(ctx)

	srv := server.New(addr, store, coll, winMgr, onTopChanged)
	return srv.Start(ctx)
}

func handleServiceCLI(args []string) {
	// If compiled with -H=windowsgui and invoked interactively from a parent terminal
	// (such as PowerShell or cmd.exe), attach to the parent console so status/errors print cleanly.
	attachParentConsole()

	action := "status"
	if len(args) > 0 {
		action = args[0]
	}

	switch action {
	case "run":
		_ = service.RunService(func(ctx context.Context) error {
			return runDaemon(ctx, defaultAddr, nil, nil)
		})
	case "install":
		if err := service.InstallAndStart(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to install service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("TraffCountService installed and started successfully.")
	case "uninstall", "remove":
		if err := service.StopAndUninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to uninstall service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("TraffCountService stopped and uninstalled.")
	case "start":
		if err := service.StartService(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("TraffCountService started.")
	case "stop":
		if err := service.StopService(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to stop service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("TraffCountService stopped.")
	case "status":
		st := service.QueryStatus()
		fmt.Printf("Service Installed : %v\n", st.Installed)
		fmt.Printf("Service Running   : %v\n", st.Running)
		fmt.Printf("Service State     : %s\n", st.StateText)
		fmt.Printf("Admin Privileges  : %v\n", st.IsAdmin)
	default:
		fmt.Printf("Usage: traffcount service [install|uninstall|start|stop|status]\n")
	}
}

func isServerAlive(baseURL string) bool {
	client := &http.Client{Timeout: 400 * time.Millisecond}
	resp, err := client.Get(baseURL + "/api/ping")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func waitForServer(baseURL string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isServerAlive(baseURL) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func attachParentConsole() {
	const ATTACH_PARENT_PROCESS = ^uint32(0) // (DWORD)-1
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole := kernel32.NewProc("AttachConsole")
	r, _, _ := procAttachConsole.Call(uintptr(ATTACH_PARENT_PROCESS))
	if r != 0 {
		if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stdout = out
			os.Stderr = out
		}
	}
}

func acquireSingleInstanceMutex() (windows.Handle, bool) {
	namePtr, _ := syscall.UTF16PtrFromString("Local\\TraffCount_Desktop_SingleInstance_Mutex")
	h, err := windows.CreateMutex(nil, false, namePtr)
	if err == windows.ERROR_ALREADY_EXISTS {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return 0, true
	}
	return h, false
}



