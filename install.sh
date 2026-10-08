#!/usr/bin/env sh
set -eu

REPOSITORY="${NOTION_TUI_REPOSITORY:-abhijitbendale/notion-tui}"
VERSION="${NOTION_TUI_VERSION:-latest}"
PREFIX="${NOTION_TUI_PREFIX:-}"
SKIP_NTN_CHECK=0

usage() {
    printf '%s\n' \
        "Usage: install.sh [--version VERSION] [--prefix DIRECTORY] [--skip-ntn-check]" \
        "" \
        "By default, the installer uses the latest GitHub release and installs to" \
        "/usr/local/bin, or ~/.local/bin when /usr/local/bin is not writable."
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version)
            [ "$#" -ge 2 ] || { usage >&2; exit 2; }
            VERSION="$2"
            shift 2
            ;;
        --prefix)
            [ "$#" -ge 2 ] || { usage >&2; exit 2; }
            PREFIX="$2"
            shift 2
            ;;
        --skip-ntn-check)
            SKIP_NTN_CHECK=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            printf 'notion-tui: unknown option: %s\n' "$1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

for command in curl tar sha256sum; do
    if ! command -v "$command" >/dev/null 2>&1; then
        printf 'notion-tui: required command not found: %s\n' "$command" >&2
        exit 1
    fi
done

if [ "$SKIP_NTN_CHECK" -eq 0 ] && ! command -v ntn >/dev/null 2>&1; then
    printf '%s\n' \
        'notion-tui: the required `ntn` CLI was not found on PATH.' \
        'Install it and run `ntn login`, or rerun with --skip-ntn-check.' >&2
    exit 1
fi

os="$(uname -s)"
arch="$(uname -m)"
[ "$os" = "Linux" ] || {
    printf 'notion-tui: this installer currently supports Linux only (detected %s).\n' "$os" >&2
    exit 1
}

case "$arch" in
    x86_64|amd64) release_arch="x86_64" ;;
    aarch64|arm64) release_arch="arm64" ;;
    *)
        printf 'notion-tui: unsupported Linux architecture: %s\n' "$arch" >&2
        exit 1
        ;;
esac

if [ "$VERSION" = "latest" ]; then
    version_path="latest/download"
    checksum_url="https://github.com/$REPOSITORY/releases/latest/download/checksums.txt"
else
    case "$VERSION" in
        v*) tag="$VERSION" ;;
        *) tag="v$VERSION" ;;
    esac
    version_path="download/$tag"
    checksum_url="https://github.com/$REPOSITORY/releases/download/$tag/checksums.txt"
fi

asset="notion-tui_Linux_$release_arch.tar.gz"
base_url="https://github.com/$REPOSITORY/releases/$version_path"
tmp_dir="$(mktemp -d)"
cleanup() { rm -rf "$tmp_dir"; }
trap cleanup EXIT INT TERM

printf 'Downloading %s...\n' "$asset"
curl --fail --location --silent --show-error "$base_url/$asset" -o "$tmp_dir/$asset"
curl --fail --location --silent --show-error "$checksum_url" -o "$tmp_dir/checksums.txt"
(cd "$tmp_dir" && grep "  $asset\$" checksums.txt | sha256sum -c -)

if [ -z "$PREFIX" ]; then
    if [ -w /usr/local/bin ]; then
        PREFIX=/usr/local/bin
    else
        PREFIX="${HOME:-}/.local/bin"
    fi
fi
[ -n "$PREFIX" ] || {
    printf 'notion-tui: cannot determine an installation directory.\n' >&2
    exit 1
}

mkdir -p "$PREFIX"
tar -xzf "$tmp_dir/$asset" -C "$tmp_dir"
install -m 0755 "$tmp_dir/notion-tui" "$PREFIX/notion-tui"
printf 'Installed notion-tui to %s/notion-tui\n' "$PREFIX"

case ":${PATH:-}:" in
    *":$PREFIX:"*) ;;
    *) printf 'Add %s to PATH before running notion-tui.\n' "$PREFIX" ;;
esac
