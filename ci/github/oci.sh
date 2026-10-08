#!/usr/bin/env bash
set -euo pipefail
umask 077
mkdir -p .oci .security/private .security/public .cache/trivy
trap 'rm -rf .security/private' EXIT
docker buildx build --platform linux/amd64 --provenance=false --sbom=false \
  --build-arg "VCS_REF=$GITHUB_SHA" --build-arg "VCS_SOURCE=https://github.com/$GITHUB_REPOSITORY" \
  --output type=oci,dest=.oci/image.tar,oci-mediatypes=true .
archive=$(sha256sum .oci/image.tar | cut -d ' ' -f1)
.security/github-oci-check .oci/image.tar "$archive" .oci/layout "$GITHUB_SHA" "$GITHUB_REPOSITORY" > .security/public/layout.json
digest=$(python3 -c 'import json; print(json.load(open(".security/public/layout.json"))["digest"])')
.security/oci-crane-check .oci/layout "$digest" > .security/public/reader.json
# Runtime uses the exact checked OCI, no registry fallback and no external network.
docker buildx build --platform linux/amd64 --network=none --provenance=false --sbom=false \
  --build-context "input=oci-layout://$PWD/.oci/layout@$digest" -f ci/oci-smoke/Dockerfile ci/oci-smoke
config=$(python3 -c 'import json; print(json.load(open(".security/public/reader.json"))["configDigest"])')
docker run --rm -e IMAGE_CONFIG="$config" --user "$(id -u):$(id -g)" -v "$PWD:/work" -w /work --entrypoint sh \
  aquasec/trivy:0.75.0@sha256:9db099105405c648166e6b94155eb32f8da12673cf1f455207f7385cc9a77283 -ec '
    trivy image --download-db-only --cache-dir .cache/trivy --timeout 5m --quiet > .security/private/db.log 2>&1
    : > .security/private/empty-ignore
    trivy image --input .oci/layout --cache-dir .cache/trivy --skip-db-update --timeout 5m --quiet --no-progress \
      --scanners vuln,secret,misconfig --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL \
      --ignorefile .security/private/empty-ignore --list-all-pkgs --format json \
      --output .security/private/image.json > .security/private/scan.log 2>&1
    .security/scan-gate -policy security/scan-policy.json -db .cache/trivy/db/metadata.json \
      -report .security/private/image.json -out .security/public/image-scan.json -scanner-exit 0 -image-config "$IMAGE_CONFIG"
    trivy convert --format cyclonedx --output .security/private/sbom.cdx.json .security/private/image.json > .security/private/sbom.log 2>&1
    .security/sbom-check .security/private/sbom.cdx.json .security/private/image.json "$IMAGE_CONFIG" > .security/public/sbom-proof.json
    cp .security/private/sbom.cdx.json .security/public/sbom.cdx.json
  '
python3 -I ci/github/release.py record
