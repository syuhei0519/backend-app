# Account runtime profile contract

`/server` accepts exactly the two reviewed Account identities below. A profile
name is a finite identity, not a free-form database or Collector selector.

| `ACCOUNT_RUNTIME_PROFILE` | workload namespace | DB host / database / role / owner | Collector |
| --- | --- | --- | --- |
| `current-existing-kind-account` | `learning-services-20261018` | `learning-account-db` / `account` / `account_app` / `account_app` | `otel-collector.observability.svc.cluster.local:4318` |
| `fresh-learning-account-release` | `learning-account-release` | `learning-account-db` / `account` / `account_app` / `account_app` | `account-release-otel-collector.observability.svc.cluster.local:4318` |

The shared and dedicated Collector Services both exist in the `observability`
namespace. Their destination/namespace pairs remain distinct. The shared
Collector accepts `account` only as the no-profile legacy transition identity;
an explicit current profile must use the actual
`learning-services-20261018` namespace. The dedicated Collector is paired only
with the fresh `learning-account-release` profile.

An empty profile preserves the pre-PLAT-211 image contract only during the
staged transition. It does not identify either current profile. A non-empty
unknown profile never falls back to legacy behavior. The compatible order is:

1. deploy an image that still accepts the no-profile legacy inputs;
2. ensure the paired Collector configuration accepts both transition resource
   identities;
3. update the manifests to set the finite profile and source `POD_NAMESPACE`
   and `POD_UID` from the Downward API.

The paired source inspection is pinned to
`application-manifest@b83720a9d5d3e15fdf91167a0cf0fc5dfd4b7278` and
`platform-gitops@6ddcf0b58b18c224cb14e85475bf112bf1a6fbe7`.
The backend change deliberately does not modify or deploy those repositories.
Before enabling the profiles, the manifest owners must update:

- `application-manifest/learning/existing-kind/services/resources.yaml`: add
  `ACCOUNT_RUNTIME_PROFILE=current-existing-kind-account` and replace the
  historical literal `POD_NAMESPACE=account` with `metadata.namespace`;
- `application-manifest/learning/account-release/chart/templates/release.yaml`
  and `learning/account-release/application-chart/templates/application.yaml`:
  add `ACCOUNT_RUNTIME_PROFILE=fresh-learning-account-release`; retain target
  namespace `learning-account-release`, `learning-account-db`, the dedicated
  Collector endpoint and the existing Downward API namespace/Pod UID;
- `platform-gitops/learning/existing-kind/managed/observability/resources.yaml`
  only if its stored resource identity needs the exact paired namespace. It
  already contains the dedicated `account-release-otel-collector` Service, so
  that Service is not a missing dependency.

Explicit profile mode accepts only `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`,
`PGPASSWORD` and `PGSSLMODE`. Every other non-empty `PG*` input, including
future pgx settings, a `DATABASE_URL`, a mismatched DB/role/namespace/Collector,
and unknown profile-prefixed settings are rejected before the pool or telemetry
SDK is initialized. Parse and pool-construction errors are fixed safe errors and
do not include input or Secret values. The OTel exporter environment validation
in `internal/telemetry` remains an independent second boundary.

`/readyz` reads `current_database()`, `current_user`, the database owner and the
schema history from PostgreSQL on every probe. `/server migrate` performs the
same database/role/owner check on its acquired connection before taking the
advisory lock or creating `schema_migrations`; identity rejection therefore
performs no writes even when the history table does not exist. Wrong credentials,
identity drift, schema drift and a transient outage return 503 from readiness,
while `/livez` remains 200 and a later readiness probe may recover.

Local synthetic tests prove the finite input guard, fixed-error non-disclosure,
Collector pair matrix, pre-DDL migration guard and HTTP readiness behavior. They
do not prove real cluster resources, SDK resource identity, two-profile boot,
DB-down recovery, wrong-Secret behavior or rollback; those remain acceptance
work and must not be reported as complete from unit results alone.
