# 固定 artifact の取得・展開・identity・receipt をネットワークや Docker なしで検査します。
import datetime as dt
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import tarfile
import tempfile
import unittest
from unittest import mock
import zipfile

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("retained_runtime_refresh", HERE / "retained_runtime_refresh.py")
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
publish_spec = importlib.util.spec_from_file_location("retained_publish", HERE / "retained_publish.py")
rp = importlib.util.module_from_spec(publish_spec); publish_spec.loader.exec_module(rp)


def encoded(value):
    return (json.dumps(value, sort_keys=True) + "\n").encode()


def context_env():
    return {
        "GITHUB_REPOSITORY": m.REPOSITORY,
        "GITHUB_EVENT_NAME": "workflow_dispatch",
        "GITHUB_REF": "refs/heads/main",
        "GITHUB_REF_PROTECTED": "true",
        "GITHUB_WORKFLOW_REF": m.WORKFLOW_REF,
        "GITHUB_SHA": "a" * 40,
        "GITHUB_RUN_ID": "500",
        "GITHUB_RUN_ATTEMPT": "2",
    }


class Workdir(unittest.TestCase):
    def setUp(self):
        self.old = Path.cwd()
        self.temp = tempfile.TemporaryDirectory()
        os.chdir(self.temp.name)
        self.addCleanup(os.chdir, self.old)
        self.addCleanup(self.temp.cleanup)


class ContextContract(unittest.TestCase):
    def test_exact_protected_main_dispatch_and_seven_files(self):
        result = m.check_context(context_env(), set(m.FULL_CHANGE_SET))
        self.assertEqual(result["runId"], 500)
        self.assertEqual(result["workflowCommit"], "a" * 40)

    def test_publish_is_opt_in_and_exact_digest(self):
        workflow = (HERE.parents[1] / ".github/workflows/retained-runtime-refresh.yml").read_text()
        self.assertIn("default: false", workflow)
        self.assertIn("environment: ghcr-publish", workflow)
        self.assertIn(
            "inputs.publish && vars.ENABLE_RETAINED_ACCOUNT_GHCR_PUBLISH == 'true'",
            workflow,
        )
        self.assertNotIn("vars.ENABLE_GHCR_PUBLISH", workflow)
        ci_workflow = (HERE.parents[1] / ".github/workflows/ci.yml").read_text()
        self.assertIn("vars.ENABLE_GHCR_PUBLISH == 'true'", ci_workflow)
        self.assertIn("packages: write", workflow)
        self.assertIn("retained_publish.py", workflow)
        self.assertEqual(rp.MANIFEST, m.IMAGE_DIGEST)

    def test_publish_tag_state_is_fail_closed(self):
        result = lambda code=0, out="", err="": m.subprocess.CompletedProcess([], code, out, err)
        self.assertEqual(rp.state(result(out=rp.MANIFEST)), "same")
        self.assertEqual(rp.state(result(1, err="MANIFEST_UNKNOWN")), "absent")
        for value in (result(out="sha256:" + "0" * 64), result(1, err="DENIED"),
                      result(1, err="MANIFEST_UNKNOWN unauthorized"), result(1, err="timeout")):
            with self.assertRaises(rp.PublishError): rp.state(value)

    def test_failed_registry_write_is_not_retried(self):
        responses = iter([m.subprocess.CompletedProcess([], 0, "", ""),
            m.subprocess.CompletedProcess([], 1, "", "MANIFEST_UNKNOWN"),
            m.subprocess.CompletedProcess([], 1, "", "MANIFEST_UNKNOWN"),
            m.subprocess.CompletedProcess([], 1, "", "uncertain")])
        calls = []
        def fake(args, secret=None): calls.append(args); return next(responses)
        with mock.patch.object(rp, "run", side_effect=fake), mock.patch.dict(os.environ, {"GITHUB_ACTOR":"actor", "GH_TOKEN":"token"}), self.assertRaises(rp.PublishError):
            rp.publish("layout")
        self.assertEqual(sum(call[0] == "oci-layout-push" for call in calls), 1)

    def test_context_refusals(self):
        mutations = {
            "GITHUB_REPOSITORY": "other/backend-app",
            "GITHUB_EVENT_NAME": "push",
            "GITHUB_REF": "refs/heads/feature",
            "GITHUB_REF_PROTECTED": "false",
            "GITHUB_WORKFLOW_REF": m.REPOSITORY + "/.github/workflows/ci.yml@refs/heads/main",
            "GITHUB_SHA": m.SOURCE_COMMIT,
            "GITHUB_RUN_ID": "0",
            "GITHUB_RUN_ATTEMPT": "x",
        }
        for key, value in mutations.items():
            with self.subTest(key=key), self.assertRaises(ValueError):
                m.check_context({**context_env(), key: value}, set(m.FULL_CHANGE_SET))

    def test_path_scope_refusals(self):
        for changed in (set(), set(m.FULL_CHANGE_SET) - {"ci/github/classify.py"},
                        set(m.FULL_CHANGE_SET) | {"cmd/server/main.go"}):
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                m.check_context(context_env(), changed)

    def test_rescan_identity_is_exact_and_has_no_exception_path(self):
        self.assertEqual(m.TRIVY_VERSION, "0.75.0")
        self.assertIn("@sha256:", m.TRIVY_IMAGE)
        self.assertRegex(m.SCAN_POLICY_SHA256, r"^[0-9a-f]{64}$")
        workflow = (HERE.parents[1] / ".github/workflows/retained-runtime-refresh.yml").read_text()
        self.assertIn("--input .oci/layout", workflow)
        self.assertIn("--ignorefile .security/private/retained-rescan/empty-ignore", workflow)
        self.assertNotIn("--ignore-unfixed", workflow)

    def test_checkout_head_must_equal_github_issuer_commit(self):
        result = m.subprocess.CompletedProcess([], 0, stdout="b" * 40 + "\n")
        with mock.patch.object(m.subprocess, "run", return_value=result), self.assertRaises(ValueError):
            m.changed_paths("a" * 40)


