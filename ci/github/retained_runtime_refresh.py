# 同じ GitHub artifact の固定 OCI だけを再実行し、現在の run に結び付く証跡を作ります。
"""Fail-closed retained OCI runtime refresh; never builds or publishes an image."""
from __future__ import annotations

import datetime as dt
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
import sys
import tarfile
import urllib.error
import urllib.parse
import urllib.request
import zipfile

REPOSITORY = "syuhei0519/backend-app"
SOURCE_COMMIT = "635814f5354f0fe238694d35137b283576a9127f"
SOURCE_RUN_ID = 40210608840
SOURCE_RUN_ATTEMPT = 1
SOURCE_JOB_ID = 121222105516
ARTIFACT_ID = 12606240607
ARTIFACT_NAME = "checked-image-1"
ARTIFACT_BYTES = 15379987
ARTIFACT_SHA256 = "8acc451d34baebd063674ad6f87fa230b1c14d1883fed0e788382a160910b6eb"
ARTIFACT_EXPIRES_AT = "2026-11-01T16:21:03Z"
ARCHIVE_BYTES = 15409664
ARCHIVE_SHA256 = "d98c960e2eaf68f5fb4e36b909d30b88c24492ae62d0f1f8fb91324d375ef6f8"
IMAGE_DIGEST = "sha256:fba8eae42982a0e818581b2ac6b41ba865920dafdbfd7d48c416f98c5b1eaebd"
CONFIG_DIGEST = "sha256:ef442084e2b900ce842504ab5b05f2ae21dbb334ecf6295e70037dbb02420306"
RUNTIME_BLOB_SHA1 = "39ef9b01266b553978989d376aa7a7fcc5d9de7d"
RELEASE_BLOB_SHA1 = "a02b05e4e7fb4b78e56b5bc693aed3db80bf012e"
WORKFLOW_REF = REPOSITORY + "/.github/workflows/retained-runtime-refresh.yml@refs/heads/main"
STATIC_VALID_UNTIL = "2026-10-29T01:21:08.391884645Z"
STATIC_SCANNED_AT = "2026-10-28T05:51:12.94045532Z"

