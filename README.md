# TraffCount — Windows 11 Network Traffic Monitor & Counter

`traffcount` is a lightweight, CGo-free Go application for Windows 11 that monitors real-time network bandwidth (with a 60-second Task Manager-style graph) and records historical inbound/outbound traffic per NIC in SQLite.

## Key Features

- **Real-Time Bandwidth Graph (Task Manager Style)**:
  - 1Hz live polling across all network interfaces (`GetAdaptersAddresses` + `gopsutil`).
  - Smooth 60-second scrolling throughput chart with automatic scale ceiling (`Kbps`, `Mbps`, `Gbps` or `KB/s`, `MB/s`, `GB/s`).
  - Displays link speed, IPv4 address, MAC address, packet rate, and live per-adapter status.
- **Historical Traffic Counter**:
  - Persists hourly traffic buckets per network interface in SQLite (`%ProgramData%\TraffCount\traffcount.db` or `%LocalAppData%\TraffCount\traffcount.db`).
  - Handles reboot and adapter counter resets gracefully.
  - Preset ranges (**Today**, **Last 24h**, **Past 7 Days**, **Past 14 Days**, **Past 30 Days**, **Past 90 Days**, or **Custom Date Range**) for **All NICs (Total)** or any individual **NIC**.
- **Modern GUI with Dark & Light Themes + "Stay on Top"**:
  - Defaults to Windows 11 **Dark Mode**, with instant switching to **Light Mode** in Settings or via the header quick-toggle.
  - Includes a native **Stay on Top (Always on Top)** pin button in both the GUI window and System Tray menu (backed by Win32 `SetWindowPos(HWND_TOPMOST)`).
- **System Tray Icon & Windows Service**:
  - Runs in the Windows 11 System Tray with a live hover tooltip showing current Download/Upload speeds.
  - Can be installed as an automatic Windows Service (`TraffCountService`) via the GUI Settings tab, System Tray menu, or CLI.

---

## Building

```powershell
# Builds traffcount.exe using the Windows GUI subsystem (-H=windowsgui)
# while retaining all Windows Service & CLI capabilities
.\build.ps1
```

---

## Usage

### Run Desktop App (System Tray + Dashboard Window)
```powershell
.\traffcount.exe
```
- Starts the background traffic collector (if `TraffCountService` is not already running), places the `TraffCount` icon in the Windows 11 System Tray, and opens the dashboard window.
- Pass `--tray-only` to start minimized in the system tray without opening the window immediately.

### Manage the Windows Service
From PowerShell (automatically prompts for UAC Administrator elevation if needed):
```powershell
.\traffcount.exe service status
.\traffcount.exe service install
.\traffcount.exe service start
.\traffcount.exe service stop
.\traffcount.exe service uninstall
```
