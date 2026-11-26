#!/bin/sh
set -eu
BASE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$BASE"
GO=${GO:-go}
. dependencies/nfqws2.env
mkdir -p "$BASE/.cache"
if [ -z "${PILOT_ENGINE_ARCHIVE:-}" ]; then
    PILOT_ENGINE_ARCHIVE=$BASE/.cache/engine.tar.gz
    if [ ! -f "$PILOT_ENGINE_ARCHIVE" ]; then
        curl --proto '=https' --tlsv1.2 -fSL --connect-timeout 15 --max-time 180 "$ENGINE_URL" -o "$PILOT_ENGINE_ARCHIVE.tmp"
        printf '%s  %s\n' "$ENGINE_SHA256" "$PILOT_ENGINE_ARCHIVE.tmp" | sha256sum -c -
        mv "$PILOT_ENGINE_ARCHIVE.tmp" "$PILOT_ENGINE_ARCHIVE"
    fi
fi
export PILOT_ENGINE_ARCHIVE
printf '%s  %s\n' "$ENGINE_SHA256" "$PILOT_ENGINE_ARCHIVE" | sha256sum -c -
python3 scripts/engine-file-pins.py "$PILOT_ENGINE_ARCHIVE"
# Tests use the native amd64 engine; router variants are cross-compiled below.
NATIVE=$(mktemp -d "$BASE/.cache/native.XXXXXX")
trap 'rm -rf "$NATIVE"' EXIT HUP INT TERM
sh scripts/fetch-engine.sh "$NATIVE/engine" amd64
PILOT_TEST_ENGINE=$NATIVE/engine "$GO" test -race ./...
"$GO" vet ./...
for FILE in install.sh scripts/*.sh packaging/*; do sh -n "$FILE"; done
if command -v node >/dev/null 2>&1; then
    node --check internal/pilot/web/app.js
    node --check internal/pilot/web/music.js
    node tests/test_music_ui.cjs
    node tests/test_vpn_profile.cjs
fi
if [ "${PILOT_TEST_BROWSER:-0}" = "1" ]; then
    node tests/test_boxes_ui.cjs
    node tests/test_services_ui.cjs
    node tests/test_menu_ui.cjs
    node tests/test_active_ui.cjs
    node tests/test_health_ui.cjs
fi
GO=$GO sh scripts/build.sh
python3 tests/test_vps_telegram.py
python3 tests/test_ssh_release.py
PILOT_TEST_PACKAGE="$BASE/dist/keenetic-pilot-$(cat VERSION)-linux-amd64.tar.gz" "$GO" test ./internal/pilot -run TestNativeUpdatePackagePreflight -count=1
python3 scripts/publish-packages.py
python3 tests/test_install.py
