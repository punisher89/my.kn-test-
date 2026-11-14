#!/bin/sh
set -eu

BASE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
. "$BASE/dependencies/nfqws2.env"
[ "$#" -eq 2 ] || { echo "Usage: $0 DEST arm64|arm|mipsle|mips|amd64" >&2; exit 2; }
DEST=$1
case "$DEST" in /*) ;; *) echo "DEST must be absolute" >&2; exit 2;; esac
[ ! -e "$DEST" ] || { echo "Destination already exists: $DEST" >&2; exit 1; }
case "$2" in
    arm64) ENGINE_ARCH=linux-arm64;;
    arm) ENGINE_ARCH=linux-arm;;
    mipsle) ENGINE_ARCH=linux-mipsel;;
    mips) ENGINE_ARCH=linux-mips;;
    amd64) ENGINE_ARCH=linux-x86_64;;
    *) echo "Unsupported architecture: $2" >&2; exit 2;;
esac
command -v sha256sum >/dev/null || { echo "sha256sum is required" >&2; exit 1; }
mkdir -p "$(dirname -- "$DEST")"
STAGE=$(mktemp -d "$(dirname -- "$DEST")/.pilot-engine.XXXXXX")
trap 'rm -rf "$STAGE"' EXIT HUP INT TERM
ARCHIVE=$STAGE/engine.tar.gz
if [ -n "${PILOT_ENGINE_ARCHIVE:-}" ]; then
    cp "$PILOT_ENGINE_ARCHIVE" "$ARCHIVE"
else
    curl --proto '=https' --tlsv1.2 --fail --show-error --location --connect-timeout 15 --max-time 180 "$ENGINE_URL" -o "$ARCHIVE"
fi
printf '%s  %s\n' "$ENGINE_SHA256" "$ARCHIVE" | sha256sum -c -
tar -xzf "$ARCHIVE" -C "$STAGE" "$ENGINE_ROOT/binaries/$ENGINE_ARCH/nfqws2" "$ENGINE_ROOT/lua"
mkdir "$STAGE/result"
chmod 0755 "$STAGE/result"
cp "$STAGE/$ENGINE_ROOT/binaries/$ENGINE_ARCH/nfqws2" "$STAGE/result/nfqws2"
cp -R "$STAGE/$ENGINE_ROOT/lua" "$STAGE/result/lua"
chmod 755 "$STAGE/result/nfqws2" "$STAGE/result/lua"
chmod 644 "$STAGE/result/lua/"*
printf '%s\n' "$ENGINE_VERSION" > "$STAGE/result/VERSION"
cp "$BASE/dependencies/nfqws2.env" "$STAGE/result/ORIGIN"
(cd "$STAGE/result" && sha256sum nfqws2 lua/* VERSION ORIGIN > SHA256SUMS)
mv "$STAGE/result" "$DEST"
echo "Verified engine prepared: $DEST ($ENGINE_ARCH)"
