#!/bin/sh
# Install step on macOS: fetch this release's binary and check it.
#
# Herdr runs this from [[build]] after cloning the plugin. It downloads the
# release asset whose tag is the manifest's `version`, and installs it only if
# its SHA-256 matches checksums.txt in this commit. A checkout that already has
# a binary (a developer's `go build`) is left alone. HERDRKEYS_RELEASE_URL
# replaces the download base, for a mirror or for testing. See ADR 0020.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
dest="$root/bin/herdrkeys"
repo="johnendean/herdrkeys"

fail() {
	echo "herdrkeys: $*" >&2
	exit 1
}

# Start the daemon now, rather than waiting for the next server start to fire
# the startup hook: boards that stay dark after install look like a broken
# install. A start from this build takes over from any older one that is
# running. A failure here never fails the install.
first_start() {
	[ -n "${HERDRKEYS_NO_START-}" ] && return 0
	"$dest" start || echo "herdrkeys: could not start the daemon; the startup hook will, on next start" >&2
}

if [ -x "$dest" ]; then
	echo "herdrkeys: $dest already exists; not downloading"
	first_start
	exit 0
fi

version=$(sed -n 's/^version *= *"\([^"]*\)".*/\1/p' "$root/herdr-plugin.toml" | head -n 1)
[ -n "$version" ] || fail "no version in herdr-plugin.toml"

case "$(uname -s)" in
	Darwin) os=darwin ;;
	*) fail "unsupported system: $(uname -s) (herdrkeys supports macOS and Windows)" ;;
esac
case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "unsupported architecture: $(uname -m)" ;;
esac

asset="herdrkeys-$os-$arch"
base=${HERDRKEYS_RELEASE_URL:-https://github.com/$repo/releases/download/v$version}
url="$base/$asset"

want=$(awk -v a="$asset" '$2 == a { print $1 }' "$root/checksums.txt")
[ -n "$want" ] || fail "no checksum for $asset in checksums.txt"

tmp="$dest.download.$$"
trap 'rm -f "$tmp"' EXIT INT TERM

echo "herdrkeys: downloading $url"
if command -v curl >/dev/null 2>&1; then
	curl -fsSL --retry 3 -o "$tmp" "$url" || fail "download failed: $url"
elif command -v wget >/dev/null 2>&1; then
	wget -q -O "$tmp" "$url" || fail "download failed: $url"
else
	fail "neither curl nor wget is installed"
fi

if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
	got=$(shasum -a 256 "$tmp" | awk '{ print $1 }')
else
	fail "neither sha256sum nor shasum is installed"
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $asset: expected $want, got $got; not installing"

chmod +x "$tmp"
mv "$tmp" "$dest"
trap - EXIT INT TERM
echo "herdrkeys: installed $asset v$version"
first_start
