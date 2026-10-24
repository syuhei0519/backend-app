# PLAT-3 / PLAT-5: three-tool SAST rollout

This change implements the **initial observation stage**, not completion of the
three-tool acceptance criteria. The authoritative Jira additions (2026-10-24
10:18 JST) require Semgrep CE + CodeQL + one shared SonarQube Community Build.
Neither successful scanner execution nor a green observation job means no findings.

## Contracts implemented here

- Semgrep CE 1.182.0, official image pinned by digest in `ci/sast.py` and
  `security/semgrep.lock.json`. Network is disabled during analysis, metrics and
  version checks are off, no Semgrep account or paid engine is used.
- Public rules are vendored from semgrep/semgrep-rules commit
  `16b9e6d45f10649f6a531ec4838ed9924372c3bc`, with per-file SHA-256 and retained
  Rules License notice. `pgxpool-concatenated-query` and
  `typed-react-html-audit` are locally authored supplements. The latter addresses
  a verified miss for typed destructured React props in the upstream taint rule.
  This is a small audited starter set, not full OWASP coverage or interfile analysis.
- Every tracked Go file (backend) or JS/JSX/TS/TSX/MJS/CJS file (frontend) at HEAD
  is scanned, including tests. Git settings, ignore files, credentials and generated
  dependencies are not copied. An empty scanner ignore file replaces built-in test
  exclusions. Missing scanned targets, JSON/version/rule mismatches, scanner nonzero
  exit, timeout, parser errors and invalid/expired exceptions fail in **both** modes.
  Semgrep uses rule prefilters; coverage means selected files were considered, not
  that every irrelevant file was fully parsed. Existing compiler/type checks remain.
- `security/sast-policy.json` starts in `observe`. ERROR and WARNING are candidate
  blocking severities; INFO is informational. `wouldPass` records the enforcement
  result, while `pass` records the current observation policy. No findings are
  silently discarded. Change to `enforce` only in a separately reviewed stage.
- Exceptions are initially empty. They bind the exact rule/path/span/severity and
  whole-source-file SHA through the finding fingerprint, with owner, reason and
  UTC expiry no more than 14 days after creation. Even unused expired exceptions
  fail. No `nosemgrep`, wildcard path, ignore list or unapproved suppression is used.
- Approval is verified against an issue comment in the same repository, authored
  by `syuhei0519`. Its entire body must equal
  `SAST-EXCEPTION-APPROVED sha256:<digest>`, where digest is SHA-256 of compact,
  sorted JSON of the exception excluding `approval`. Copy `fingerprint` from the
  sanitized finding. Approval lookup failure fails closed. Test approvals are mocks;
  no live exception was granted during implementation.
- Public artifact `sast-semgrep-<attempt>` contains only allowlisted locations,
  severities, fingerprints, hashes, timing and policy decision. Raw source snippets,
  scanner stderr and matches are never uploaded. Existing Trivy, secret, IaC,
  image digest, CycloneDX and release artifacts are unchanged.
- `sast` and `validate` execute independently; a SAST failure does not prevent the
  existing Trivy job from running. `image` requires both. `ci-result` explicitly
  rejects failed/skipped/cancelled SAST; publish/propose retain their original
  image/validation and protected-main/environment controls. No required-check,
  branch-protection or repository permission setting has been changed.

Source blobs are read from the fixed commit using `git ls-tree` and
`git cat-file --batch`; archive `export-ignore` and `export-subst` cannot hide or
rewrite tracked source. Symlinks/submodules and oversized source snapshots fail.

The two repositories carry identical small Python wrappers/tests. Existing
backend shared-tools Go modules and frontend's
`ba8b3d211ac3e497900abd6c6f44f45388e3801c` references are untouched. There is no
cross-repository SHA upgrade or merge-order dependency in this initial stage.

## Observation exit criteria and staged gates