FULL_CHANGE_SET = {
    ".github/workflows/ci.yml",
    ".github/workflows/retained-runtime-refresh.yml",
    "ci/github/classify.py",
    "ci/github/classify_test.py",
    "ci/github/release_test.py",
    "ci/github/retained_runtime_refresh.py",
    "ci/github/retained_runtime_refresh_test.py",
    "ci/github/retained_publish.py",
    "tools/oci-input/cmd/oci-layout-push/main.go",
}
EXPECTED_MEMBERS = {
    ".oci/image.tar": (15409664, ARCHIVE_SHA256),
    ".security/public/image-proof.json": (890, "7ffcf65965683ac7cc24894c72dc6433132dfac61e2b12d935dc16ebeaf6c940"),
    ".security/public/image-scan.json": (1836, "a9d86edc55bc720cfc2551e3cb4a07f3a3cdaaa96ea40cc4eacec1319cf235d7"),
    ".security/public/layout.json": (375, "c348e70496f2dc1cda48c35bc61a02f6b2e82ac8432f546a15cd768612d62be2"),
    ".security/public/reader.json": (279, "921e3dea15ce80c88a6d9628bc5fdf20f685a89203a5cd2b469c0c7f55077fd2"),
    ".security/public/release-record.json": (1245, "0db334ea979da0d82a1fe6ab0eb9d03918dab636a0439f2e62db5da4a3fb38ea"),
    ".security/public/runtime.json": (359, "9dbdcc884c470ab8ce73a787fba6297bfc0a44dbf83416e3f01cae1188b75175"),
    ".security/public/sbom-proof.json": (365, "dcfd878cc9c0b6f173ec8c1d91fe1fbfaceffd2f2d34186f741eb57b4d7d80a9"),
    ".security/public/sbom.cdx.json": (45803, "580ce6ae4f1dcc58ea380629e7ebf6bdf374b74818717bc86e1c91fbf6c3a4cd"),
    ".security/public/source-identity.json": (228, "a0e5343f50b4712f2ccbee35cbfcd0f0818b77481d7fe715663e384594f2356c"),
    ".security/public/source-scan.json": (1249, "6996b8da79224bce87a9bd035eabacd482d3d3ddade5a67a67d00b2f6f7ed31c"),
}
STATIC_EVIDENCE = {
    "accountStatusSha256": "e171ec105070e49e2117ae1253696098241dd364193aede2cc4e0d0bb548eecd",
    "combinedRunSummarySha256": "0ff411c7a77b25c3a08a42ee2f472cf3aebeb4090afcda7cb3d6d63a5e89e778",
    "rawScanSha256": "87bed70a5b5b9b8823aee6be88082d908fdca78163d0ff994c92b5a2cd410e3c",
    "safeSummarySha256": "b8df2841f36c9c40348f7e1b0d4797f434708d4d352ee1744d35d002b8130af1",
    "sbomSha256": "33f8cd4aa87ce5d67af34d4f5eef8a3ef835b99a48a5dc9d6f30eb82ce2baa6d",
    "trivySha256": "93f9da8e4ba5e0c1c76d8234ed2494cf9afb0a96fd21953e424bb795f3299b8e",
    "databaseSha256": "e68b667c74000c274351566d7bce26de2022f7937e4cc6e2787e2dbdac96e978",
    "scanGateSha256": "418d630a4428e8cd65ac0b51b5e020024cd4c1b70b946ecb021533fbef364a26",
    "scanPolicySha256": "d10f26ad064971e74b94f821560ede5303f0bcf4d364ea2514c45faac4c27337",
}
HEX40 = re.compile(r"[0-9a-f]{40}")
HEX64 = re.compile(r"[0-9a-f]{64}")
PRIVATE = Path(".security/private/retained-runtime")
PUBLIC = Path(".security/retained-runtime")


def require(value: object, message: str = "retained runtime contract refused") -> None:
    if not value:
        raise ValueError(message)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def decode(data: bytes) -> object:
    def unique(pairs):
        out = {}
        for key, value in pairs:
            require(key not in out, "duplicate JSON key")
            out[key] = value
        return out
    return json.loads(data, object_pairs_hook=unique, parse_constant=lambda _: require(False, "nonfinite JSON"))


def timestamp(value: str) -> dt.datetime:
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    require(parsed.tzinfo is not None)
    return parsed


def now() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


def git_blob_sha1(data: bytes) -> str:
    return hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise urllib.error.HTTPError(req.full_url, code, msg, headers, fp)


class GitHubAPI:
    def __init__(self, token: str):
        require(token and "\n" not in token)
        self.token = token
        self.opener = urllib.request.build_opener(NoRedirect)

    def _request(self, path: str, authorized: bool = True):
        require(path.startswith("/repos/" + REPOSITORY + "/") and "\n" not in path)
        headers = {
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
            "User-Agent": "backend-retained-runtime-refresh",
        }
        if authorized:
            headers["Authorization"] = "Bearer " + self.token
        return urllib.request.Request("https://api.github.com" + path, headers=headers)

    def json(self, path: str) -> object:
        with self.opener.open(self._request(path), timeout=40) as response:
            data = response.read(1024 * 1024 + 1)
        require(len(data) <= 1024 * 1024, "API response too large")
        return decode(data)

    def artifact_zip(self) -> bytes:
        path = f"/repos/{REPOSITORY}/actions/artifacts/{ARTIFACT_ID}/zip"
        try:
            self.opener.open(self._request(path), timeout=40)
        except urllib.error.HTTPError as error:
            require(error.code in (302, 307), "artifact redirect refused")
            location = error.headers.get("Location", "")
        else:
            raise ValueError("artifact endpoint did not redirect")
        parsed = urllib.parse.urlsplit(location)
        host = (parsed.hostname or "").lower()
        require(parsed.scheme == "https" and not parsed.username and not parsed.password)
        require(parsed.port in (None, 443))
        require(host == "objects.githubusercontent.com" or host.endswith(".actions.githubusercontent.com") or host.endswith(".blob.core.windows.net"))
        # Deliberately construct a fresh request: never forward the GitHub token.
        request = urllib.request.Request(location, headers={"User-Agent": "backend-retained-runtime-refresh"})
        with self.opener.open(request, timeout=60) as response:
            data = response.read(ARTIFACT_BYTES + 1)
        require(len(data) == ARTIFACT_BYTES and sha256(data) == ARTIFACT_SHA256)
        return data


