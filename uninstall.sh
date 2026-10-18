#!/usr/bin/env bash
# Removes docker-buildx-defaults, and puts back the real docker-buildx that
# install.sh moved aside, if any
set -euo pipefail

config="${DOCKER_CONFIG:-$HOME/.docker}"
target="$config/cli-plugins/docker-buildx"
real="$config/docker-buildx-defaults/docker-buildx"

if ! "$target" docker-buildx-defaults-version >/dev/null; then
  echo "$target is not docker-buildx-defaults" >&2
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
