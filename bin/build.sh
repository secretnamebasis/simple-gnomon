#!/usr/bin/env bash
set -euo pipefail

# Resolve repo root (works no matter where script is run from)
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

APP_NAME="simple-gnomon" MAIN_PACKAGE="$ROOT_DIR/cmd/main.go" DIST="$ROOT_DIR/build"

VERSION="$(go run "$MAIN_PACKAGE" -v)"

mkdir -p "$DIST"
cd "$DIST"

build() {
  local goos="$1" ; local goarch="$2" ; local ext="$3"
  local output="${APP_NAME}${ext}" ; local archive="${APP_NAME}-${goos}-${goarch}-${VERSION}"

  echo "Building $archive..."

  GOOS="$goos" GOARCH="$goarch" go build -o "$output" "$MAIN_PACKAGE"

  if [[ "$ext" == ".exe" ]]; then
    zip -q "${archive}.zip" "$output"
  else
    tar -czf "${archive}.tar.gz" "$output"
  fi

  rm "$output"
}

# Windows
build windows amd64 ".exe"

# Linux
build linux amd64 ""
build linux arm64 ""

# macOS
build darwin amd64 ""
build darwin arm64 ""
