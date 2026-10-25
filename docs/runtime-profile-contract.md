# Account runtime profile contract

`/server` can run the two reviewed Account identities below. The profile name is
finite; it is not a free-form database or Collector selector.

| `ACCOUNT_RUNTIME_PROFILE` | namespace | DB host / database / role / owner | Collector |
| --- | --- | --- | --- |
| `existing-kind-account` | `account` | `postgresql` / `account` / `account_app` / `account_app` | `otel-collector.observability.svc.cluster.local:4318` |
| `learning-account-release` | `learning-account-release` | `learning-account-db` / `account` / `account_app` / `account_app` | `account-release-otel-collector.observability.svc.cluster.local:4318` |

An empty profile preserves the previous image contract only for the staged
transition: deploy a compatible image, make the Collector accept both resource
identities, then add the profile plus `POD_NAMESPACE`/`POD_UID` Downward API
inputs. A non-empty unknown profile never falls back to legacy behavior.

Explicit profile mode rejects a missing password, a `DATABASE_URL` or libpq
override, a mismatched DB/role/namespace/Collector, and unknown profile-prefixed
settings before the pool or telemetry SDK is initialized. Errors are fixed and
do not contain input or Secret values. The OTel exporter environment validation
in `internal/telemetry` remains an independent second boundary.

`/readyz` reads `current_database()`, `current_user`, the database owner and the
schema history from PostgreSQL on every probe. Wrong credentials, a wrong role
or database, ownership drift, schema drift, and a transient outage therefore
return 503. `/livez` remains 200 during a DB outage and the next readiness probe
can recover without restarting the process. The API response and request log do
not expose the driver error or supplied credential.
