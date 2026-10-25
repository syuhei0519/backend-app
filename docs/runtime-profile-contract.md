# Account runtime profile contract

`/server` accepts exactly the two reviewed Account identities below. A profile
name is a finite identity, not a free-form database or Collector selector.

| `ACCOUNT_RUNTIME_PROFILE` | workload namespace | DB host / database / schema | runtime role | migration role / schema owner | DB owner | Collector |
| --- | --- | --- | --- | --- | --- | --- |
| `current-existing-kind-account` | `learning-services-20261018` | `learning-account-db` / `account` / `public` | `account_app` | `account_app` / unchanged legacy owner | `account_app` | `otel-collector.observability.svc.cluster.local:4318` |
| `fresh-account` | `fresh-account` | `account-postgresql.fresh-account.svc` / `account` / `account` | `account_runtime` | `account_migrator` / `account_migrator` | `postgres` | `otel-collector.fresh-observability.svc.cluster.local:4318` |

The current and fresh Collector destinations remain distinct. The shared
Collector accepts `account` only as the no-profile legacy transition identity;
an explicit current profile must use the actual
`learning-services-20261018` namespace. The fresh profile uses the exact
`fresh-account` namespace and the Collector in `fresh-observability`.

The command selects the database operation before configuration is loaded.
Plain `/server` resolves the finite runtime role; `/server migrate` resolves the
finite migration role. `PGUSER` cannot choose a role, profile aliases are not
accepted, and a runtime profile cannot enter the migration path. The fresh
schema is explicitly `account`: CRUD and migration history are schema-qualified,
and the migration transaction sets a quoted local search path before applying
the embedded SQL. `DATABASE_URL` is not a fallback in explicit profile mode.

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
- the fresh application manifests: set `ACCOUNT_RUNTIME_PROFILE=fresh-account`,
  use `account-postgresql.fresh-account.svc`, set the runtime or migration role
  for the matching operation, and retain Downward API namespace/Pod UID;
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

`/readyz` reads `current_database()`, `current_user`, the database owner, schema
owner and schema history from PostgreSQL on every probe, and accepts only the
runtime operation identity. `/server migrate` performs the migration-specific
database/role/owner/schema-owner check on its acquired connection before taking
the advisory lock or creating `schema_migrations`; identity rejection therefore
performs no writes even when the history table does not exist. Wrong credentials,
identity drift, schema drift and a transient outage return 503 from readiness,
while `/livez` remains 200 and a later readiness probe may recover.

Local synthetic tests prove the finite input guard, fixed-error non-disclosure,
Collector pair matrix, pre-DDL migration guard and HTTP readiness behavior. They
do not prove real cluster resources, SDK resource identity, two-profile boot,
DB-down recovery, wrong-Secret behavior or rollback; those remain acceptance
work and must not be reported as complete from unit results alone.
