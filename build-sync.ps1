# Build script for TraffCount Sync Server on Windows 11 (CLI + Windows Service support)
$ErrorActionPreference = "Stop"

# 1. Gracefully ask any running standalone traffcount-sync instance to release port 9880
if (Test-Path .\traffcount-sync.exe) {
    Start-Process -FilePath .\traffcount-sync.exe -ArgumentList "quit" -WindowStyle Hidden -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 350
}

# 2. If TraffCountSyncService is currently running, stop it before overwriting traffcount-sync.exe
$svc = Get-Service -Name "TraffCountSyncService" -ErrorAction SilentlyContinue
$wasServiceRunning = ($null -ne $svc -and $svc.Status -eq "Running")
if ($wasServiceRunning) {
    Write-Host "Stopping TraffCountSyncService before build..." -ForegroundColor Yellow
    try {
        Invoke-RestMethod -Uri "http://127.0.0.1:9880/api/service" -Method Post -ContentType "application/json" -Body '{"action":"stop"}' -TimeoutSec 3 | Out-Null
    } catch {}
    for ($i = 0; $i -lt 15; $i++) {
        $s = Get-Service -Name "TraffCountSyncService" -ErrorAction SilentlyContinue
        if ($null -eq $s -or $s.Status -eq "Stopped") { break }
        Start-Sleep -Milliseconds 400
    }
}

Stop-Process -Name traffcount-sync -Force -ErrorAction SilentlyContinue

Write-Host "Building traffcount-sync.exe (Windows 11 Multi-Host Sync Server + Service)..." -ForegroundColor Cyan
go build -trimpath -ldflags="-s -w" -o traffcount-sync.exe ./cmd/traffcount-sync

Write-Host "Build complete: $(Resolve-Path .\traffcount-sync.exe)" -ForegroundColor Green
Write-Host "Run foreground server : .\traffcount-sync.exe" -ForegroundColor DarkGray
Write-Host "Install as service    : .\traffcount-sync.exe service install" -ForegroundColor DarkGray
