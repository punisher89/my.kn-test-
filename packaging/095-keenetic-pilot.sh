#!/bin/sh
# Keenetic NDM hook. Bounded/coalesced; never invokes another project's script.
set -eu
PREFIX=${PILOT_PREFIX:-@PREFIX@}
case "${table:-}" in mangle|raw) ;; *) exit 0;; esac
BIN=$PREFIX/keenetic-pilot/bin/pilot
CONFIG=$PREFIX/etc/keenetic-pilot/config.json
[ -x "$BIN" ] && [ -S "$PREFIX/etc/keenetic-pilot/control.sock" ] || exit 0
LOCK=$PREFIX/var/run/keenetic-pilot-repair.lock
mkdir -p "$PREFIX/var/run"
if ! mkdir "$LOCK" 2>/dev/null; then
    if [ -f "$LOCK/pid" ]; then
        PID=$(cat "$LOCK/pid")
        case "$PID" in ''|*[!0-9]*) exit 0;; esac
        if ! kill -0 "$PID" 2>/dev/null; then rm -f "$LOCK/pid"; rmdir "$LOCK" 2>/dev/null || true; fi
    fi
    exit 0
fi
(
    trap 'rm -f "$LOCK/pid"; rmdir "$LOCK" 2>/dev/null || true' EXIT HUP INT TERM
    sleep 1
    "$BIN" --config "$CONFIG" --repair-firewall >/dev/null 2>&1 || true
) </dev/null >/dev/null 2>&1 &
printf '%s\n' "$!" > "$LOCK/pid"
exit 0
