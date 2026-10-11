# Order B2: dedicated image and release producer

Order uses Dockerfile.order, /order-server (including its migrate subcommand),
linux/amd64 and the same pinned builder/runtime bases as Account. Account's
runtime and v1 image/tag/artifact identities remain unchanged. Order SQL is
embedded by the existing B1 implementation. No Account migration files are copied
into the Order image.

The image matrix is fixed to backend/order with fail-fast=false. Each job has its
own checkout, OCI archive and reports under .images/<service>. A single shared
source scan is bound to repository/commit/run/attempt and checksum. ci-result
requires both complete artifacts, distinct image/config/archive identities and
matching source checksums. Publication also depends on this aggregate gate.

Runtime validation converts the validated OCI into Docker transport without a
rebuild, checks the loaded config ID, and runs migration, liveness/readiness and
restart against a newly created PostgreSQL fixture on an internal Docker network.
App containers run read-only, UID/GID 10001, cap-drop ALL and no-new-privileges.
Only randomly named resources created by this invocation are removed, including
the fixture's new anonymous volume. Existing databases and volumes are not used.
The import is only runtime transport: publication uses the original checked OCI
archive and requires its checksum again. Scan and CycloneDX SBOM use that same OCI.

Order release schema is core-platform/github-release/v2 with service=order,
source repository backend-app and image backend-app-order. Release tags are
 gha-order-<sha>-<run>-<attempt>; artifacts are checked-image-order-<attempt> and
published-evidence-order-<attempt>. Release asset names are unchanged within the
separate release. Release creation does not overwrite/reuse an existing release;
a collision fails. A partial two-service publication remains a failure and M0
requires both canonical published records. Service concurrency groups serialize
publication across runs without canceling either service.

Protected main push, ENABLE_GHCR_PUBLISH, ghcr-publish approval, and the existing
contents/packages permissions still guard publication. Proposal keeps the existing
manifest-update environment/token; Order targets only environments/local/order.yaml
on github/order-* and opens a PR. Missing Order values fail closed until M1 provides
them. No enable flag is changed. This task does not create credentials, publish a
real image, merge, dispatch publication or deploy.

The unchanged M0 consumer and synthetic fixtures are vendored under
ci/github/fixtures/m0 with exact commit and raw SHA-256 inventory in SOURCE.json.
They are executable test inputs, not production imports. Tests execute producer
record/publish logic using synthetic local bytes and mocked external transport,
then validate generated v2 records with the actual fixed consumer. The consumer's
own negative suite covers matrix omissions, canonical ZIP identity, registry/config,
peer archive, moved tags, source/main/run and checksum spoofing. Synthetic success
does not claim GHCR publication or deployed AC-3.

## Independent verification

Use Go 1.27.1 and Docker on the local Unix socket. Run bash ci/github/test.sh,
go vet ./..., go test -race -count=1 ./..., and build both commands. Pull only the
pinned PostgreSQL image, then run ORDER_DISPOSABLE_TEST=1 go test -race -count=1 ./...
for B1 HTTP/DB persistence and Account regression. Optional Collector/kind tests
remain separate. The OCI stage is bash ci/github/oci.sh for each RELEASE_SERVICE
with the normal GitHub identity variables, compiled .security validators and the
same .validation source-scan/identity pair. Use separate clean source directories
for the two services. No existing .oci/layout or publish-layout is reused.

## Design choices and limits

A single image with startup arguments was rejected by the adopted design. Separate
repositories would exceed scope. A transport conversion was chosen over rebuilding
an image for runtime tests; loaded config equality and independent OCI reader checks
bind the test to checked bytes. The full checked artifacts are downloaded once more
by ci-result to reject an absent member even if a matrix definition is accidentally
reduced; this adds network/storage cost.

