#!/bin/sh
# Run from an unpacked, checksum-verified controller package.
set -eu
BASE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
PREFIX=/opt
WAN=
LISTEN=
START=1
PACKAGES=1
LAN_PANEL=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --prefix) PREFIX=$2; shift 2;;
        --wan) WAN=$2; shift 2;;
        --listen) LISTEN=$2; shift 2;;
        --no-start) START=0; shift;;
        --skip-packages) PACKAGES=0; shift;;
        --lan-panel) LAN_PANEL=1; shift;;
        *) echo "Usage: $0 --wan INTERFACE [--listen LAN_IP:9080] [--no-start] [--skip-packages] [--prefix /opt]" >&2; exit 2;;
    esac
done
case "$PREFIX" in /*) ;; *) echo "Prefix must be absolute" >&2; exit 2;; esac
case "$PREFIX" in *[!a-zA-Z0-9_./-]*|/|*/../*|*/..|*/./*|*/.) echo "Unsafe prefix" >&2; exit 2;; esac
PREFIX=${PREFIX%/}
if [ "$PREFIX" = /opt ]; then
    [ "$(id -u)" = 0 ] || { echo "Installation in Entware requires root" >&2; exit 1; }
    command -v opkg >/dev/null || { echo "Install Entware first" >&2; exit 1; }
else
    # Custom prefixes are for local staging tests; do not modify system packages.
    PACKAGES=0
fi
command -v sha256sum >/dev/null || { echo "sha256sum is required before installation" >&2; exit 1; }
(cd "$BASE" && sha256sum -c SHA256SUMS)
ARCH=$(cat "$BASE/PLATFORM")
case "$(uname -m):$ARCH" in aarch64:arm64|arm64:arm64|armv7*:arm|armv8l:arm|x86_64:amd64|mipsel:mipsle|mips:mips|mips:mipsle) ;; *) echo "Package architecture does not match this machine ($ARCH)" >&2; exit 1;; esac
if [ "$PACKAGES" = 1 ]; then opkg update; opkg install curl ca-bundle iptables kmod_ndms xtables-addons_legacy; fi
APP=$PREFIX/keenetic-pilot
CONFIG_DIR=$PREFIX/etc/keenetic-pilot
INIT=$PREFIX/etc/init.d/S98keenetic-pilot
HOOK=$PREFIX/etc/ndm/netfilter.d/095-keenetic-pilot.sh
for OWNED in "$APP" "$INIT" "$HOOK"; do
    if [ -e "$OWNED" ]; then
        if [ "$OWNED" = "$APP" ]; then
            [ -f "$APP/.pilot-managed" ] && [ "$(cat "$APP/.pilot-managed")" = keenetic-pilot ] || { echo "Refusing to overwrite unrecognized $APP" >&2; exit 1; }
        else
            grep -q 'Keenetic Pilot\|keenetic-pilot' "$OWNED" || { echo "Refusing to overwrite $OWNED" >&2; exit 1; }
        fi
    fi