class MetadataContract(unittest.TestCase):
    class FakeAPI:
        def json(self, path):
            if path.endswith("/actions/runs/500"):
                return {"id": 500, "run_attempt": 2, "head_sha": "a" * 40, "head_branch": "main",
                        "event": "workflow_dispatch", "path": ".github/workflows/retained-runtime-refresh.yml",
                        "repository": {"full_name": m.REPOSITORY}}
            if path.endswith(f"/actions/runs/{m.SOURCE_RUN_ID}"):
                return {"id": m.SOURCE_RUN_ID, "run_attempt": 1, "head_sha": m.SOURCE_COMMIT,
                        "head_branch": "main", "event": "push", "path": ".github/workflows/ci.yml",
                        "repository": {"full_name": m.REPOSITORY}}
            if path.endswith(f"/actions/artifacts/{m.ARTIFACT_ID}"):
                return {"id": m.ARTIFACT_ID, "name": m.ARTIFACT_NAME, "size_in_bytes": m.ARTIFACT_BYTES,
                        "expired": False, "digest": "sha256:" + m.ARTIFACT_SHA256,
                        "expires_at": m.ARTIFACT_EXPIRES_AT, "created_at": "2026-10-25T16:21:05Z",
                        "archive_download_url": f"https://api.github.com/repos/{m.REPOSITORY}/actions/artifacts/{m.ARTIFACT_ID}/zip",
                        "workflow_run": {"id": m.SOURCE_RUN_ID, "repository_id": 1410557587,
                            "head_repository_id": 1410557587, "head_branch": "main", "head_sha": m.SOURCE_COMMIT}}
            if "/attempts/1/jobs" in path:
                return {"jobs": [{"id": m.SOURCE_JOB_ID, "name": "image (backend)",
                                   "status": "completed", "conclusion": "success"}]}
            raise AssertionError(path)

    def test_source_run_job_artifact_and_current_issuer(self):
        result = m.validate_api_metadata(self.FakeAPI(), m.check_context(context_env(), set(m.FULL_CHANGE_SET)))
        self.assertEqual(result["sourceJobId"], m.SOURCE_JOB_ID)
        self.assertEqual(result["artifactExpiresAt"], m.ARTIFACT_EXPIRES_AT)

    def test_artifact_digest_or_job_failure_is_refused(self):
        class Bad(self.FakeAPI):
            def json(self, path):
                value = super().json(path)
                if path.endswith(f"/actions/artifacts/{m.ARTIFACT_ID}"):
                    value["digest"] = "sha256:" + "0" * 64
                return value
        with self.assertRaises(ValueError):
            m.validate_api_metadata(Bad(), m.check_context(context_env(), set(m.FULL_CHANGE_SET)))

    def test_github_token_header_is_only_on_api_request(self):
        api = m.GitHubAPI("private-sentinel")
        request = api._request(f"/repos/{m.REPOSITORY}/actions/runs/1")
        self.assertEqual(request.get_header("Authorization"), "Bearer private-sentinel")
        with self.assertRaises(ValueError):
            api._request("/repos/other/repository/actions/runs/1")

    def test_redirect_handler_refuses_automatic_token_forwarding(self):
        request = m.urllib.request.Request("https://api.github.com/source", headers={"Authorization": "Bearer secret"})
        with self.assertRaises(m.urllib.error.HTTPError) as caught:
            m.NoRedirect().redirect_request(request, None, 302, "Found", {"Location": "https://blob.invalid/signed"},
                                             "https://blob.invalid/signed")
        self.assertEqual(caught.exception.code, 302)

    def test_artifact_download_uses_a_fresh_unauthorized_blob_request(self):
        payload = b"zip"
        class Response:
            def __init__(self, data): self.data = data
            def __enter__(self): return self
            def __exit__(self, *args): return False
            def read(self, limit): return self.data[:limit]
        class Opener:
            def __init__(self): self.calls = []
            def open(self, request, timeout):
                self.calls.append(request)
                if len(self.calls) == 1:
                    raise m.urllib.error.HTTPError(request.full_url, 302, "Found",
                        {"Location": "https://example.blob.core.windows.net/result?sig=redacted"}, None)
                self_test.assertIsNone(request.get_header("Authorization"))
                return Response(payload)
        self_test = self
        api = m.GitHubAPI("private-sentinel"); api.opener = Opener()
        with mock.patch.object(m, "ARTIFACT_BYTES", len(payload)), \
             mock.patch.object(m, "ARTIFACT_SHA256", m.sha256(payload)):
            self.assertEqual(api.artifact_zip(), payload)
        self.assertEqual(api.opener.calls[0].get_header("Authorization"), "Bearer private-sentinel")


