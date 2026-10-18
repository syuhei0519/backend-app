# Order API and independent persistence

Related issue: https://github.com/syuhei0519/backend-app/issues/2

Order is an independent process (`cmd/order-server`) with immutable records.
It does not modify or call the Account service. Notification, lists,
updates, deletes, authentication and caller-specific authorization are not provided.
An optional [Order → Payment endpoint](order-payment-communication.md) calls the
existing Payment API for an existing Order without updating Order state.
This API is intended for an isolated lab behind a ClusterIP; external exposure
requires a separate access-control design.

## HTTP contract

`POST /api/orders` accepts `Content-Type: application/json`, optionally with
`charset=utf-8`. No other media parameters are accepted. Example request:

```json
{"account_id":1,"item_code":"SKU-001","quantity":2}
```

The response is `201`, after the database transaction commits, with
`Location: /api/orders/<id>` and the following JSON shape:

```json
{"id":1,"account_id":1,"item_code":"SKU-001","quantity":2,"created_at":"2026-10-10T00:00:00Z"}
```

IDs are positive signed 64-bit integers. `created_at` is the database timestamp,
serialized as UTC RFC3339 (fractional seconds may be present). Clients with
floating-point JSON numbers must preserve 64-bit IDs without loss of precision.
`GET /api/orders/{id}` returns the same representation, or `404` for an absent
record. The path ID must match `[1-9][0-9]*` and fit in a signed 64-bit integer.
Other methods on these routes return `405` with `Allow: POST` or `Allow: GET`.
Unknown paths return `404`. HEAD is not implicitly supported.

All three input keys are required and non-null:

| Key | Validation |
| --- | --- |
| `account_id` | Positive signed 64-bit integer |
| `item_code` | ASCII `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`; never trimmed |
| `quantity` | Integer from 1 through 999 |

Unknown keys (including `id` and `created_at`), duplicate keys (including escaped
spellings), missing/null fields, wrong types, decimal/exponent integer spellings,
empty/non-object/malformed JSON, trailing JSON/data, invalid UTF-8 and invalid
values return `400 validation_error`. Validation failures never invoke the Store.
The body limit is 16 KiB, including whitespace: larger JSON bodies return
`413 payload_too_large`. Missing/unsupported Content-Type returns
`415 unsupported_media_type`. Media type is checked before reading the body.

Errors have `{"error":{"code":"...","message":"..."}}` format. Codes include
`validation_error`, `payload_too_large`, `unsupported_media_type`,
`method_not_allowed`, `not_found`, `unavailable` (503), and `internal_error` (500).
Responses and process logs do not include SQL, connection strings, request bodies,
or credentials. POST has no idempotency key and is never automatically retried by
the service. A connection failure after commit can leave a created record even
if the caller did not receive 201; retrying POST can create a duplicate.

`account_id` is a historical reference, **not an existence or ownership guarantee**.
A positive ID for a nonexistent Account is accepted. No cross-database foreign key
or Account lookup is used. Deleting an Account retains the Order with its original
`account_id`; Order never cascades, restricts, creates, updates or deletes Accounts.

`GET /livez` returns 200 while the process is serving. `GET /readyz` checks Order
DB connectivity and exactly version 1 of `order_schema_migrations` within one
second, returning 200 or 503. SIGINT/SIGTERM changes readiness to 503, waits five
seconds, then drains HTTP for up to 15 seconds. Liveness remains 200 during drain.

## Process and database boundary

```sh
go build -o /tmp/order-server ./cmd/order-server
# Inject DATABASE_URL or PGHOST/PGPORT/PGDATABASE/PGUSER/PGPASSWORD/PGSSLMODE
# through the approved environment; never put credentials in command history.
/tmp/order-server migrate
/tmp/order-server
```

The server listens on `0.0.0.0:8080`; `ORDER_HTTP_ADDRESS` optionally overrides the
address (for example, loopback in tests). `LOG_LEVEL` accepts debug/info/warn/error.
Configuration is independent of the Account entrypoint. Migration runs only with
the `migrate` argument and exits without starting HTTP; unknown arguments exit 2.
Configuration/target/migration failures exit 1 with a sanitized message.

