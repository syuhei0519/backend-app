# Notification learning API (#41)

Independent synchronous fake notification service. `order_id` is an immutable
historical positive int64; no Order lookup or cross-service foreign key is made.
The sink is a durable PostgreSQL receipt, with no external delivery.

## Source and local build handoff for deployment #22

Implemented source commit: `a651f95955f77ed123e564136e3b2d723d6c092a` (full SHA).
Checkout that revision to reproduce the source; this runbook is added by the
following documentation commit on the same PR. Dockerfile identity:
`Dockerfile.notification` at the source revision. Runbook identity:
`docs/notification-api.md` at the final PR head. Resolve the full final head with
`git rev-parse HEAD` and retain it alongside the image identity in deployment #22.
The PR records the exact final head/base/tree.

Platform: `linux/amd64`, Go 1.27.3; pinned builder/runtime bases match the Order
Dockerfile. Runtime UID/GID: 10001:10001. From the repository root:

```sh
SOURCE_COMMIT=a651f95955f77ed123e564136e3b2d723d6c092a
test "$(git rev-parse HEAD)" = "$SOURCE_COMMIT"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags="-X main.buildCommit=$SOURCE_COMMIT -X main.version=0.0.0+git.$SOURCE_COMMIT" \
  -o /tmp/notification-server ./cmd/notification-server
# Run only in an environment approved for local app image builds:
docker build --platform linux/amd64 -f Dockerfile.notification \
  --build-arg VCS_REF="$SOURCE_COMMIT" \
  --build-arg VCS_SOURCE=https://github.com/syuhei0519/backend-app \
  -t notification-learning:local .
docker image inspect notification-learning:local --format '{{.Id}}'
```

An optional build-only CA bundle uses `--secret id=build_ca,src=<approved-file>`.
No app image build was executed for this implementation. Local image ID and
runtime startup measurements are pending the separately authorized environment;
there is no GHCR/release artifact. Carry source SHA, final runbook SHA, platform,
local image ID and startup input names into deployment #22 before running it.

## Configuration, migration and serving

The approved environment must supply credentials privately. Never paste a DSN or
password into tracked files or public logs. `DATABASE_URL` or existing pgx `PG*`
settings must target a dedicated `notifications` database as `notification_app`.
Configured identity and actual identity on every connection must both match.
Use a dedicated instance and LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT
owner role. Provisioning is a separate environment action.

```sh
# With approved private DATABASE_URL / PG* already present:
/tmp/notification-server migrate
NOTIFICATION_HTTP_ADDRESS=0.0.0.0:8080 LOG_LEVEL=info /tmp/notification-server
curl -i http://127.0.0.1:8080/livez
curl -i http://127.0.0.1:8080/readyz
curl -i http://127.0.0.1:8080/api/notifications \
  -H 'Content-Type: application/json' \
  --data '{"request_id":"550e8400-e29b-41d4-a716-446655440001","order_id":1,"recipient":"demo-user","message":"Order accepted","simulate_result":"success"}'
curl -i http://127.0.0.1:8080/api/notifications/1
```

Default listen: `0.0.0.0:8080`; `NOTIFICATION_HTTP_ADDRESS` overrides it.
`LOG_LEVEL` accepts debug/info/warn/error. No argument starts serving; `migrate`
is the only accepted argument. Migrate owns only `notification-migrations/`,
`public.notifications`, `public.notification_sink_receipts` and
`public.notification_schema_migrations`. Version is exactly 1, advisory lock
7742340084, bounded wait/migration deadline 170 seconds. Migration SQL and history
commit atomically; rerunning version 1 preserves its applied timestamp. Higher
unknown history versions stop migration; readiness rejects any version/count
other than one version-1 record. Stop on wrong target, unknown version or failed
migration; inspect the dedicated environment rather than editing history or
running SQL against Account/Order. Migrations are forward only. Before commit,
failure rolls back; after commit, reuse the same request_id to resolve response
loss. No automatic retry or compensation is performed.

`/livez` reports process liveness; `/readyz` checks owned migration readiness and
returns 503 during SIGTERM/SIGINT draining. Shutdown uses the existing five-second
drain and bounded graceful close. DB driver/SQL/DSN errors are sanitized.

## Wire and persistence contract

POST `/api/notifications` requires exactly request_id, order_id, recipient,
message and simulate_result. request_id is a non-nil lowercase canonical UUID;
order_id is a positive int64; recipient matches ASCII
`[A-Za-z0-9][A-Za-z0-9._-]{0,63}`; message is nonempty UTF-8 of at most 1024 bytes
with no trimming; simulate_result is success or failure.

