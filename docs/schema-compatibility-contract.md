# Schema compatibility contract

This contract applies independently to Account, Order, Payment, and Notification.
The current runtime is compatibility generation C0 and supports exactly migration
history `{1}`. The empty `SCHEMA_COMPATIBILITY_PROFILE` is strict C0. The only
other accepted value, `prepared-v1-v2`, identifies a compatibility-prepared
binary whose unchanged C0 columns have been tested against `{1}` and `{1,2}`.
Unknown profile names fail before a database pool is returned.

| Observed history | Classification | strict C0 Ready | prepared Ready | migration |
| --- | --- | --- | --- | --- |
| database unavailable/read failure | `unavailable` | no | no | no |
| history table absent | `no-table` | no | no | create the table, then treat as empty |
| `{}` | `empty` | no | no | apply migration 1 |
| `{1}` | `c0-v1` | yes | yes | safe idempotent no-op |
| `{1,2}` | `expand-v1-v2` | no | yes | no; version 2 is externally staged/test-only |
| `{2}` | `missing-base-v2` | no | no | no |
| `{1,3}` or any non-contiguous/duplicate history | `gap` | no | no | no |
| contiguous history beyond prepared version 2 | `unknown-future` | no | no | no |

The table is stricter than `max(version) >= minimum`. Unknown or partially
observed histories fail closed. No version 2 production DDL is part of C0.

## Required C1 sequence before a version 2 migration exists

1. **Prepare:** land the finite `prepared-v1-v2` binary while the database is
   still `{1}`. Prove its real process is Ready and completes service CRUD.
2. **Expand:** apply additive DDL and a C1 binary that reads both shapes. Prove
   the prepared binary still reads rows written by C1, including every field it
   uses.
3. **Mixed operation:** run the prepared binary and C1 simultaneously. C1 dual-writes old and new
   representations; tests cover CRUD from both binaries and retry/idempotency.
4. **Backfill:** make progress restartable and observable. A partial failure,
   lock contention, cancellation, and rerun must not create a history gap or
   expose a row that neither binary can read.
5. **Rollback window:** while C1 writes, downgrade to the fixed prepared binary and
   prove old-code reads after new-code writes. Keep dual-write active for the
   documented rollback window; do not stop old-column writes early.
6. **Contract:** only after the rollback window and its evidence may a later
   generation remove the old representation. Contract DDL is never bundled into
   the expand step.

The four-service disposable DB gate must exercise the same classification and
failure behavior before any version 2 SQL is introduced. A skipped suite, an
unavailable DB, an integrity failure, or an assertion failure is a failed gate.

## Test-only E1 evidence is not a production migration

The Account compatibility fixture may add a nullable `name_e1` column and the
history row `{1,2}` only inside its fresh disposable PostgreSQL container. It
must prove these four real-DB cells: C0 schema/C0 writer, C0 schema/C1 writer
zero-write rejection, E1 schema/C0 writer with C1 fallback read, and E1
schema/C1 dual writer with the fixed C0 SQL data reader. It also covers old-only,
dual-written, and divergent rows; mixed create/update/delete and uniqueness;
transactional backfill failure, restart, and idempotent rerun.

Strict C0 remains deliberately **not Ready** for `{1,2}`. Both strict and
prepared migration entry points reject that history, so the prepared profile
cannot manufacture or advance an unshipped migration. The mandatory
four-service process fixture starts each real service binary on `{1}`, applies
test-only additive columns and `{1,2}`, proves strict readiness rejection,
starts the prepared binaries, runs a separate test-only C1 dual-writer process,
reads those rows through all four prepared APIs, then stops and restarts every
prepared process. This is an operational rollback-window test in disposable
databases, not authorization or DDL for a live database.
