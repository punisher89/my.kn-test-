#!/bin/sh
set -eu
BASE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=$(cat "$BASE/VERSION")
ARCH=${1:-arm64}
: "${PILOT_ENGINE_ARCHIVE:?Set PILOT_ENGINE_ARCHIVE to the pinned original engine archive}"
. "$BASE/dependencies/nfqws2.env"
printf '%s  %s\n' "$ENGINE_SHA256" "$PILOT_ENGINE_ARCHIVE" | sha256sum -c -
STAGE=$(mktemp -d "$BASE/build/offline.XXXXXX")
trap 'rm -rf "$STAGE"' EXIT HUP INT TERM
KIT=$STAGE/keenetic-pilot-offline
mkdir -p "$KIT/catalog"
tar -xzf "$BASE/dist/keenetic-pilot-$VERSION-linux-$ARCH.tar.gz" -C "$KIT"
cp "$PILOT_ENGINE_ARCHIVE" "$KIT/engine.tar.gz"
cp "$BASE/catalog/strategies.json" "$BASE/catalog/strategies.json.sig" "$KIT/catalog/"
cat > "$KIT/install.sh" <<'INSTALL'
#!/bin/sh
set -eu
BASE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
PREFIX=${PILOT_PREFIX:-/opt}
(cd "$BASE" && sha256sum -c OFFLINE-SHA256SUMS)
export PILOT_ENGINE_ARCHIVE=$BASE/engine.tar.gz
sh "$BASE/keenetic-pilot/scripts/install.sh" --prefix "$PREFIX" --lan-panel "$@"
# Pause only our controller while loading a validated local pool.
"$PREFIX/etc/init.d/S98keenetic-pilot" stop
if ! "$PREFIX/keenetic-pilot/bin/pilot" --config "$PREFIX/etc/keenetic-pilot/config.json" --import-catalog "$BASE/catalog/strategies.json"; then
    "$PREFIX/etc/init.d/S98keenetic-pilot" start
    exit 1
fi
"$PREFIX/etc/init.d/S98keenetic-pilot" start
INSTALL
(cd "$KIT" && find . -type f ! -name OFFLINE-SHA256SUMS -print | LC_ALL=C sort | xargs sha256sum > OFFLINE-SHA256SUMS)
tar -czf "$BASE/dist/keenetic-pilot-offline-$VERSION-$ARCH.tar.gz" -C "$STAGE" keenetic-pilot-offline