Narrow design deviation: message U+0000 is rejected with 400 because PostgreSQL
TEXT cannot store it. All other message whitespace is preserved.

Requests use application/json with optional UTF-8 charset, a 16 KiB body limit,
and reject duplicate/unknown/missing/null keys, non-integer order IDs and trailing
JSON/data. New accepted requests return 201 with Location; success yields sent,
failure yields failed. Both are saved terminal business results. Responses include
all input fields, positive id and DB-created UTC RFC3339 timestamp. GET by positive
canonical decimal id returns 200 or 404. Exact valid replays return 200 with the
same resource and Location. Changed decoded input under the same request_id
returns 409 `idempotency_conflict` without modifying it. A new request_id with the
same order_id is permitted.

The transaction inserts received with `ON CONFLICT(request_id) DO NOTHING`.
The existing-key path uses a separate READ COMMITTED SELECT after conflict
resolution and compares all typed input fields. Only the new-row path creates
one receipt and transitions to sent/failed. Receipt primary key and deferred
result/status foreign key protect durable sink consistency. Commit precedes the
HTTP response. No pending row is publicly committed by the API; state, identity
and receipts survive API restart in PostgreSQL. This models one durable fake
invocation, including failed outcomes, per accepted request.

Errors use `{"error":{"code":"...","message":"..."}}`: validation 400,
conflict 409, oversized body 413, unsupported media type 415, store unavailable
503, internal failure 500. Wrong method is 405 with Allow. Unsupported paths are
404. No list/update/delete API is provided.

## Bounded validation and independent Integration

Measured for this implementation with Go 1.27.1:

```sh
go test ./internal/notification ./internal/notificationhttp \
  ./internal/notificationconfig ./internal/store/notificationpostgres \
  ./cmd/notification-server
go vet ./internal/notification ./internal/notificationhttp \
  ./internal/notificationconfig ./internal/store/notificationpostgres \
  ./cmd/notification-server ./internal/notificationtest ./tests/notificationintegration
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -o /tmp/notification-server ./cmd/notification-server
go test -c -o /tmp/notificationintegration.test ./tests/notificationintegration
```

Units, static checks, binary build and fixture compilation passed. No DB fixture
was run, no DB credentials/resources were created, and no app image was built.
Compilation is not product Integration evidence. All three Issue AC remain
unverified until independent Test/Review.

The owned fixture requires an explicitly approved **dedicated Kubernetes test
environment**, kubectl, an approved context, the pinned PostgreSQL image
`postgres@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`,
local port-forward access and Go. It never accepts an external operator DSN.
Only there, after environment and credential approval:

```sh
NOTIFICATION_DISPOSABLE_TEST=1 \
NOTIFICATION_TEST_APPROVED=dedicated-kubernetes \
NOTIFICATION_TEST_CONTEXT='<approved-dedicated-context>' \
  go test -count=1 -run '^TestOwnedLifecycle$' -v ./tests/notificationintegration
```

Do not execute this command in the implementation workspace. The fixture creates
one unpredictable notification-test-* namespace, one PostgreSQL pod using emptyDir,
a nonprivileged owned login/database and a loopback port-forward. Cleanup deletes
only its exact created namespace (including pod and ephemeral data) and closes its
port-forward. It never reuses an existing namespace/pod/volume. On uncertain create
or cleanup failure, stop and inspect that exact fixture resource before any retry;
never use broad label/name wildcard deletion. Private credential/resource/log
metadata must stay outside public Git.

One lifecycle measures initial/no-op migration history, sent and failed responses,
GET, exact replay, changed-field conflict, concurrent duplicates with one creation,
one receipt per accepted request, API process restart with changed PID and the
same DB, persisted GET/replay, and DB-unavailable 503. Account/Order regression,
DB-server restart and broad release suites are outside this fixture.

Independent Test must measure:

- AC-1: テストsinkの成功/失敗をAPIから確認できる
- AC-2: 同一要求を再送してもsinkに重複送出しない
- AC-3: 再起動後も受付済み状態を取得できる

The dedicated Kubernetes environment remains unverified; the reported /dev/kmsg
startup blocker is separate from code completion. Normal required PR CI remains
unchanged; an external Trivy DB outage must be reported independently. This source
handoff grants no merge, deployment, publication, credential or Issue-close action.
