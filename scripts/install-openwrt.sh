#!/bin/sh
# OpenWrt 24.10/25.12 installer; a separate path from Keenetic / Entware.
# Run ONLY from a verified, unpacked Keenetic Pilot archive.
set -eu
BASE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
START=1 WAN= LISTEN=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --no-start) START=0; shift;;
    --wan) [ "$#" -ge 2 ] || exit 2; WAN=$2; shift 2;;
    --listen) [ "$#" -ge 2 ] || exit 2; LISTEN=$2; shift 2;;
    *) echo "Usage: $0 [--no-start] [--wan IFACE] [--listen LAN_IP:9080]" >&2; exit 2;;
  esac
done
[ "$(id -u)" = 0 ] || { echo "OpenWrt requires root" >&2; exit 1; }
[ -f /etc/openwrt_release ] && [ -f /etc/rc.common ] || { echo "Not an OpenWrt system" >&2; exit 1; }
command -v fw4 >/dev/null && command -v nft >/dev/null || { echo "firewall4 and nftables required" >&2; exit 1; }
command -v opkg >/dev/null || command -v apk >/dev/null || { echo "Expected opkg (24.10) or apk (25.12+)" >&2; exit 1; }
for tool in sha256sum curl tar mktemp; do command -v "$tool" >/dev/null || { echo "Missing $tool, install with opkg/apk" >&2; exit 1; }; done
[ -f "$BASE/SHA256SUMS" ] && [ -f "$BASE/PLATFORM" ] || { echo "Unverified archive layout" >&2; exit 1; }
(cd "$BASE" && sha256sum -c SHA256SUMS)
ARCH=$(cat "$BASE/PLATFORM")
case "$(uname -m):$ARCH" in
  aarch64:arm64|arm64:arm64|armv7*:arm|armv8l:arm|x86_64:amd64|mipsel:mipsle|mips:mips|mips:mipsle) ;;
  *) echo "Architecture mismatch: $(uname -m):$ARCH" >&2; exit 1;;
esac
case "$WAN" in ""|*[!a-zA-Z0-9_.:-]*) [ -z "$WAN" ] || { echo "Bad WAN interface" >&2;exit 2; };; esac
APP=/usr/lib/keenetic-pilot
INIT=/etc/init.d/keenetic-pilot
CONFIG_DIR=/etc/keenetic-pilot
CONFIG=$CONFIG_DIR/config.json
if [ -e "$APP" ]; then
  [ ! -L "$APP" ] && [ -f "$APP/.pilot-managed" ] && [ "$(cat "$APP/.pilot-managed")" = keenetic-pilot ] || { echo "Unmanaged application path" >&2;exit 1; }
fi
if [ -e "$INIT" ]; then
  grep -q 'Keenetic Pilot - OpenWrt edition' "$INIT" || { echo "Unmanaged init script" >&2;exit 1; }
fi
if [ -e "$CONFIG_DIR" ] && [ ! -f "$CONFIG" ]; then echo "Unexpected existing config directory" >&2;exit 1;fi
if [ -x /opt/keenetic-pilot/bin/pilot ] && /opt/keenetic-pilot/bin/pilot --config /opt/etc/keenetic-pilot/config.json --health >/dev/null 2>&1; then
  echo "An Entware Pilot is already running; do not run two engines" >&2; exit 1