Deployment impact is none for this backend-only diff under the inspected fixed
GitOps configuration: workloads track application-manifest main and values pin image
digests; no registry tag updater was found. Publication can create a separate
manifest PR after approval. Merging that later PR can cause automatic Argo sync.
Actual cluster state and out-of-repository automation are not observed here. AC-3,
Deployment Ready and aggregate Issue Completion remain Manager/later-stage gates.

## Optional build-time CA bundle

Ordinary GitHub CI needs no new Secret, variable or Environment. Both Dockerfiles
use the builder's standard trust when `build_ca` is absent. An environment with an
already authorized CA bundle may pass that existing file read-only as a BuildKit
secret, outside the source context:

```sh
docker buildx build --secret "id=build_ca,src=$SSL_CERT_FILE" -f Dockerfile.order .
# With an already configured buildctl worker:
buildctl build --frontend dockerfile.v0 --local context=. --local dockerfile=. \
  --opt filename=Dockerfile.order --secret "id=build_ca,src=$SSL_CERT_FILE" \
  --output type=oci,dest=order.oci.tar
# Existing GitHub OCI chain, optional local invocation:
OCI_BUILD_CA_FILE="$SSL_CERT_FILE" RELEASE_SERVICE=order bash ci/github/oci.sh
```

Only `go mod download` receives the secret and command-local `SSL_CERT_FILE`.
The subsequent test/build RUN and runtime stage do not mount it. TLS and Go module
checksum verification remain enabled, and module endpoints are unchanged. The
bundle must be PEM trust material appropriate for the environment; an empty bundle
fails immediately and a bundle lacking the required issuer fails TLS on a cold
build. Go can also consult standard certificate directories, so an unrelated bundle
does not force failure for endpoints already trusted by the standard store.

Secret mounts were selected over COPY/delete (which can retain bytes in layers),
ARG/ENV (persistent metadata), or truststore installation (unnecessary persistent
change). The BuildKit daemon's own registry trust is a separate prerequisite; this
option addresses only the Dockerfile dependency RUN. No trust provisioning or
permission changes are performed by the helper. Keep the bundle outside the build
context; do not log it or supply it as a build argument.

Secret contents do not invalidate BuildKit cache. Use a fresh worker or `--no-cache`
when verifying missing/valid/invalid trust or after CA rotation; a cached dependency
step is not evidence of current TLS acceptance. A trusted CA can authenticate
connections for that RUN: only use an existing authorized bundle. Normal runtime
trust is unchanged, and the custom CA cannot be used by the resulting application.

References: [Docker build secrets](https://docs.docker.com/build/building/secrets/)
and [secret mount reference](https://docs.docker.com/reference/dockerfile/#run---mounttypesecret).

## Scan provenance before record creation

`image-policy.py` runs the trusted scan-gate and SBOM validator against the same
private Trivy JSON after conversion. Each validator checks the raw ImageID against
the selected OCI config. The producer also checks OCI manifest/config bytes,
source labels, service entrypoint, shared source identity and run/attempt before
emitting the public summary. It never blesses an existing public image-scan.json.
A failed producer removes previous summary/proof outputs.

The summary carries imageIdentity (repository/source/run/attempt/service,
manifest/config/archive and source-scan checksum), rawScanSha256 and sbomSha256.
The separate image-proof.json seals that identity and exact summary/SBOM-proof
bytes; the SBOM proof carries the same raw-scan fingerprint. record(), ci-result
and publish() all require these bindings. Missing/foreign/malformed proofs and
pre-record summary swaps fail closed, even if a new record hashes the swapped
summary. Publication rechecks the original archive and its manifest/config first,
before any API or registry effects. Raw scans and trust inputs remain private.

These are trusted CI outputs, not signatures: an attacker controlling the trusted
producer or coherently forging every proof is outside this artifact-mixing
boundary. Hashing alone is not scan authentication. Account v1 and Order v2
release-record fields and M0/M1 consumer contracts are unchanged; historical
unbound checked-image artifacts cannot be newly recorded or published by this
producer. Existing published release consumption retains its versioned contract.