def check_context(env: dict[str, str], changed_paths: set[str]) -> dict[str, object]:
    require(env.get("GITHUB_REPOSITORY") == REPOSITORY)
    require(env.get("GITHUB_EVENT_NAME") == "workflow_dispatch")
    require(env.get("GITHUB_REF") == "refs/heads/main")
    require(env.get("GITHUB_REF_PROTECTED") == "true")
    require(env.get("GITHUB_WORKFLOW_REF") == WORKFLOW_REF)
    workflow_commit = env.get("GITHUB_SHA", "")
    require(HEX40.fullmatch(workflow_commit) and workflow_commit != SOURCE_COMMIT)
    require(changed_paths == FULL_CHANGE_SET, "workflow tree is not the reviewed seven-file scope")
    run_id = env.get("GITHUB_RUN_ID", "")
    run_attempt = env.get("GITHUB_RUN_ATTEMPT", "")
    require(run_id.isdecimal() and int(run_id) > 0)
    require(run_attempt.isdecimal() and int(run_attempt) > 0)
    return {"repository": REPOSITORY, "workflowCommit": workflow_commit,
            "runId": int(run_id), "runAttempt": int(run_attempt), "workflowRef": WORKFLOW_REF}


def changed_paths(expected_head: str | None = None) -> set[str]:
    head = subprocess.run(["git", "rev-parse", "HEAD"], capture_output=True, text=True, check=True).stdout.strip()
    require(HEX40.fullmatch(head))
    if expected_head is not None:
        require(head == expected_head, "checkout HEAD is not the GitHub issuer commit")
    require(subprocess.run(["git", "merge-base", "--is-ancestor", SOURCE_COMMIT, "HEAD"]).returncode == 0)
    result = subprocess.run(["git", "diff", "--name-only", "--no-renames", "-z", SOURCE_COMMIT, "HEAD"],
                            capture_output=True, check=True)
    return {item for item in result.stdout.decode().split("\0") if item}


def validate_source_files() -> None:
    for path, expected in ((Path("ci/github/runtime.py"), RUNTIME_BLOB_SHA1),
                           (Path("ci/github/release.py"), RELEASE_BLOB_SHA1)):
        require(git_blob_sha1(path.read_bytes()) == expected, f"source identity refused: {path}")


