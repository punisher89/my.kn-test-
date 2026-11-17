#!/bin/sh
set -eu

BASE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
. "$BASE/dependencies/nfqws2.env"
[ "$#" -ge 2 ] && [ "$#" -le 3 ] || { echo "Usage: $0 DEST arm64|arm|mipsle|mips|amd64 [INSTALLED_ENGINE]" >&2; exit 2; }
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
PINS=$BASE/dependencies/nfqws2-files/$2.sha256
[ -f "$PINS" ] || { echo "Trusted engine file pins are missing" >&2; exit 1; }
# Reuse only files listed by the new verified package, never the installed
# engine's own mutable SHA256SUMS. Copy to staging and verify again before use.
REUSE=${3:-}
if [ -z "${PILOT_ENGINE_ARCHIVE:-}" ] && [ -n "$REUSE" ] && [ -d "$REUSE" ] && [ ! -L "$REUSE" ] && [ ! -L "$REUSE/lua" ]; then
    REUSE_OK=1
    while read -r HASH FILE; do
        [ -f "$REUSE/$FILE" ] && [ ! -L "$REUSE/$FILE" ] || REUSE_OK=0
    done < "$PINS"
    if [ "$REUSE_OK" = 1 ] && (cd "$REUSE" && sha256sum -c "$PINS") >/dev/null 2>&1; then
        mkdir -p "$STAGE/result/lua"
        while read -r HASH FILE; do cp "$REUSE/$FILE" "$STAGE/result/$FILE"; done < "$PINS"
        (cd "$STAGE/result" && sha256sum -c "$PINS")
        chmod 0755 "$STAGE/result" "$STAGE/result/lua" "$STAGE/result/nfqws2"
        chmod 0644 "$STAGE/result/lua/"* "$STAGE/result/ORIGIN" "$STAGE/result/VERSION"
        cp "$PINS" "$STAGE/result/SHA256SUMS"
        mv "$STAGE/result" "$DEST"
        echo "Verified installed engine reused: $DEST ($ENGINE_ARCH); download skipped"
        exit 0
    fi
    echo "Installed engine does not match trusted file pins; fetching pinned archive"
fi
ARCHIVE=$STAGE/engine.tar.gz
if [ -n "${PILOT_ENGINE_ARCHIVE:-}" ]; then
    cp "$PILOT_ENGINE_ARCHIVE" "$ARCHIVE"
else
    echo "Downloading pinned engine archive: $ENGINE_VERSION"
    if curl --proto '=https' --tlsv1.2 --fail --silent --show-error --location --retry 2 --retry-delay 2 --retry-max-time 180 --connect-timeout 15 --max-time 180 "$ENGINE_URL" -o "$ARCHIVE"; then :; else
        RESULT=$?
        echo "Engine archive download failed (curl exit $RESULT); existing installation is unchanged" >&2
        exit "$RESULT"
    fi
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
(cd "$STAGE/result" && sha256sum -c "$PINS")
mv "$STAGE/result" "$DEST"
echo "Verified engine prepared: $DEST ($ENGINE_ARCH)"
