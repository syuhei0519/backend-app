#!/bin/sh
# scanの内側成功と外側資源判定失敗をstubで再現し、成功record/SBOMが下流へ流れないことを確認する。
# テストの一時directory/偽commandは終了trapで回収する。保存済み受入証跡とは別の作業用テストソース。
# Exercise a successful inner scan followed by a failed outer budget guard.
set -eu
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
for scenario in passed budget-failed sbom-failed identity-unavailable; do
 dir="$fixture/$scenario"
 mkdir -p "$dir/ci" "$dir/.security"
 cp ci/run-image-scan.sh "$dir/ci/"
 cat > "$dir/ci/run-with-oci-budget.sh" <<'SH'
#!/bin/sh
printf 'completed\n' > .image-private/outcome
printf '{}\n' > .image-policy/public/sbom.cdx.json
printf '{}\n' > .image-policy/public/input.json
case "$SCENARIO" in
 budget-failed) exit 2;;
 sbom-failed) printf 'sbom-invalid\n' > .image-private/outcome; exit 1;;
esac
SH
 cat > "$dir/.security/scan-record" <<'SH'
#!/bin/sh
printf '%s' "$1" > .image-policy/public/observed-outcome
if [ "$SCENARIO" = identity-unavailable ]; then exit 1; fi
printf '{}\n' > .image-policy/public/record.json
SH
 chmod +x "$dir/.security/scan-record"
 export SCENARIO="$scenario"
 status=0
 (cd "$dir"; sh ci/run-image-scan.sh) || status=$?
 test ! -e "$dir/.image-private"
 case "$scenario" in
 passed) test "$status" -eq 0; test -f "$dir/.image-policy/public/sbom.cdx.json"; test -f "$dir/.image-policy/public/record.json"; test ! -s "$dir/.image-policy/public/observed-outcome";;
 budget-failed) test "$status" -ne 0; test "$(cat "$dir/.image-policy/public/observed-outcome")" = scanner-unavailable;;
 sbom-failed) test "$status" -ne 0; test "$(cat "$dir/.image-policy/public/observed-outcome")" = sbom-invalid;;
 identity-unavailable) test "$status" -ne 0; test ! -e "$dir/.image-policy/public/record.json"; test -f "$dir/.image-policy/public/failure.json";;
 esac
 if [ "$status" -ne 0 ]; then test ! -e "$dir/.image-policy/public/sbom.cdx.json"; test ! -e "$dir/.image-policy/public/input.json"; fi
done
printf 'Scan finalizer orchestration passed\n'
