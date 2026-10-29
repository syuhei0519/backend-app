#!/usr/bin/env python3
"""Publish the reviewed retained OCI once, failing closed on unknown tag state."""
import datetime as dt
import json
import os
from pathlib import Path
import subprocess
import sys

REFERENCE = "ghcr.io/syuhei0519/backend-app:source-635814f5354f"
MANIFEST = "sha256:fba8eae42982a0e818581b2ac6b41ba865920dafdbfd7d48c416f98c5b1eaebd"
CONFIG = "sha256:ef442084e2b900ce842504ab5b05f2ae21dbb334ecf6295e70037dbb02420306"

class PublishError(ValueError): pass

def run(args, secret=None):
    return subprocess.run(args, input=secret, text=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, check=False)

def state(result):
    if result.returncode == 0:
        digest = result.stdout.strip()
        if digest != MANIFEST: raise PublishError("existing tag has different digest")
        return "same"
    error = result.stderr
    if "MANIFEST_UNKNOWN" in error and not any(x in error.lower() for x in ("unauthorized", "denied", "forbidden")):
        return "absent"
    raise PublishError("tag state unavailable")

def decode(path):
    data = Path(path).read_bytes()
    if len(data) > 1024 * 1024: raise PublishError("receipt too large")
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result: raise PublishError("duplicate receipt key")
            result[key] = value
        return result
    return json.loads(data, object_pairs_hook=unique)

def timestamp(value):
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None: raise PublishError("receipt timestamp refused")
    return parsed

def validate_receipt(layout, receipt, instant=None):
    instant = dt.datetime.now(dt.timezone.utc) if instant is None else instant
    record = decode(receipt)
    if record["schema"] != "core-platform/retained-runtime-refresh/v1": raise PublishError("receipt schema refused")
    if record["published"] is not False or record["deploymentAuthorized"] is not False: raise PublishError("receipt authority refused")
    if record["image"]["imageDigest"] != MANIFEST or record["image"]["configDigest"] != CONFIG: raise PublishError("receipt image refused")
    scan = record["freshImageScan"]
    if scan["imageDigest"] != MANIFEST or scan["configDigest"] != CONFIG: raise PublishError("scan image refused")
    if scan["status"] != "scanned-policy-passed" or scan["evidenceBytesVerifiedByThisWorkflow"] is not True:
        raise PublishError("scan status refused")
    if record["validUntil"] != scan["validUntil"]: raise PublishError("scan deadline mismatch")
    if not timestamp(scan["scannedAt"]) <= instant < timestamp(scan["validUntil"]): raise PublishError("scan expired")
    root = Path(layout)
    index = decode(root / "index.json")
    if len(index["manifests"]) != 1 or index["manifests"][0]["digest"] != MANIFEST:
        raise PublishError("layout manifest refused")
    manifest = decode(root / "blobs/sha256" / MANIFEST[7:])
    if manifest["config"]["digest"] != CONFIG: raise PublishError("layout config refused")

def publish(layout, receipt):
    validate_receipt(layout, receipt)
    login = run(["crane", "auth", "login", "ghcr.io", "-u", os.environ["GITHUB_ACTOR"], "--password-stdin"], os.environ["GH_TOKEN"])
    if login.returncode: raise PublishError("authentication rejected")
    before = state(run(["crane", "digest", REFERENCE]))
    if before == "absent":
        if state(run(["crane", "digest", REFERENCE])) != "absent":
            raise PublishError("tag changed before write")
        # One invocation, and the helper disables go-containerregistry retries.
        written = run(["oci-layout-push", str(layout), REFERENCE, MANIFEST])
        if written.returncode: raise PublishError("write failed or outcome uncertain")
    verified = state(run(["crane", "digest", REFERENCE]))
    if verified != "same": raise PublishError("remote verification failed")

if __name__ == "__main__":
    try:
        if len(sys.argv) != 3: raise PublishError("layout and receipt required")
        publish(sys.argv[1], sys.argv[2])
    except Exception as error: raise SystemExit("publication stopped; registry state requires inspection: " + str(error))
