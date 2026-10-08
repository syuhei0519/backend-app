#!/bin/sh
# 保護main再検査入口。元buildをregistry-originで証明し、固定digestを取得して再度layout検証する。
# 元build IDと今回scan IDを分け、取得archiveの実checksumを使う。後段はci/verify-oci-policy.sh。
set -eu
umask 077
test "${OCI_REGISTRY_RETRIEVAL_ENABLED:-false}" = true
test "$CI_JOB_NAME" = image-scan
test "$CI_COMMIT_BRANCH" = main && test "$CI_COMMIT_REF_PROTECTED" = true
test "$CI_PIPELINE_SOURCE" = api && test "$CI_ENVIRONMENT_NAME" = release-evidence
printf 'input-unavailable\n' > .image-private/outcome
.security/registry-origin > .image-private/origin.log 2>&1
. .image-private/registry-origin.env
.security/oci-registry-pull "$REGISTRY_SERVICE" "$REGISTRY_SOURCE_COMMIT" "$REGISTRY_IMAGE_DIGEST" > .image-private/retrieval.log 2>&1
sha256sum .oci/image.tar > .oci/image.tar.sha256
archive_sha=$(cut -d ' ' -f 1 .oci/image.tar.sha256)
.security/oci-check .oci/image.tar "$archive_sha" .oci/producer-layout "$REGISTRY_SOURCE_COMMIT" > .oci/layout-proof.json
# scan前に同じサイズ上限付きarchiveを独立再検証する。
sh ci/verify-oci-policy.sh
