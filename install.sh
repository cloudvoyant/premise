#!/usr/bin/env bash
# install.sh — Install premise binary
# Usage: curl -fsSL https://raw.githubusercontent.com/cloudvoyant/premise/main/install.sh | bash
#
# Set VERSION=vX.Y.Z to pin a release; defaults to the latest.
# Set INSTALL_DIR to override the install location.
#
# Prebuilt binaries cover linux/macOS on x86_64 and aarch64. To build from source:
#   go install github.com/cloudvoyant/premise@vX.Y.Z
set -euo pipefail

PROJECT="premise"
GITHUB_ORG="cloudvoyant"
REPO="$GITHUB_ORG/$PROJECT"

# Detect OS and arch
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64) ARCH="x86_64" ;;
    aarch64 | arm64) ARCH="aarch64" ;;
    *)
        echo "Unsupported architecture: $ARCH" >&2
        exit 1
        ;;
esac

case "$OS" in
    linux) TARGET="${ARCH}-linux" ;;
    darwin) TARGET="${ARCH}-macos" ;;
    *)
        echo "Unsupported OS: $OS" >&2
        exit 1
        ;;
esac

# Determine install directory
if [[ -n "${INSTALL_DIR:-}" ]]; then
    mkdir -p "$INSTALL_DIR"
elif [[ $EUID -eq 0 ]]; then
    INSTALL_DIR="/usr/local/bin"
else
    INSTALL_DIR="$HOME/.local/bin"
    mkdir -p "$INSTALL_DIR"
fi

# Resolve release tag (VERSION pins it; otherwise use the latest release)
if [[ -n "${VERSION:-}" ]]; then
    TAG="$VERSION"
else
    echo "Fetching latest $PROJECT release..."
    LATEST_URL="https://api.github.com/repos/$REPO/releases/latest"
    TAG=$(curl -fsSL "$LATEST_URL" | grep '"tag_name"' | sed 's/.*"tag_name": *"\(.*\)".*/\1/')
fi

ASSET_NAME="${PROJECT}-${TAG}-${TARGET}.tar.gz"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$TAG/$ASSET_NAME"

echo "Downloading $ASSET_NAME..."
TMP_DIR=$(mktemp -d)
STAGED_BINARY="$INSTALL_DIR/.$PROJECT.tmp.$$"
cleanup() {
    rm -rf "$TMP_DIR"
    rm -f "$STAGED_BINARY"
}
trap cleanup EXIT
curl -fsSL "$DOWNLOAD_URL" | tar -xz -C "$TMP_DIR"

cp "$TMP_DIR/$PROJECT" "$STAGED_BINARY"
chmod +x "$STAGED_BINARY"
mv -f "$STAGED_BINARY" "$INSTALL_DIR/$PROJECT"
ln -sf "$PROJECT" "$INSTALL_DIR/pm"

echo "Installed $PROJECT $TAG to $INSTALL_DIR/$PROJECT"
echo "Installed pm alias to $INSTALL_DIR/pm"
echo "Make sure $INSTALL_DIR is in your PATH."
