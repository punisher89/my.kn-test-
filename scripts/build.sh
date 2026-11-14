#!/bin/sh
set -eu
BASE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$BASE"
GO=${GO:-go}
VERSION=$(cat VERSION)
ARCHES=${ARCHES:-"arm64 arm mipsle mips amd64"}
"$GO" version
mkdir -p build dist
for ARCH in $ARCHES; do
    case "$ARCH" in arm64|arm|mipsle|mips|amd64) ;; *) echo "Unsupported architecture: $ARCH" >&2; exit 2;; esac
    STAGE=$(mktemp -d "$BASE/build/package.XXXXXX")
    trap 'rm -rf "$STAGE"' EXIT HUP INT TERM
    PKG=$STAGE/keenetic-pilot
    mkdir -p "$PKG/bin" "$PKG/scripts" "$PKG/dependencies" "$PKG/packaging"
    CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH GOARM=7 GOMIPS=softfloat GOTOOLCHAIN=local \
      "$GO" build -trimpath -ldflags "-s -w -X github.com/punisher89/my.kn-test-/internal/pilot.Version=$VERSION" -o "$PKG/bin/pilot" ./cmd/pilot
    cp scripts/install.sh scripts/uninstall.sh scripts/fetch-engine.sh "$PKG/scripts/"
    cp packaging/S98keenetic-pilot packaging/095-keenetic-pilot.sh "$PKG/packaging/"
    cp dependencies/nfqws2.env "$PKG/dependencies/"
    cp README.md THIRD_PARTY.md LICENSE VERSION "$PKG/"
    cp -R docs "$PKG/docs"
    cp -R lua "$PKG/lua"
    chmod 755 "$PKG/lua"
    chmod 644 "$PKG/lua/"*
    cp -R licenses "$PKG/licenses"
    "$GO" version > "$PKG/TOOLCHAIN"
    printf '%s\n' "$ARCH" > "$PKG/PLATFORM"
    chmod 755 "$PKG/bin/pilot" "$PKG/scripts/"* "$PKG/packaging/"*
    (cd "$PKG" && find . -type f ! -name SHA256SUMS -print | LC_ALL=C sort | xargs sha256sum > SHA256SUMS)
    tar -czf "dist/keenetic-pilot-$VERSION-linux-$ARCH.tar.gz" -C "$STAGE" keenetic-pilot
    rm -rf "$STAGE"
    trap - EXIT HUP INT TERM
done
(cd dist && sha256sum ./*.tar.gz > SHA256SUMS)
echo "Controller packages written to $BASE/dist (engine downloaded separately with a pinned hash)"
