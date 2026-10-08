#!/bin/sh
# 検証済みOCIを隔離runtimeで実行するCI検査。外部networkなしのloopbackでprobeを確認する。
# 元DBへの接続・本番配備の代用ではない。成功proofとdisk proofをnative jobの実成功へ結合する。
set -eu
umask 077
test "${OCI_VALIDATION_ENABLED:-false}" = true || exit 1
# registry検査wrapperが作る空directoryだけを再利用可能とする。
if [ -e .oci-runtime ] || [ -L .oci-runtime ]; then
 test "${OCI_REGISTRY_INSPECTION_ENABLED:-false}" = true && test "${CI_JOB_NAME:-}" = registry-runtime-inspection || exit 1
 test -d .oci-runtime && test ! -L .oci-runtime && test -d .oci-runtime/public && test ! -L .oci-runtime/public || exit 1
 test "$(find .oci-runtime -mindepth 1 -maxdepth 1)" = .oci-runtime/public || exit 1
 test -z "$(find .oci-runtime/public -mindepth 1 -maxdepth 1)" || exit 1
fi
mkdir -p .oci-runtime/private/auth .oci-runtime/public
phase=layout
cleanup() {
 status=$?
 rm -rf .oci-runtime/private
 if [ "$status" -ne 0 ]; then
  rm -f .oci-runtime/public/runtime.json
  printf '{"schemaVersion":1,"status":"failed","stage":"%s"}\n' "$phase" > .oci-runtime/public/failure.json
 fi
 exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
archive_sha=$(cut -d ' ' -f 1 .oci/image.tar.sha256)
source_commit=$CI_COMMIT_SHA
if [ "${OCI_REGISTRY_INSPECTION_ENABLED:-false}" = true ]; then source_commit=$REGISTRY_SOURCE_COMMIT; fi
.security/oci-check .oci/image.tar "$archive_sha" .oci/layout "$source_commit" > .oci-runtime/private/rechecked-proof.json
digest=$(sed -n 's/.*"digest":"\(sha256:[0-9a-f]\{64\}\)".*/\1/p' .oci-runtime/private/rechecked-proof.json)
build_job=$(sed -n 's/^buildJobId=\([0-9][0-9]*\)$/\1/p' .oci/build-input.txt)
build_pipeline=$(sed -n 's/^buildPipelineId=\([0-9][0-9]*\)$/\1/p' .oci/build-input.txt)

test -n "$digest" && test -n "$build_job" && test -n "$build_pipeline" || exit 1
export DOCKER_CONFIG="$PWD/.oci-runtime/private/auth"
printf '{"auths":{}}\n' > "$DOCKER_CONFIG/config.json"
phase=isolated-http
# OCI contextは検証済みlocal directory。registryのbase取得/pushは行わない。network noneでもloopbackは使え、同じbackend binaryがlivez成功・意図的なDB不在時readyz拒否を返すことを調べる。
buildctl-daemonless.sh build \
 --frontend dockerfile.v0 \
 --local context=ci/oci-smoke \
 --local dockerfile=ci/oci-smoke \
 --oci-layout "validated=$PWD/.oci/layout" \
 --opt "context:input=oci-layout:validated@$digest" \
 --opt platform=linux/amd64 \
 --opt force-network-mode=none \
 > .oci-runtime/private/runtime.log 2>&1
phase=public-proof
printf '{"schemaVersion":1,"scope":"PE016B isolated runtime only","sourceProjectId":86247033,"sourceCommit":"%s","buildPipelineId":%s,"buildJobId":%s,"runtimePipelineId":%s,"runtimeJobId":%s,"archiveSha256":"%s","digest":"%s","runtimeUid":10001,"backendReadyHttp":503,"backendLiveHttp":200,"databaseReadinessRefused":true,"network":"none-loopback-only","registryPush":false,"sourceDatabaseConnected":false,"phase2Adoptable":false}\n' \
 "$source_commit" "$build_pipeline" "$build_job" "$CI_PIPELINE_ID" "$CI_JOB_ID" "$archive_sha" "$digest" > .oci-runtime/public/runtime.json
