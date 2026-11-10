# Build script for TraffCount on Windows 11 (GUI subsystem + Windows Service support)
$ErrorActionPreference = "Stop"

# 1. Gracefully ask any running TraffCount tray instance to exit before overwriting traffcount.exe
if (Test-Path .\traffcount.exe) {
    Start-Process -FilePath .\traffcount.exe -ArgumentList "quit" -WindowStyle Hidden -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 400
}

# 2. Check if TraffCountService is currently running; if so, ask it to stop via its local API (which runs as SYSTEM)
$svc = Get-Service -Name "TraffCountService" -ErrorAction SilentlyContinue
$wasServiceRunning = ($null -ne $svc -and $svc.Status -eq "Running")
if ($wasServiceRunning) {
    Write-Host "Stopping TraffCountService before build..." -ForegroundColor Yellow
    try {
        Invoke-RestMethod -Uri "http://127.0.0.1:9876/api/service" -Method Post -ContentType "application/json" -Body '{"action":"stop"}' -TimeoutSec 3 | Out-Null
    } catch {}
    for ($i = 0; $i -lt 12; $i++) {
        $s = Get-Service -Name "TraffCountService" -ErrorAction SilentlyContinue
        if ($null -eq $s -or $s.Status -eq "Stopped") { break }
        Start-Sleep -Milliseconds 400
    }
}

Stop-Process -Name traffcount -Force -ErrorAction SilentlyContinue

Write-Host "Generating app icon and PE resource (.ico + .syso)..." -ForegroundColor Cyan
go run ./cmd/genicon

Write-Host "Building traffcount.exe (Windows GUI subsystem + Windows Service)..." -ForegroundColor Cyan

go build -ldflags="-s -w -H=windowsgui" -o traffcount.exe ./cmd/traffcount

Write-Host "Build complete: $(Resolve-Path .\traffcount.exe)" -ForegroundColor Green
