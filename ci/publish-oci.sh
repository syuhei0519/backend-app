#!/bin/sh
# 検査したOCI layoutをregistryへ初回公開する。ci/run-oci-publication.shが呼ぶ。
# record/scan/SBOMとlayoutをpublication-checkで照合し、公開後digest/OCIラベルを再確認してimage.envを渡す。
# 既存SHAタグ・認証失敗・通信異常は停止。認証用一時資源はEXIT trapで削除する。
# Same archive/scan only. Existing SHA tags require the separate retrieval path.
set -eu
umask 077
test "${OCI_DELIVERY_VALIDATION_ENABLED:-false}" = true || exit 1
test "${CI_COMMIT_REF_PROTECTED:-}" = true && test "${CI_COMMIT_BRANCH:-}" = main || exit 1
test "${CI_REGISTRY_IMAGE:-}" = registry.gitlab.com/syuhei-platform-engineering-lab/backend-app || exit 1
test "${CI_REGISTRY:-}" = registry.gitlab.com || exit 1
test ! -e .oci-publish || exit 1
mkdir -p .oci-publish/private/auth .oci-publish/public
export DOCKER_CONFIG="$CI_PROJECT_DIR/.oci-publish/private/auth"
phase=input
cleanup() {
 status=$?
 rm -rf .oci-publish/private
 if [ "$status" -ne 0 ]; then
  rm -f image.env .oci-publish/public/published.json
  printf '{"schemaVersion":1,"status":"failed","stage":"%s","proposalAuthorized":false}\n' "$phase" > .oci-publish/public/failure.json
 fi
 exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
archive_sha=$(sha256sum .oci/image.tar | cut -d ' ' -f 1)
.security/oci-check .oci/image.tar "$archive_sha" .oci/layout "$CI_COMMIT_SHA" > .oci-publish/private/layout.json
digest=$(sed -n 's/.*"digest":"\(sha256:[0-9a-f]\{64\}\)".*/\1/p' .oci-publish/private/layout.json)
test -n "$digest" || exit 1
.security/oci-crane-check .oci/layout "$digest" > .oci-publish/private/crane.json
.security/publication-check
. .oci-publish/private/verified.env
test "$IMAGE_DIGEST" = "$digest" && test "$SOURCE_COMMIT" = "$CI_COMMIT_SHA" || exit 1
image="$CI_REGISTRY_IMAGE:$IMAGE_TAG"
phase=existing-tag
lookup=0
sh ci/registry-digest.sh "$image" > .oci-publish/private/remote-digest 2> .oci-publish/private/lookup.log || lookup=$?
# 初回pushを許すのは明示404だけ。既存tag・認証/通信異常・不明結果は停止する。resource_groupによる直列化も必要。
test "$lookup" -eq 10 || exit 1
phase=publish
printf '%s' "$CI_REGISTRY_PASSWORD" | crane auth login --username "$CI_REGISTRY_USER" --password-stdin "$CI_REGISTRY" > .oci-publish/private/auth.log 2>&1
crane push .oci/layout "$image" > .oci-publish/private/push.log 2>&1
phase=remote-verify
actual=$(crane digest "$image" 2> .oci-publish/private/digest.log)
test "$actual" = "$IMAGE_DIGEST" || exit 1
sh ci/verify-image.sh "$image" "$IMAGE_DIGEST" > .oci-publish/private/verify.log 2>&1
cp .oci-publish/private/verified.env image.env
printf '{"schemaVersion":1,"imageDigest":"%s","sourceCommit":"%s","scanPipelineId":%s,"scanJobId":%s,"sameLayoutPublished":true,"registryReused":false,"remoteDigestMatched":true,"recordSha256":"%s","proposalAuthorized":false}\n' "$IMAGE_DIGEST" "$SOURCE_COMMIT" "$SCAN_PIPELINE_ID" "$SCAN_JOB_ID" "$RELEASE_RECORD_SHA256" > .oci-publish/public/published.json
chmod 0755 .oci-publish .oci-publish/public
chmod 0644 image.env .oci-publish/public/published.json
