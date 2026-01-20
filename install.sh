#!/bin/sh
set -e

# ssd installer script
# Usage: curl -fsSL https://raw.githubusercontent.com/byteink/ssd/main/install.sh | sh

REPO="byteink/ssd"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS and architecture
detect_platform() {
    OS="$(uname -s)"
    ARCH="$(uname -m)"

    case "$OS" in
        Linux*)     OS="Linux" ;;
        Darwin*)    OS="Darwin" ;;
        MINGW*|MSYS*|CYGWIN*) OS="Windows" ;;
        *)          echo "Unsupported OS: $OS"; exit 1 ;;
    esac

    case "$ARCH" in
        x86_64|amd64)   ARCH="x86_64" ;;
        aarch64|arm64)  ARCH="arm64" ;;
        *)              echo "Unsupported architecture: $ARCH"; exit 1 ;;
    esac
}

# Get latest release version
get_latest_version() {
    VERSION=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v?([^"]+)".*/\1/')
    if [ -z "$VERSION" ]; then
        echo "Failed to get latest version"
        exit 1
    fi
    echo "Latest version: $VERSION"
}

# Download and install
install_ssd() {
    FILENAME="ssd_${OS}_${ARCH}"
    
    if [ "$OS" = "Windows" ]; then
        FILENAME="${FILENAME}.zip"
    else
        FILENAME="${FILENAME}.tar.gz"
    fi
    
    DOWNLOAD_URL="https://github.com/$REPO/releases/download/v${VERSION}/${FILENAME}"
    
    echo "Downloading ssd from $DOWNLOAD_URL"
    
    TMP_DIR=$(mktemp -d)
    cd "$TMP_DIR"
    
    if command -v curl > /dev/null 2>&1; then
        curl -fsSL -o "$FILENAME" "$DOWNLOAD_URL"
    elif command -v wget > /dev/null 2>&1; then
        wget -q -O "$FILENAME" "$DOWNLOAD_URL"
    else
        echo "Error: curl or wget is required"
        exit 1
    fi
    
    # Extract
    if [ "$OS" = "Windows" ]; then
        unzip -q "$FILENAME"
    else
        tar -xzf "$FILENAME"
    fi
    
    # Install
    if [ -w "$INSTALL_DIR" ]; then
        mv ssd "$INSTALL_DIR/ssd"
        chmod +x "$INSTALL_DIR/ssd"
    else
        echo "Installing to $INSTALL_DIR (requires sudo)"
        sudo mv ssd "$INSTALL_DIR/ssd"
        sudo chmod +x "$INSTALL_DIR/ssd"
    fi
    
    # Cleanup
    cd -
    rm -rf "$TMP_DIR"
    
    echo ""
    echo "✅ ssd v${VERSION} installed successfully to $INSTALL_DIR/ssd"
    echo ""
    echo "Run 'ssd --help' to get started"
}

# Main
detect_platform
get_latest_version
install_ssd
