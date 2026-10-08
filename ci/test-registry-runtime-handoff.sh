#!/bin/sh
# Controlled fixtures, no network, DB, registry credentials or OCI export.
set -eu
root=$PWD
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
for scenario in native registry-empty stale-private stale-public wrong-job symlink; do
 dir="$work/$scenario"
 mkdir -p "$dir/ci" "$dir/.oci" "$dir/.security" "$dir/bin"
 cp "$root/ci/verify-oci-runtime.sh" "$dir/ci/"
 printf '%s\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa > "$dir/.oci/image.tar.sha256"
 printf 'buildPipelineId=122\nbuildJobId=111\n' > "$dir/.oci/build-input.txt"
 cat > "$dir/.security/oci-check" <<'EOF'
#!/bin/sh
test "$4" = "$EXPECTED_SOURCE" || exit 1
printf '{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}\n'
EOF
 cat > "$dir/bin/buildctl-daemonless.sh" <<'EOF'
#!/bin/sh
printf '%s\n' "$@" > build-arguments
EOF
 chmod +x "$dir/.security/oci-check" "$dir/bin/buildctl-daemonless.sh"
 (
  cd "$dir"
  export OCI_VALIDATION_ENABLED=true OCI_REGISTRY_INSPECTION_ENABLED=true CI_JOB_NAME=registry-runtime-inspection CI_COMMIT_SHA=cccccccccccccccccccccccccccccccccccccccc REGISTRY_SOURCE_COMMIT=dddddddddddddddddddddddddddddddddddddddd CI_PIPELINE_ID=123 CI_JOB_ID=456
  export EXPECTED_SOURCE=$REGISTRY_SOURCE_COMMIT PATH="$dir/bin:$PATH"
  case "$scenario" in
   native) OCI_REGISTRY_INSPECTION_ENABLED=false; EXPECTED_SOURCE=$CI_COMMIT_SHA;;
   registry-empty) mkdir -p .oci-runtime/public;;
   stale-private) mkdir -p .oci-runtime/public .oci-runtime/private;;
   stale-public) mkdir -p .oci-runtime/public; printf stale > .oci-runtime/public/runtime.json;;
   wrong-job) mkdir -p .oci-runtime/public; CI_JOB_NAME=other-job;;
   symlink) mkdir public-target; ln -s public-target .oci-runtime;;
  esac
  export OCI_REGISTRY_INSPECTION_ENABLED EXPECTED_SOURCE CI_JOB_NAME
  status=0
  sh ci/verify-oci-runtime.sh > fixture-output 2>&1 || status=$?
  case "$scenario" in
   native|registry-empty)
    test "$status" -eq 0
    grep -q '"backendReadyHttp":503' .oci-runtime/public/runtime.json
    grep -q '"backendLiveHttp":200' .oci-runtime/public/runtime.json
    grep -q '"databaseReadinessRefused":true' .oci-runtime/public/runtime.json
    grep -q '"sourceDatabaseConnected":false' .oci-runtime/public/runtime.json
    grep -q '"buildPipelineId":122' .oci-runtime/public/runtime.json
    grep -q '"runtimePipelineId":123' .oci-runtime/public/runtime.json
    grep -q 'force-network-mode=none' build-arguments
    test ! -e .oci-runtime/private;;
   *) test "$status" -ne 0; test ! -e build-arguments;;
  esac
 ) || { printf 'Backend runtime handoff fixture refused: %s\n' "$scenario" >&2; exit 1; }
done
printf 'Backend native/registry runtime handoff and stale-state refusal accepted\n'
