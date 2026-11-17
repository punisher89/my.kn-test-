#!/bin/sh
# Keenetic Pilot 0.2.0-alpha.22 [ ChatGPT / OpenWrt experimental ]
# This is the OpenWrt/fwch4 port, NOT the Entware or signed panel updater.
set -eu
VERSION='0.2.0-alpha.22'
MANIFEST_SHA256='e1e3a0480e4951f507d70e01ddd2b7847a6664d6791d5cca621f5f8e201f3237'
BASE_URL='https://raw.githubusercontent.com/punisher89/my.kn-test-/refs/heads/ssh-releases/v0.2.0-alpha.22'
[ "$(id -u)" = 0 ] || { echo "Root required on OpenWrt" >&2;exit 1; }
[ -f /etc/openwrt_release ] && [ -f /etc/rc.common ] || { echo "NOT OpenWrt" >&2;exit 1; }
for tool in curl sha256sum tar awk mktemp; do
    command -v "$tool" >/dev/null || { echo "Missing $tool" >&2;exit 1; }
done
case "$(uname -m)" in
    aarch64|arm64) ARCH=arm64;;
    armv7*|armv8l) ARCH=arm;;
    x86_64) ARCH=amd64;;
    mipsel*) ARCH=mipsle;;
    mips*) if grep -qi mipsel /etc/openwrt_release;then ARCH=mipsle;else ARCH=mips;fi;;
    *) echo "Unsupported CPU architecture" >&2;exit 1;;
esac
FILE="keenetic-pilot-$VERSION-linux-$ARCH.tar.gz"
STAGE=$(mktemp -d /tmp/.pilot-ow.XXXXXX)
chmod 0700 "$STAGE"
cleanup() { rm -rf "$STAGE"; }
trap cleanup EXIT HUP INT TERM
download() { curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --show-error --location --retry 2 --connect-timeout 15 --max-time 180 "$1" -o "$2"; }
download "$BASE_URL/SHA256SUMS" "$STAGE/SHA256SUMS"
printf '%s  %s\n' "$MANIFEST_SHA256" "$STAGE/SHA256SUMS" | sha256sum -c -
awk -v name="$FILE" '$2==name || $2=="./"name { print; count++ } END { if(count!=1)exit 1 }' "$STAGE/SHA256SUMS" > "$STAGE/selected.sha256"
download "$BASE_URL/$FILE" "$STAGE/$FILE"
(cd "$STAGE" && sha256sum -c selected.sha256)
tar -xzf "$STAGE/$FILE" -C "$STAGE"
[ -f "$STAGE/keenetic-pilot/scripts/install-openwrt.sh" ] || { echo "OpenWrt package installer missing" >&2;exit 1; }
sh "$STAGE/keenetic-pilot/scripts/install-openwrt.sh" "$@"