def validate_api_metadata(api: GitHubAPI, context: dict[str, object]) -> dict[str, object]:
    current = api.json(f"/repos/{REPOSITORY}/actions/runs/{context['runId']}")
    require(current["id"] == context["runId"] and current["run_attempt"] == context["runAttempt"])
    require(current["head_sha"] == context["workflowCommit"] and current["head_branch"] == "main")
    require(current["event"] == "workflow_dispatch" and current["path"] == ".github/workflows/retained-runtime-refresh.yml")
    require(current["repository"]["full_name"] == REPOSITORY)

    source = api.json(f"/repos/{REPOSITORY}/actions/runs/{SOURCE_RUN_ID}")
    require(source["id"] == SOURCE_RUN_ID and source["run_attempt"] == SOURCE_RUN_ATTEMPT)
    require(source["head_sha"] == SOURCE_COMMIT and source["head_branch"] == "main")
    require(source["event"] == "push" and source["path"] == ".github/workflows/ci.yml")
    require(source["repository"]["full_name"] == REPOSITORY)

    artifact = api.json(f"/repos/{REPOSITORY}/actions/artifacts/{ARTIFACT_ID}")
    require(artifact["id"] == ARTIFACT_ID and artifact["name"] == ARTIFACT_NAME)
    require(artifact["size_in_bytes"] == ARTIFACT_BYTES and artifact["expired"] is False)
    require(artifact["digest"] == "sha256:" + ARTIFACT_SHA256)
    require(artifact["expires_at"] == ARTIFACT_EXPIRES_AT)
    require(artifact["archive_download_url"] == f"https://api.github.com/repos/{REPOSITORY}/actions/artifacts/{ARTIFACT_ID}/zip")
    require(artifact["workflow_run"] == {"id": SOURCE_RUN_ID, "repository_id": 1410557587,
            "head_repository_id": 1410557587, "head_branch": "main", "head_sha": SOURCE_COMMIT})

    jobs = api.json(f"/repos/{REPOSITORY}/actions/runs/{SOURCE_RUN_ID}/attempts/{SOURCE_RUN_ATTEMPT}/jobs?per_page=100")
    matching = [job for job in jobs["jobs"] if job["id"] == SOURCE_JOB_ID]
    require(len(matching) == 1)
    job = matching[0]
    require(job["name"] == "image (backend)" and job["status"] == "completed" and job["conclusion"] == "success")
    return {"sourceJobId": SOURCE_JOB_ID, "artifactCreatedAt": artifact["created_at"],
            "artifactExpiresAt": artifact["expires_at"]}


def safe_zip_materialize(data: bytes, root: Path) -> None:
    require(len(data) == ARTIFACT_BYTES and sha256(data) == ARTIFACT_SHA256)
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        infos = archive.infolist()
        require(len(infos) == len(EXPECTED_MEMBERS))
        require({info.filename for info in infos} == set(EXPECTED_MEMBERS))
        for info in infos:
            expected_size, expected_hash = EXPECTED_MEMBERS[info.filename]
            mode = info.external_attr >> 16
            require(not info.is_dir() and stat.S_ISREG(mode) and not (info.flag_bits & 1))
            require(info.file_size == expected_size and info.file_size <= ARCHIVE_BYTES)
            payload = archive.read(info)
            require(len(payload) == expected_size and sha256(payload) == expected_hash)
            path = PurePosixPath(info.filename)
            require(not path.is_absolute() and ".." not in path.parts)
            destination = root.joinpath(*path.parts)
            destination.parent.mkdir(parents=True, exist_ok=True)
            require(not destination.exists())
            destination.write_bytes(payload)


def safe_oci_materialize(source: Path, destination: Path) -> None:
    data = source.read_bytes()
    require(len(data) == ARCHIVE_BYTES and sha256(data) == ARCHIVE_SHA256)
    seen: set[str] = set()
    total = 0
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:") as archive:
        for member in archive:
            require(member.name not in seen)
            seen.add(member.name)
            path = PurePosixPath(member.name)
            require(not path.is_absolute() and ".." not in path.parts)
            require(member.isdir() or member.isreg())
            require(not member.issym() and not member.islnk() and not member.isdev())
            require(member.name in ("oci-layout", "index.json", "blobs", "blobs/sha256") or
                    re.fullmatch(r"blobs/sha256/[0-9a-f]{64}", member.name))
            target = destination.joinpath(*path.parts)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
                continue
            total += member.size
            require(total <= ARCHIVE_BYTES)
            payload = archive.extractfile(member).read()
            require(len(payload) == member.size)
            if member.name.startswith("blobs/sha256/"):
                require(sha256(payload) == path.name)
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(payload)
    require({"oci-layout", "index.json", "blobs", "blobs/sha256"} <= seen)


