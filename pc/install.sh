#!/bin/sh
# KindleFetch for PC - macOS (and Linux) installer.
#
#   curl -fsSL https://raw.githubusercontent.com/jokot/KindleFetch/main/pc/install.sh | sh
#
# Installs for the current user only (no sudo needed):
#   ~/.local/bin/kindlefetch
#   on macOS, ~/Applications/KindleFetch.command, which opens it in Terminal
#   when double-clicked.
# Running it again updates to the newest release.
#
# To uninstall:  curl -fsSL .../pc/install.sh | KF_UNINSTALL=1 sh

set -eu

REPO="jokot/KindleFetch"
BIN_DIR="$HOME/.local/bin"
BIN="$BIN_DIR/kindlefetch"
LAUNCHER="$HOME/Applications/KindleFetch.command"

if [ "${KF_UNINSTALL:-}" = 1 ]; then
    rm -f "$BIN" "$LAUNCHER"
    echo "KindleFetch removed. Your downloaded books and settings were kept."
    exit 0
fi

case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux)  os=linux ;;
    *) echo "Unsupported system: $(uname -s). On Windows use install.ps1." >&2; exit 1 ;;
esac
case "$(uname -m)" in
    arm64|aarch64) arch=arm64 ;;
    x86_64|amd64)  arch=amd64 ;;
    *) echo "Unsupported CPU: $(uname -m)" >&2; exit 1 ;;
esac
# A shell running under Rosetta reports x86_64 on Apple Silicon; prefer the
# native build.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && \
   [ "$(sysctl -in sysctl.proc_translated 2>/dev/null)" = 1 ]; then
    arch=arm64
fi
asset="kindlefetch-$os-$arch"

echo "Finding the latest KindleFetch for PC release..."
releases="$(curl -fsSL -H 'User-Agent: KindleFetch-installer' \
    "https://api.github.com/repos/$REPO/releases?per_page=30")"
tag="$(printf '%s\n' "$releases" | grep -o '"tag_name": *"pc-v[^"]*"' | head -1 | sed 's/.*"\(pc-v[^"]*\)"/\1/')"
if [ -z "$tag" ]; then
    echo "No KindleFetch for PC release found at https://github.com/$REPO/releases" >&2
    exit 1
fi
base="https://github.com/$REPO/releases/download/$tag"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $tag ($os/$arch)..."
curl -fsSL -o "$tmp/$asset" "$base/$asset"

if curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"; then
    expected="$(grep " \*\{0,1\}$asset\$" "$tmp/SHA256SUMS" | awk '{print $1}')"
    if command -v shasum >/dev/null 2>&1; then
        actual="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
    else
        actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
    fi
    if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
        echo "Checksum mismatch for $asset - download corrupted or tampered with." >&2
        exit 1
    fi
    echo "Checksum verified."
fi

mkdir -p "$BIN_DIR"
chmod 755 "$tmp/$asset"
mv -f "$tmp/$asset" "$BIN"

if [ "$os" = darwin ]; then
    # curl does not set the quarantine flag, but clear it in case the file
    # arrived some other way, so Gatekeeper does not block the first run.
    xattr -d com.apple.quarantine "$BIN" 2>/dev/null || true

    mkdir -p "$HOME/Applications"
    cat > "$LAUNCHER" <<EOF
#!/bin/sh
exec "$BIN"
EOF
    chmod 755 "$LAUNCHER"
fi

echo ""
echo "KindleFetch for PC $tag installed to $BIN"

case ":$PATH:" in
    *":$BIN_DIR:"*)
        echo "  Start it by typing: kindlefetch"
        ;;
    *)
        # Add ~/.local/bin to PATH for future shells.
        profile="$HOME/.profile"
        case "${SHELL:-}" in
            */zsh)  profile="$HOME/.zshrc" ;;
            */bash) [ "$os" = darwin ] && profile="$HOME/.bash_profile" || profile="$HOME/.bashrc" ;;
        esac
        if ! grep -qs 'KindleFetch' "$profile"; then
            printf '\n# Added by KindleFetch installer\nexport PATH="$HOME/.local/bin:$PATH"\n' >> "$profile"
        fi
        echo "  Start it by typing: kindlefetch   (in a new Terminal window)"
        echo "  or right now:       $BIN"
        ;;
esac
[ "$os" = darwin ] && echo "  or double-click KindleFetch in your Applications folder (Home > Applications)."
exit 0
