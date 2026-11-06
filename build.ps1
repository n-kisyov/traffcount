# Build script for TraffCount on Windows 11 (GUI subsystem + Windows Service support)
$ErrorActionPreference = "Stop"

# 1. Check if TraffCountService is currently running; if so, stop it before overwriting traffcount.exe
$svc = Get-Service -Name "TraffCountService" -ErrorAction SilentlyContinue
$wasServiceRunning = ($null -ne $svc -and $svc.Status -eq "Running")
if ($wasServiceRunning) {
    Write-Host "Stopping TraffCountService before build..." -ForegroundColor Yellow
    .\traffcount.exe service stop
    Start-Sleep -Seconds 1
}

# 2. Gracefully ask any running TraffCount tray instance to exit before overwriting traffcount.exe
if (Test-Path .\traffcount.exe) {
    Start-Process -FilePath .\traffcount.exe -ArgumentList "quit" -WindowStyle Hidden -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 300
}
Stop-Process -Name traffcount -Force -ErrorAction SilentlyContinue

Write-Host "Building traffcount.exe (Windows GUI subsystem + Windows Service)..." -ForegroundColor Cyan

go build -ldflags="-s -w -H=windowsgui" -o traffcount.exe ./cmd/traffcount

Write-Host "Build complete: $(Resolve-Path .\traffcount.exe)" -ForegroundColor Green

# 3. If TraffCountService was running before the build, start it back up automatically
if ($wasServiceRunning) {
    Write-Host "Restarting TraffCountService..." -ForegroundColor Cyan
    .\traffcount.exe service start
}
