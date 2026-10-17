# Payment learning API

Related: [API #39](https://github.com/syuhei0519/backend-app/issues/39),
[deployment #21](https://github.com/syuhei0519/backend-app/issues/21).
This is a small synchronous fake payment service. It makes no external payment
request. Order ID is a historical reference without a foreign key or lookup.
Different request IDs may reference the same Order.

## Fixed source and local inputs

Implemented source commit: `baa711c7af888ad081cf168305ac0f684920dd35`.
Source repository: `https://github.com/syuhei0519/backend-app`.
Dockerfile: `Dockerfile.payment`, SHA-256
`ae3935eab9ec574134a4174631e35ee669c3f8185ae8d6deba15cbe54436158c`.
Runbook: `docs/payment-api.md` in this branch; obtain its immutable commit
and blob identity from the implementation Handoff. Subsequent documentation
commits leave the source commit's implementation paths unchanged.
Target image platform: `linux/amd64`. Builder and runtime are digest pinned.

From a matching clean checkout containing this runbook:

```sh
git status --porcelain
git diff --exit-code baa711c7af888ad081cf168305ac0f684920dd35 HEAD -- Dockerfile.payment cmd/payment-server internal/payment internal/paymentconfig internal/paymenthttp internal/paymenttest internal/store/paymentpostgres payment-migrations tests/paymentintegration
PAYMENT_SOURCE_REF=$(git rev-parse HEAD)
PAYMENT_OUT=$(mktemp -d)
go build -trimpath -ldflags="-X main.buildCommit=$PAYMENT_SOURCE_REF -X main.version=0.0.0+git.$PAYMENT_SOURCE_REF" -o "$PAYMENT_OUT/payment-server" ./cmd/payment-server
docker build --platform linux/amd64 -f Dockerfile.payment --build-arg VCS_REF="$PAYMENT_SOURCE_REF" --build-arg VCS_SOURCE=https://github.com/syuhei0519/backend-app --iidfile "$PAYMENT_OUT/payment-image.id" -t payment-learning:local .
cat "$PAYMENT_OUT/payment-image.id"
```

Stop if the checkout is dirty or the implementation diff is nonempty. Outputs
stay outside the source context. Optional enterprise build trust uses BuildKit
secret `build_ca`, following the existing Dockerfile pattern.
Record the actual source ref, image ID, platform and Dockerfile hash for #21.

No app image was built or run during implementation; local image ID is
**unverified / unavailable**. This runbook supplies reproducible inputs, not
image publication or deployment evidence. Payment is not added to the formal
release/GHCR pipeline.

## Database and process

Payment requires its own PostgreSQL instance, database `payments`, and LOGIN
owner role `payment_app` with NOSUPERUSER, NOCREATEDB, NOCREATEROLE, NOINHERIT.
Provision these only in an explicitly approved environment. No credentials
or resources were created in the implementation workspace.

Supply `DATABASE_URL` through the approved environment's private mechanism;
if absent, pgx uses standard PG environment variables. Never put a DSN/password
in source, shell history, reports or this document. The configured DB/user and
actual identity on every physical connection must match. Stop on target
rejection or migration failure; do not substitute another database or role.

```sh
"$PAYMENT_OUT/payment-server" migrate
PAYMENT_HTTP_ADDRESS=127.0.0.1:18083 "$PAYMENT_OUT/payment-server"
```

Normal serve takes no arguments; `migrate` is the only accepted argument.
Default address is `0.0.0.0:8080`; override with `PAYMENT_HTTP_ADDRESS`.
`LOG_LEVEL` accepts debug/info/warn/error. Migration owns only
`public.payments` and `public.payment_schema_migrations`, version 1,
advisory lock 7742340083. Migration SQL and history insertion commit together,
reruns preserve applied_at, unknown history versions fail. No rollback/down
migration exists. Account/Order/shared migrations remain independent.

For local container execution in an approved environment, pass the private
environment file by reference and use the approved database network:

```sh
docker run --rm --env-file "$PAYMENT_PRIVATE_ENV_FILE" payment-learning:local migrate
docker run --rm --env-file "$PAYMENT_PRIVATE_ENV_FILE" -p 127.0.0.1:18083:8080 payment-learning:local
```

The environment file must use a DB address reachable from the container.
HTTP readiness requires the exact owned migration version; liveness is process
only. During SIGTERM/SIGINT drain, readiness becomes 503, then HTTP shuts down.

## HTTP contract

```sh
curl -i http://127.0.0.1:18083/livez
curl -i http://127.0.0.1:18083/readyz
curl -i -H 'Content-Type: application/json' -d '{"request_id":"550e8400-e29b-41d4-a716-446655440000","order_id":1,"amount_minor":1000,"currency":"JPY","simulate_result":"success"}' http://127.0.0.1:18083/api/payments
curl -i http://127.0.0.1:18083/api/payments/1
```

POST requires exactly request_id, order_id, amount_minor, currency and
simulate_result. request_id is a non-nil lowercase canonical UUID; order_id and
amount_minor are positive signed int64; currency is JPY; simulate_result is
success or decline. The response preserves all input values and adds positive
int64 id, terminal status succeeded/declined and a DB timestamp in UTC RFC3339.

New attempts return 201 only after commit, including simulated declines.
A valid identical request_id replay returns 200 with the same resource,
timestamp, status and Location. Comparison uses decoded input values, so JSON
key order/whitespace do not matter. Any changed valid input for an existing
request_id returns 409 idempotency_conflict without changing the row.
Invalid replays still fail validation first. No Idempotency-Key header is used.

UNIQUE request_id is service wide and has no TTL. INSERT ON CONFLICT is followed
by a separate SELECT at READ COMMITTED, after the winning insert commits.
Only a new insert transitions received to a terminal state inside the same
transaction. received is never a public pending result. Failure before commit
rolls back; response loss after commit is resolved using the same request_id.
DB state survives API process restarts.

GET returns 200 or 404. Invalid JSON, duplicate/unknown/missing/null keys,
invalid UTF-8 and non-integer number spelling return 400. The body limit is
16 KiB (413), content type application/json optionally charset=utf-8 (415).
Wrong methods return 405 with Allow, store unavailability 503, internal errors
500. Errors use a sanitized `error.code`/`error.message` envelope.
No list/update/delete, worker, automatic retry, Saga or Kafka is included.

## Verification and independent integration

Implementation verification passed on the source commit above:

```sh
go test -race ./internal/payment ./internal/paymenthttp ./internal/paymentconfig ./internal/store/paymentpostgres ./cmd/payment-server
go vet ./internal/payment ./internal/paymenthttp ./internal/paymentconfig ./internal/store/paymentpostgres ./internal/paymenttest ./cmd/payment-server ./tests/paymentintegration
go build -o "$PAYMENT_OUT/payment-server" ./cmd/payment-server
go test -c -o "$PAYMENT_OUT/paymentintegration.test" ./tests/paymentintegration
```

Integration was compiled only, never executed here. Product AC-1 (terminal
results), AC-2 (durable deduplication), AC-3 (Order reference/owned migration)
remain unverified pending independent Test. Unit mocks do not prove PostgreSQL
concurrency, restart persistence or integration acceptance.

Independent Test must use an explicitly approved isolated Kubernetes context
with kubectl, Go, loopback forwarding and the pinned PostgreSQL image available.
No external operator DSN or reused DB/volume is accepted. Only after that
environment and credential handling are approved:

```sh
PAYMENT_DISPOSABLE_TEST=1 PAYMENT_TEST_KUBE_CONTEXT="$APPROVED_ISOLATED_CONTEXT" go test -count=1 -run '^TestDisposablePaymentLifecycle$' -v ./tests/paymentintegration
```

The opt-in fixture creates exactly one fresh `payment-test-<random>` namespace,
one PostgreSQL pod with emptyDir, one private ephemeral Secret and the
payments/payment_app identity. It runs one API/DB lifecycle: success/decline,
sequential and concurrent duplicates/conflicts, row counts, arbitrary positive
Order reference, initial/no-op migration, one API OS-process restart with a
changed PID using the same DB, and DB-unavailable 503. It does not require a
DB-server restart. It touches no Account/Order service.

Fixture support follows the existing isolation pattern but uses Kubernetes
instead of Order's Docker fixture to honor the approved test-environment
boundary. Cleanup kills only its own API/port-forward processes and deletes
only the exact namespace it successfully created. If cleanup fails, stop and
have the test owner inspect/delete that exact recorded namespace in that same
context; never broad-delete namespaces, containers or volumes.

A skipped opt-in test is not integration evidence. Current dedicated test
environment readiness is blocked upstream by /dev/kmsg; implementation did not
attempt to repair or bypass it. Independent Test/Review, required PR CI and
Manager readiness/integration decisions remain pending.
