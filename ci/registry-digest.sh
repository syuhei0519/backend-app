#!/bin/sh
# registry参照の結果を終了コードで分類する。ci/publish-oci.shなどが呼ぶ。
# 明示的な404を新規公開の条件にし、認証/通信異常を「未存在」と誤認しない。
set -eu

image=${1:?image is required}
registry=${CI_REGISTRY:?CI_REGISTRY is required}
repository=${image%:*}
tag=${image##*:}
path=${repository#${registry}/}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

basic_auth=$(printf '%s:%s' "$CI_REGISTRY_USER" "$CI_REGISTRY_PASSWORD" | base64 | tr -d '\n')
wget -qO "$work/token.json" --header="Authorization: Basic $basic_auth" \
  "https://gitlab.com/jwt/auth?service=container_registry&scope=repository:${path}:pull"
token=$(sed -n 's/.*"token":"\([^"]*\)".*/\1/p' "$work/token.json")
test -n "$token"

if ! wget -qO /dev/null --server-response \
  --header="Authorization: Bearer $token" \
  --header='Accept: application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json' \
  "https://${registry}/v2/${path}/manifests/${tag}" 2>"$work/headers"; then
  grep -Eq 'HTTP/[0-9.]+ 404' "$work/headers" && exit 10
  exit 1
fi

digest=$(sed -n 's/^[[:space:]]*[Dd]ocker-[Cc]ontent-[Dd]igest:[[:space:]]*\(sha256:[0-9a-f]\{64\}\).*/\1/p' "$work/headers" | tail -n 1)
test -n "$digest"
printf '%s\n' "$digest"