fi
mkdir -p /usr/lib
STAGE=$(mktemp -d /usr/lib/.pilot-openwrt.XXXXXX)
chmod 0711 "$STAGE"
BACKUP=/usr/lib/keenetic-pilot.backup.$(date +%s).$$
SWAPPED=0 HAD_APP=0 OLD_STOPPED=0 INSTALL_STEP=stage
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "$status" != 0 ]; then
    echo "OpenWrt installation failed at $INSTALL_STEP (exit $status)" >&2
    if [ "$SWAPPED" = 1 ]; then
      [ -e "$INIT" ] && "$INIT" stop >/dev/null 2>&1 || true
      rm -rf "$APP"
      if [ "$HAD_APP" = 1 ]; then mv "$BACKUP" "$APP"; fi
      if [ -f "$STAGE/old-init" ]; then cp "$STAGE/old-init" "$INIT"; else rm -f "$INIT";fi
    fi
    if [ "$OLD_STOPPED" = 1 ] && [ -x "$INIT" ]; then "$INIT" start || true;fi
  fi
  rm -rf "$STAGE"
  exit "$status"
}
trap cleanup EXIT HUP INT TERM
cp -R "$BASE" "$STAGE/app"
chmod 0755 "$STAGE/app" "$STAGE/app/lua"
chmod 0644 "$STAGE/app/lua/"*
INSTALL_STEP=engine
sh "$STAGE/app/scripts/fetch-engine.sh" "$STAGE/app/engine" "$ARCH" "$APP/engine"
INSTALL_STEP=config
"$STAGE/app/bin/pilot" --version
if [ -f "$CONFIG" ]; then
  "$STAGE/app/bin/pilot" --config "$CONFIG" --check-config
  [ -f "$CONFIG_DIR/access-token" ] || { echo "Access token missing" >&2;exit 1; }
  # Do not import Keenetic-specific PPE or configuration directory.
  if grep -Eq '"ppe"[[:space:]]*:[[:space:]]*true' "$CONFIG"; then echo "Keenetic PPE enabled; refuse cross-platform config" >&2;exit 1;fi
  CHECK_CONFIG=$CONFIG
else
  mkdir -p "$STAGE/new-config"
  set -- --mode openwrt --init --auto-network --config "$STAGE/new-config/config.json" --engine-dir "$APP/engine"
  [ -z "$WAN" ] || set -- "$@" --wan "$WAN"
  [ -z "$LISTEN" ] || set -- "$@" --listen "$LISTEN"
  "$STAGE/app/bin/pilot" "$@"
  CHECK_CONFIG="$STAGE/new-config/config.json"
fi
INSTALL_STEP=engine-check
"$STAGE/app/bin/pilot" --mode openwrt --config "$CHECK_CONFIG" --engine-dir "$STAGE/app/engine" --check-engine
# Check network rules only without modifying nftables or fw4.
INSTALL_STEP=nft-check
"$STAGE/app/bin/pilot" --mode openwrt --config "$CHECK_CONFIG" --engine-dir "$STAGE/app/engine" --doctor
[ ! -f "$INIT" ] || cp "$INIT" "$STAGE/old-init"
INSTALL_STEP=stop
if [ -x "$INIT" ] && "$INIT" status >/dev/null 2>&1; then "$INIT" stop;OLD_STOPPED=1;fi
INSTALL_STEP=swap
if [ -d "$APP" ]; then mv "$APP" "$BACKUP";HAD_APP=1;SWAPPED=1;fi
mv "$STAGE/app" "$APP";SWAPPED=1
printf '%s\n' keenetic-pilot > "$APP/.pilot-managed"
cp "$BASE/packaging/keenetic-pilot.openwrt" "$INIT"
chmod 0755 "$INIT"
if [ -d "$STAGE/new-config" ]; then
  [ ! -e "$CONFIG_DIR" ] || { echo "Unexpected config directory" >&2;exit 1; }
  mv "$STAGE/new-config" "$CONFIG_DIR"
fi
INSTALL_STEP=start
if [ "$START" = 1 ]; then
  "$INIT" enable
  "$INIT" start
  "$APP/bin/pilot" --config "$CONFIG" --panel-info
  if [ ! -f "$CONFIG_DIR/password.json" ]; then "$APP/bin/pilot" --config "$CONFIG" --pair;fi
else
  echo "Installed without starting; manually use $INIT start"
fi
echo "OpenWrt Pilot installed. NFQUEUE disabled by default until enabled in the panel."
if [ "$HAD_APP" = 1 ]; then echo "Previous application retained at $BACKUP";fi
