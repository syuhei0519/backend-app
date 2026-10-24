# PLAT-3 / PLAT-5 staged enforcement proposal

This is a reviewable proposal, not authorization to modify GitHub security settings.
Current main retains initial observation until the implementation PRs are approved
and merged. The proposal branch uses enforcement so its CI can validate the contract.
No public image/release approval or new persistent access is included.

## Exact scope and order

| Repository | Target | Semgrep blocking rules |
| --- | --- | --- |
| syuhei0519/backend-app | PR CI, then main after merge | pgx-sqli, pgxpool-concatenated-query, dynamic-shell-command |
| syuhei0519/frontend-app | PR CI, then main after merge | detect-eval-with-expression, insecure-innerhtml, react-dangerouslysetinnerhtml, typed-react-html-audit |

All selected ERROR/WARNING findings fail. INFO is reported. The Go shell rule
matches a dynamic command argument passed to named sh/bash/dash/ksh/csh/tcsh/zsh
`-c` calls, including the explicit `/usr/bin/env` form. It is bounded pattern
coverage, not comprehensive taint/interfile analysis. CLI/image remains Semgrep CE
1.182.0; upstream rule commit remains 16b9e6d45f10649f6a531ec4838ed9924372c3bc,
and the added local rule has an exact file checksum in semgrep.lock.json.

Backend's broad LOW-confidence `dangerous-exec-command` audit remains fully visible.
Eleven baseline test harnesses were independently traced to os.Executable or a
test-owned temporary build, with no HTTP input to a shell. Its rule observation
window is owned by syuhei0519 and expires at **2026-11-07T00:00:00Z** (14 days from
the recorded creation). This is explicit rule selection, not a finding exemption.
The precise shell rule still blocks dynamic shell commands. Unknown/missing rule
selections and an expired audit review fail in either global mode, even with no
matching finding. There is no test-path exclusion or discarded audit result.

The public report keeps core-platform/sast/v1 and adds `ruleMode` per finding;
fingerprints retain rule/path/span/severity/whole-file hash. Policy schema is now
strict version 2; it cannot silently fall back to the old global-severity policy.
Parser/scanner/coverage/version/rule-lock/cleanup errors remain hard failures.
Image -> SAST/validate and ci-result -> SAST dependencies remain unchanged.

Merge the clean implementation PRs only after final CI/review, then check exact
main CI and CodeQL. GitOps retains its reviewed common scan-gate SHA; frontend's
Go shared-tools SHA is unchanged. The OCI change only pre-creates the literal OCI
blob directory before the same archive-name allowlist and removes archive-derived
mkdir calls. Traversal/link/duplicate/checksum limits and public Go interfaces stay.
It does not dismiss CodeQL alert 1; full-main analysis must confirm closure.

## Additive CodeQL ruleset: separate approval required

The exact creation body is security/codeql/ruleset-proposal.json in each repo:
name `sast-codeql-high`, target branch, active, refs/heads/main only, no exclusions,
no bypass actors, required tool CodeQL, `security_alerts_threshold=high_or_higher`,
`alerts_threshold=none`. Security High/Critical block; nonsecurity quality alerts
remain observational. Required-tool absence or analysis still in progress also
prevents merge; a successful action is not a zero-finding verdict.

Create a separate ruleset; do not PUT, delete or replace the current
`main-ci-and-pr` rulesets: backend 24733411, frontend 24733415. Before and after,
compare their name/enforcement/conditions/rules/bypass actors, and verify aggregated
main rules still require strict ci-result, PRs, resolved threads and prohibit
deletion/force-push with no bypass actors. Preserve the current review-count and
merge-method settings. Do not add a paid evaluate-mode dependency.

An existing main High does **not** prove every unrelated PR would block: GitHub
documents that all alert-identified lines must be in the PR diff. Main go/zipslip
is still tracked until a full-main result confirms the literal-path refactor.
Do not infer main closure from a PR analysis reporting zero results. If creation
or verification fails, stop, report the exact result, and do not weaken the old
ruleset, dismiss an alert, expand credentials or add a bypass. An uncertain POST
must be resolved by name/config readback before any retry.

## Observation exit and exceptions