Use a dedicated PostgreSQL instance, database `orders` and LOGIN role `order_app`
with NOSUPERUSER/NOCREATEDB/NOCREATEROLE/NOINHERIT, owning only `orders`.
The configured and actual database/user are checked; the actual check runs on
all new physical connections, on server startup and before migration DDL.
The guard supplements PostgreSQL permissions; matching names do not identify a
safe physical host. Provisioning and choosing the approved host remain operator
responsibilities. No production database provisioning is performed by this code.
The lab owner role can perform DDL; a future DML-only runtime role needs its own
migration/authentication design.

The pool allows five connections, each with a three-second connect timeout.
Create/Get have three-second deadlines and parameterized SQL. Tables are
schema-qualified in `public` and search_path is fixed to `pg_catalog,public`.
Order migrations embed only `order-migrations/*.sql`. History is
`public.order_schema_migrations(version,applied_at)` and advisory lock ID is
`7742340082`. Account's embed, SQL, `schema_migrations`, lock `7742340081`, readiness
max-version 1, code and data are unchanged.

Migrations are forward-only: do not edit applied SQL, run down migrations or
manually mark failed versions successful. A rerun is a no-op with unchanged
history timestamps. Migration waits at most ten connection attempts (five-second
attempt deadline, two-second retry delay) inside an overall 170-second deadline.
Each SQL file and its history insert commit atomically under the session lock.
Cancellation rolls back using a separate five-second cleanup context. A possibly
locked connection is discarded if cancellation/unlock makes its state uncertain.
Unknown future versions fail closed. Deployment must stop on migration failure.

Independent image, release CI/GHCR, Helm and GitOps are subsequent work. Existing
CI configuration is unchanged: main can trigger the existing Account image
publish/manifest-proposal flows only under their configured variable and
Environment approval conditions. B1 does not produce an Order image or deploy it.

## Reproducible verification

Use Go 1.27.1, Docker on the local `/var/run/docker.sock`, and this locally present
PostgreSQL image (the harness never pulls):

```text
postgres@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f
```

Run in an approved disposable development environment:

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./...
test -z "$(gofmt -l .)"
ORDER_DISPOSABLE_TEST=1 go test -v -count=1 ./internal/store/orderpostgres ./tests/orderintegration
```

Without `ORDER_DISPOSABLE_TEST=1`, real DB tests explicitly skip; a skipped run
is not Integration evidence. The harness ignores external DSNs and Docker
contexts, uses only the local socket, creates random `order-b1-test-*` containers
with label `order-b1-disposable=true`, ephemeral random credentials, loopback-only
random ports and new anonymous volumes. Account and Order use separate engines.
It does not connect to, migrate or erase existing/shared databases or volumes.

The tests verify strict real HTTP validation/no writes, missing/deleted Account
references, Account CRUD/409/404/400, preserved sentinel columns and complete
Account migration history, two orderly Order process restarts with changed PIDs,
a DB restart retaining that test container's volume, startup/migration reruns,
shutdown health and DB outage 503. Migration tests cover fresh/no-op/concurrent
runs, lock contention/cancellation/recovery, interrupted DDL/history rollback and
retry, SQL constraints, wrong target rejection and future version rejection.

`testing.T.Cleanup` removes only each container created by that invocation using
`docker rm --force --volumes <exact-created-name>`; the temp process binary is
also removed. For an externally killed test, inspect only the labeled containers,
confirm their exact names belong to the interrupted invocation, and remove those
specific containers with the same command. Never use global prune, reused volumes,
existing containers, or a shared DB for these tests. The preexisting Account
`TestMigrationDisposableDatabase` retains its original opt-in/context/port guard;
without its separately approved target it remains skipped. Account migration and
CRUD are additionally exercised by the new isolated harness.

This verifies the B1 slice of AC-1/AC-2. AC-3 (Synced/Healthy/image digest) remains
unverified and belongs to the independent image and deployment work. Independent
Test/Review and Manager Readiness checks are still required before merge. Issue
completion requires the cross-repository aggregate and integrated deployment
validation; a B1 test pass or merge does not complete the Issue.
