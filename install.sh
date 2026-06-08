#!/bin/bash
set -euo pipefail

REPO="genai-io/san"
BINARY="san"
INSTALL_DIR="${HOME}/.local/bin"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

info() { echo -e "${GREEN}$1${NC}"; }
warn() { echo -e "${YELLOW}$1${NC}"; }
error() { echo -e "${RED}$1${NC}" >&2; exit 1; }

usage() {
    echo "Usage: $0 [install|upgrade|uninstall]"
    echo ""
    echo "Commands:"
    echo "  install    Install san (default)"
    echo "  upgrade    Upgrade to latest version"
    echo "  uninstall  Remove san and config"
    exit 0
}

normalize_version() {
    local version="$1"
    version="${version#v}"
    echo "$version"
}

# Detect OS and architecture
detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) error "Unsupported architecture: $ARCH" ;;
    esac

    case "$OS" in
        darwin|linux) ;;
        *) error "Unsupported OS: $OS" ;;
    esac
}

get_latest_version() {
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/'
}

get_download_url() {
    local version="$1"
    local asset_name="san_${OS}_${ARCH}.tar.gz"
    local api_url="https://api.github.com/repos/${REPO}/releases/tags/v${version}"

    curl -fsSL "$api_url" | awk -v asset="$asset_name" '
        /"name":/ {
            if ($0 ~ "\"" asset "\"") {
                found=1
            }
        }
        found && /"browser_download_url":/ {
            line=$0
            sub(/^.*"browser_download_url":[[:space:]]*"/, "", line)
            sub(/".*$/, "", line)
            if (line != "" && !printed) {
                print line
                printed=1
            }
        }
    '
}

do_install() {
    detect_platform
    
    info "Fetching latest version..."
    VERSION=$(get_latest_version)
    [ -z "$VERSION" ] && error "Failed to get latest version"

    # Check if already installed
    if command -v "$BINARY" &>/dev/null; then
        CURRENT=$("$BINARY" version 2>/dev/null | awk '{print $3}' || echo "unknown")
        CURRENT="$(normalize_version "$CURRENT")"
        if [ "$CURRENT" = "$VERSION" ]; then
            info "✓ san v${VERSION} is already installed"
            return
        fi
        info "Upgrading san from v${CURRENT} to v${VERSION}..."
    else
        info "Installing san v${VERSION} for ${OS}/${ARCH}..."
    fi

    # Download and extract
    DOWNLOAD_URL="$(get_download_url "$VERSION")"
    [ -z "$DOWNLOAD_URL" ] && error "Release asset san_${OS}_${ARCH}.tar.gz not found for v${VERSION}"
    TMP_DIR=$(mktemp -d)
    trap "rm -rf $TMP_DIR" EXIT

    curl -fL --progress-bar "$DOWNLOAD_URL" -o "$TMP_DIR/san.tar.gz" || error "Download failed"
    if ! tar -tzf "$TMP_DIR/san.tar.gz" >/dev/null 2>&1; then
        error "Downloaded asset is not a valid tar.gz archive"
    fi
    tar -xzf "$TMP_DIR/san.tar.gz" -C "$TMP_DIR" || error "Extract failed"

    # Install
    mkdir -p "$INSTALL_DIR"
    # Show warning first
    EXISTING_BIN=$(command -v "$BINARY" || true)
    if [ -n "$EXISTING_BIN" ] && [ "$EXISTING_BIN" != "$INSTALL_DIR/$BINARY" ]; then
        warn "Found existing installation at $EXISTING_BIN."
        warn "This script will install to $INSTALL_DIR/$BINARY instead."
    fi
    mv "$TMP_DIR/$BINARY" "$INSTALL_DIR/"
    chmod +x "$INSTALL_DIR/$BINARY"

    # Hint if not in PATH
    if ! echo "$PATH" | tr ':' '\n' | grep -qxF "$INSTALL_DIR"; then
        warn "Add $INSTALL_DIR to your PATH:"
        warn "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    fi

    info "✓ san v${VERSION} installed to $INSTALL_DIR/$BINARY"
}

do_uninstall() {
    info "Uninstalling san..."

    # Remove binary
    if [ -f "$INSTALL_DIR/$BINARY" ]; then
        echo -n "Remove binary "$INSTALL_DIR/$BINARY" [y/N] "
        read -r response < /dev/tty
        if [[ "$response" =~ ^[Yy]$ ]]; then
            rm "$INSTALL_DIR/$BINARY"
            info "✓ Removed $INSTALL_DIR/$BINARY"
        fi
    else
        warn "Binary not found at $INSTALL_DIR/$BINARY"
    fi

    # Ask about config (~/.san)
    cfg="$HOME/.san"
    if [ -d "$cfg" ]; then
        echo -n "Remove config directory ${cfg}? [y/N] "
        read -r response < /dev/tty
        if [[ "$response" =~ ^[Yy]$ ]]; then
            rm -rf "$cfg"
            info "✓ Removed config directory ${cfg}"
        fi
    fi

    info "✓ Uninstall complete"
}

# Main
case "${1:-install}" in
    install|upgrade) do_install ;;
    uninstall|remove) do_uninstall ;;
    -h|--help|help) usage ;;
    *) error "Unknown command: $1" ;;
esac
