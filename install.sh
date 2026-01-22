#!/bin/sh
set -e

REPO="byteink/ssd"
INSTALL_DIR="/usr/local/bin"

detect_os() {
    case "$(uname -s)" in
        Linux*)  echo "Linux" ;;
        Darwin*) echo "Darwin" ;;
        *)       echo "Unsupported OS" >&2; exit 1 ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "x86_64" ;;
        arm64|aarch64) echo "arm64" ;;
        *)             echo "Unsupported architecture" >&2; exit 1 ;;
    esac
}

main() {
    OS=$(detect_os)
    ARCH=$(detect_arch)

    VERSION=$(curl -sI "https://github.com/${REPO}/releases/latest" | grep -i "location:" | sed 's/.*tag\///' | tr -d '\r\n')
    if [ -z "$VERSION" ]; then
        echo "Failed to fetch latest version" >&2
        exit 1
    fi

    FILENAME="ssd_${OS}_${ARCH}.tar.gz"
    URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"

    TMPDIR=$(mktemp -d)
    trap 'rm -rf "$TMPDIR"' EXIT

    echo "Downloading ssd ${VERSION} for ${OS}/${ARCH}..."
    curl -sL "$URL" -o "${TMPDIR}/${FILENAME}"

    tar -xzf "${TMPDIR}/${FILENAME}" -C "$TMPDIR"

    if [ -w "$INSTALL_DIR" ]; then
        mv "${TMPDIR}/ssd" "${INSTALL_DIR}/ssd"
    else
        sudo mv "${TMPDIR}/ssd" "${INSTALL_DIR}/ssd"
    fi

    echo "ssd installed to ${INSTALL_DIR}/ssd"
}

main
