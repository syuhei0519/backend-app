#!/bin/sh
# build/format/runtime/publishの処理を包む一時disk sampler。ci/includes/oci-validation.ymlなどから呼ぶ。
# 数値だけをpublic/disk.jsonへ保存。計測不能・予算超過は2、計測成功時は内部commandの終了コードを返す。
# 2秒sampleの最大値であり、sample間の瞬間peakを保証しない。
# Numeric-only sampler against the pre-existing PE009 Runner disk profile.
set -eu
mapped_sampler=0
if [ "${1:-}" = __mapped_sampler ]; then mapped_sampler=1; shift; fi
kind=$1; shift
case "$kind" in build|format|runtime|publish) ;; *) exit 1;; esac
base=".oci-budget-$kind"
if [ "$mapped_sampler" = 0 ]; then
 test ! -e "$base" || exit 1
 mkdir -p "$base/private" "$base/public"
else
 test "$#" = 0 && test -d "$base/private" || exit 1
fi
sample_fail() {
 # 固定categoryだけを公開し、du/dfのstderr・path・raw内容は出力しない。
 if [ ! -e "$base/private/failure-stage" ]; then
  printf '%s\n' "$1" > "$base/private/failure-stage"
  reason=unknown
  log=${2:-$base/private/du.log}
  if grep -Eq 'newuidmap|newgidmap|uid_map|gid_map' "$log"; then reason=uid-mapping
  elif grep -Eq 'failed to start the child|unshare' "$log"; then reason=namespace-start
  elif grep -Fq 'Permission denied' "$log"; then reason=permission-denied
  elif grep -Fq 'Operation not permitted' "$log"; then reason=operation-denied
  elif grep -Fq 'No such file or directory' "$log"; then reason=missing-during-sample
  fi
  printf '%s\n' "$reason" > "$base/private/failure-reason"
 fi
 return 1
}
failure_proof() {
 stage=$(cat "$base/private/failure-stage" 2>/dev/null || printf unknown)
 case "$stage" in workspace-du|rootless-tool|user-namespace-du|filesystem-free|measurement-values) ;; *) stage=unknown;; esac
 reason=$(cat "$base/private/failure-reason" 2>/dev/null || printf unknown)
 case "$reason" in uid-mapping|namespace-start|missing-during-sample|permission-denied|operation-denied) ;; *) reason=unknown;; esac
 printf '{"schemaVersion":1,"scope":"PE016B sampled temporary disk","measurementAvailable":false,"withinBudget":false,"failureStage":"%s","failureReason":"%s"}\n' "$stage" "$reason" > "$base/public/disk.json"
}
# BuildKitが一時snapshotを消す競合だけを最大3試行で再計測。権限/namespaceの異常は即失敗する。
# 部分的なdu結果を最大使用量として採用せず、完全成功した走査のみsampleに加える。
measure_du() {
 log=$1; shift
 attempt=0
 while ! raw=$("$@" 2> "$log"); do
  # du走査中にBuildKitが一時snapshotを作成/削除することがある。部分結果は捨て、完全成功した走査だけを採用する。
  if [ "$attempt" -ge 2 ] || ! grep -Fq 'No such file or directory' "$log" || grep -Eq 'Permission denied|Operation not permitted|newuidmap|newgidmap|uid_map|gid_map|unshare|failed to start the child' "$log"; then
   return 1
  fi
  attempt=$((attempt + 1))
  printf '1\n' >> "$base/private/transient-retries"
  sleep 1
 done
}
sample() {
 measure_du "$base/private/du.log" du -sk "$CI_PROJECT_DIR" || { sample_fail workspace-du; return 1; }
 work=$(printf "%s\n" "$raw" | cut -f 1)
 store=0; observed=0; namespace=$mapped_sampler
 for root in /home/user/.local/share/buildkit /tmp/.local/share/buildkit; do
  if [ -d "$root" ]; then
   if ! measure_du "$base/private/du.log" du -sk "$root"; then
    if [ "$mapped_sampler" = 1 ]; then sample_fail user-namespace-du; return 1; fi
    # BuildKitのsubordinate UIDのrootfsはuser namespaceの外から読めないことがある。同じ固定imageのUID mappingで作るnamespaceから計測する。
    command -v rootlesskit >/dev/null 2>&1 || { sample_fail rootless-tool; return 1; }
    measure_du "$base/private/namespace.log" rootlesskit du -sk "$root" || { sample_fail user-namespace-du "$base/private/namespace.log"; return 1; }
    namespace=1
   fi
   size=$(printf "%s\n" "$raw" | cut -f 1)
   store=$((store + size))
   observed=1
  fi
 done
 free=$(df -Pk "$CI_PROJECT_DIR" 2> "$base/private/df.log" | awk 'NR==2 { if ($2>0) printf "%.6f",100*$4/$2 }')
 test -n "$free" || { sample_fail filesystem-free "$base/private/df.log"; return 1; }
 test -n "$work" || { sample_fail measurement-values; return 1; }
 printf '%s %s %s %s %s\n' "$work" "$store" "$free" "$observed" "$namespace" >> "$base/private/samples.txt"
}
if [ "$mapped_sampler" = 1 ]; then
 sample || exit 2
 : > "$base/private/sampler-ready"
 while [ ! -e "$base/private/sampler-stop" ]; do
  sleep 2
  sample || { : > "$base/private/sample-failed"; exit 2; }
 done
 sample || { : > "$base/private/sample-failed"; exit 2; }
 exit 0
