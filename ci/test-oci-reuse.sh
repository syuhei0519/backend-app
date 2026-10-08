#!/bin/sh
# 既存image再利用の入力結合と停止条件をfixtureで確認する。古いbuild由来と現在scanを混同しない。
# テストの一時directory/偽commandは終了trapで回収する。保存済み受入証跡とは別の作業用テストソース。
set -eu
root=$(pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
for scenario in passed current-refused historical missing-tag digest-swap labels-refused; do
 dir="$work/$scenario"
 mkdir -p "$dir/ci" "$dir/.security" "$dir/bin"
 cp "$root/ci/verify-reused-oci.sh" "$dir/ci/"
 cat > "$dir/.security/registry-reuse-check" <<'EOF'
#!/bin/sh
printf 'current-inspection\n' >> calls
test "$SCENARIO" != current-refused || exit 1
cat > .oci-publish/private/verified.env <<'DATA'
IMAGE_TAG=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
SOURCE_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
IMAGE_DIGEST=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
SCAN_PIPELINE_ID=123
SCAN_JOB_ID=124
RELEASE_RECORD_SHA256=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
DATA
EOF
 cat > "$dir/ci/registry-digest.sh" <<'EOF'
#!/bin/sh
printf 'lookup\n' >> calls
test "$SCENARIO" != missing-tag || exit 10
if [ "$SCENARIO" = digest-swap ]; then printf 'sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\n'; else printf 'sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd\n'; fi
EOF
 cat > "$dir/ci/verify-image.sh" <<'EOF'
#!/bin/sh
printf 'remote-identity\n' >> calls
test "$SCENARIO" != labels-refused
EOF
 cat > "$dir/bin/crane" <<'EOF'
#!/bin/sh
printf 'forbidden registry write/auth invocation\n' >&2
exit 99
EOF
 chmod +x "$dir/.security/registry-reuse-check" "$dir/bin/crane"
 (
 cd "$dir"
 export SCENARIO="$scenario" PHASE2_DELIVERY_ENABLED=true OCI_REGISTRY_RETRIEVAL_ENABLED=true OCI_ROLLBACK_RETRIEVAL_ENABLED=false CI_COMMIT_REF_PROTECTED=true CI_COMMIT_BRANCH=main CI_REGISTRY_IMAGE=registry.gitlab.com/syuhei-platform-engineering-lab/backend-app CI_REGISTRY=registry.gitlab.com CI_COMMIT_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
 export PATH="$dir/bin:$PATH"
 if [ "$scenario" = historical ]; then export OCI_ROLLBACK_RETRIEVAL_ENABLED=true; fi
 status=0
 sh ci/verify-reused-oci.sh > output 2>&1 || status=$?
 test ! -e .oci-publish/private || exit 1
 if [ "$scenario" = passed ]; then
  test "$status" -eq 0 && test -s image.env && test -s .oci-publish/public/published.json || exit 1
  test "$(wc -l < calls | tr -d '[:space:]')" = 3 || exit 1
  grep -q '"sameLayoutPublished":false,"registryReused":true' .oci-publish/public/published.json || exit 1
  test "$(stat -c %a .oci-publish/public/published.json)" = 644 || exit 1
 else
  test "$status" -ne 0 && test ! -e image.env && test ! -e .oci-publish/public/published.json || exit 1
  if [ "$scenario" = historical ]; then test ! -e calls || exit 1; fi
  if [ "$scenario" = current-refused ]; then test "$(cat calls)" = current-inspection || exit 1; fi
 fi
 ) || { printf 'Read-only existing-tag fixture failed: %s\n' "$scenario" >&2; exit 1; }
done
printf 'Existing-tag fixtures accepted; no network, auth or push used\n'
