#!/bin/sh
# Remove only the OpenWrt Pilot application and procd service.
# Preserve /etc/keenetic-pilot (password, config and diagnostics).
set -eu
[ "$(id -u)" = 0 ] || { echo "Root is required" >&2;exit 1; }
[ -f /etc/openwrt_release ] || { echo "Not OpenWrt" >&2;exit 1; }
APP=/usr/lib/keenetic-pilot
INIT=/etc/init.d/keenetic-pilot
if [ -d "$APP" ]; then
    [ ! -L "$APP" ] && [ -f "$APP/.pilot-managed" ] &&
       [ "$(cat "$APP/.pilot-managed")" = keenetic-pilot ] ||
       { echo "Unmanaged app, refusing removal" >&2;exit 1; }
fi
if [ -f "$INIT" ]; then
    grep -q 'Keenetic Pilot - OpenWrt edition' "$INIT" ||
      { echo "Unmanaged procd service, refusing removal" >&2;exit 1; }
    "$INIT" stop
    "$INIT" disable
fi
# If the daemon stopped gracefully, its isolated table was removed.
# Never flush fw4 and never delete an unrecognized table.
if command -v nft >/dev/null 2>&1; then
    TABLE=$(nft list table inet keenetic_pilot 2>/dev/null || true)
    if [ -n "$TABLE" ]; then
        case "$TABLE" in
           *'comment "keenetic-pilot:openwrt"'*) nft delete table inet keenetic_pilot;;
           *) echo "Unknown nft table keenetic_pilot; not deleting it" >&2;exit 1;;
        esac
    fi
fi
rm -f "$INIT"
[ ! -d "$APP" ] || rm -rf "$APP"
echo "OpenWrt Pilot removed; /etc/keenetic-pilot and other firewall rules preserved."
