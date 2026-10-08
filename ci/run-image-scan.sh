#!/bin/sh
# scan jobの最外側。ci/verify-oci-policy.shまたはci/retrieve-and-scan-oci.shを資源guard内で実行。
# 内側検査だけ成功しても外側資源判定が失敗すればfailed recordへ確定する。終了statusを下流の停止へつなぐ。
# Finalize in the scan job after its outer disk-budget guard completes.
set -eu
umask 077
test ! -e .image-private && test ! -e .image-policy || exit 1
mkdir -p .image-private .image-policy/public
# EXIT時点の終了コードと段階markerからrecordを確定する。trapを解除して再帰実行を避ける。
# 失敗時はSBOM成功proofを撤去し、失敗record作成も失敗すればrecordAvailable=falseを残す。
cleanup() {
 status=$?
 trap - EXIT HUP INT TERM
 if [ "$status" -eq 0 ]; then
  outcome=
 elif [ -s .image-private/outcome ]; then
  outcome=$(cat .image-private/outcome)
 else
  outcome=scanner-unavailable
 fi
 # 内側scan成功でも外側の資源判定は失敗し得る。その場合completed markerを成功根拠にせず、失敗証跡を残す。
 if [ "$outcome" = completed ]; then outcome=scanner-unavailable; fi
 if ! .security/scan-record "$outcome"; then
  status=1
  rm -f .image-policy/public/record.json
  printf '{"schemaVersion":1,"status":"failed","failureReason":"build-unproven","recordAvailable":false}\n' > .image-policy/public/failure.json
 fi
 if [ "$status" -ne 0 ]; then
  rm -f .image-policy/public/sbom.cdx.json .image-policy/public/sbom-proof.json .image-policy/public/input.json
 fi
 rm -rf .image-private
 exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
if [ "${OCI_REGISTRY_RETRIEVAL_ENABLED:-false}" = true ]; then
 sh ci/run-with-oci-budget.sh format sh ci/retrieve-and-scan-oci.sh
else
 sh ci/run-with-oci-budget.sh format sh ci/verify-oci-policy.sh
fi
