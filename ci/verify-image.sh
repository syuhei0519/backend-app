#!/bin/sh
# registryのdigestとOCI source/revisionラベルを要求値へ照合する公開後確認。
# digest同一性とsource由来の照合であり、作者署名・入場制御の実装ではない。
set -eu

image=${1:?image is required}
expected_digest=${2:?expected digest is required}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

crane auth login -u "$CI_REGISTRY_USER" -p "$CI_REGISTRY_PASSWORD" "$CI_REGISTRY" >/dev/null
actual_digest=$(crane digest "$image")
test "$actual_digest" = "$expected_digest" || {
  echo "remote image digest did not match build metadata" >&2
  exit 1
}

crane config "$image" > "$work/config.json"
tr -d '[:space:]' < "$work/config.json" > "$work/config.compact.json"
grep -Fq "\"org.opencontainers.image.revision\":\"${CI_COMMIT_SHA}\"" "$work/config.compact.json" || {
  echo "image revision label did not match the commit" >&2
  exit 1
}
grep -Fq "\"org.opencontainers.image.source\":\"${CI_PROJECT_URL}\"" "$work/config.compact.json" || {
  echo "image source label did not match the project" >&2
  exit 1
}
