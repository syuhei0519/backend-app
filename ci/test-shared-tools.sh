#!/usr/bin/env bash
set -euo pipefail
script=$(cd "$(dirname "$0")" && pwd)/materialize-tools.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bash "$script" "$work" scan-gate release-record
test -f "$work/tools/scan-gate/go.mod"
test -f "$work/tools/release-record/cmd/sbom-check/main.go"
test ! -e "$work/tools/oci-input"
if bash "$script" "$work" scan-gate >/dev/null 2>&1; then exit 1; fi
if bash "$script" "$work" ../oci-input >/dev/null 2>&1; then exit 1; fi
if bash "$script" "$work" >/dev/null 2>&1; then exit 1; fi
printf '%s\n' 'PASS: selected modules copied; existing, traversal and missing selections refused'
