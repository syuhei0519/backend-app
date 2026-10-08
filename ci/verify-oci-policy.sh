#!/bin/sh
# 同じ検証済みOCIからTrivy scanとCycloneDX SBOMを生成する。ci/run-image-scan.shから呼ぶ。
# private raw reportは外へ出さず、safe scan report・SBOM・proofを出す。
# SBOMは実行用イメージの部品表で、無脆弱性やbuild段階の全開発依存を証明するものではない。
# PE017B same-input policy/SBOM preparation; run-image-scan finalizes its record.
set -eu
umask 077
test "${OCI_VALIDATION_ENABLED:-false}" = true || exit 1
test "${IMAGE_SCAN_VALIDATION_ENABLED:-false}" = true || exit 1
test -d .image-private || exit 1
mkdir -p .cache/trivy
printf 'input-unavailable\n' > .image-private/outcome
archive_sha=$(cut -d ' ' -f 1 .oci/image.tar.sha256)
source_commit=$CI_COMMIT_SHA
if [ "${OCI_REGISTRY_RETRIEVAL_ENABLED:-false}" = true ]; then
 . .image-private/registry-origin.env
 source_commit=$REGISTRY_SOURCE_COMMIT
fi
.security/oci-check .oci/image.tar "$archive_sha" .oci/layout "$source_commit" > .image-private/layout.json
digest=$(sed -n 's/.*"digest":"\(sha256:[0-9a-f]\{64\}\)".*/\1/p' .image-private/layout.json)
test -n "$digest"
.security/oci-crane-check .oci/layout "$digest" > .image-private/crane.json
config=$(sed -n 's/.*"configDigest":"\(sha256:[0-9a-f]\{64\}\)".*/\1/p' .image-private/crane.json)
test -n "$config"
printf 'db-unavailable\n' > .image-private/outcome
# DBを更新してmetadataの鮮度を検査。その後skip-db-updateで同じDBをscan中に固定する。
trivy image --download-db-only --cache-dir .cache/trivy --timeout 5m --quiet > .image-private/db.log 2>&1
.security/scan-record db-check
: > .image-private/empty-ignore
printf 'scanner-unavailable\n' > .image-private/outcome
trivy image --input .oci/layout --cache-dir .cache/trivy --skip-db-update --timeout 5m --quiet --no-progress --scanners vuln,secret,misconfig --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL --ignorefile .image-private/empty-ignore --list-all-pkgs --format json --output .image-private/raw.json > .image-private/scan.log 2>&1
printf 'policy-rejected\n' > .image-private/outcome
.security/scan-gate -policy security/scan-policy.json -db .cache/trivy/db/metadata.json -report .image-private/raw.json -out .image-policy/public/scan-report.json -scanner-exit 0 -image-config "$config"
printf 'sbom-unavailable\n' > .image-private/outcome
if [ "${AT17_SAME_DIGEST_RESCAN:-false}" = true ]; then
 # 実scannerと実更新DBのguard成功後、このレビュー済みopt-in main受入jobだけでSBOM生成を意図的に失敗させる。
 test "$CI_JOB_NAME" = oci-image-rescan-validation && test "$CI_COMMIT_BRANCH" = main && test "$CI_COMMIT_REF_PROTECTED" = true && test "$CI_PIPELINE_SOURCE" = api && test "${AT17_RESCAN_ACCEPTANCE_ENABLED:-false}" = true || exit 1
 exit 1
fi
case "${AT17_SBOM_FAILURE:-}" in
 '') ;;
 generate|invalid)
  # 明示指定された制御下の負例fixture。保護releaseの入力には使わない。
  test "$CI_COMMIT_REF_PROTECTED" = false && test "$CI_PIPELINE_SOURCE" = api || exit 1
  if [ "$AT17_SBOM_FAILURE" = generate ]; then exit 1; fi
  ;;
 *) exit 1;;
esac
# Convert the already gated report, including --list-all-pkgs inventory, from
# this exact OCI scan. No second input lookup, DB refresh or registry fallback.
# 合格判定したraw reportからSBOMへ変換し、別imageの再取得やDBの再更新を挟まない。
# SBOM生成・schema/内容照合の失敗はscan job失敗となり、公開/提案へ進めない。
trivy convert --format cyclonedx --output .image-private/sbom.cdx.json .image-private/raw.json > .image-private/sbom.log 2>&1
if [ "${AT17_SBOM_FAILURE:-}" = invalid ]; then printf '{"bomFormat":"CycloneDX","specVersion":"1.7","components":"invalid-fixture"}\n' > .image-private/sbom.cdx.json; fi
printf 'sbom-invalid\n' > .image-private/outcome
.security/sbom-check .image-private/sbom.cdx.json .image-private/raw.json "$config" .image-policy/public/sbom-validation-failure.json > .image-policy/public/sbom-proof.json
mv .image-private/sbom.cdx.json .image-policy/public/sbom.cdx.json
printf '{"schemaVersion":1,"scope":"PE017B policy foundation","imageDigest":"%s","configDigest":"%s","phase2Adoptable":false,"registryPush":false}\n' "$digest" "$config" > .image-policy/public/input.json
printf 'completed\n' > .image-private/outcome
