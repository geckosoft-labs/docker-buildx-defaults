#!/usr/bin/env bash
# Removes docker-buildx-cache, and puts back the real docker-buildx that
# install.sh moved aside, if any
set -euo pipefail

config="${DOCKER_CONFIG:-$HOME/.docker}"
target="$config/cli-plugins/docker-buildx"
real="$config/docker-buildx-cache/docker-buildx"

if ! "$target" docker-buildx-cache-version >/dev/null; then
  echo "$target is not docker-buildx-cache" >&2
  exit 1
fi
rm "$target"
echo "Removed $target"
if [[ -e "$real" || -L "$real" ]]; then
  mv "$real" "$target"
  rmdir "$(dirname "$real")"
  echo "Put back the real docker-buildx at $target"
fi

docker buildx version
