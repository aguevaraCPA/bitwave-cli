#!/bin/sh
# bitwave installer — https://github.com/bitwave-io/bitwave-cli
#
#   curl -fsSL https://cli.bitwave.io/install.sh | sh
#
# Options (env vars):
#   BITWAVE_VERSION      release tag to install (e.g. v0.2.0); default: latest
#   BITWAVE_INSTALL_DIR  target directory; default: ~/.local/bin
# Options (arguments):
#   --org ORG_ID         authenticate, verify, and select this org after install
set -eu

REPO="bitwave-io/bitwave-cli"
INSTALL_DIR="${BITWAVE_INSTALL_DIR:-$HOME/.local/bin}"

err() { printf 'bitwave install: %s\n' "$1" >&2; exit 1; }

ORG_ID=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --org)
      [ "$#" -ge 2 ] || err "--org requires an organization ID"
      ORG_ID="$2"
      shift 2
      ;;
    *) err "unknown argument: $1" ;;
  esac
done

command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar >/dev/null 2>&1 || err "tar is required"

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) err "unsupported OS $(uname -s) — see https://github.com/$REPO#install for other options (Windows: npm install -g bitwave)" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch="amd64" ;;
  aarch64 | arm64) arch="arm64" ;;
  *) err "unsupported architecture $(uname -m)" ;;
esac

version="${BITWAVE_VERSION:-}"
if [ -z "$version" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
    grep '"tag_name"' | head -1 | sed 's/.*"tag_name" *: *"\([^"]*\)".*/\1/')
  [ -n "$version" ] || err "could not determine the latest release (no releases yet?)"
fi

# Archive names carry the version without the leading v.
bare_version=${version#v}
archive="bitwave_${bare_version}_${os}_${arch}.tar.gz"
base_url="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'downloading bitwave %s (%s/%s)...\n' "$version" "$os" "$arch"
curl -fsSL -o "$tmp/$archive" "$base_url/$archive" ||
  err "download failed: $base_url/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base_url/checksums.txt" ||
  err "download failed: $base_url/checksums.txt"

(
  cd "$tmp"
  expected=$(grep " $archive\$" checksums.txt | awk '{print $1}')
  [ -n "$expected" ] || err "$archive not found in checksums.txt"
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$archive" | awk '{print $1}')
  else
    actual=$(shasum -a 256 "$archive" | awk '{print $1}')
  fi
  [ "$expected" = "$actual" ] || err "checksum mismatch for $archive"
)

tar -xzf "$tmp/$archive" -C "$tmp" bitwave
mkdir -p "$INSTALL_DIR"
install -m 755 "$tmp/bitwave" "$INSTALL_DIR/bitwave"

printf 'installed %s\n' "$INSTALL_DIR/bitwave"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) printf 'note: %s is not on your PATH — add:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR" "$INSTALL_DIR" ;;
esac
"$INSTALL_DIR/bitwave" version || true

if [ -n "$ORG_ID" ]; then
  "$INSTALL_DIR/bitwave" auth login --orgId "$ORG_ID"
fi
