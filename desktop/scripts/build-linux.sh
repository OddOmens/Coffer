#!/usr/bin/env bash
# Builds the Coffer desktop app for Linux (.deb + AppImage) inside Docker.
# Nothing is installed on the host: the toolchain lives in the image built
# from scripts/Dockerfile.linux, and caches live in ~/.cache/coffer-desktop-*.
#
# Usage: scripts/build-linux.sh [--test] [extra `tauri build` args]
#   --test   also run `cargo test` before building
# Output:  artifacts/*.deb and artifacts/*.AppImage
set -euo pipefail

DESKTOP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${COFFER_DESKTOP_IMAGE:-coffer-desktop-builder:local}"
CACHE_ROOT="${XDG_CACHE_HOME:-$HOME/.cache}"
CARGO_CACHE="$CACHE_ROOT/coffer-desktop-cargo"
NPM_CACHE="$CACHE_ROOT/coffer-desktop-npm"
HOME_CACHE="$CACHE_ROOT/coffer-desktop-home"     # tauri's linuxdeploy downloads etc.
TARGET_CACHE="$CACHE_ROOT/coffer-desktop-target" # cargo target dir
ARTIFACTS="$DESKTOP_DIR/artifacts"

RUN_TESTS=0
if [[ "${1:-}" == "--test" ]]; then
  RUN_TESTS=1
  shift
fi

mkdir -p "$CARGO_CACHE" "$NPM_CACHE" "$HOME_CACHE" "$TARGET_CACHE" "$ARTIFACTS"

echo "==> Building image $IMAGE"
docker build -t "$IMAGE" -f "$DESKTOP_DIR/scripts/Dockerfile.linux" "$DESKTOP_DIR/scripts"

TTY_FLAGS=()
[[ -t 1 ]] && TTY_FLAGS=(-t)

echo "==> Building Coffer desktop (deb, AppImage)"
docker run --rm "${TTY_FLAGS[@]}" \
  -u "$(id -u):$(id -g)" \
  -e HOME=/cache/home \
  -e CARGO_HOME=/cache/cargo \
  -e CARGO_TARGET_DIR=/cache/target \
  -e npm_config_cache=/cache/npm \
  -e APPIMAGE_EXTRACT_AND_RUN=1 \
  -e CI=true \
  -e RUN_TESTS="$RUN_TESTS" \
  -v "$CARGO_CACHE:/cache/cargo" \
  -v "$NPM_CACHE:/cache/npm" \
  -v "$HOME_CACHE:/cache/home" \
  -v "$TARGET_CACHE:/cache/target" \
  -v "$DESKTOP_DIR:/app" \
  -w /app \
  "$IMAGE" \
  bash -euo pipefail -c '
    export PATH="/usr/local/cargo/bin:$PATH"
    if [[ -f package-lock.json ]]; then npm ci --no-audit --no-fund; else npm install --no-audit --no-fund; fi
    if [[ "$RUN_TESTS" == 1 ]]; then
      npm run build:web
      (cd src-tauri && cargo test)
    fi
    npx tauri build --bundles deb,appimage "$@"
  ' bash "$@"

BUNDLE="$TARGET_CACHE/release/bundle"
rm -f "$ARTIFACTS"/*.deb "$ARTIFACTS"/*.AppImage
cp -v "$BUNDLE"/deb/*.deb "$BUNDLE"/appimage/*.AppImage "$ARTIFACTS"/

echo "==> Artifacts"
ls -lh "$ARTIFACTS"