def validate_artifact_identity(root: Path) -> None:
    public = root / ".security/public"
    layout = decode((public / "layout.json").read_bytes())
    reader = decode((public / "reader.json").read_bytes())
    require(layout["sourceCommit"] == SOURCE_COMMIT and layout["archiveSha256"] == ARCHIVE_SHA256)
    require(layout["archiveBytes"] == ARCHIVE_BYTES and layout["digest"] == IMAGE_DIGEST)
    require(layout["platform"] == "linux/amd64" and layout["registryPush"] is False)
    require(reader["digest"] == IMAGE_DIGEST and reader["configDigest"] == CONFIG_DIGEST)
    require(reader["fullLayerValidation"] is True and reader["registryPush"] is False)
    source = decode((public / "source-identity.json").read_bytes())
    require(source == {"repository": REPOSITORY, "runAttempt": SOURCE_RUN_ATTEMPT,
            "runId": SOURCE_RUN_ID, "sourceCommit": SOURCE_COMMIT,
            "sourceScanSha256": EXPECTED_MEMBERS[".security/public/source-scan.json"][1]})
    proof = decode((public / "image-proof.json").read_bytes())
    identity = proof["identity"]
    require(identity["repository"] == REPOSITORY and identity["sourceCommit"] == SOURCE_COMMIT)
    require(identity["runId"] == SOURCE_RUN_ID and identity["runAttempt"] == SOURCE_RUN_ATTEMPT)
    require(identity["service"] == "backend" and identity["imageDigest"] == IMAGE_DIGEST)
    require(identity["configDigest"] == CONFIG_DIGEST and identity["archiveSha256"] == ARCHIVE_SHA256)
    release = decode((public / "release-record.json").read_bytes())
    require(release["workflowRef"] == REPOSITORY + "/.github/workflows/ci.yml@refs/heads/main")
    require(release["sourceCommit"] == SOURCE_COMMIT and release["runId"] == SOURCE_RUN_ID)
    require(release["runAttempt"] == SOURCE_RUN_ATTEMPT and release["published"] is False)
    require(release["deploymentAuthorized"] is False and release["isolatedRuntimePassed"] is True)
    # Historical scan/runtime bytes establish artifact provenance only; they are
    # deliberately not treated as current evidence.
    require(timestamp(release["validUntil"]) < timestamp(STATIC_VALID_UNTIL))

    oci = root / ".oci/layout"
    index = decode((oci / "index.json").read_bytes())
    require(len(index["manifests"]) == 1 and index["manifests"][0]["digest"] == IMAGE_DIGEST)
    manifest = decode((oci / "blobs/sha256" / IMAGE_DIGEST[7:]).read_bytes())
    require(manifest["config"]["digest"] == CONFIG_DIGEST)
    config = decode((oci / "blobs/sha256" / CONFIG_DIGEST[7:]).read_bytes())
    require(config["architecture"] == "amd64" and config["os"] == "linux")
    require(config["config"]["Entrypoint"] == ["/server"] and config["config"]["User"] == "10001:10001")
    require(config["config"]["Labels"]["org.opencontainers.image.revision"] == SOURCE_COMMIT)
    require(config["config"]["Labels"]["org.opencontainers.image.source"] == "https://github.com/" + REPOSITORY)


def prepare(api: GitHubAPI, env: dict[str, str] | None = None) -> None:
    env = dict(os.environ if env is None else env)
    require(now() < timestamp(STATIC_VALID_UNTIL), "fresh static scan binding expired")
    context = check_context(env, changed_paths(env.get("GITHUB_SHA")))
    validate_source_files()
    api_details = validate_api_metadata(api, context)
    PRIVATE.mkdir(parents=True, exist_ok=False)
    workspace = PRIVATE / "artifact"
    workspace.mkdir()
    safe_zip_materialize(api.artifact_zip(), workspace)
    safe_oci_materialize(workspace / ".oci/image.tar", workspace / ".oci/layout")
    validate_artifact_identity(workspace)
    # Materialize exact checked bytes for unchanged runtime.py.  Never copy an
    # unverified path, and retain the input runtime hash in metadata.
    Path(".oci").mkdir(exist_ok=True)
    Path(".security/public").mkdir(parents=True, exist_ok=True)
    Path(".oci/image.tar").write_bytes((workspace / ".oci/image.tar").read_bytes())
    safe_oci_materialize(Path(".oci/image.tar"), Path(".oci/layout"))
    for name in ("layout.json", "reader.json"):
        Path(".security/public", name).write_bytes((workspace / ".security/public" / name).read_bytes())
    metadata = {"context": context, "api": api_details,
                "inputRuntimeSha256": EXPECTED_MEMBERS[".security/public/runtime.json"][1]}
    (PRIVATE / "metadata.json").write_text(json.dumps(metadata, sort_keys=True) + "\n")


