#!/usr/bin/env bash
set -euo pipefail

# TraffCount build script for Raspberry Pi OS (Pi 3B+, Pi 4, Pi 400, Pi 5, Zero 2 W)
# Supports native Raspberry Pi builds and Linux x86_64 cross-compilation (CGO_ENABLED=0).

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

TARGET_ARCH="auto"
OUTPUT_PATH=""
AUTO_YES=false
FORCE_INSTALL_GO=false
SKIP_GO_CHECK=false

usage() {
  cat <<'EOF'
Usage: ./build-pi.sh [options]

Options:
  --arch <auto|arm64|armv7|all>
                        Target architecture (default: auto).
                          arm64 : 64-bit Raspberry Pi OS (Pi 3B+, Pi 4, Pi 5, Zero 2 W)
                          armv7 : 32-bit Raspberry Pi OS armhf / GOARM=7 (Pi 3B+, Pi 4+)
                          all   : Build both arm64 and armv7 binaries into ./dist/
  -o, --output <path>   Custom output binary path (single-arch builds only)
  --install-go          Install or upgrade Go into /usr/local/go without prompting
  -y, --yes             Assume yes for interactive prompts (including Go install)
  --skip-go-check       Skip Go version check against go.mod
  -h, --help            Show this help message

Examples:
  ./build-pi.sh                        # Auto-detect Pi OS arch, check/install Go, build ./traffcount
  ./build-pi.sh --arch arm64           # Build 64-bit binary (Pi 3B+ / Pi 4 / Pi 5)
  ./build-pi.sh --arch armv7           # Build 32-bit ARMv7 binary (Pi 3B+ / Pi 4 32-bit)
  ./build-pi.sh --arch all             # Cross-compile both arm64 and armv7 into ./dist/
  ./build-pi.sh --install-go -y        # Unattended Go setup + build
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --arch)
      TARGET_ARCH="${2:-}"
      shift 2
      ;;
    -o|--output)
      OUTPUT_PATH="${2:-}"
      shift 2
      ;;
    --install-go)
      FORCE_INSTALL_GO=true
      AUTO_YES=true
      shift
      ;;
    -y|--yes)
      AUTO_YES=true
      shift
      ;;
    --skip-go-check)
      SKIP_GO_CHECK=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage
      exit 2
      ;;
  esac
done

log() { printf "\033[1;34m[build-pi]\033[0m %s\n" "$*"; }
warn() { printf "\033[1;33m[warn]\033[0m %s\n" "$*" >&2; }
err() { printf "\033[1;31m[error]\033[0m %s\n" "$*" >&2; exit 1; }

# Include /usr/local/go/bin in case Go was previously installed system-wide
if [[ -d /usr/local/go/bin && ":$PATH:" != *":/usr/local/go/bin:"* ]]; then
  export PATH="/usr/local/go/bin:$PATH"
fi

# Extract required Go version from go.mod (fallback to 1.24 if absent)
REQUIRED_GO="1.24"
if [[ -f go.mod ]]; then
  parsed_req="$(awk '/^go[[:space:]]+[0-9]+\.[0-9]+/ {print $2; exit}' go.mod || true)"
  if [[ -n "$parsed_req" ]]; then
    REQUIRED_GO="$parsed_req"
  fi
fi

version_gte() {
  # Returns 0 if $1 >= $2 (numeric dot-separated comparison)
  local a="$1" b="$2"
  [[ "$(printf '%s\n%s\n' "$b" "$a" | sort -V | head -n1)" == "$b" ]]
}

detect_host_go_arch() {
  local uname_arch
  uname_arch="$(uname -m)"
  case "$uname_arch" in
    aarch64|arm64)
      # Check if 32-bit userland is running on a 64-bit kernel (common on some Pi setups)
      if command -v getconf >/dev/null 2>&1 && [[ "$(getconf LONG_BIT 2>/dev/null || echo 64)" == "32" ]]; then
        echo "armv6l"
      else
        echo "arm64"
      fi
      ;;
    armv7l|armv6l|armhf|arm)
      echo "armv6l"
      ;;
    x86_64|amd64)
      echo "amd64"
      ;;
    *)
      err "Unsupported host CPU architecture for Go tarball auto-install: $uname_arch"
      ;;
  esac
}

install_golang() {
  command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || err "curl or wget is required to download Go."
  command -v tar >/dev/null 2>&1 || err "tar is required to extract Go."

  local host_go_arch
  host_go_arch="$(detect_host_go_arch)"

  log "Fetching latest Go version from go.dev..."
  local latest_ver=""
  if command -v curl >/dev/null 2>&1; then
    latest_ver="$(curl -fsSL https://go.dev/VERSION?m=text | head -n1 | tr -d '[:space:]')"
  else
    latest_ver="$(wget -qO- https://go.dev/VERSION?m=text | head -n1 | tr -d '[:space:]')"
  fi

  if [[ -z "$latest_ver" || "$latest_ver" != go* ]]; then
    latest_ver="go1.27.3"
    warn "Could not query go.dev/VERSION; falling back to $latest_ver"
  fi

  local tarball="${latest_ver}.linux-${host_go_arch}.tar.gz"
  local url="https://go.dev/dl/${tarball}"
  local tmp_tar
  tmp_tar="$(mktemp /tmp/go-download.XXXXXX.tar.gz)"
  trap 'rm -f "$tmp_tar"' RETURN

  log "Downloading $url ..."
  if command -v curl >/dev/null 2>&1; then
    curl -fL --progress-bar "$url" -o "$tmp_tar"
  else
    wget -O "$tmp_tar" "$url"
  fi

  local sudo_cmd=""
  if [[ "$(id -u)" -ne 0 ]]; then
    command -v sudo >/dev/null 2>&1 || err "sudo or root privileges required to install Go into /usr/local/go."
    sudo_cmd="sudo"
  fi

  log "Installing $latest_ver into /usr/local/go ..."
  $sudo_cmd rm -rf /usr/local/go
  $sudo_cmd tar -C /usr/local -xzf "$tmp_tar"

  if ! echo "$PATH" | grep -q "/usr/local/go/bin"; then
    export PATH="/usr/local/go/bin:$PATH"
  fi

  # Make Go available cleanly for future interactive shells if /etc/profile.d exists
  if [[ -d /etc/profile.d && ! -f /etc/profile.d/golang.sh ]]; then
    $sudo_cmd sh -c 'echo "export PATH=/usr/local/go/bin:\$PATH" > /etc/profile.d/golang.sh' || true
  fi

  log "Installed $(go version)"
}

