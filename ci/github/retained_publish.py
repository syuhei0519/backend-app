#!/usr/bin/env python3
"""Publish the reviewed retained OCI once, failing closed on unknown tag state."""
import os
import subprocess
import sys

REFERENCE = "ghcr.io/syuhei0519/backend-app:source-635814f5354f"
MANIFEST = "sha256:fba8eae42982a0e818581b2ac6b41ba865920dafdbfd7d48c416f98c5b1eaebd"

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

def publish(layout):
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
    try: publish(sys.argv[1])
    except Exception as error: raise SystemExit("publication stopped; registry state requires inspection: " + str(error))
