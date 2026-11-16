#!/bin/sh
# Single-command entry point; metadata is pinned by scripts/publish-packages.py.
set -eu
PACKAGE_VERSION=0.2.0-alpha.9
MANIFEST_SHA256=f4ce3041fc0cc8d91b04764f7284d1ed1c6613a80d672decf4fec8e3a1913bd9
BASE_URL=https://raw.githubusercontent.com/punisher89/my.kn-test-/refs/heads/packages
PREFIX=${PILOT_PREFIX:-/opt}
case "$PREFIX" in /*) ;; *) echo "Нужен абсолютный путь установки" >&2; exit 2;; esac
case "$PREFIX" in *[!a-zA-Z0-9_./-]*|/|*/../*|*/..|*/./*|*/.) echo "Некорректный путь установки" >&2; exit 2;; esac
PATH="$PREFIX/bin:$PREFIX/sbin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH"
export PATH
if [ "$PREFIX" = /opt ]; then
    [ "$(id -u)" = 0 ] || { echo "Войдите в Entware по SSH от root." >&2; exit 1; }
    command -v opkg >/dev/null || { echo "Сначала установите Entware." >&2; exit 1; }
fi
for TOOL in curl sha256sum tar; do command -v "$TOOL" >/dev/null || { echo "Нужна команда $TOOL. Выполните opkg update и opkg install curl ca-bundle." >&2; exit 1; }; done
case "$(uname -m)" in
    aarch64|arm64) ARCH=arm64;;
    armv7*|armv8l) ARCH=arm;;
    x86_64) ARCH=amd64;;
    mipsel*) ARCH=mipsle;;
    mips*) if command -v opkg >/dev/null && opkg print-architecture | grep -q mipsel; then ARCH=mipsle; else ARCH=mips; fi;;
    *) echo "Архитектура не поддерживается: $(uname -m)" >&2; exit 1;;
esac
[ "$MANIFEST_SHA256" != UNPREPARED ] || { echo "Установщик ещё не связан с опубликованным пакетом." >&2; exit 1; }
mkdir -p "$PREFIX"
STAGE=$(mktemp -d "$PREFIX/.pilot-download.XXXXXX")
trap 'rm -rf "$STAGE"' EXIT HUP INT TERM
download() { curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --show-error --location --retry 2 --connect-timeout 15 --max-time 180 "$1" -o "$2"; }
echo "Keenetic Pilot: загрузка готового пакета для $ARCH…"
download "$BASE_URL/v$PACKAGE_VERSION/SHA256SUMS" "$STAGE/SHA256SUMS"
printf '%s  %s\n' "$MANIFEST_SHA256" "$STAGE/SHA256SUMS" | sha256sum -c -
FILE=keenetic-pilot-$PACKAGE_VERSION-linux-$ARCH.tar.gz
download "$BASE_URL/v$PACKAGE_VERSION/$FILE" "$STAGE/$FILE"
awk -v name="$FILE" '$2==name || $2=="./"name {print;count++} END{if(count!=1)exit 1}' "$STAGE/SHA256SUMS" > "$STAGE/selected.sha256"
(cd "$STAGE" && sha256sum -c selected.sha256)
tar -xzf "$STAGE/$FILE" -C "$STAGE"
sh "$STAGE/keenetic-pilot/scripts/install.sh" --prefix "$PREFIX" --lan-panel "$@"
