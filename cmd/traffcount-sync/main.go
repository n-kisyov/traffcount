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

	"traffcount/internal/service"
	"traffcount/internal/syncpoller"
	"traffcount/internal/syncserver"
	"traffcount/internal/syncstore"
)

const (
	appVersion  = "1.0.0"
	defaultAddr = "0.0.0.0:9880"
)

func main() {
	svcMgr := service.NewManager(service.SyncConfig())

	// 1. Check if started by Windows SCM or systemd background service
	if service.IsServiceProcess() {
		addr := envOrDefaultAddr()
		if err := svcMgr.RunService(func(ctx context.Context) error {
			return runSyncDaemon(ctx, addr, svcMgr, true)
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Service error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 2. CLI Subcommands
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "service":
			handleServiceCLI(svcMgr, args[1:])
			return
		case "hosts":
			handleHostsCLI(args[1:])
			return
		case "quit", "stop":
			handleQuitCLI(args[1:])
			return
		case "version", "--version", "-v":
			fmt.Printf("traffcount-sync %s\n", appVersion)
			return
		case "help", "--help", "-h":
			printUsage()
			return
		case "serve":
			args = args[1:]
		}
	}

	// 3. Standalone foreground Sync Server mode
	addr := parseServeAddr(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runSyncDaemon(ctx, addr, svcMgr, false); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func envOrDefaultAddr() string {
	if a := strings.TrimSpace(os.Getenv("TRAFFCOUNT_SYNC_ADDR")); a != "" {
		return a
	}
	return defaultAddr
}

func parseServeAddr(args []string) string {
	addr := envOrDefaultAddr()
	fs := flag.NewFlagSet("traffcount-sync", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var flagAddr string
	var flagPort int
	fs.StringVar(&flagAddr, "addr", "", "HTTP listen address (default 0.0.0.0:9880)")
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

func runSyncDaemon(ctx context.Context, addr string, svcMgr *service.Manager, isSvc bool) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	dbPath := syncstore.DefaultDBPath()
	store, err := syncstore.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open sync database (%s): %w", dbPath, err)
	}
	defer store.Close()

	poller := syncpoller.New(store)
	go poller.Run(runCtx)

	srv := syncserver.New(addr, store, poller, svcMgr)
	srv.SetReleaseHandler(cancel)

	if !isSvc {
		printStartupBanner(addr, dbPath)
	} else {
		fmt.Printf("TraffCount Sync Server service listening on http://%s (db: %s)\n", addr, dbPath)
	}

	return srv.Start(runCtx)
}

func handleServiceCLI(svcMgr *service.Manager, args []string) {
	if len(args) == 0 {
		printServiceUsage()
		os.Exit(2)
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	addr := parseServeAddr(args[1:])

	switch sub {
	case "run":
		if err := svcMgr.RunService(func(ctx context.Context) error {
			return runSyncDaemon(ctx, addr, svcMgr, true)
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Service error: %v\n", err)
			os.Exit(1)
		}
	case "install":
		if err := svcMgr.InstallAndStart(); err != nil {
			fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s installed and started successfully.\n", svcMgr.Config().ServiceName)
	case "uninstall", "remove":
		if err := svcMgr.StopAndUninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "Uninstall failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s stopped and uninstalled.\n", svcMgr.Config().ServiceName)
	case "start":
		if err := svcMgr.StartService(); err != nil {
			fmt.Fprintf(os.Stderr, "Start failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s started.\n", svcMgr.Config().ServiceName)
	case "stop":
		if err := svcMgr.StopService(); err != nil {
			fmt.Fprintf(os.Stderr, "Stop failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s stopped.\n", svcMgr.Config().ServiceName)
	case "restart":
		_ = svcMgr.StopService()
		time.Sleep(500 * time.Millisecond)
		if err := svcMgr.StartService(); err != nil {
			fmt.Fprintf(os.Stderr, "Restart failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s restarted.\n", svcMgr.Config().ServiceName)
	case "status":
		st := svcMgr.QueryStatus()
		fmt.Printf("Service Name     : %s\n", svcMgr.Config().ServiceName)
		fmt.Printf("Installed        : %v\n", st.Installed)
		fmt.Printf("Running          : %v\n", st.Running)
		fmt.Printf("State            : %s\n", st.StateText)
		fmt.Printf("Admin Privileges : %v\n", st.IsAdmin)
		if isServerAlive(addr) {
			fmt.Printf("Web UI           : http://%s\n", localLoopbackAddr(addr))
		}
	default:
		printServiceUsage()
		os.Exit(2)
	}
}

func handleHostsCLI(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: traffcount-sync hosts [list | add <name> <url> [token] | remove <id>]")
		return
	}
	store, err := syncstore.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Open sync db failed: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	switch strings.ToLower(args[0]) {
	case "list":
		hosts, err := store.ListHosts()
		if err != nil {
			fmt.Fprintf(os.Stderr, "List hosts failed: %v\n", err)
			os.Exit(1)
		}
		if len(hosts) == 0 {
			fmt.Println("No remote hosts registered.")
			return
		}
		for _, h := range hosts {
			fmt.Printf("%-14s %-20s %-30s %-10s %s\n", h.ID, h.Name, h.BaseURL, h.Status, h.Platform)
		}
	case "add":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: traffcount-sync hosts add <name> <url> [token]")
			os.Exit(2)
		}
		tok := ""
		if len(args) >= 4 {
			tok = args[3]
		}
		h, err := store.CreateHost(syncstore.HostInput{
			Name:     args[1],
			BaseURL:  args[2],
			APIToken: tok,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Add host failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Added host %s (%s) -> %s\n", h.Name, h.ID, h.BaseURL)
	case "remove", "delete":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: traffcount-sync hosts remove <id>")
			os.Exit(2)
		}
		if err := store.DeleteHost(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Remove host failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Removed host %s.\n", args[1])
	default:
		fmt.Println("Usage: traffcount-sync hosts [list | add <name> <url> [token] | remove <id>]")
	}
}

func handleQuitCLI(args []string) {
	addr := parseServeAddr(args)
	u := "http://" + localLoopbackAddr(addr) + "/api/server/release"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(u, "application/json", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No running standalone TraffCount Sync Server at %s (%v)\n", addr, err)
		os.Exit(1)
	}
	_ = resp.Body.Close()
	fmt.Println("Shutdown signal sent to TraffCount Sync Server.")
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
		return "127.0.0.1:9880"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func printStartupBanner(addr, dbPath string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = "9880"
		host = "0.0.0.0"
	}
	fmt.Printf("TraffCount Sync Server v%s\n", appVersion)
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
	fmt.Println(`TraffCount Sync Server — Multi-Host Telemetry Aggregator & Web UI

Usage:
  traffcount-sync [serve] [--addr 0.0.0.0:9880 | --port 9880]
  traffcount-sync service <install|uninstall|start|stop|restart|status|run>
  traffcount-sync hosts <list | add <name> <url> [token] | remove <id>>
  traffcount-sync quit [--addr 127.0.0.1:9880]
  traffcount-sync version

Environment variables:
  TRAFFCOUNT_SYNC_ADDR  Default HTTP listen address (default 0.0.0.0:9880)
  TRAFFCOUNT_SYNC_DB    Custom SQLite database file path`)
}

func printServiceUsage() {
	fmt.Println(`Usage:
  traffcount-sync service install     Install and start background service
  traffcount-sync service uninstall   Stop and remove background service
  traffcount-sync service start       Start background service
  traffcount-sync service stop        Stop background service
  traffcount-sync service restart     Restart background service
  traffcount-sync service status      Show service status`)
}
