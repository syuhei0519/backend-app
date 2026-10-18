#!/usr/bin/env bash
# Source is the fixed Action checkout (or an explicitly reviewed local checkout).
set -euo pipefail
source_root=$(cd "$(dirname "$0")/.." && pwd)
target=$(cd "${1:?destination required}" && pwd)
shift
(( $# > 0 )) || { echo 'At least one module is required' >&2; exit 1; }
paths=()
for module in "$@"; do
  case "$module" in oci-input|release-record|scan-gate) ;; *) echo 'Unknown shared module' >&2; exit 1;; esac
  test -f "$source_root/tools/$module/go.mod"
  test ! -e "$target/tools/$module" || { echo 'Shared module destination already exists' >&2; exit 1; }
  paths+=("tools/$module")
done
tar -C "$source_root" -cf - "${paths[@]}" | tar -C "$target" -xf -