1. Record one clean normal PR run in each repo, plus one deliberately vulnerable
   source PR/commit for comparison. Do not execute vulnerable samples. Capture rule
   IDs, overlaps, missed cases, false positives, source SHA and elapsed scan time.
2. Reconcile every existing finding with evidence; fixes or owner-approved short
   exceptions must explain all candidate blockers. Confirm both tools detect their
   intended negative fixture, document each miss, and remove fixtures afterward.
3. Review the selected Semgrep rule set and activate `mode=enforce` in a separate
   stage. Candidate threshold is ERROR/WARNING because the selected rules represent
   SQL, command execution and HTML injection concerns; audit-rule false positives
   must be narrowed or explicitly accepted before enforcement. No automatic baseline
   suppression or expiry extension is permitted.
4. CodeQL initial proposal: Default Setup, language Go or JavaScript/TypeScript,
   `extended` queries and remote threat model. See
   `security/codeql/default-setup.request.json`. This file records the approved request body. Both Default Setup configurations
   are now enabled; see `sast-observation.md` for verified analysis results. GitHub manages
   CodeQL CLI/query updates in Default Setup; it offers no repository CLI/pack pin.
   Record analyzer versions from each run. Strict CodeQL pinning would require a
   separately approved switch to Advanced Setup, contrary to the initial default
   setup plan; never claim Default Setup is reproducibly pinned.
5. After observation, propose `Require code scanning results` for CodeQL with
   security **High/Critical (>=7.0)** and no non-security quality alert threshold.
   This balances high-impact security blocking with observational quality results.
   Creating/changing a ruleset needs separate approval. Action success is not
   equivalent to no CodeQL findings. Verify real PR block/release after activation.
6. Run both main projects against the shared SonarQube and record dashboard and
   Quality Gate evidence. Only then assess all Jira acceptance criteria. Local
   fixture tests do not prove protected-main scheduler behavior or release delivery.

The initial backend observation found 11 `dangerous-exec-command` findings, all
in test harnesses running `os.Executable()` or locally built binaries. They require
review/triage; they are visible, not suppressed and not called vulnerabilities by
this report. The observation mode is therefore necessary before selecting the
production blocking subset.

## SonarQube main-only preparation and requested infrastructure

`sonar-project.properties` selects a distinct project in each repo:
`platform-backend-app` and `platform-frontend-app`. `ci/sonar-main.py` accepts only a
clean main checkout whose HEAD matches the current remote main. It requires an
approved HTTPS `SONAR_HOST_URL` and a project-scoped `SONAR_TOKEN` in the process
environment, archives tracked files without Git credentials, and uses pinned
SonarScanner CLI image `12.3.0.4285_8.1.0` (CLI 8.1.0.6389). It waits for the Quality
Gate and returns failure for scanner/connectivity/quality failure. No PR/branch
analysis or public-fork local runner is configured. The scanner can send source to
the approved private server; running it is an explicit operator action.

Pending shared-server proposal (platform-gitops owns this, not either app repo):

- One `sonarqube` namespace, one dedicated Argo CD AppProject and Application;
  official `https://SonarSource.github.io/helm-chart-sonarqube` chart
  `sonarqube` 2026.5.1003, Community Build enabled. Pin the community image digest
  after rendering the approved chart; do not accidentally deploy a paid edition.
- A dedicated PostgreSQL instance/database/user in the same namespace, independent
  of application databases. PostgreSQL PVC 10 GiB and SonarQube PVC 5 GiB using the
  verified local storage class. Proposed Sonar request 1 CPU/4 GiB, limit 2 CPU/6 GiB;
  PostgreSQL request 250m/512 MiB, limit 1 CPU/1 GiB. Node capacity is not yet checked.
- Secrets for JDBC password, monitoring passcode and non-default admin bootstrap;
  create two scoped analysis tokens after project creation. Store secrets only in
  approved encrypted storage, never Git, manifests, logs or artifacts.
