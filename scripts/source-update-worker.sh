#!/bin/sh
# Runs a checksum-pinned ChatGPT/Codex update separately from the original signed worker.\n# Detached from the controller being replaced; original signed updater unchanged.
set -eu
JOB=$1
PREFIX=$2
VERSION=$3
case "$PREFIX" in /*) ;; *) exit 2;; esac
case "$PREFIX" in /|*[!a-zA-Z0-9_./-]*|*/../*|*/..) exit 2;; esac
[ "$(dirname "$JOB")" = "$PREFIX/etc/keenetic-pilot" ] || exit 2
case "$(basename "$JOB")" in update-job-*) ;; *) exit 2;; esac
[ -d "$JOB" ] && [ ! -L "$JOB" ] || exit 2
case "$VERSION" in ''|*[!0-9a-zA-Z.-]*) exit 2;; esac
STATUS=$PREFIX/etc/keenetic-pilot/update-status.json
LOG=$PREFIX/etc/keenetic-pilot/update.log
umask 077
PHASE=install
write_status() {
    printf '{"running":%s,"stage":"%s","target":"%s","pid":%s,"job":"%s","phase":"%s","exit_code":%s}\n' "$1" "$2" "$VERSION" "$$" "$JOB" "$PHASE" "${3:-0}" > "$STATUS.tmp.$$"
    mv "$STATUS.tmp.$$" "$STATUS"
}
cleanup() {
    RESULT=$?
    trap - EXIT HUP INT TERM
    if [ "$RESULT" = 0 ]; then write_status false completed; else
        echo "Update failed during $PHASE (exit $RESULT)" >> "$LOG"
        write_status false failed "$RESULT"
    fi
    rm -rf "$JOB"
    exit "$RESULT"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
write_status true installing
# The verified package's installer validates config/engine before stopping Pilot,
# keeps credentials/state, and restores the previous application on failure.
sh "$JOB/keenetic-pilot/scripts/install.sh" --prefix "$PREFIX" --skip-packages >> "$LOG" 2>&1

PHASE=version-check
[ "$("$PREFIX/keenetic-pilot/bin/pilot" --version)" = "$VERSION" ] || { echo "Installed version does not match requested release" >> "$LOG"; exit 1; }
