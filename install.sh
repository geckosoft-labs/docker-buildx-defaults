#!/usr/bin/env bash
# Installs docker-buildx-cache as the current user's docker-buildx (Linux and
# macOS; on Windows, install.ps1), from a release: the latest, or the given
# one, checked against its SHA256SUMS.
#
# Usage: install.sh [<version>, e.g. v0.1.0]
#   or: curl -fsSL https://raw.githubusercontent.com/geckosoft-labs/docker-buildx-cache/main/install.sh | bash
set -euo pipefail

REPO=geckosoft-labs/docker-buildx-cache
version=${1:-latest}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "Unsupported OS: $(uname -s) (on Windows, use install.ps1)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
if [ "$version" = latest ]; then
  base=https://github.com/$REPO/releases/latest/download
else
  base=https://github.com/$REPO/releases/download/$version
fi
asset=docker-buildx-cache-$os-$arch

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"
if ! grep -q " $asset\$" "$tmp/SHA256SUMS"; then
  echo "$asset is not in the release's SHA256SUMS" >&2
  exit 1
fi
if command -v sha256sum >/dev/null; then
  (cd "$tmp" && grep " $asset\$" SHA256SUMS | sha256sum -c -)
else # macOS
  (cd "$tmp" && grep " $asset\$" SHA256SUMS | shasum -a 256 -c -)
fi

config="${DOCKER_CONFIG:-$HOME/.docker}"
target="$config/cli-plugins/docker-buildx"
real="$config/docker-buildx-cache/docker-buildx"

# A docker-buildx already in the user's directory that isn't the wrapper is
# the real one (a manual install, or a symlink to Docker Desktop's): it moves
# aside, where the wrapper finds it. The real one rejects the argument, which
# is the answer looked for, so its error is not shown.
if [[ -e "$target" || -L "$target" ]] && ! "$target" docker-buildx-cache-version >/dev/null 2>&1; then
  if [[ -e "$real" || -L "$real" ]]; then
    echo "Both $target and $real exist: remove the one that isn't the real docker-buildx" >&2
    exit 1
  fi
  mkdir -p "$(dirname "$real")"
  mv "$target" "$real"
  echo "Moved the real docker-buildx to $real"
fi

mkdir -p "$(dirname "$target")"
install -m 0755 "$tmp/$asset" "$target"
echo "Installed $("$target" docker-buildx-cache-version) as $target"

# Checks that the wrapper finds the real docker-buildx
docker buildx version
