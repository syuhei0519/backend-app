#!/bin/sh
# BuildKitでlinux/amd64のOCI archiveを作る。container-build-ociが呼ぶ。
# .oci/image.tarとarchive checksum、build-input.txt、layout-proof.jsonが出力。
# archive checksumはtar全体、image digestはmanifest内容のhashで別識別子。tools/oci-input/layout.goで両者を検証する。
# PE016B preparation: export only. Never authenticate to or push a registry.
set -eu

test "${OCI_VALIDATION_ENABLED:-false}" = true || { echo 'OCI validation is disabled' >&2; exit 1; }
test "${CI_PROJECT_ID:-}" = 86247033 || { echo 'Fixed backend project required' >&2; exit 1; }
test "${CI_PROJECT_URL:-}" = https://gitlab.com/syuhei-platform-engineering-lab/backend-app || exit 1
case "${CI_COMMIT_SHA:-}" in ''|*[!0-9a-f]*) exit 1;; esac
test "${#CI_COMMIT_SHA}" -eq 40 || exit 1
for id in "${CI_PIPELINE_ID:-}" "${CI_JOB_ID:-}"; do
 case "$id" in ''|0|*[!0-9]*) exit 1;; esac
done
# 公開済みSHAの再試行は明示的なregistry再検査経路へ進める。新規buildを許すのは実manifest取得の404を観測した場合だけ。
if [ "${PHASE2_DELIVERY_ENABLED:-false}" = true ] && [ "${OCI_DELIVERY_VALIDATION_ENABLED:-false}" = true ] && [ "${CI_COMMIT_REF_PROTECTED:-false}" = true ] && [ "${CI_COMMIT_BRANCH:-}" = main ]; then
 .security/oci-tag-state
fi
# 出力は新規作成のみ。欠落/部分archiveを再試行時に合格入力と誤認しない。
test ! -e .oci || { echo 'OCI output already exists' >&2; exit 1; }
mkdir .oci
public_auth=$(mktemp -d)
cleanup() {
 status=$?
 rm -rf "$public_auth"
 if [ "$status" -ne 0 ]; then
  rm -f .oci/image.tar .oci/image.tar.sha256 .oci/layout-proof.json
  printf '{"schemaVersion":1,"status":"failed","reason":"OCI export or input verification unavailable"}\n' > .oci/failure.json
 fi
 exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
export DOCKER_CONFIG="$public_auth"
printf '{"auths":{}}\n' > "$DOCKER_CONFIG/config.json"

# registry cacheの入出力・push・attestation・複数platform出力は行わない。registry資格情報や秘密をbuild引数へ渡さない。
buildctl-daemonless.sh build \
 --frontend dockerfile.v0 \
 --local context=. \
 --local dockerfile=. \
 --opt platform=linux/amd64 \
 --opt build-arg:VCS_REF="$CI_COMMIT_SHA" \
 --opt build-arg:VCS_SOURCE="$CI_PROJECT_URL" \
 --output type=oci,dest=.oci/image.tar \
 --metadata-file .oci/build-metadata.json

test -s .oci/image.tar || { echo 'OCI archive missing' >&2; exit 1; }
bytes=$(wc -c < .oci/image.tar | tr -d '[:space:]')
# 凍結した初期目標値。GitLab instanceのupload上限を示す数値ではない。
test "$bytes" -le 104857600 || { echo 'OCI archive exceeds initial 100MiB target; review required' >&2; exit 1; }
# tar bytesのchecksumをartifactへ付ける。次jobは受け取ったbytesから再計算し、途中差替えや破損を拒否する。
sha256sum .oci/image.tar > .oci/image.tar.sha256
printf 'schemaVersion=1\nservice=backend\nsourceProjectId=%s\nsourceCommit=%s\nbuildPipelineId=%s\nbuildJobId=%s\nplatform=linux/amd64\narchiveBytes=%s\nregistryPush=false\nregistryCache=false\nlayoutAccepted=false\n' \
 "$CI_PROJECT_ID" "$CI_COMMIT_SHA" "$CI_PIPELINE_ID" "$CI_JOB_ID" "$bytes" > .oci/build-input.txt
archive_sha=$(sha256sum .oci/image.tar | cut -d ' ' -f 1)
.security/oci-check .oci/image.tar "$archive_sha" .oci/layout "$CI_COMMIT_SHA" > .oci/layout-proof.json
# 厳密layout consumerと固定BuildKit/Trivy/craneの実互換性受入が成功するまで、archiveは準備成果物として扱う。