class SafeArchives(Workdir):
    def make_zip(self, members, modes=None):
        out = io.BytesIO()
        with zipfile.ZipFile(out, "w") as archive:
            for name, payload in members.items():
                info = zipfile.ZipInfo(name)
                info.external_attr = ((modes or {}).get(name, stat.S_IFREG | 0o600)) << 16
                archive.writestr(info, payload)
        return out.getvalue()

    def test_exact_regular_zip_members_materialize(self):
        data = self.make_zip({"a/file": b"payload"})
        with mock.patch.object(m, "ARTIFACT_BYTES", len(data)), \
             mock.patch.object(m, "ARTIFACT_SHA256", m.sha256(data)), \
             mock.patch.object(m, "EXPECTED_MEMBERS", {"a/file": (7, m.sha256(b"payload"))}):
            m.safe_zip_materialize(data, Path("out"))
        self.assertEqual(Path("out/a/file").read_bytes(), b"payload")

    def test_zip_extra_traversal_symlink_and_digest_refused(self):
        cases = [
            ({"a": b"x", "b": b"y"}, None),
            ({"../a": b"x"}, None),
            ({"a": b"x"}, {"a": stat.S_IFLNK | 0o777}),
        ]
        for members, modes in cases:
            data = self.make_zip(members, modes)
            expected = {next(iter(members)): (1, m.sha256(b"x"))}
            with self.subTest(members=members), mock.patch.object(m, "ARTIFACT_BYTES", len(data)), \
                 mock.patch.object(m, "ARTIFACT_SHA256", m.sha256(data)), \
                 mock.patch.object(m, "EXPECTED_MEMBERS", expected), self.assertRaises(ValueError):
                m.safe_zip_materialize(data, Path("out-" + str(len(list(Path('.').glob('out-*'))))))

    def make_tar(self, members):
        out = io.BytesIO()
        with tarfile.open(fileobj=out, mode="w") as archive:
            for name, kind, payload in members:
                info = tarfile.TarInfo(name)
                info.type = kind
                if kind == tarfile.REGTYPE:
                    info.size = len(payload)
                    archive.addfile(info, io.BytesIO(payload))
                else:
                    archive.addfile(info)
        return out.getvalue()

    def test_oci_regular_files_are_content_addressed(self):
        payload = b"blob"
        digest = m.sha256(payload)
        data = self.make_tar([("blobs", tarfile.DIRTYPE, b""), ("blobs/sha256", tarfile.DIRTYPE, b""),
            ("blobs/sha256/" + digest, tarfile.REGTYPE, payload),
            ("index.json", tarfile.REGTYPE, b"{}"), ("oci-layout", tarfile.REGTYPE, b"{}")])
        Path("image.tar").write_bytes(data)
        with mock.patch.object(m, "ARCHIVE_BYTES", len(data)), mock.patch.object(m, "ARCHIVE_SHA256", m.sha256(data)):
            m.safe_oci_materialize(Path("image.tar"), Path("layout"))
        self.assertEqual(Path("layout/blobs/sha256", digest).read_bytes(), payload)

    def test_oci_link_and_bad_blob_name_are_refused(self):
        for name, kind in (("link", tarfile.SYMTYPE), ("blobs/sha256/" + "0" * 64, tarfile.REGTYPE)):
            payload = b"bad"
            data = self.make_tar([("blobs", tarfile.DIRTYPE, b""), ("blobs/sha256", tarfile.DIRTYPE, b""),
                (name, kind, payload), ("index.json", tarfile.REGTYPE, b"{}"),
                ("oci-layout", tarfile.REGTYPE, b"{}")])
            path = Path("bad.tar"); path.write_bytes(data)
            with self.subTest(name=name), mock.patch.object(m, "ARCHIVE_BYTES", len(data)), \
                 mock.patch.object(m, "ARCHIVE_SHA256", m.sha256(data)), self.assertRaises(ValueError):
                m.safe_oci_materialize(path, Path("bad-layout-" + str(len(list(Path('.').glob('bad-layout-*'))))))


