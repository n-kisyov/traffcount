//go:build !windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"traffcount/internal/collector"
	"traffcount/internal/server"
	"traffcount/internal/service"
	"traffcount/internal/storage"
	"traffcount/internal/tray"
	"traffcount/internal/winui"
)

const (
	appVersion  = "1.1.0"
	defaultAddr = "0.0.0.0:9876"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "service":
			handleServiceCLI(args[1:])
			return
		case "quit", "stop":
			handleQuitCLI(args[1:])
			return
		case "version", "--version", "-v":
			fmt.Printf("traffcount %s\n", appVersion)
			return
		case "help", "--help", "-h":
			printUsage()
			return
		case "serve":
			args = args[1:]
		}
	}

	addr := parseServeAddr(args)
	if err := runStandalone(addr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func parseServeAddr(args []string) string {
	addr := strings.TrimSpace(os.Getenv("TRAFFCOUNT_ADDR"))
	if addr == "" {
		addr = defaultAddr
	}
	fs := flag.NewFlagSet("traffcount", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var flagAddr string
	var flagPort int
	fs.StringVar(&flagAddr, "addr", "", "HTTP listen address (default 0.0.0.0:9876)")
	fs.IntVar(&flagPort, "port", 0, "HTTP listen port override")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage()
			os.Exit(0)
		}
		os.Exit(2)
	}
	if strings.TrimSpace(flagAddr) != "" {
		addr = strings.TrimSpace(flagAddr)
	} else if flagPort > 0 && flagPort <= 65535 {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || host == "" {
			host = "0.0.0.0"
		}
		addr = net.JoinHostPort(host, fmt.Sprintf("%d", flagPort))
	}
	return addr
}

func runStandalone(addr string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return runServerAndCollector(ctx, cancel, addr, false)
}

func runServerAndCollector(ctx context.Context, cancel context.CancelFunc, addr string, isSvc bool) error {
	dbPath := storage.DefaultDBPath()
	store, err := storage.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database (%s): %w", dbPath, err)
	}
	defer store.Close()

	// Sync persistent app blocklist into collector cache before starting probes.
	if blocked, err := store.GetBlockedApps(); err == nil {
		collector.SetBlockedAppsCache(blocked)
	}

	col := collector.New(store)
	go col.Run(ctx)
	defer col.FlushToDB()

	winMgr := winui.NewManager("http://"+localLoopbackAddr(addr), false)
	srv := server.New(addr, store, col, winMgr, nil)

	tray.RegisterReleaseHandler(func() {
		cancel()
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	if !isSvc {
		printStartupBanner(addr, dbPath)
	} else {
		fmt.Printf("TraffCount systemd service listening on http://%s (db: %s)\n", addr, dbPath)
	}

	err = <-errCh
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func handleServiceCLI(args []string) {
	if len(args) == 0 {
		printServiceUsage()
		os.Exit(2)
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	addr := parseServeAddr(args[1:])

	switch sub {
	case "run":
		service.SetServiceProcessMode(true)
		err := service.RunService(func(ctx context.Context) error {
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			return runServerAndCollector(runCtx, cancel, addr, true)
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Service exited with error: %v\n", err)
			os.Exit(1)
		}
	case "install":
		requireRoot("install the systemd service")
		if err := service.InstallAndStart(); err != nil {
			fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("traffcount.service installed and started successfully.")
	case "uninstall", "remove":
		requireRoot("uninstall the systemd service")
		if err := service.StopAndUninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "Uninstall failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("traffcount.service stopped and removed.")
	case "start":
		requireRoot("start the systemd service")
		if err := service.StartService(); err != nil {
			fmt.Fprintf(os.Stderr, "Start failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("traffcount.service started.")
	case "stop":
		requireRoot("stop the systemd service")
		if err := service.StopService(); err != nil {
			fmt.Fprintf(os.Stderr, "Stop failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("traffcount.service stopped.")
	case "restart":
		requireRoot("restart the systemd service")
		_ = service.StopService()
		if err := service.StartService(); err != nil {
			fmt.Fprintf(os.Stderr, "Restart failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("traffcount.service restarted.")
	case "status":
		st := service.QueryStatus()
		fmt.Printf("Service:    traffcount.service\n")
		fmt.Printf("Installed:  %v\n", st.Installed)
		fmt.Printf("Running:    %v\n", st.Running)
		fmt.Printf("State:      %s\n", st.StateText)
		if isServerAlive(addr) {
			fmt.Printf("Web UI:     http://%s\n", localLoopbackAddr(addr))
		}
	default:
		printServiceUsage()
		os.Exit(2)
	}
}

func handleQuitCLI(args []string) {
	addr := parseServeAddr(args)
	url := "http://" + localLoopbackAddr(addr) + "/api/server/release"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No running standalone TraffCount instance found at %s (%v)\n", addr, err)
		os.Exit(1)
	}
	_ = resp.Body.Close()
	fmt.Println("Shutdown signal sent to TraffCount.")
}

func requireRoot(action string) {
	if !service.IsAdmin() {
		fmt.Fprintf(os.Stderr, "Root privileges required to %s. Re-run with sudo.\n", action)
		os.Exit(1)
	}
}

func isServerAlive(addr string) bool {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get("http://" + localLoopbackAddr(addr) + "/api/ping")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func localLoopbackAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1:9876"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func printStartupBanner(addr, dbPath string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = "9876"
		host = "0.0.0.0"
	}
	fmt.Printf("TraffCount v%s (Web Server Mode)\n", appVersion)
	fmt.Printf("Database: %s\n", dbPath)
	fmt.Printf("Local UI: http://%s\n", net.JoinHostPort("127.0.0.1", port))
	if host == "" || host == "0.0.0.0" || host == "::" {
		for _, ip := range localLANIPv4s() {
			fmt.Printf("LAN UI:   http://%s\n", net.JoinHostPort(ip, port))
		}
	} else {
		fmt.Printf("Listen:   http://%s\n", addr)
	}
	fmt.Println("Press Ctrl+C to stop.")
}

func localLANIPv4s() []string {
	var ips []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP != nil {
				if ip4 := ipNet.IP.To4(); ip4 != nil && !ip4.IsLoopback() {
					ips = append(ips, ip4.String())
				}
			}
		}
	}
	return ips
}

func printUsage() {
	fmt.Println(`TraffCount — Network Bandwidth & Connections Monitor (CLI + Web UI)

Usage:
  traffcount [serve] [--addr 0.0.0.0:9876 | --port 9876]
  traffcount service <install|uninstall|start|stop|restart|status|run>
  traffcount quit [--addr 127.0.0.1:9876]
  traffcount version

Environment variables:
  TRAFFCOUNT_ADDR   Default HTTP listen address (default 0.0.0.0:9876)
  TRAFFCOUNT_DB     Custom SQLite database file path`)
}

func printServiceUsage() {
	fmt.Println(`Usage:
  sudo traffcount service install     Install and enable systemd unit (traffcount.service)
  sudo traffcount service uninstall   Stop, disable, and remove systemd unit
  sudo traffcount service start       Start systemd unit
  sudo traffcount service stop        Stop systemd unit
  sudo traffcount service restart     Restart systemd unit
  traffcount service status           Show systemd service status
  traffcount service run              Run foreground daemon (invoked by systemd)`)
}
