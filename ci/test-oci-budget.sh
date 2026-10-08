#!/bin/sh
# samplerの計測不能・一時snapshot消失の限定retry・終了コード伝播をfake commandで検証する。
# 実CI/負荷試験は行わない。ci/run-with-oci-budget.shの退行を防ぐ。
set -eu
sampler="$PWD/ci/run-with-oci-budget.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir -p "$work/bin"
# fake du/dfで数値拒否と診断の情報制限を確認するfixture。実mapped namespaceは固定imageのnative OCI jobで別に確認する。
cat > "$work/bin/rootlesskit" <<'SH'
#!/bin/sh
exec "$@"
SH
cat > "$work/bin/du" <<'SH'
#!/bin/sh
if [ "$CASE" = workspace ]; then echo 'Permission denied: sensitive-fixture-content' >&2; exit 1; fi
if [ "$CASE" = transient ] && [ ! -e once ]; then touch once; echo 'No such file or directory: sensitive-fixture-content' >&2; exit 1; fi
if [ "$CASE" = persistent ]; then echo 'No such file or directory: sensitive-fixture-content' >&2; exit 1; fi
if [ "$CASE" = mixed ]; then echo 'No such file or directory; Permission denied: sensitive-fixture-content' >&2; exit 1; fi
printf '1\tfixture\n'
SH
cat > "$work/bin/df" <<'SH'
#!/bin/sh
if [ "$CASE" = filesystem ]; then echo sensitive-fixture-content >&2; exit 1; fi
printf 'Filesystem 1024-blocks Used Available Capacity Mounted\nfixture 100 10 90 10%% /\n'
SH
chmod +x "$work/bin/du" "$work/bin/df" "$work/bin/rootlesskit"
for fixture in workspace filesystem valid transient persistent mixed; do
 mkdir "$work/$fixture"
 (
  cd "$work/$fixture"
  export CASE="$fixture" CI_PROJECT_DIR="$PWD" PATH="$work/bin:$PATH"
  result=0
  sh "$sampler" build sh -c 'sleep 2' || result=$?
  proof=.oci-budget-build/public/disk.json
  test -s "$proof"
  test ! -e .oci-budget-build/private
  ! grep -q sensitive-fixture-content "$proof"
  case "$fixture" in
   workspace) test "$result" = 2; grep -q '"failureStage":"workspace-du"' "$proof"; grep -q '"failureReason":"permission-denied"' "$proof";;
   filesystem) test "$result" = 2; grep -q '"failureStage":"filesystem-free"' "$proof";;
   valid) test "$result" = 0; grep -q '"withinBudget":true' "$proof";;
   transient) test "$result" = 0; grep -q '"withinBudget":true' "$proof"; grep -q '"transientMissingRetryCount":1' "$proof";;
   persistent) test "$result" = 2; grep -q '"failureStage":"workspace-du"' "$proof";;
   mixed) test "$result" = 2; grep -q '"failureReason":"permission-denied"' "$proof";;
  esac
 )
done
printf 'Budget refusal categories and non-leakage accepted\n'