def finalize(env: dict[str, str] | None = None) -> dict[str, object]:
    env = dict(os.environ if env is None else env)
    instant = now()
    require(timestamp(STATIC_SCANNED_AT) <= instant < timestamp(STATIC_VALID_UNTIL),
            "fresh static scan binding is future-dated or expired")
    metadata = decode((PRIVATE / "metadata.json").read_bytes())
    context = check_context(env, changed_paths(env.get("GITHUB_SHA")))
    require(metadata["context"] == context)
    runtime_bytes = Path(".security/public/runtime.json").read_bytes()
    runtime = decode(runtime_bytes)
    require(runtime == {"archiveSha256": ARCHIVE_SHA256, "capDrop": "ALL",
            "configDigest": CONFIG_DIGEST, "digest": IMAGE_DIGEST, "network": "internal",
            "passed": True, "readOnly": True, "service": "backend"})
    receipt = {
        "schema": "core-platform/retained-runtime-refresh/v1",
        "issuer": {**context, "event": "workflow_dispatch", "ref": "refs/heads/main", "protectedRef": True},
        "sourceArtifact": {"repository": REPOSITORY, "sourceCommit": SOURCE_COMMIT,
            "runId": SOURCE_RUN_ID, "runAttempt": SOURCE_RUN_ATTEMPT,
            "jobId": metadata["api"]["sourceJobId"], "artifactId": ARTIFACT_ID,
            "artifactName": ARTIFACT_NAME, "artifactBytes": ARTIFACT_BYTES,
            "artifactSha256": ARTIFACT_SHA256, "artifactCreatedAt": metadata["api"]["artifactCreatedAt"],
            "artifactExpiresAt": metadata["api"]["artifactExpiresAt"]},
        "image": {"archiveBytes": ARCHIVE_BYTES, "archiveSha256": ARCHIVE_SHA256,
            "imageDigest": IMAGE_DIGEST, "configDigest": CONFIG_DIGEST, "platform": "linux/amd64"},
        "runtime": {"runtimeSha256": sha256(runtime_bytes), "inputRuntimeSha256": metadata["inputRuntimeSha256"],
            "producerGitBlobSha1": RUNTIME_BLOB_SHA1,
            "passed": True, "readOnly": True, "capDrop": "ALL", "network": "internal",
            "migrationAndServiceSameImage": True, "restartReadinessPassed": True},
        "staticScanEvidence": {**STATIC_EVIDENCE, "status": "scanned-policy-passed",
            "scannedAt": STATIC_SCANNED_AT, "validUntil": STATIC_VALID_UNTIL,
            "externalEvidenceHashReference": True, "evidenceBytesVerifiedByThisWorkflow": False,
            "warningFindingCount": 5},
        "createdAt": instant.isoformat(), "validUntil": STATIC_VALID_UNTIL,
        "published": False, "deploymentAuthorized": False,
    }
    PUBLIC.mkdir(parents=True, exist_ok=False)
    (PUBLIC / "runtime.json").write_bytes(runtime_bytes)
    (PUBLIC / "receipt.json").write_text(json.dumps(receipt, sort_keys=True, indent=2) + "\n")
    return receipt


def main() -> None:
    require(len(sys.argv) == 2 and sys.argv[1] in ("prepare", "finalize"))
    try:
        if sys.argv[1] == "prepare":
            prepare(GitHubAPI(os.environ.get("GITHUB_TOKEN", "")))
        else:
            finalize()
    except Exception:
        # Do not print headers, signed URLs, tokens, artifact payloads, or DB data.
        print("Retained runtime refresh refused; no evidence was issued.", file=sys.stderr)
        raise SystemExit(1)


if __name__ == "__main__":
    main()
