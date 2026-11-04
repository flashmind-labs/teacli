#!/usr/bin/env sh
# teacli installer. Downloads the right release binary for this machine and
# installs it. All settings are overridable via environment variables:
#
#   TEACLI_RELEASE_HOST  where releases live   (default: https://releases.flashmind.com/teacli)
#   TEACLI_RELEASE_REPO  owner/repo            (default: flashmind-labs/teacli)
#   TEACLI_VERSION       tag or "latest"       (default: latest)
#   TEACLI_INSTALL_DIR   install directory     (default: ~/.local/bin)
#
# The release host is not the Gitea or Forgejo server teacli talks to; it is
# only where the binaries are downloaded from. Point it at your own Gitea or
# Forgejo instance to install from a mirror.
#
# Usage:
#   curl -fsSL https://releases.flashmind.com/teacli/install.sh | sh
#   TEACLI_VERSION=v1.0.0 ./install.sh
set -eu

RELEASE_HOST="${TEACLI_RELEASE_HOST:-https://releases.flashmind.com/teacli}"
RELEASE_REPO="${TEACLI_RELEASE_REPO:-flashmind-labs/teacli}"
TEACLI_VERSION="${TEACLI_VERSION:-latest}"
INSTALL_DIR="${TEACLI_INSTALL_DIR:-$HOME/.local/bin}"
BIN_NAME="teacli"

err() { echo "install: $*" >&2; exit 1; }

os=$(uname -s)
case "$os" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) err "unsupported OS '$os'. This installer supports Linux and macOS. On Windows, download teacli-windows-amd64.exe from the releases page." ;;
esac

arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) err "unsupported architecture '$arch' (need amd64 or arm64)" ;;
esac

asset="teacli-${os}-${arch}"

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1"; }
	fetch_to() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO- "$1"; }
	fetch_to() { wget -qO "$2" "$1"; }
else
	err "need curl or wget on PATH"
fi

# Resolve "latest" to a concrete tag (no jq required). GitHub serves release
# metadata from api.github.com; Gitea and Forgejo serve it from /api/v1 on the
# same host. Asset URLs have the same shape on all three.
if [ "$TEACLI_VERSION" = "latest" ]; then
	case "$RELEASE_HOST" in
		https://releases.flashmind.com/teacli | https://releases.flashmind.com/teacli/)
			tag=$(fetch "${RELEASE_HOST%/}/latest.txt") ;;
		*)
		case "$RELEASE_HOST" in
		https://github.com | https://github.com/)
			api="https://api.github.com/repos/${RELEASE_REPO}/releases/latest" ;;
		*)
			api="${RELEASE_HOST%/}/api/v1/repos/${RELEASE_REPO}/releases/latest" ;;
	esac
		tag=$(fetch "$api" | tr ',' '\n' | grep '"tag_name"' | head -n1 | sed 's/.*"tag_name" *: *"\([^"]*\)".*/\1/') ;;
	esac
	[ -n "$tag" ] || err "could not resolve the latest release tag. Set TEACLI_VERSION to a tag, e.g. TEACLI_VERSION=v1.0.0."
else
	tag="$TEACLI_VERSION"
fi

case "$tag" in *[!0-9.v]*|'') err "invalid release tag" ;; esac
case "$RELEASE_HOST" in
	https://releases.flashmind.com/teacli | https://releases.flashmind.com/teacli/) base="${RELEASE_HOST%/}/${tag}" ;;
	*) base="${RELEASE_HOST%/}/${RELEASE_REPO}/releases/download/${tag}" ;;
esac
url="${base}/${asset}"

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

echo "Downloading ${asset} (${tag})"
fetch_to "$url" "$tmp" || err "download failed: $url"

# Require a checksum before installing.
if command -v sha256sum >/dev/null 2>&1; then
    got=$(sha256sum "$tmp" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
    got=$(shasum -a 256 "$tmp" | awk '{print $1}')
else
    err "need sha256sum or shasum to verify the download"
fi
sums=$(fetch "${base}/checksums.txt") || err "could not download checksums"
want=$(echo "$sums" | awk -v a="$asset" '$2 == a {print $1}' | head -n1)
[ -n "$want" ] || err "checksum missing for ${asset}"
[ "$want" = "$got" ] || err "checksum mismatch for ${asset}"
echo "Checksum verified"

# 755, not +x: mktemp creates the file 0600, and "+x" on that leaves 711, so a
# binary installed into a root-owned directory would be unreadable to everyone
# else.
chmod 755 "$tmp"

target="${INSTALL_DIR}/${BIN_NAME}"
if mkdir -p "$INSTALL_DIR" 2>/dev/null && [ -w "$INSTALL_DIR" ]; then
	mv "$tmp" "$target"
elif command -v sudo >/dev/null 2>&1; then
	echo "Installing to ${target} (requires sudo)"
	sudo mkdir -p "$INSTALL_DIR"
	sudo mv "$tmp" "$target"
else
	err "cannot write to ${INSTALL_DIR} and sudo is unavailable. Set TEACLI_INSTALL_DIR to a writable directory."
fi
trap - EXIT

echo "Installed ${BIN_NAME} to ${target}"
case ":$PATH:" in *":$INSTALL_DIR:"*) ;; *) echo "warning: $INSTALL_DIR is not on your PATH" ;; esac
"$target" --version 2>/dev/null || true
