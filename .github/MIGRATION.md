# GitHub release operation

The GitHub repository starts from a source-only import. GitLab commit history
is retained in the original local repository and is not published here.

CI on protected `main` builds and checks one OCI image, including its isolated
runtime, security policy and SBOM. The `ghcr-publish` Environment requires
owner approval before that checked image and its release evidence are published.
SHA tags must retain the checked digest. Public GHCR access is verified before
approving the `manifest-update` Environment for the initial migration.

Manifest updates use pull requests. Run `Trusted manifest verification` from
the protected application-manifest main branch for the exact current PR head
and base before merging. A changed head/base or expired evidence requires a
new verification. Cluster access and cluster credentials are not used by CI.

This account has one operator. Main requires `ci-result`, an up-to-date PR,
resolved review threads, and prohibits direct push, force push and deletion.
Required PR approvals are zero; Environment approval remains required.
