#!/bin/sh
# Reversible router-side comparison: saved strategies without automatic tuning.
set -eu
PREFIX=/opt
ACTION=enter
while [ "$#" -gt 0 ]; do
    case "$1" in
        --prefix) PREFIX=$2; shift 2;;
        --restore) ACTION=restore; shift;;
        *) echo "Usage: $0 [--prefix /opt] [--restore]" >&2; exit 2;;
    esac
done
case "$PREFIX" in /*) ;; *) exit 2;; esac
case "$PREFIX" in /|*[!a-zA-Z0-9_./-]*|*/../*|*/..|*/./*|*/.) exit 2;; esac
PREFIX=${PREFIX%/}
BIN=$PREFIX/keenetic-pilot/bin/pilot
DIR=$PREFIX/etc/keenetic-pilot
CONFIG=$DIR/config.json
BACKUP=$DIR/config.before-static-test.json
INIT=$PREFIX/etc/init.d/S98keenetic-pilot
MARKER=$PREFIX/keenetic-pilot/.pilot-managed
[ -f "$MARKER" ] && [ "$(cat "$MARKER")" = keenetic-pilot ] || { echo "Managed Pilot installation not found" >&2; exit 1; }
[ -x "$BIN" ] && [ -x "$INIT" ] && [ -f "$CONFIG" ] && [ ! -L "$CONFIG" ] || exit 1
[ "$PREFIX" != /opt ] || [ "$(id -u)" = 0 ] || { echo "Run from the Entware root shell" >&2; exit 1; }
umask 077
STAGE=$(mktemp -d "$DIR/.static-test.XXXXXX")
STOPPED=0
SUCCESS=0
cleanup() {
    RESULT=$?
    trap - EXIT HUP INT TERM
    if [ "$SUCCESS" != 1 ] && [ "$STOPPED" = 1 ]; then
        echo "Static-mode operation failed; restoring the configuration from before this command" >&2
        "$INIT" stop || true
        cp -p "$STAGE/original.json" "$CONFIG"
        "$INIT" start || true
    fi
    rm -rf "$STAGE"
    exit "$RESULT"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
cp -p "$CONFIG" "$STAGE/original.json"
if [ "$ACTION" = enter ]; then
    [ ! -e "$BACKUP" ] && [ ! -L "$BACKUP" ] || { echo "A saved static-mode test already exists. Use --restore first: $BACKUP" >&2; exit 1; }
    # Match only these exact JSON keys; domains, strategies and other settings
    # are copied byte-for-byte. Existing pinned strategies keep their priority.
    sed -e 's/"adaptive"[[:space:]]*:[[:space:]]*true/"adaptive": false/g' \
        -e 's/"auto"[[:space:]]*:[[:space:]]*true/"auto": false/g' "$CONFIG" > "$STAGE/candidate.json"
else
    [ -f "$BACKUP" ] && [ ! -L "$BACKUP" ] || { echo "No static-mode backup found" >&2; exit 1; }
    cp -p "$BACKUP" "$STAGE/candidate.json"
fi
"$BIN" --config "$STAGE/candidate.json" --check-config
if [ "$ACTION" = enter ]; then cp -p "$CONFIG" "$BACKUP"; fi
"$INIT" stop
STOPPED=1
cp -p "$STAGE/candidate.json" "$CONFIG"
"$INIT" start
SUCCESS=1
if [ "$ACTION" = restore ]; then
    rm -f "$BACKUP"
    echo "Original Pilot settings restored."
else
    echo "Static comparison mode is running with the saved profile strategies."
    echo "Domains, credentials and learning files are preserved."
    echo "Test the same video, then restore with: sh $0 --prefix $PREFIX --restore"
fi
