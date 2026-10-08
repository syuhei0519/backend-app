#!/bin/sh
# registry再取得のformat/runtimeをdisk guard内で実行するwrapper。
# 失敗なら成功proofを除去して安全なfailure.jsonを保存し、private入力・一時OCIを終了時に削除する。
set -eu
umask 077
kind=$1
case "$kind" in
 format) proof=.oci-compatibility/public/compatibility.json; public=.oci-compatibility/public;;
 runtime) proof=.oci-runtime/public/runtime.json; public=.oci-runtime/public;;
 *) exit 1;;
esac
test ! -e .image-private && test ! -e .oci
mkdir -p .image-private "$public"
cleanup() {
 status=$?
 trap - EXIT HUP INT TERM
 if [ "$status" -ne 0 ]; then
  rm -f "$proof"
  if [ ! -s "$public/failure.json" ]; then
   printf '{"schemaVersion":1,"status":"failed","stage":"retrieval-inspection-or-budget"}\n' > "$public/failure.json"
  fi
 fi
 rm -rf .image-private .oci
 exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
sh ci/run-with-oci-budget.sh "$kind" sh ci/retrieve-and-inspect-oci.sh "$kind"
test -s "$proof"
