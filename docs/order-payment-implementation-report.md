# Order → Payment backend implementation verification

Related: [backend-app#50](https://github.com/syuhei0519/backend-app/issues/50).
This is the backend implementer's Cloud verification summary, not independent
Test/Review or a product acceptance decision. No internal execution logs,
private task identities or credentials are included.

- Base: `e030275abbf52ecd27b7152ce597b3d89be3f830`.
- Tested implementation: `1016610f74d222284d70f3f4360be495b4ce16a0` (Git-published equivalent of the tested source tree `799fe2da70a23a624011142f2fc67cb1200f64cc`).
- Venue: isolated Cloud development environment; Go 1.27.1; local Docker;
  two newly created PostgreSQL instances and two actual compiled API processes.
- PostgreSQL input: `postgres@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`.
  Locally inspected image ID: `sha256:8164afa59e26be9f78959e538d6c9da8553d67e601d8ebd0d5e9cbf558c3986e`.
- Existing Order/Payment migrations and provider source were reused unchanged.
  No Account/Notification request or existing/shared DB mutation was performed.

## Observed results

| Check | Observation |
| --- | --- |
| `go test -race -count=1 -v ./internal/orderpayment ./internal/paymentclient ./internal/orderhttp ./internal/orderconfig ./cmd/order-server` | PASS: 15 top-level tests, 145 including subtests |
| `ORDER_PAYMENT_DISPOSABLE_TEST=1 go test -count=1 -run '^TestOrderPaymentLifecycle$' -v ./tests/orderpaymentintegration` | PASS: 11.926 s |
| `go test ./...` | PASS; separately opt-in DB/Kubernetes suites skipped unless enabled; not substitutes for those suites |
| `go vet ./...` | PASS |
| Order binary build, tracked Go formatting, `git diff --check`, OpenAPI YAML parsing/path/response checks | PASS |
| Cleanup | Test-owned API children terminated; created containers/volumes removed; remaining fixture-labeled containers 0 |

The real integration observed new success and decline records, identical serial
replay, eight concurrent requests producing one new record and seven replays,
same identity/time after restarting Order, changed amount/result/Order conflicts,
and missing Order/invalid input producing zero downstream calls and zero rows.
Existing create/get and repeated migration passed. With URL unset, the new route
returned 503 while readiness succeeded.

A relay confined to the integration fixture allowed Payment to commit and then
withheld the response. Order returned 504 after **2.001127363 s**, with exactly
one wire POST and one committed Payment row. Manual same-ID replay returned 200
and preserved one row. This demonstrates an unknown outcome and recovery; it does
not imply rollback or absence of a payment on timeout.

Contract timeout observations were **2.001143799 s** before headers and
**2.000323807 s** during a slow body, each with one wire POST. Earlier parent
cancellation/deadlines propagated. Other checks covered the unchanged Payment
HTTP provider, malformed/oversized/duplicate/trailing JSON, missing/invalid or
mismatched result fields, terminal status, compatible unknown extension fields,
fixed error mapping, rejected redirects, reset without resend and disabled proxy.

## Remaining validation and ownership

Service DNS communication in kind and manifest integration are **unverified**.
The shared manifest writer must finish and its accepted base must be fixed before
the separate Order-only configuration change. Proposed value and steps are in
[the communication runbook](order-payment-communication.md); shared migration
helpers must remain unchanged.

No cluster operation, deployment, image publication, main push, merge, security
configuration or CI/Ruleset change was performed. Independent Test/Review,
current-head CI and Manager assessment of all six AC and required integration
remain outstanding. This report must not close the Issue automatically.