check_and_setup_go() {
  local installed_ver=""
  local need_install=false
  local reason=""

  if command -v go >/dev/null 2>&1; then
    installed_ver="$(go version | awk '{print $3}' | sed 's/^go//')"
    log "Detected Go $installed_ver ($(command -v go))"
    if [[ "$SKIP_GO_CHECK" == "false" ]] && ! version_gte "$installed_ver" "$REQUIRED_GO"; then
      need_install=true
      reason="Go $installed_ver is older than required Go $REQUIRED_GO (from go.mod)."
    fi
  else
    need_install=true
    reason="Go compiler is not installed on this system."
  fi

  if [[ "$FORCE_INSTALL_GO" == "true" ]]; then
    install_golang
    return
  fi

  if [[ "$need_install" == "true" ]]; then
    warn "$reason"
    if [[ "$AUTO_YES" == "true" ]]; then
      install_golang
    elif [[ -t 0 ]]; then
      read -r -p "Would you like to download and install the latest Go toolchain into /usr/local/go? [Y/n] " reply
      reply="${reply:-Y}"
      case "$reply" in
        [Yy]|[Yy][Ee][Ss])
          install_golang
          ;;
        *)
          if [[ -z "$installed_ver" ]]; then
            err "Go $REQUIRED_GO+ is required to build TraffCount. Re-run with --install-go to install it."
          else
            warn "Continuing with Go $installed_ver at your request..."
          fi
          ;;
      esac
    else
      if [[ -z "$installed_ver" ]]; then
        err "Go $REQUIRED_GO+ is required (non-interactive shell). Re-run with --install-go."
      fi
    fi
  fi
}

resolve_target_arch() {
  if [[ "$TARGET_ARCH" != "auto" ]]; then
    echo "$TARGET_ARCH"
    return
  fi

  local uname_arch
  uname_arch="$(uname -m)"
  case "$uname_arch" in
    aarch64|arm64)
      if command -v getconf >/dev/null 2>&1 && [[ "$(getconf LONG_BIT 2>/dev/null || echo 64)" == "32" ]]; then
        echo "armv7"
      else
        echo "arm64"
      fi
      ;;
    armv7l|armv7|armhf)
      echo "armv7"
      ;;
    x86_64|amd64)
      # Default to arm64 when cross-compiling from x86_64 (modern Pi 3B+/4/5 64-bit Pi OS)
      echo "arm64"
      ;;
    *)
      err "Could not auto-detect Raspberry Pi target for host arch '$uname_arch'. Specify --arch arm64, --arch armv7, or --arch all."
      ;;
  esac
}

build_target() {
  local arch="$1"
  local out_file="$2"
  mkdir -p "$(dirname "$out_file")"

  case "$arch" in
    arm64)
      log "Building TraffCount for Raspberry Pi 64-bit (GOOS=linux GOARCH=arm64 CGO_ENABLED=0) -> $out_file"
      GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$out_file" ./cmd/traffcount
      ;;
    armv7)
      log "Building TraffCount for Raspberry Pi 32-bit ARMv7 (GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0) -> $out_file"
      GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$out_file" ./cmd/traffcount
      ;;
    *)
      err "Invalid --arch '$arch'. Expected auto, arm64, armv7, or all."
      ;;
  esac

  chmod +x "$out_file"
  local sz
  sz="$(du -h "$out_file" | awk '{print $1}')"
  log "Built $out_file ($sz)"
}

check_and_setup_go

RESOLVED_ARCH="$(resolve_target_arch)"

if [[ "$RESOLVED_ARCH" == "all" ]]; then
  if [[ -n "$OUTPUT_PATH" ]]; then
    err "--output cannot be combined with --arch all."
  fi
  build_target "arm64" "$SCRIPT_DIR/dist/traffcount-linux-arm64"
  build_target "armv7" "$SCRIPT_DIR/dist/traffcount-linux-armv7"
else
  OUT="${OUTPUT_PATH:-$SCRIPT_DIR/traffcount}"
  build_target "$RESOLVED_ARCH" "$OUT"
fi

echo ""
log "Build complete!"
echo "Quick Start on Raspberry Pi:"
echo "  Run CLI + Web Server (http://<pi-ip>:9876):  ./traffcount"
echo "  Install system binary + systemd service:"
echo "    sudo install -m 0755 ${OUTPUT_PATH:-./traffcount} /usr/local/bin/traffcount"
echo "    sudo traffcount service install"
echo "    traffcount service status"
