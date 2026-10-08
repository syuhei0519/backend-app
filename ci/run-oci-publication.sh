#!/bin/sh
# 公開または既存OCI再利用をdisk guard内で実行。publish-oci jobが呼ぶ。
# 失敗時は成功handoffのdotenv/proofを消してfailure.jsonを残す。registry書込の巻戻しは保証しない。
set -eu
status=0
if [ "${OCI_REGISTRY_RETRIEVAL_ENABLED:-false}" = true ]; then
 sh ci/run-with-oci-budget.sh publish sh ci/verify-reused-oci.sh || status=$?
else
 sh ci/run-with-oci-budget.sh publish sh ci/publish-oci.sh || status=$?
fi
if [ "$status" -ne 0 ]; then
 # 処理後のsampler失敗も成功handoffを無効にする。registry書込は戻せないため、配備/提案はjob成功を必要とする。
 mkdir -p .oci-publish/public
 rm -f image.env .oci-publish/public/published.json
 printf '{"schemaVersion":1,"status":"failed","stage":"publication-or-budget","proposalAuthorized":false,"registryWriteRollbackClaim":false}\n' > .oci-publish/public/failure.json
 exit "$status"
fi
