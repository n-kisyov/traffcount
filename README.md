# TraffCount

Real-time network bandwidth monitor and historical traffic counter for **Windows 11** and **Raspberry Pi OS / Linux**, written in Go.

## Screenshots

### 1. Real-Time Bandwidth Monitor
Live per-second upload and download throughput chart, adapter selection, real-time connectivity pings, and daemon data collection status.

![Real-Time Bandwidth Monitor](docs/screenshots/01-realtime-monitor.png)

### 2. Historical Traffic Counter & Analytics
Long-term historical bandwidth accounting stored in SQLite across reboots, with timeframe filters (Today, 24h, 7d, 14d, 30d, 90d, custom), daily volume bar charts, interface distribution, and CSV/JSON export.

![Historical Traffic Counter](docs/screenshots/02-traffic-history.png)

### 3. Live Network Connections, Process Monitor & Firewall Control
Active TCP/UDP sockets and process bandwidth table displaying PIDs, local and remote endpoints, TCP RTT, Geo/Host lookups, socket close / process termination actions, and persistent per-app firewall blocking.

![Live Network Connections](docs/screenshots/03-active-connections.png)

### 4. Speed Test & Bufferbloat Diagnostics
Built-in latency, jitter, download/upload throughput benchmark, Bufferbloat grade evaluation via Cloudflare Global Anycast, and historical speed test log.

![Speed & Bufferbloat Diagnostics](docs/screenshots/04-speed-test.png)

### 5. Settings, Service Management & Remote Sync Polling
Color themes (Win11 Dark, Antigravity IDE, Nord, Dracula, Emerald, Light), Windows/systemd service controls, opt-in LAN Sync Polling & API token configuration, monthly billing-cycle data cap, and SQLite backup/retention settings.

![Settings & Windows Service Management](docs/screenshots/05-settings-service.png)

> **Tip:** TraffCount also includes a compact **Mini Widget** floating overlay mode (`380x230`) that stays pinned on top of your desktop for quick glanceable throughput monitoring:
>
> ![Mini Widget Mode](docs/screenshots/06-mini-widget.png)

### 6. TraffCount Sync Server — Multi-Host Fleet Overview
Central multi-host web dashboard (`traffcount-sync.exe` on port `9880`) aggregating 1 Hz real-time bandwidth across multiple Windows 11 and Raspberry Pi hosts.

![TraffCount Sync Fleet Overview](docs/screenshots/07-sync-fleet-overview.png)

### 7. TraffCount Sync Server — Host Explorer
Per-host drill-down showing live throughput, gateway/DNS ping probes, monthly data cap quota progress, proxied live connections, and remote speed test triggers.

![TraffCount Sync Host Explorer](docs/screenshots/08-sync-host-explorer.png)

### 8. TraffCount Sync Server — Fleet History
Synced hourly/daily multi-host traffic volume charts, per-host traffic share breakdown, top applications across hosts, and CSV/JSON fleet export.

![TraffCount Sync Fleet History](docs/screenshots/09-sync-fleet-history.png)

## Features

- **24/7 Background Counting:** Runs as a native Windows Service (`TraffCountService`) in Session 0 so traffic is counted continuously even before user logon or when the app window is closed.
- **Seamless Desktop Fallback:** If the Windows Service is stopped, the desktop tray application seamlessly takes over in-process data collection with zero interruption.
- **Per-Process Traffic Tracking:** Uses Windows `GetPerTcpConnectionEStats` / TCP Extended Statistics to accurately attribute network volume to individual executables.
- **System Tray Integration:** Native Windows 11 tray icon with real-time speed tooltips, quick actions, "Stay on Top", and smooth DWM minimize/restore animations.
- **Standalone App Window:** Frameless standalone Edge `--app` window with dedicated profile isolation, persistent window bounds, and themes.

## Build

```powershell
.\build.ps1
```

## CLI Usage

```powershell
# Start TraffCount (System Tray icon + GUI dashboard window)
.\traffcount.exe

# Start minimized in the System Tray only
.\traffcount.exe --tray-only

# Close a running TraffCount tray instance
.\traffcount.exe quit
```

### Windows Service Commands

