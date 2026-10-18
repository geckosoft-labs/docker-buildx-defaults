#!/usr/bin/env bash
# Builds the release binaries in dist/, with their SHA256SUMS.
# Usage: build.sh <version>
set -euo pipefail
cd "$(dirname "$0")"

version=${1:?usage: $0 <version>}
rm -rf dist
mkdir dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${target%/*}
  arch=${target#*/}
  ext=
  if [ "$os" = windows ]; then
    ext=.exe
  fi
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X main.version=$version" \
    -o "dist/docker-buildx-cache-$os-$arch$ext" .
done
(cd dist && sha256sum docker-buildx-cache-* > SHA256SUMS)
cat dist/SHA256SUMS
