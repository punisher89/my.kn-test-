#!/bin/sh
# Keenetic Pilot 0.2.0-beta.18 (Codex)
# Independent one-off SSH installation. NOT the signed panel updater.
set -eu
VERSION='0.2.0-beta.18'
SOURCE='Codex'
MANIFEST_SHA256='80938dbd215674743e1a6ffb486a5aea94a703293b3712a40f1655afc7d62d01'
BASE_URL='https://raw.githubusercontent.com/punisher89/my.kn-test-/refs/heads/ssh-releases/v0.2.0-beta.18'
PREFIX=/opt
printf '%s\n' 'Installing Keenetic Pilot 0.2.0-beta.18 (Codex) over SSH (existing panel updater unchanged)'
[ "$(id -u)" -eq 0 ] || { echo "Connect to Entware SSH as root" >&2; exit 1; }
command -v opkg >/dev/null || { echo "Entware opkg is required" >&2; exit 1; }
for tool in curl sha256sum tar awk mktemp; do
    command -v "$tool" >/dev/null || { echo "Missing required executable: $tool" >&2; exit 1; }
done
case "$(uname -m)" in
    aarch64|arm64) ARCH=arm64;;
    armv7*|armv8l) ARCH=arm;;
    x86_64) ARCH=amd64;;
    mipsel*) ARCH=mipsle;;
    mips*) if opkg print-architecture | grep -q mipsel; then ARCH=mipsle; else ARCH=mips; fi;;
    *) echo "Unsupported router architecture" >&2; exit 1;;
esac
FILE="keenetic-pilot-$VERSION-linux-$ARCH.tar.gz"
STAGE=$(mktemp -d "$PREFIX/.pilot-ssh.XXXXXX")
chmod 0700 "$STAGE"
cleanup() { rm -rf "$STAGE"; }
trap cleanup EXIT HUP INT TERM
download() { curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --show-error --location --retry 2 --connect-timeout 15 --max-time 180 "$1" -o "$2"; }
download "$BASE_URL/SHA256SUMS" "$STAGE/SHA256SUMS"
printf '%s  %s\n' "$MANIFEST_SHA256" "$STAGE/SHA256SUMS" | sha256sum -c -
awk -v name="$FILE" '$2==name || $2=="./"name { print; count++ } END { if (count!=1) exit 1 }' "$STAGE/SHA256SUMS" > "$STAGE/selected.sha256"
download "$BASE_URL/$FILE" "$STAGE/$FILE"
(cd "$STAGE" && sha256sum -c selected.sha256)
# The SHA-256 verified archive is built by Pilot's cross-platform CI.
tar -xzf "$STAGE/$FILE" -C "$STAGE"
[ -f "$STAGE/keenetic-pilot/scripts/install.sh" ] || { echo "Package is missing its installer" >&2; exit 1; }
# The stock installer checks the package's internal SHA256SUMS, platform,
# pinned nfqws2 engine, existing configuration and backups before swapping.
# Skip opkg changes on an already installed Entware/Pilot router.
sh "$STAGE/keenetic-pilot/scripts/install.sh" --prefix "$PREFIX" --skip-packages
printf '%s\n' 'Installed Keenetic Pilot 0.2.0-beta.18 (Codex). Original signed panel updater is unchanged.'
