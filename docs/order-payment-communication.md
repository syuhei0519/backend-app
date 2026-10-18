# Order → Payment synchronous communication

Related issue: [backend-app#50](https://github.com/syuhei0519/backend-app/issues/50).
The [OpenAPI contract](order-openapi.yaml) defines the wire format. This is an
isolated lab API; authentication and caller authorization require separate work.

## Request and result

`POST /api/orders/{id}/payments` accepts four required non-null fields:

```json
{"request_id":"550e8400-e29b-41d4-a716-446655440000","amount_minor":1000,"currency":"JPY","simulate_result":"success"}
```

The path must be a positive int64 in `[1-9][0-9]*` form. `request_id` must be a
canonical lowercase non-nil UUID, `amount_minor` a positive int64, currency `JPY`,
and simulate_result `success` or `decline`. The JSON/media/16 KiB rules are the
same as [Order creation](order-api.md). Unknown fields (including `order_id`),
duplicates including escaped keys, null, invalid UTF-8, trailing data, decimal
and exponent integer spellings are rejected before either dependency is called.
Method errors return 405 with `Allow: POST`; invalid input returns 400, oversized
input 413, unsupported media 415.

After validation, the service reads the Order and sends exactly one
`POST /api/payments` with `order_id` taken from the path. Missing Order returns
404; Order DB errors keep the existing sanitized 503/500 responses. None call
Payment. There is no Order write, transaction held over HTTP, new table,
migration, cross-database foreign key, Saga or automatic retry.

The response contains Payment's validated `id`, `request_id`, `order_id`,
`amount_minor`, `currency`, `simulate_result`, `status` and `created_at`.
New records return 201; identical replays 200. `success` produces `succeeded`;
`decline` produces `declined`, including on replay. A recorded decline is not a
successful payment. `Location` and other downstream headers are not forwarded.
The client requires all fields, positive id, valid nonzero timestamp, exact
input identity and matching terminal status; it accepts bounded unknown future
fields but rejects duplicate keys at every depth and trailing JSON. Response
bodies are limited to 16 KiB and always closed.

Payment's existing persistent unique `request_id` and input comparison provide
idempotency. Identical sequential/concurrent requests and requests after Order
restart return the same id/status/time with one Payment row. Changed valid input
with the same request_id returns 409 `idempotency_conflict`; invalid input is
rejected with 400 first. A different request_id is a different attempt, even for
the same Order. No Order-side idempotency store or expiration is introduced.

## Deadlines and recovery

After input validation the route has a five-second context budget, including
Order's existing three-second Get budget. A separate two-second child deadline
covers Payment DNS, connect, request and response headers/body. Earlier parent
deadlines/cancellation propagate. Server read/write timeouts remain ten seconds.
These deadlines bound work subject to ordinary OS scheduling delays.

All error bodies use `{"error":{"code":"...","message":"..."}}`, with fixed
HTTP status text as message. Raw downstream bodies, URLs, SQL and credentials
are never returned.

| Condition | HTTP/code |
| --- | --- |
| URL unset | 503 `payment_unconfigured` |
| Downstream deadline | 504 `payment_timeout` |
| Non-timeout transport failure or valid Payment unavailable response | 503 `payment_unavailable` |
| Valid Payment conflict | 409 `idempotency_conflict` |
| Redirect, unexpected HTTP status, invalid error/resource, oversized or mismatched response | 502 `payment_bad_response` |

503/504 mean the outcome may be unknown: Payment might have committed before its
response was lost. Do not report unpaid, rollback success, or retry with a new
request_id. To recover, manually send the same request_id and identical input;
a terminal result is returned as 200 if it was already recorded, otherwise 201.
The client disables proxies, redirects and implicit replayable POST bodies and
adds no idempotency headers or retry middleware.

## Configuration and later manifest integration

`ORDER_PAYMENT_URL` is optional. Empty leaves existing create/get/readyz and
`order-server migrate` working; only valid new payment requests return 503.
A configured value must be `http://host:port` with an explicit port 1–65535;
userinfo, path (even `/`), query, fragment and invalid hosts are rejected using
a fixed configuration error. It is an operator-selected origin, never a request
parameter. The client is initialized once per process and closes idle connections
on shutdown. Payment health is not part of Order readiness.

For a later same-namespace kind integration, use `http://learning-payment:8080`.
The manifest owner must wait for the monitoring shared writer's accepted base.
Suggested chart setting is `orderPaymentURL: ''`, opt-in only for service `order`,
rejecting nonempty values for other services. Inject `ORDER_PAYMENT_URL` only
into the Order API template. Never modify the shared migration `_helpers.tpl`,
add this variable to migration jobs, or change other services' source/image pins.
No manifest or cluster change is part of this backend implementation.

## Cloud verification

Use Go 1.27.1, local Docker socket, and the locally available pinned image:

```text
postgres@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f
```

```sh
go test -race ./internal/orderpayment ./internal/paymentclient ./internal/orderhttp ./internal/orderconfig ./cmd/order-server
go vet ./internal/orderpayment ./internal/paymentclient ./internal/orderhttp ./internal/orderconfig ./cmd/order-server ./tests/orderpaymentintegration
ORDER_PAYMENT_DISPOSABLE_TEST=1 go test -count=1 -run '^TestOrderPaymentLifecycle$' -v ./tests/orderpaymentintegration
```

The opt-in integration harness creates two fresh local PostgreSQL containers
(`orders/order_app`, `payments/payment_app`), applies unchanged migrations, and
builds/runs both actual API binaries. It ignores external DSNs and remote Docker
contexts, never pulls, binds random loopback ports and uses fresh anonymous
volumes and generated fixture credentials. It touches no existing data/roles.
Cleanup kills only its API children and removes only successfully created exact
container names and their volumes. A failed cleanup is a test failure. An
interrupted invocation requires checking its exact labeled container inventory;
never use global prune or remove unrelated volumes.

The integration checks success/decline, serial/parallel/restart replay, input
conflict, missing Order/invalid input with zero downstream calls, unchanged
create/get, repeated migration and unset-URL readiness. A fixture-only relay
lets Payment commit, then suppresses that response. It measures 504 after two
seconds, exactly one wire POST, one committed row, and manual same-ID recovery
with 200. Production code does not use that relay. Contract tests separately
exercise the unchanged Payment HTTP provider, no headers/slow body deadlines,
malformed responses, redirect rejection, connection failure and parent cancel.
A skipped integration invocation is not real DB evidence.

## Separate kind acceptance

The Manager's independent Test must confirm the fixed backend source/image and
accepted manifest values against the approved existing cluster/namespace/owner.
Update only the authorized Order workload and record Service `learning-payment`,
its ready EndpointSlice, actual imageIDs and `ORDER_PAYMENT_URL` origin. Enter
through Order Service and check success, decline and same-ID replay in one
session. Use an already approved in-cluster diagnostic for DNS if available;
otherwise record that diagnostic limitation with the Service HTTP evidence.
Restore only the prior Order image/config if needed, and clean up only owned
forwarding processes. Never delete shared namespaces, Payment/DB/PVC/Secrets or
change security policies. No kind observation is claimed by the Cloud tests.
Independent Test/Review, current-head CI and the Manager's full AC/integration
assessment remain required; this implementation alone does not close the issue.