fi
# BuildKit開始前から単一のmapped namespaceを維持する。高負荷中のnamespace新規起動で計測不能になることを避ける。
mapped=0
case "$kind" in
 build|runtime)
  if ! command -v rootlesskit >/dev/null 2>&1; then
   sample_fail rootless-tool || true; failure_proof; rm -rf "$base/private"; exit 2
  fi
  mapped=1
  rootlesskit sh "$0" __mapped_sampler "$kind" > "$base/private/sampler-output" 2> "$base/private/namespace.log" & sampler=$!
  attempt=0
  while [ ! -e "$base/private/sampler-ready" ]; do
   if ! kill -0 "$sampler" 2>/dev/null || [ "$attempt" -ge 100 ]; then
    kill "$sampler" 2>/dev/null || true; wait "$sampler" 2>/dev/null || true
    sample_fail user-namespace-du "$base/private/namespace.log" || true
    failure_proof; rm -rf "$base/private"; exit 2
   fi
   attempt=$((attempt + 1)); sleep .1
  done
 ;;
 *)
  if ! sample; then
   failure_proof; rm -rf "$base/private"; exit 2
  fi
  (while :; do sleep 2; sample || { : > "$base/private/sample-failed"; exit 1; }; done) & sampler=$!
 ;;
esac
# samplerを停止してwaitで回収し、private計測資源を削除する。
# 内部commandの成否と計測の成否を別々に確認し、計測不能を正常扱いしない。
cleanup() { if [ -n "$sampler" ]; then kill "$sampler" 2>/dev/null || true; wait "$sampler" 2>/dev/null || true; fi; rm -rf "$base/private"; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
status=0
"$@" || status=$?
if [ "$mapped" = 1 ]; then
 : > "$base/private/sampler-stop"
 wait "$sampler" || : > "$base/private/sample-failed"
else
 kill "$sampler" 2>/dev/null || true
 wait "$sampler" 2>/dev/null || true
 sample || : > "$base/private/sample-failed"
fi
sampler=''
if [ -e "$base/private/sample-failed" ]; then
 failure_proof
 exit 2
fi
retries=0
if [ -f "$base/private/transient-retries" ]; then retries=$(wc -l < "$base/private/transient-retries" | tr -d '[:space:]'); fi
awk -v retries="$retries" -v kind="$kind" 'BEGIN {min=100;n=0;w=0;s=0;o=0;u=0} {n++;if($1>w)w=$1;if($2>s)s=$2;if($3<min)min=$3;if($4>o)o=$4;if($5>u)u=$5} END {ok=(n>=2 && w<=2097152 && s<=10485760 && min>=20);printf "{\"schemaVersion\":1,\"scope\":\"PE016B sampled temporary disk\",\"kind\":\"%s\",\"samples\":%d,\"intervalSeconds\":2,\"transientMissingRetryCount\":%d,\"duAttemptsPerMeasurementMax\":3,\"maxWorkspaceKiB\":%d,\"workspaceLimitKiB\":2097152,\"maxBuildkitStoreKiB\":%d,\"buildkitStoreLimitKiB\":10485760,\"buildkitStoreObserved\":%s,\"buildkitStoreAggregation\":\"sumConfiguredRoots\",\"rootlessNamespaceUsed\":%s,\"minFilesystemFreePercent\":%.6f,\"measurementAvailable\":true,\"withinBudget\":%s,\"continuousPeakClaim\":false}\n",kind,n,retries,w,s,(o?"true":"false"),(u?"true":"false"),min,(ok?"true":"false");if(!ok)exit 2}' "$base/private/samples.txt" > "$base/public/disk.json" || exit 2
exit "$status"