class IdentityContract(Workdir):
    def fixture(self):
        public = Path("artifact/.security/public"); public.mkdir(parents=True)
        layout = {"sourceCommit": m.SOURCE_COMMIT, "archiveSha256": m.ARCHIVE_SHA256,
                  "archiveBytes": m.ARCHIVE_BYTES, "digest": m.IMAGE_DIGEST,
                  "platform": "linux/amd64", "registryPush": False}
        reader = {"digest": m.IMAGE_DIGEST, "configDigest": m.CONFIG_DIGEST,
                  "fullLayerValidation": True, "registryPush": False}
        source = {"repository": m.REPOSITORY, "runAttempt": 1, "runId": m.SOURCE_RUN_ID,
                  "sourceCommit": m.SOURCE_COMMIT,
                  "sourceScanSha256": m.EXPECTED_MEMBERS[".security/public/source-scan.json"][1]}
        identity = {"repository": m.REPOSITORY, "sourceCommit": m.SOURCE_COMMIT,
                    "runId": m.SOURCE_RUN_ID, "runAttempt": 1, "service": "backend",
                    "imageDigest": m.IMAGE_DIGEST, "configDigest": m.CONFIG_DIGEST,
                    "archiveSha256": m.ARCHIVE_SHA256}
        release = {"workflowRef": m.REPOSITORY + "/.github/workflows/ci.yml@refs/heads/main",
                   "sourceCommit": m.SOURCE_COMMIT, "runId": m.SOURCE_RUN_ID, "runAttempt": 1,
                   "published": False, "deploymentAuthorized": False, "isolatedRuntimePassed": True,
                   "validUntil": "2026-10-26T12:57:48.463991Z"}
        for name, value in (("layout.json", layout), ("reader.json", reader),
                            ("source-identity.json", source), ("image-proof.json", {"identity": identity}),
                            ("release-record.json", release)):
            (public / name).write_bytes(encoded(value))
        oci = Path("artifact/.oci/layout/blobs/sha256"); oci.mkdir(parents=True)
        config = {"architecture": "amd64", "os": "linux", "config": {"Entrypoint": ["/server"],
                  "User": "10001:10001", "Labels": {"org.opencontainers.image.revision": m.SOURCE_COMMIT,
                  "org.opencontainers.image.source": "https://github.com/" + m.REPOSITORY}}}
        manifest = {"config": {"digest": m.CONFIG_DIGEST}}
        (oci / m.CONFIG_DIGEST[7:]).write_bytes(encoded(config))
        (oci / m.IMAGE_DIGEST[7:]).write_bytes(encoded(manifest))
        Path("artifact/.oci/layout/index.json").write_bytes(encoded({"manifests": [{"digest": m.IMAGE_DIGEST}]}))

    def test_exact_identity_is_accepted_without_reviving_old_scan(self):
        self.fixture()
        m.validate_artifact_identity(Path("artifact"))

    def test_manifest_config_or_source_mismatch_is_refused(self):
        for target in ("layout", "source", "config"):
            with self.subTest(target=target):
                if Path("artifact").exists():
                    import shutil; shutil.rmtree("artifact")
                self.fixture()
                if target == "layout":
                    path = Path("artifact/.security/public/layout.json"); value = json.loads(path.read_text()); value["digest"] = "sha256:" + "0" * 64
                elif target == "source":
                    path = Path("artifact/.security/public/source-identity.json"); value = json.loads(path.read_text()); value["runId"] = 1
                else:
                    path = Path("artifact/.oci/layout/blobs/sha256", m.CONFIG_DIGEST[7:]); value = json.loads(path.read_text()); value["config"]["User"] = "0"
                path.write_bytes(encoded(value))
                with self.assertRaises(ValueError): m.validate_artifact_identity(Path("artifact"))


