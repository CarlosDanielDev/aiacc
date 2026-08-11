#!/bin/sh
# install.sh — download and install the latest aiacc release.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/CarlosDanielDev/aiacc/main/install.sh | sh
#
# Environment:
#   BIN_DIR   Install directory. Default: /usr/local/bin if writable, else ~/.local/bin.

set -eu

REPO="CarlosDanielDev/aiacc"
BINARY="aiacc"

fail() {
	printf 'error: %s\n' "$1" >&2
	exit 1
}

# Detect operating system.
os="$(uname -s)"
case "$os" in
	Linux) os="linux" ;;
	Darwin) os="darwin" ;;
	*) fail "unsupported OS: $os (only linux and darwin are supported)" ;;
esac

# Detect architecture.
arch="$(uname -m)"
case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*) fail "unsupported architecture: $arch (only amd64 and arm64 are supported)" ;;
esac

command -v curl >/dev/null 2>&1 || fail "curl is required but not found"
command -v tar >/dev/null 2>&1 || fail "tar is required but not found"

# Resolve the latest release tag via the GitHub API (no jq dependency).
printf 'Resolving latest release of %s...\n' "$REPO"
api_url="https://api.github.com/repos/${REPO}/releases/latest"
tag="$(curl -fsSL "$api_url" \
	| grep '"tag_name"' \
	| head -n 1 \
	| sed -e 's/.*"tag_name"[[:space:]]*:[[:space:]]*"//' -e 's/".*//')"
[ -n "$tag" ] || fail "could not determine the latest release tag from $api_url"

# Build the asset URL. Matches .goreleaser.yaml name_template:
#   {{ .ProjectName }}_{{ .Os }}_{{ .Arch }}.tar.gz
asset="${BINARY}_${os}_${arch}.tar.gz"
download_url="https://github.com/${REPO}/releases/download/${tag}/${asset}"

# Choose an install directory.
if [ -n "${BIN_DIR:-}" ]; then
	bin_dir="$BIN_DIR"
elif [ -w /usr/local/bin ]; then
	bin_dir="/usr/local/bin"
else
	bin_dir="${HOME}/.local/bin"
fi

# Work in a temp dir cleaned up on exit.
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

printf 'Downloading %s (%s)...\n' "$asset" "$tag"
if ! curl -fsSL "$download_url" -o "${tmp_dir}/${asset}"; then
	fail "failed to download $download_url"
fi

printf 'Extracting %s...\n' "$BINARY"
tar -xzf "${tmp_dir}/${asset}" -C "$tmp_dir" "$BINARY" \
	|| fail "failed to extract $BINARY from $asset"
[ -f "${tmp_dir}/${BINARY}" ] || fail "$BINARY not found in archive $asset"

mkdir -p "$bin_dir" || fail "could not create install directory: $bin_dir"

dest="${bin_dir}/${BINARY}"
if ! mv "${tmp_dir}/${BINARY}" "$dest" 2>/dev/null; then
	# Fall back to cp in case mv across filesystems or perms differ.
	cp "${tmp_dir}/${BINARY}" "$dest" || fail "could not install to $dest (try setting BIN_DIR to a writable path)"
fi
chmod +x "$dest" || fail "could not set executable bit on $dest"

printf '\nInstalled %s %s to %s\n' "$BINARY" "$tag" "$dest"

# Warn if the install dir is not on PATH.
case ":${PATH}:" in
	*":${bin_dir}:"*) : ;;
	*)
		printf '\nNote: %s is not on your PATH.\n' "$bin_dir"
		printf 'Add it, e.g.:\n'
		printf '  export PATH="%s:$PATH"\n' "$bin_dir"
		;;
esac
