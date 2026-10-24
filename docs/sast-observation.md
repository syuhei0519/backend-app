# SAST observation: initial main baselines

Observed 2026-10-24. These are existing-source results, before the deliberate
vulnerability PR comparison. Findings remain visible; no alert has been dismissed
and no exception has been added.

| Repository / baseline | Semgrep CE 1.182.0 | CodeQL 2.27.3 extended / remote |
| --- | --- | --- |
| backend-app `f0425a9c0a70c200fbeb5a549bc8b631bf5a1906` | 100 files, 11 ERROR audit findings; 4.875 s local; `wouldPass=false` | 35 rules, 1 High result; analysis 2017524631 |
| frontend-app `88523f20be3c02b6cf0dba88199593041b3fab78` | 11 files, 0 findings; 2.671 s local; `wouldPass=true` | 103 rules, 0 results; analysis 2017523426 |

CodeQL Default Setup was explicitly authorized and enabled for both repositories
with the extended suite, remote threat model and standard GitHub-hosted runners.
The confirmed languages are `go` for backend and the GitHub-managed JavaScript /
TypeScript language group for frontend. Weekly and PR/main trigger behavior is
managed by GitHub. The successful setup runs below are separate from the analysis
counts above; success does not mean no security findings:

- [backend setup run 40051212618](https://github.com/syuhei0519/backend-app/actions/runs/40051212618)
- [frontend setup run 40051216413](https://github.com/syuhei0519/frontend-app/actions/runs/40051216413)

## Backend Semgrep classification

All eleven findings come from the upstream `dangerous-exec-command` rule
(ERROR, LOW confidence audit). They are existing subprocess test harness calls,
not newly introduced findings and not an application request reaching a shell.
The following is the proposed false-positive disposition for command injection,
based on tracing the executable variable to `os.Executable()` or a binary built
in a test-owned temporary directory. An independent reviewer must confirm the
disposition before selecting a blocking rule subset. This table does not suppress
the findings or approve an exception.

| Existing location | Count | Executable provenance | Proposed disposition |
| --- | ---: | --- | --- |
| `internal/ordertrace/trace_test.go:143` | 1 | current test executable from `os.Executable()` | test harness / injection false-positive candidate |
| `tests/distributedtrace/cloud_test.go:126,131` | 2 | temporary Order binary built from local source | test harness / injection false-positive candidate |
| `tests/notificationintegration/notification_test.go:44,107` | 2 | temporary Notification binary built from local source | test harness / injection false-positive candidate |
| `tests/orderintegration/order_test.go:117,143` | 2 | temporary Order binary built from local source | test harness / injection false-positive candidate |
| `tests/orderpaymentintegration/orderpayment_test.go:56,64` | 2 | temporary service binary built from local source | test harness / injection false-positive candidate |
| `tests/paymentintegration/payment_test.go:45,84` | 2 | temporary Payment binary built from local source | test harness / injection false-positive candidate |

No SQL finding appeared on the baseline under the selected pgx/pgxpool rules.
This is bounded coverage, not proof of absence of SQL injection. In particular,
the local pgxpool pattern does not track a query through an intermediate variable.

## Existing CodeQL High

[backend CodeQL alert 1](https://github.com/syuhei0519/backend-app/security/code-scanning/1)
is `go/zipslip` at `tools/oci-input/layout.go:317`, an archive-derived path reaching
`os.MkdirAll`. The implementation already:

- rejects absolute paths, backslash/colon, noncanonical paths, `..`, duplicates,
  symlinks and hardlinks;
- permits directory entries only at the two literal paths `blobs` and `blobs/sha256`;
- permits regular files only at `index.json`, `oci-layout` or `blobs/sha256/<64 hex>`;
- extracts into a newly created private temporary directory and publishes only
  after checksum and full OCI validation.

The custom allowlist suggests an analyzer-model false positive, rather than an
unvalidated archive destination. Existing
`TestArchivePathsLinksDuplicateAndChecksumRefused` covers traversal, absolute
paths, links, duplicates and checksum substitution. Preserve that boundary and
confirm its results plus independent review before deciding whether to refactor
for analyzer recognition or formally dismiss the alert. No shared Go tool change
or frontend shared-tools SHA update was made merely to clear this result. This
open High would block the proposed future High/Critical CodeQL ruleset and must
be resolved before that stage.

## Required evidence before observation ends

1. Both normal PRs show successful Semgrep execution with complete selected-source
   coverage, no scanner/parser failures and all existing findings dispositioned.
2. A safe, never-executed source fixture in each PR is analyzed by both scanners.
   Record detections, overlap, misses and durations, then remove the fixture.
3. Independently confirm all eleven backend dispositions and the existing CodeQL
   High; apply reviewed fixes, analyzer-compatible validation or approved exceptions
   where needed. Do not hide these with blanket test-path exclusions.
4. Explicitly review the Semgrep blocking subset. Candidate severity is ERROR /
   WARNING for the small selected injection rule set; the broad executable audit
   rule needs refinement or precise dispositions before enforcement. CodeQL's
   candidate security threshold is High/Critical (`>=7.0`), with quality alerts
   observational. Ruleset changes require separate authorization.
5. Demonstrate a blocked negative PR and a passing corrected PR, scanner-error
   failure, and exact approved/expired exception behavior. Local evaluator tests
   alone do not prove GitHub merge blocking.
6. Analyze each exact main with the shared SonarQube project and capture its
   Quality Gate, server/analyzer versions and result links. SonarQube infrastructure
   and credentials have not yet been deployed, so the Jira tasks remain incomplete.

Known Semgrep limitations also include typed React props passed via an intermediate
HTML object and a locally shadowed `DOMPurify.sanitize` name. Direct typed raw-HTML
and upstream taint cases are tested. These limits must remain visible in the
comparison; do not claim interfile or comprehensive XSS analysis.
