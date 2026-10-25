# Schema compatibility contract

This contract applies independently to Account, Order, Payment, and Notification.
The current runtime is compatibility generation C0 and supports exactly migration
history `{1}`. Readiness and migration use the same finite classifier:

| Observed history | Classification | C0 Ready | C0 migration |
| --- | --- | --- | --- |
| database unavailable/read failure | `unavailable` | no | no |
| history table absent | `no-table` | no | create the table, then treat as empty |
| `{}` | `empty` | no | apply migration 1 |
| `{1}` | `c0-v1` | yes | safe idempotent no-op |
| `{1,2}` | `expand-v1-v2` | no | no; requires the tested C1 binary |
| `{2}` | `missing-base-v2` | no | no |
| `{1,3}` or any non-contiguous/duplicate history | `gap` | no | no |
| contiguous history beyond prepared version 2 | `unknown-future` | no | no |

The table is stricter than `max(version) >= minimum`. Unknown or partially
observed histories fail closed. No version 2 production DDL is part of C0.

## Required C1 sequence before a version 2 migration exists

1. **Expand:** land additive DDL and a C1 binary that reads both shapes. Prove C0
   still reads rows written by C1, including every field used by C0.
2. **Mixed operation:** run C0 and C1 simultaneously. C1 dual-writes old and new
   representations; tests cover CRUD from both binaries and retry/idempotency.
3. **Backfill:** make progress restartable and observable. A partial failure,
   lock contention, cancellation, and rerun must not create a history gap or
   expose a row that neither binary can read.
4. **Rollback window:** while C1 writes, downgrade to the fixed C0 binary and
   prove old-code reads after new-code writes. Keep dual-write active for the
   documented rollback window; do not stop old-column writes early.
5. **Contract:** only after the rollback window and its evidence may a later
   generation remove the old representation. Contract DDL is never bundled into
   the expand step.

The four-service disposable DB gate must exercise the same classification and
failure behavior before any version 2 SQL is introduced. A skipped suite, an
unavailable DB, an integrity failure, or an assertion failure is a failed gate.
