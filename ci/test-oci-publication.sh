#!/bin/sh
# 公開helperの正常/失敗とguardをstubで確認する。実registryへの公開やCI作成を必要としない。
# テストの一時directory/偽commandは終了trapで回収する。保存済み受入証跡とは別の作業用テストソース。
# Controlled local fixtures. No network, credentials, registry or Package write.
set -eu
root=$(pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
for scenario in passed input-refused existing-tag lookup-error push-error digest-swap labels-refused; do
 dir="$work/$scenario"
 mkdir -p "$dir/ci" "$dir/.oci" "$dir/.security" "$dir/bin"
 cp "$root/ci/publish-oci.sh" "$dir/ci/"
 printf 'fixture archive' > "$dir/.oci/image.tar"
 cat > "$dir/.security/oci-check" <<'EOF'
#!/bin/sh
printf '{"digest":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}\n'
EOF
 cat > "$dir/.security/oci-crane-check" <<'EOF'
#!/bin/sh
printf '{}\n'
EOF
 cat > "$dir/.security/publication-check" <<'EOF'
#!/bin/sh
test "$SCENARIO" != input-refused || exit 1
cat > .oci-publish/private/verified.env <<'ENV'
IMAGE_TAG=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
SOURCE_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
IMAGE_DIGEST=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
SCAN_PIPELINE_ID=10
SCAN_JOB_ID=20
RELEASE_RECORD_SHA256=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
ENV
EOF
 cat > "$dir/ci/registry-digest.sh" <<'EOF'
#!/bin/sh
printf 'lookup\n' >> calls
case "$SCENARIO" in existing-tag) exit 0;; lookup-error) exit 1;; *) exit 10;; esac
EOF
 cat > "$dir/ci/verify-image.sh" <<'EOF'
#!/bin/sh
printf 'verify\n' >> calls
test "$SCENARIO" != labels-refused
EOF
 cat > "$dir/bin/crane" <<'EOF'
#!/bin/sh
case "$1" in
 auth) cat >/dev/null; printf 'auth\n' >> calls;;
 push) printf 'push\n' >> calls; test "$SCENARIO" != push-error;;
 digest) printf 'digest\n' >> calls; if [ "$SCENARIO" = digest-swap ]; then printf 'sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\n'; else printf 'sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd\n'; fi;;
 *) exit 99;;
esac
EOF
 chmod +x "$dir/.security/"* "$dir/bin/crane"
 (
  cd "$dir"
  export SCENARIO="$scenario" OCI_DELIVERY_VALIDATION_ENABLED=true CI_COMMIT_REF_PROTECTED=true CI_COMMIT_BRANCH=main CI_REGISTRY_IMAGE=registry.gitlab.com/syuhei-platform-engineering-lab/backend-app CI_REGISTRY=registry.gitlab.com CI_PROJECT_DIR="$dir" CI_COMMIT_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa CI_REGISTRY_USER=synthetic CI_REGISTRY_PASSWORD=synthetic-no-network
  export PATH="$dir/bin:$PATH"
  status=0
  sh ci/publish-oci.sh > output 2>&1 || status=$?
  if [ "$scenario" = passed ] && [ "$status" -ne 0 ]; then cat output >&2; exit 1; fi
  test ! -e .oci-publish/private || exit 1
  if [ "$scenario" = passed ]; then
   test "$status" -eq 0 && test -s image.env && test -s .oci-publish/public/published.json || exit 1
   test "$(grep -c '^push$' calls)" -eq 1 || exit 1
   grep -q '"proposalAuthorized":false' .oci-publish/public/published.json || exit 1
   grep -q '"registryReused":false' .oci-publish/public/published.json || exit 1
   test "$(stat -c %a .oci-publish/public/published.json)" = 644 || exit 1
   test "$(stat -c %a .oci-publish/public)" = 755 || exit 1
  else
   test "$status" -ne 0 && test ! -e image.env && test ! -e .oci-publish/public/published.json && test -s .oci-publish/public/failure.json || exit 1
   case "$scenario" in
    input-refused) test ! -e calls || exit 1;;
    existing-tag|lookup-error) test "$(wc -l < calls | tr -d '[:space:]')" -eq 1 && test "$(cat calls)" = lookup || exit 1;;
   esac
  fi
 ) || { printf 'OCI publication fixture failed: %s\n' "$scenario" >&2; exit 1; }
done
dir="$work/post-budget"
mkdir -p "$dir/ci" "$dir/.oci-publish/public"
cp "$root/ci/run-oci-publication.sh" "$dir/ci/"
cat > "$dir/ci/run-with-oci-budget.sh" <<'EOF'
#!/bin/sh
printf 'positive fixture' > image.env
printf '{}' > .oci-publish/public/published.json
exit 2
EOF
(
 cd "$dir"
 for mode in false true; do
 status=0
 OCI_REGISTRY_RETRIEVAL_ENABLED=$mode sh ci/run-oci-publication.sh || status=$?
 test "$status" -eq 2 && test ! -e image.env && test ! -e .oci-publish/public/published.json && test -s .oci-publish/public/failure.json || exit 1
 done
) || { printf 'Post-budget handoff refusal fixture failed\n' >&2; exit 1; }
printf 'OCI publication fixtures accepted; no network or write authority claimed\n'
