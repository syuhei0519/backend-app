# Order B2: dedicated image and release producer

Order uses Dockerfile.order, /order-server (including its migrate subcommand),
linux/amd64 and the same pinned builder/runtime bases as Account. Account's
Dockerfile and v1 image/tag/artifact identities remain unchanged. Order SQL is
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
