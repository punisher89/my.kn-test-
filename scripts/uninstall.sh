#!/bin/sh
set -eu
PREFIX=${PILOT_PREFIX:-/opt}
case "$PREFIX" in /*) ;; *) exit 2;; esac
case "$PREFIX" in *[!a-zA-Z0-9_./-]*|/|*/../*|*/..|*/./*|*/.) echo "Unsafe prefix" >&2; exit 2;; esac
APP=$PREFIX/keenetic-pilot
[ -f "$APP/.pilot-managed" ] && [ "$(cat "$APP/.pilot-managed")" = keenetic-pilot ] || { echo "No recognized Keenetic Pilot installation" >&2; exit 1; }
INIT=$PREFIX/etc/init.d/S98keenetic-pilot
[ ! -x "$INIT" ] || "$INIT" stop
# Preserve journals until any stale own rules have been cleaned on next start.
if [ -f "$PREFIX/etc/keenetic-pilot/firewall.json" ]; then
    echo "Firewall journal remains; start and stop the controller to clean it before uninstalling" >&2; exit 1
fi
rm -f "$PREFIX/etc/ndm/netfilter.d/095-keenetic-pilot.sh" "$INIT" "$PREFIX/var/run/keenetic-pilot.pid"
rm -rf "$APP"
echo "Application removed. Configuration, access key, reports, logs and backups retained."
echo "State: $PREFIX/etc/keenetic-pilot"
