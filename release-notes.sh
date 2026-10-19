#!/usr/bin/env bash
# Prints the release notes of a version: its section of CHANGELOG.md
# ("## <version> (<date>)"), without the heading, as GitHub shows them.
# Fails when the changelog has no such section, or it's empty.
# Usage: release-notes.sh <version, e.g. v0.3.0>
set -euo pipefail
cd "$(dirname "$0")"

version=${1:?usage: $0 <version>}
notes=$(awk -v v="$version" '/^## / { p = ($2 == v); next } p' CHANGELOG.md)
if [ -z "${notes//[[:space:]]/}" ]; then
  echo "CHANGELOG.md has no notes for $version" >&2
  exit 1
fi
# Without the blank lines around the section, and with each paragraph and
# list item on one line: GitHub shows the line breaks of a release's notes
printf '%s\n' "$notes" | sed -e '/./,$!d' | tac | sed -e '/./,$!d' | tac | awk '
  function flush() { if (buf != "") print buf; buf = "" }
  /^```/ { flush(); print; code = !code; next }
  code { print; next }
  /^[[:space:]]*$/ || /^#/ { flush(); print; next }
  /^[[:space:]]*([-*]|[0-9]+\.) / { flush(); buf = $0; next }
  { line = $0; sub(/^[[:space:]]+/, "", line); buf = buf == "" ? line : buf " " line }
  END { flush() }
'