done
mkdir -p "$PREFIX" "$PREFIX/etc/init.d" "$PREFIX/etc/ndm/netfilter.d" "$PREFIX/var/run" "$PREFIX/var/log"
STAGE=$(mktemp -d "$PREFIX/.pilot-install.XXXXXX")
# nfqws2 drops to an unprivileged UID before reading Lua. Allow traversal,
# not directory listing. Configuration/key files retain their private modes.
chmod 0711 "$STAGE"
SWAPPED=0
OLD_STOPPED=0
HAD_APP=0
BACKUP=$PREFIX/keenetic-pilot.backup.$(date +%s).$$
cleanup() {
    RESULT=$?
    trap - EXIT HUP INT TERM
    if [ "$RESULT" != 0 ] && [ "$SWAPPED" = 1 ]; then
        if [ -x "$INIT" ]; then "$INIT" stop || true; fi
        rm -rf "$APP"
        if [ "$HAD_APP" = 1 ]; then mv "$BACKUP" "$APP"; fi
        for NAME in init hook; do
            case "$NAME" in init) LIVE=$INIT;; hook) LIVE=$HOOK;; esac
            if [ -f "$STAGE/old-$NAME" ]; then cp "$STAGE/old-$NAME" "$LIVE"; else rm -f "$LIVE"; fi
        done
        if [ -f "$STAGE/old-config" ]; then cp "$STAGE/old-config" "$CONFIG_DIR/config.json"; fi
    fi
    if [ "$RESULT" != 0 ] && [ "$OLD_STOPPED" = 1 ] && [ -x "$INIT" ]; then "$INIT" start || true; fi
    rm -rf "$STAGE"
    exit "$RESULT"
}
trap cleanup EXIT HUP INT TERM
cp -R "$BASE" "$STAGE/app"
chmod 0755 "$STAGE/app"
# Copy/extraction under umask 077 may remove traversal from new directories.
# The packet engine reads our Lua after dropping its root privileges.
chmod 0755 "$STAGE/app/lua"
chmod 0644 "$STAGE/app/lua/"*
sh "$STAGE/app/scripts/fetch-engine.sh" "$STAGE/app/engine" "$ARCH"
"$STAGE/app/bin/pilot" --version
if [ -f "$CONFIG_DIR/config.json" ]; then
    "$STAGE/app/bin/pilot" --config "$CONFIG_DIR/config.json" --check-config
    [ -f "$CONFIG_DIR/access-token" ] || { echo "Existing configuration has no access token" >&2; exit 1; }
    CHECK_CONFIG=$CONFIG_DIR/config.json
    if [ "$LAN_PANEL" = 1 ]; then
        cp "$CONFIG_DIR/config.json" "$STAGE/old-config"
        "$STAGE/app/bin/pilot" --config "$CONFIG_DIR/config.json" --prepare-lan-config "$STAGE/config-candidate.json" --listen "$LISTEN"
        CHECK_CONFIG=$STAGE/config-candidate.json
    fi
else
    "$STAGE/app/bin/pilot" --init --auto-network --config "$STAGE/new-config/config.json" --wan "$WAN" --listen "$LISTEN" --engine-dir "$APP/engine"
    CHECK_CONFIG=$STAGE/new-config/config.json
fi
"$STAGE/app/bin/pilot" --config "$CHECK_CONFIG" --engine-dir "$STAGE/app/engine" --check-engine
# All network downloads and validation finish before stopping any existing app.
[ ! -f "$INIT" ] || cp "$INIT" "$STAGE/old-init"
[ ! -f "$HOOK" ] || cp "$HOOK" "$STAGE/old-hook"
if [ -x "$INIT" ] && "$INIT" status >/dev/null 2>&1; then "$INIT" stop; OLD_STOPPED=1; fi
if [ -d "$APP" ]; then mv "$APP" "$BACKUP"; HAD_APP=1; SWAPPED=1; fi
mv "$STAGE/app" "$APP"
SWAPPED=1
printf '%s\n' keenetic-pilot > "$APP/.pilot-managed"
sed "s|@PREFIX@|$PREFIX|g" "$BASE/packaging/S98keenetic-pilot" > "$INIT"
sed "s|@PREFIX@|$PREFIX|g" "$BASE/packaging/095-keenetic-pilot.sh" > "$HOOK"
chmod 755 "$INIT" "$HOOK"
if [ -f "$STAGE/config-candidate.json" ]; then mv "$STAGE/config-candidate.json" "$CONFIG_DIR/config.json"; fi
if [ -d "$STAGE/new-config" ]; then
    [ ! -e "$CONFIG_DIR" ] || { echo "Configuration directory exists without config.json; refusing to overwrite" >&2; exit 1; }
    mv "$STAGE/new-config" "$CONFIG_DIR"
fi
if [ "$START" = 1 ]; then "$INIT" start; fi
echo "Keenetic Pilot установлен."
"$APP/bin/pilot" --config "$CONFIG_DIR/config.json" --panel-info
if [ ! -f "$CONFIG_DIR/password.json" ]; then "$APP/bin/pilot" --config "$CONFIG_DIR/config.json" --pair; fi
echo "Откройте панель, задайте пароль и нажмите «Настроить подключение»."
if [ "$HAD_APP" = 1 ]; then echo "Previous application retained: $BACKUP"; fi
