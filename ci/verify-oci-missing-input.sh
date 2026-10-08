#!/bin/sh
set -eu
umask 077
test "${OCI_VALIDATION_ENABLED:-false}" = true && test "${AT16_MISSING_INPUT:-false}" = true || exit 1
test "${CI_COMMIT_REF_PROTECTED:-true}" = false || exit 1
mkdir -p .oci-missing/private .oci-missing/public
trap 'rm -rf .oci-missing/private' EXIT HUP INT TERM
archive_sha=$(cut -d ' ' -f 1 .oci/image.tar.sha256)
# Delete only this job's downloaded copy to model an expired/unavailable input.
# The producer artifact and Source cluster/data are not changed.
rm -f .oci/image.tar
if .security/oci-check .oci/image.tar "$archive_sha" .oci-missing/private/layout "$CI_COMMIT_SHA" > .oci-missing/private/result.json 2> .oci-missing/private/error.log; then
 printf '{"schemaVersion":1,"inputMissingRejected":false,"unexpectedSuccess":true}\n' > .oci-missing/public/failure.json
 exit 1
fi
test ! -e .oci-missing/private/layout || exit 1
printf '{"schemaVersion":1,"scope":"PE016B artifact expiry/missing input fixture","sourceCommit":"%s","pipelineId":%s,"jobId":%s,"inputMissingRejected":true,"partialLayoutAbsent":true,"registryFallbackUsed":false,"producerArtifactDeleted":false}\n' "$CI_COMMIT_SHA" "$CI_PIPELINE_ID" "$CI_JOB_ID" > .oci-missing/public/failure.json
# This is deliberately a failed upstream job, not an allow_failure test.
exit 2
