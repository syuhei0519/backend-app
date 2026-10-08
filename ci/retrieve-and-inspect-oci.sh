#!/bin/sh
# 既存registry digestと元build由来を解決し、format/runtimeの検証へ引き渡す。
# scan入力archiveとの同一性とnative job由来はtools/release-record/inspection_consumer.goでも照合する。
set -eu
umask 077
test "${OCI_REGISTRY_RETRIEVAL_ENABLED:-false}" = true
test "${OCI_REGISTRY_INSPECTION_ENABLED:-false}" = true
test "$CI_COMMIT_BRANCH" = main && test "$CI_COMMIT_REF_PROTECTED" = true
test "$CI_PIPELINE_SOURCE" = api && test "$CI_ENVIRONMENT_NAME" = release-evidence
case "$CI_JOB_NAME:$1" in
 registry-format-inspection:format|registry-runtime-inspection:runtime) ;;
 *) exit 1;;
esac
public=.oci-compatibility/public
[ "$1" != runtime ] || public=.oci-runtime/public
stage=origin-authority
failed() {
 status=$?
 trap - EXIT
 if [ "$status" -ne 0 ] && [ ! -s "$public/failure.json" ]; then
  printf '{"schemaVersion":1,"status":"failed","stage":"%s"}\n' "$stage" > "$public/failure.json"
 fi
 exit "$status"
}
trap failed EXIT
.security/registry-origin > .image-private/origin.log 2>&1
. .image-private/registry-origin.env
stage=registry-pull
.security/oci-registry-pull "$REGISTRY_SERVICE" "$REGISTRY_SOURCE_COMMIT" "$REGISTRY_IMAGE_DIGEST" > .image-private/retrieval.log 2>&1
stage=archive-hash
sha256sum .oci/image.tar > .oci/image.tar.sha256
archive_sha=$(cut -d ' ' -f 1 .oci/image.tar.sha256)
test "$archive_sha" = "$REGISTRY_INPUT_ARCHIVE_SHA256"
bytes=$(wc -c < .oci/image.tar | tr -d ' ')
printf 'schemaVersion=1\nservice=backend\nsourceProjectId=%s\nsourceCommit=%s\nbuildPipelineId=%s\nbuildJobId=%s\nplatform=linux/amd64\narchiveBytes=%s\nregistryPush=false\nregistryCache=false\nlayoutAccepted=false\n' \
 "$CI_PROJECT_ID" "$REGISTRY_SOURCE_COMMIT" "$REGISTRY_BUILD_PIPELINE_ID" "$REGISTRY_BUILD_JOB_ID" "$bytes" > .oci/build-input.txt
export REGISTRY_SCAN_PIPELINE_ID REGISTRY_SCAN_JOB_ID REGISTRY_SOURCE_COMMIT
stage=inspection
case "$1" in
 format) sh ci/verify-oci-format.sh;;
 runtime) sh ci/verify-oci-runtime.sh;;
esac