Use evidence rather than an arbitrary elapsed week: normal PR/main scans complete
with exact coverage and no scanner errors; known baselines have independent
dispositions; harmless static vulnerability fixtures show detections, overlap,
misses and timings; chosen-rule positives/negatives and exception expiry pass;
Sonar exact main CE and evaluated QG succeed. The existing temporary-enforce PRs
already stopped ci-result and image/publication, but they did not enforce CodeQL
results. The proposed ruleset and clean/blocked/corrected PR checks are still needed.

Exception owner is syuhei0519. Individual Semgrep exceptions retain the exact
fingerprint, reason, owner, createdAt and expiresAt <=14 days, plus the authenticated
same-repository approval-comment digest. Review on each relevant PR and before
expiry; changed source invalidates the fingerprint, expired/forged/unused-expired
entries fail. No exception is added here. CodeQL dismissal has no native expiry
in this contract, so no dismissal/bypass exception is proposed: fix or obtain a
separately reviewed disposition before merging. Never accept an indefinite ignore.

## Independent block / correction proofs

security/sast-fixtures holds `.txt` templates; CI's real Semgrep engine scans their
bytes without executing them. On disposable PR branches only, copy the appropriate
template to root `sast-verification.go` or `sast-verification.tsx`, commit and push.
Functions have no initializer, test, main, handler registration or application
import. Never invoke/import them or merge their active form.

1. Semgrep case: Go concatenated pgxpool SQL / typed React raw HTML. Require selected
   rule detection, failed sast and ci-result, image/publish/propose skipped, and
   required-check merge blocking. CodeQL overlap is recorded, not assumed absent.
2. CodeQL case: Go database/sql request concatenation / browser document.write from
   location.hash. Require Semgrep clean, ordinary CI successful, CodeQL High alert,
   and the **active code_scanning rule's** blocked verdict. CI success alone is not
   that proof. Before ruleset approval, this is only a detection comparison.
3. Remove the exact active fixture in the same disposable PR, keep templates only,
   rerun on the corrected SHA, require no new CodeQL High/Critical and all required
   checks passing. Prove the code-scanning block clears. A Draft flag alone must
   not be used as evidence of security blocking: temporarily Ready/closing those
   verification PRs needs explicit workflow approval; never merge the negative SHA.

Final implementation heads must contain no active verification source. Existing
main alerts, medium/low alerts and quality findings stay visible. No GHCR approval
is part of these tests. Current main CI runs 40058514885/backend and
40058524737/frontend are waiting for publication: after a newer approved merge,
cancelling only those obsolete waiting runs may be needed to free concurrency,
but that cancellation needs explicit authorization and is not publication approval.

## Main-only Sonar operation

Initial operator-run scans and QG were verified on both main SHAs; current analysis
tokens were revoked and deleted after validation. No automatic main trigger exists.
The existing approved manual route needs no new persistent access. Do not present
manual operation as an unattended main-update service.

An unattended option needs separate approval: a current-user local scheduled job
polls the two fixed public main refs, uses an approved immutable local scanner
wrapper (never executes a newly fetched repo script), snapshots only source, and
uses separate project-only tokens stored in Windows Credential Manager. A human
renews them at most every seven days; the job never reads the admin password or
mints credentials. Keep last-successful-analysis SHA, reject expired/missing tokens,
verify explicit leaf/hostname/expiry/fingerprint, and fail visibly if the approved
localhost TLS route is absent. Do not mark a newer main verified on such failure.

The job definition, ongoing token retention and a bounded TLS-forward availability
window are additional persistent operation, not approved by initial validation.
Prefer an operator-started fixed localhost forward; do not give an unattended job
the existing administrative kubeconfig or create a new token/ServiceAccount as a
shortcut. A least-privilege forwarding identity would require a separate concrete
credential/RBAC proposal. No self-hosted public-fork runner or external ingress.

Current observation cannot close PLAT-3/5: required-code-scanning merge enforcement,
chosen-rule production enforcement and corrected/blocked merge evidence are not
yet all present. Automatic Sonar scheduling is separately unresolved if required
for the steady-state main-update operating model.

Sources: [merge protection and diff limits](https://docs.github.com/en/code-security/concepts/code-scanning/merge-protection),
[rules REST contract](https://docs.github.com/en/rest/repos/rules),
[setup](https://docs.github.com/en/code-security/how-tos/find-and-fix-code-vulnerabilities/manage-your-configuration/set-merge-protection).
