#!/bin/sh
# Native needs supplies the exact original archive and first-run public record.
set -eu
test "$CI_PROJECT_ID" = 86247033 && test "$CI_JOB_NAME" = oci-image-rescan-validation || exit 1
test "$CI_COMMIT_BRANCH" = main && test "$CI_COMMIT_REF_PROTECTED" = true && test "$CI_PIPELINE_SOURCE" = api || exit 1
test "${AT17_RESCAN_ACCEPTANCE_ENABLED:-false}" = true && test "${AT17_SAME_DIGEST_RESCAN:-false}" = true || exit 1
test -s .image-policy/public/record.json && test ! -e .prior-scan || exit 1
mv .image-policy/public .prior-scan
rmdir .image-policy
# The first run's budget artifact is evidence, not this run's sampler output.
if [ -d .oci-budget-format ]; then test ! -e .prior-budget || exit 1; mv .oci-budget-format .prior-budget; fi
sh ci/run-image-scan.sh