class ReceiptContract(Workdir):
    def setup_inputs(self, scanned="2026-10-29T16:00:00+00:00",
                     valid="2026-10-30T16:00:00+00:00"):
        m.PRIVATE.mkdir(parents=True)
        context = m.check_context(context_env(), set(m.FULL_CHANGE_SET))
        metadata = {"context": context, "api": {"sourceJobId": m.SOURCE_JOB_ID,
                    "artifactCreatedAt": "2026-10-25T16:21:05Z", "artifactExpiresAt": m.ARTIFACT_EXPIRES_AT},
                    "inputRuntimeSha256": m.EXPECTED_MEMBERS[".security/public/runtime.json"][1]}
        (m.PRIVATE / "metadata.json").write_bytes(encoded(metadata))
        Path(".security/public").mkdir(parents=True)
        runtime = {"archiveSha256": m.ARCHIVE_SHA256, "capDrop": "ALL", "configDigest": m.CONFIG_DIGEST,
                   "digest": m.IMAGE_DIGEST, "network": "internal", "passed": True,
                   "readOnly": True, "service": "backend"}
        Path(".security/public/runtime.json").write_bytes(encoded(runtime))
        m.RESCAN.mkdir(parents=True)
        summary = {"schemaVersion": 1, "pass": True, "trivyVersion": m.TRIVY_VERSION,
                   "dbUpdatedAt": scanned, "scannedAt": scanned, "findings": []}
        summary_bytes = encoded(summary)
        (m.RESCAN / "summary.json").write_bytes(summary_bytes)
        binding = {"schema": "core-platform/retained-image-rescan/v1",
            "imageDigest": m.IMAGE_DIGEST, "configDigest": m.CONFIG_DIGEST,
            "scannerImage": m.TRIVY_IMAGE, "trivyVersion": m.TRIVY_VERSION,
            "scannedAt": scanned, "dbUpdatedAt": scanned, "validUntil": valid,
            "rawScanSha256": "1" * 64, "safeSummarySha256": m.sha256(summary_bytes),
            "databaseSha256": "2" * 64, "scanPolicySha256": m.SCAN_POLICY_SHA256,
            "warningFindingCount": 0}
        (m.RESCAN / "binding.json").write_bytes(encoded(binding))

    def test_receipt_binds_current_issuer_runtime_artifact_and_external_scan(self):
        self.setup_inputs()
        instant = dt.datetime(2026, 10, 29, 16, 5, tzinfo=dt.timezone.utc)
        with mock.patch.object(m, "changed_paths", return_value=set(m.FULL_CHANGE_SET)), \
             mock.patch.object(m, "now", return_value=instant):
            receipt = m.finalize(context_env())
        self.assertEqual(receipt["issuer"]["runId"], 500)
        self.assertEqual(receipt["sourceArtifact"]["artifactSha256"], m.ARTIFACT_SHA256)
        self.assertEqual(receipt["image"]["configDigest"], m.CONFIG_DIGEST)
        self.assertTrue(receipt["runtime"]["migrationAndServiceSameImage"])
        self.assertEqual(receipt["runtime"]["producerGitBlobSha1"], m.RUNTIME_BLOB_SHA1)
        self.assertTrue(receipt["freshImageScan"]["evidenceBytesVerifiedByThisWorkflow"])
        self.assertEqual(receipt["freshImageScan"]["imageDigest"], m.IMAGE_DIGEST)
        self.assertEqual(receipt["validUntil"], "2026-10-30T16:00:00+00:00")
        self.assertFalse(receipt["published"] or receipt["deploymentAuthorized"])

    def test_expired_scan_or_failed_runtime_is_refused(self):
        self.setup_inputs(valid="2026-10-29T16:00:00+00:00")
        with mock.patch.object(m, "changed_paths", return_value=set(m.FULL_CHANGE_SET)), \
             mock.patch.object(m, "now", return_value=dt.datetime(2026, 10, 29, 16, 1, tzinfo=dt.timezone.utc)), \
             self.assertRaises(ValueError):
            m.finalize(context_env())

    def test_future_dated_scan_binding_is_refused(self):
        self.setup_inputs(scanned="2026-10-29T16:10:00+00:00")
        with mock.patch.object(m, "changed_paths", return_value=set(m.FULL_CHANGE_SET)), \
             mock.patch.object(m, "now", return_value=dt.datetime(2026, 10, 29, 16, 5, tzinfo=dt.timezone.utc)), \
             self.assertRaises(ValueError):
            m.finalize(context_env())

    def test_runtime_mutation_is_refused(self):
        self.setup_inputs()
        path = Path(".security/public/runtime.json"); value = json.loads(path.read_text()); value["readOnly"] = False
        path.write_bytes(encoded(value))
        with mock.patch.object(m, "changed_paths", return_value=set(m.FULL_CHANGE_SET)), \
             mock.patch.object(m, "now", return_value=dt.datetime(2026, 10, 29, 16, 5, tzinfo=dt.timezone.utc)), \
             self.assertRaises(ValueError):
            m.finalize(context_env())