```powershell
# Check Windows Service status
.\traffcount.exe service status

# Install and start TraffCountService (prompts for Admin UAC)
.\traffcount.exe service install

# Start or stop an installed service
.\traffcount.exe service start
.\traffcount.exe service stop

# Stop and remove TraffCountService
.\traffcount.exe service uninstall
```

## Raspberry Pi OS / Linux

TraffCount runs on **Raspberry Pi 3B+**, **Raspberry Pi 4**, and newer models (both 64-bit `arm64` and 32-bit `armv7` Raspberry Pi OS) as a pure-Go CLI daemon + standalone HTTP/WebSocket web dashboard (`http://<pi-ip>:9876`).

### Build on Raspberry Pi (or cross-compile on Linux)

`build-pi.sh` checks whether Go is installed and meets the version in `go.mod`, offers to install/upgrade Go automatically into `/usr/local/go`, and compiles a static binary (`CGO_ENABLED=0`):

```bash
chmod +x build-pi.sh

# Auto-detect Raspberry Pi architecture (arm64 or armv7), check/install Go, and build ./traffcount
./build-pi.sh

# Or explicitly target 64-bit / 32-bit / both
./build-pi.sh --arch arm64
./build-pi.sh --arch armv7
./build-pi.sh --arch all

# Unattended build with automatic Go installation
./build-pi.sh --install-go -y
```

### CLI & systemd Service Usage (Raspberry Pi)

```bash
# Run standalone CLI + web server on 0.0.0.0:9876 (accessible from your LAN)
./traffcount

# Or specify a custom listen address / port
./traffcount --addr 0.0.0.0:8080

# Install system-wide and run 24/7 as a systemd background service
sudo install -m 0755 ./traffcount /usr/local/bin/traffcount
sudo traffcount service install

# Manage or inspect the systemd service
traffcount service status
sudo traffcount service start
sudo traffcount service stop
sudo traffcount service restart
sudo traffcount service uninstall

# Stop a running standalone CLI instance gracefully
traffcount quit
```

## TraffCount Sync Server (Multi-Host Central Aggregator)

`traffcount-sync` is a standalone central server + web dashboard that polls multiple Windows 11 and Raspberry Pi TraffCount hosts on your LAN, reconciles hourly SQLite metrics losslessly, and visualizes fleet-wide and per-host traffic (`http://<sync-host>:9880`).

### Build & Run on Windows 11

```powershell
.\build-sync.ps1
.\traffcount-sync.exe                  # Foreground server on http://0.0.0.0:9880
.\traffcount-sync.exe service install  # Install & start Windows Service (TraffCountSyncService)
.\traffcount-sync.exe service status   # Check service state
.\traffcount-sync.exe hosts list       # List registered remote hosts
```

### Build & Run on Raspberry Pi OS / Linux (`traffcount-sync`)

```bash
chmod +x build-sync-pi.sh
./build-sync-pi.sh                        # Auto-detects Pi arch (arm64/armv7), checks/installs Go, builds ./traffcount-sync
sudo install -m 0755 ./traffcount-sync /usr/local/bin/traffcount-sync
sudo traffcount-sync service install      # Run Sync Server 24/7 as traffcount-sync.service
```

### Polling Windows 11 Agents Remotely

Raspberry Pi agents listen on `0.0.0.0:9876` by default. On Windows 11 agents (`traffcount.exe`), open **Settings & Service**, enable **Allow Remote Sync Server Polling (LAN)** (which opens TCP `9876` on Windows Firewall when Service/Admin and optionally enforces a Bearer **Sync API Token**), or click **Scan LAN** inside Sync Server's **Add Remote Host** dialog to auto-discover hosts on your network.

In **Host Explorer**, you can inspect remote host Monthly Data Cap quotas, view active sockets/processes, remotely close sockets, terminate processes, toggle per-app firewall blocks, or trigger on-demand / scheduled automatic speed & Bufferbloat tests.

### Capturing Screenshots

To regenerate all documentation screenshots (`docs/screenshots/01-*.png` through `09-*.png`) using headless Edge/Chrome via CDP (connects to running local instances if active, or automatically spawns isolated temporary instances of `traffcount.exe` and `traffcount-sync.exe` with sample multi-host telemetry):

```powershell
node scripts/capture_all.js
```