- Default ClusterIP service; no public ingress. Use a separately approved local
  TLS endpoint/tunnel reachable from the local scanner container. Do not expose
  the existing kind cluster or register a public-fork self-hosted runner.
- Disable chart privileged `initSysctl` and `initFs`; first check host prerequisites
  (`vm.max_map_count >= 524288`, file/process limits). Any required host/kernel
  change affects other kind workloads and needs explicit approval. Do not weaken
  the existing privileged/secret policy to make the chart pass.
- Manually review rendered resources and storage/security/network impact before
  the first Argo sync. No existing kind resources have been read or changed by the
  application implementation, and no deployment/token operation is part of this PR.

This proposal is not an install-ready chart or server acceptance result. Capacity,
storage class, TLS route/trust and host prerequisites must be resolved before apply.
PLAT-193..195, PLAT-177 and PLAT-4 remain outside this work.

## Confirmed setup and implementation review fixes

CodeQL initialization and actual baseline analyses were checked separately. The
backend baseline has one existing High `go/zipslip` alert; frontend has no results.
The analysis counts, proposed dispositions of all eleven Semgrep audit findings,
and observation exit criteria are recorded in [the observation record](sast-observation.md).

Both scanner wrappers now assign a unique owned container name and remove only
that container in `finally`, with a 20-second cleanup bound. A scanner timeout or
cleanup failure fails the job. A real timeout/removal fixture supplements mocked
failure tests. Unregistered nested `.yaml` and `.yml` rules are also rejected.

The shared-server approval proposal now pins the official chart package SHA-256
`c85b05ffd646deb9fea7c28463622e42d2a93bbe988625530eda7d6cf6ee96fc`, Sonar Community
26.9.0.129388 image digest
`c0f1160bccfa435db4168c2d7df69af3c3ea7bfeb87395613014048fb33a68c1`, and dedicated
PostgreSQL 18.6-bookworm digest
`bbf83594798e144bff365a9c191c5d779f181fb3135902064ab8ff11918020fc`.
Its fixed Helm4.2.4 local render succeeded. Proposed TLS uses a nonprivileged
nginx sidecar and loopback-only port-forward with a per-run public truststore.
Credentials, live preflight, foundation resources, TLS handshake and server/main
acceptance still need the separately scoped approval and execution. The configured
kind Kubernetes1.36.4 is newer than the currently documented vendor matrix1.32..1.35;
template success does not establish supported live compatibility.

## Verification commands

On Linux with Docker available, from either repo:

```sh
SAST_REAL_TESTS=1 python3 -I ci/sast_test.py -v
python3 -I ci/sast-workflow_test.py -v
python3 -I ci/sonar-main_test.py -v
GITHUB_REPOSITORY=syuhei0519/backend-app python3 -I ci/sast.py # use frontend-app there
```

Real engine fixtures cover clean source, dangerous source, `nosemgrep` refusal and
partial parse failure without executing the source. Policy tests cover scanner
exit failures, missing coverage, version mismatch, allowed/expired/forged approvals,
changed-source exception mismatch and sanitized output. Workflow tests execute the
actual aggregate shell checks and inspect its image/publish dependency graph.

References: [Semgrep CLI](https://docs.semgrep.dev/cli-reference),
[rule license](https://semgrep.dev/legal/rules-license),
[CodeQL default setup](https://docs.github.com/en/code-security/how-tos/find-and-fix-code-vulnerabilities/configure-code-scanning/configure-code-scanning),
[merge protection](https://docs.github.com/en/code-security/how-tos/find-and-fix-code-vulnerabilities/manage-your-configuration/set-merge-protection),
[Community Build scanner](https://docs.sonarsource.com/sonarqube-community-build/analyzing-source-code/scanners/sonarscanner),
[official chart](https://github.com/SonarSource/helm-chart-sonarqube/tree/sonarqube-2026.5.1003-sonarqube-dce-2026.5.1003/charts/sonarqube).
