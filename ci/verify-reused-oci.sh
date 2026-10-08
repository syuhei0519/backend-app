#!/bin/sh
# 既存registry imageを再利用する際の公開側照合。ci/run-oci-publication.shから呼ばれる。
# 新たなscan証跡を古いbuild/digestへ結合する。成功してもmanifest提案の検証は後段で独立に行う。
# Existing tag only: no login/push command or legacy-success safety fallback.
set -eu
umask 077
test "${PHASE2_DELIVERY_ENABLED:-false}" = true && test "${OCI_REGISTRY_RETRIEVAL_ENABLED:-false}" = true || exit 1
test "${OCI_ROLLBACK_RETRIEVAL_ENABLED:-false}" != true || exit 1
test "${CI_COMMIT_REF_PROTECTED:-}" = true && test "${CI_COMMIT_BRANCH:-}" = main || exit 1
test "${CI_REGISTRY_IMAGE:-}" = registry.gitlab.com/syuhei-platform-engineering-lab/backend-app || exit 1
test "${CI_REGISTRY:-}" = registry.gitlab.com || exit 1
test ! -e .oci-publish && test ! -L .oci-publish || exit 1
mkdir -p .oci-publish/private .oci-publish/public
phase=current-inspection
cleanup() {
 status=$?
 rm -rf .oci-publish/private
 if [ "$status" -ne 0 ]; then
  rm -f image.env .oci-publish/public/published.json
  printf '{"schemaVersion":1,"status":"failed","stage":"%s","proposalAuthorized":false,"registryWrite":false}\n' "$phase" > .oci-publish/public/failure.json
 fi
 exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
.security/registry-reuse-check
. .oci-publish/private/verified.env
test "$SOURCE_COMMIT" = "$CI_COMMIT_SHA" && test "$IMAGE_TAG" = "$CI_COMMIT_SHA" || exit 1
phase=existing-tag
image="$CI_REGISTRY_IMAGE:$IMAGE_TAG"
actual=$(sh ci/registry-digest.sh "$image" 2> .oci-publish/private/lookup.log)
test "$actual" = "$IMAGE_DIGEST" || exit 1
phase=remote-verify
sh ci/verify-image.sh "$image" "$IMAGE_DIGEST" > .oci-publish/private/verify.log 2>&1
cp .oci-publish/private/verified.env image.env
printf '{"schemaVersion":1,"imageDigest":"%s","sourceCommit":"%s","scanPipelineId":%s,"scanJobId":%s,"sameLayoutPublished":false,"registryReused":true,"remoteDigestMatched":true,"recordSha256":"%s","proposalAuthorized":false}\n' "$IMAGE_DIGEST" "$SOURCE_COMMIT" "$SCAN_PIPELINE_ID" "$SCAN_JOB_ID" "$RELEASE_RECORD_SHA256" > .oci-publish/public/published.json
chmod 0755 .oci-publish .oci-publish/public
chmod 0644 image.env .oci-publish/public/published.json