class FreshScanContract(Workdir):
    def test_current_db_raw_config_and_safe_summary_are_bound(self):
        m.RESCAN.mkdir(parents=True)
        Path(".cache/retained-trivy/db").mkdir(parents=True)
        Path("security").mkdir()
        policy = (HERE.parents[1] / "security/scan-policy.json").read_bytes()
        Path("security/scan-policy.json").write_bytes(policy)
        instant = dt.datetime(2026, 10, 29, 16, 0, tzinfo=dt.timezone.utc)
        raw = {"SchemaVersion": 2, "Trivy": {"Version": m.TRIVY_VERSION},
               "ArtifactType": "container_image", "ArtifactName": ".oci/layout",
               "Metadata": {"ImageID": m.CONFIG_DIGEST}, "Results": [{"Target": "image"}]}
        db = {"Version": 2, "UpdatedAt": instant.isoformat()}
        summary = {"schemaVersion": 1, "pass": True, "trivyVersion": m.TRIVY_VERSION,
                   "dbUpdatedAt": instant.isoformat(), "scannedAt": instant.isoformat(),
                   "findings": [{"kind": "vulnerability", "rule": "CVE-1", "path": "image/os",
                                 "severity": "HIGH", "decision": "warning"}]}
        (m.RESCAN / "raw.json").write_bytes(encoded(raw))
        Path(".cache/retained-trivy/db/metadata.json").write_bytes(encoded(db))
        (m.RESCAN / "summary.json").write_bytes(encoded(summary))
        with mock.patch.object(m.subprocess, "run", return_value=m.subprocess.CompletedProcess([], 0)), \
             mock.patch.object(m, "now", return_value=instant + dt.timedelta(minutes=1)):
            binding = m.bind_scan()
        self.assertEqual(binding["configDigest"], m.CONFIG_DIGEST)
        self.assertEqual(binding["warningFindingCount"], 1)
        self.assertEqual(binding["scanPolicySha256"], m.SCAN_POLICY_SHA256)


class WorkflowContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = (HERE.parents[1] / ".github/workflows/retained-runtime-refresh.yml").read_text()

    def test_read_only_permissions_and_pinned_actions(self):
        header = self.workflow.split("jobs:", 1)[0]
        self.assertIn("contents: read", header)
        self.assertIn("actions: read", header)
        self.assertNotRegex(header, r"(?m)^\s*(contents|packages|id-token): write")
        for use in [line.split("uses:", 1)[1].strip() for line in self.workflow.splitlines() if "uses:" in line]:
            self.assertRegex(use, r"@[0-9a-f]{40}$")

    def test_runtime_has_no_publish_or_deploy_permissions(self):
        runtime, publish = self.workflow.split("\n  publish:\n    needs: runtime", 1)
        self.assertIn("python3 -I ci/github/runtime.py", runtime)
        self.assertIn("docker pull postgres@sha256:9b18b783", runtime)
        for forbidden in ("docker build", "docker push", "release.py publish", "kubectl", "gh pr", "packages: write"):
            self.assertNotIn(forbidden, runtime)
        self.assertIn("retained_runtime_refresh.py finalize", runtime)
        self.assertIn("packages: write", publish)
        self.assertNotIn("kubectl", publish)

    def test_only_metadata_evidence_is_uploaded(self):
        self.assertIn("path: .security/retained-runtime/", self.workflow)
        self.assertIn("if: inputs.publish", self.workflow)
        self.assertIn("path: .security/private/retained-runtime/.oci/layout/", self.workflow)


if __name__ == "__main__":
    unittest.main()
