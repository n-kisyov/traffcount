# Build script for TraffCount on Windows 11 (GUI subsystem + Windows Service support)
$ErrorActionPreference = "Stop"

Write-Host "Building traffcount.exe (Windows GUI subsystem + Windows Service)..." -ForegroundColor Cyan

go build -ldflags="-s -w -H=windowsgui" -o traffcount.exe ./cmd/traffcount

Write-Host "Build complete: $(Resolve-Path .\traffcount.exe)" -ForegroundColor Green
