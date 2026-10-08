#!/bin/sh
# 生成OCIを固定ツールで再読込する互換性検証。OCI構造の自前検証だけではツール間互換を保証できないため実施。
# 安全なcompatibility proofを出力し、公開そのものはci/publish-oci.shが担当する。
set -eu
umask 077
test "${OCI_VALIDATION_ENABLED:-false}" = true || exit 1
test ! -e .oci-private || exit 1
mkdir -p .oci-private .oci-compatibility/public .cache/trivy
phase=archive
cleanup() {
 status=$?
 rm -rf .oci-private
 if [ "$status" -ne 0 ]; then
  rm -f .oci-compatibility/public/compatibility.json
  printf '{"schemaVersion":1,"status":"failed","stage":"%s"}\n' "$phase" > .oci-compatibility/public/failure.json
 fi
 exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
archive_sha=$(cut -d ' ' -f 1 .oci/image.tar.sha256)
phase=layout
source_commit=$CI_COMMIT_SHA
if [ "${OCI_REGISTRY_INSPECTION_ENABLED:-false}" = true ]; then source_commit=$REGISTRY_SOURCE_COMMIT; fi
.security/oci-check .oci/image.tar "$archive_sha" .oci/layout "$source_commit" > .oci/rechecked-proof.json
digest=$(sed -n 's/.*"digest":"\(sha256:[0-9a-f]\{64\}\)".*/\1/p' .oci/rechecked-proof.json)
test -n "$digest" || exit 1
phase=crane-reader
.security/oci-crane-check .oci/layout "$digest" > .oci-private/crane.json
phase=db
trivy --version --format json > .oci-private/version.json 2> .oci-private/version.log
trivy image --download-db-only --cache-dir .cache/trivy --timeout 5m --quiet > .oci-private/db.log 2>&1
: > .oci-private/empty-ignore
phase=trivy-input
trivy image --input .oci/layout --cache-dir .cache/trivy --skip-db-update --timeout 5m --quiet --no-progress --scanners vuln,secret,misconfig --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL --ignorefile .oci-private/empty-ignore --list-all-pkgs --format json --output .oci-private/report.json > .oci-private/scan.log 2>&1
phase=public-proof
.security/oci-compat-proof > .oci-compatibility/public/compatibility.json
