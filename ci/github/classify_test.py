import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("classify.py").resolve()


class ClassifyTests(unittest.TestCase):
    def classify(self, path, event_name, ref, before, base=None):
        event = {"repository": {"default_branch": "main"}, "before": before}
        if event_name == "pull_request":
            event["pull_request"] = {"base": {"sha": base}}
        event_path = path / "event.json"
        output = path / "output"
        event_path.write_text(json.dumps(event), encoding="utf-8")
        env = {
            **os.environ,
            "GITHUB_EVENT_PATH": str(event_path),
            "GITHUB_EVENT_NAME": event_name,
            "GITHUB_REF": ref,
            "GITHUB_OUTPUT": str(output),
        }
        subprocess.run([sys.executable, "-I", str(SCRIPT)], cwd=path, env=env, check=True)
        return dict(line.split("=", 1) for line in output.read_text(encoding="utf-8").splitlines())

    def repository(self, filename, content):
        root = Path(tempfile.mkdtemp(prefix=".classify-", dir=Path.cwd()))
        self.addCleanup(shutil.rmtree, root, ignore_errors=True)
        subprocess.run(["git", "init", "-b", "main"], cwd=root, check=True, capture_output=True)
        subprocess.run(["git", "config", "user.name", "fixture"], cwd=root, check=True)
        subprocess.run(["git", "config", "user.email", "fixture@example.invalid"], cwd=root, check=True)
        (root / "README.md").write_text("base\n", encoding="utf-8")
        subprocess.run(["git", "add", "."], cwd=root, check=True)
        subprocess.run(["git", "commit", "-m", "base"], cwd=root, check=True, capture_output=True)
        base = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
        target = root / filename
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")
        subprocess.run(["git", "add", "."], cwd=root, check=True)
        subprocess.run(["git", "commit", "-m", "change"], cwd=root, check=True, capture_output=True)
        return root, base

    def test_docs_pull_request_stays_lightweight(self):
        root, base = self.repository("docs/change.md", "docs\n")
        result = self.classify(root, "pull_request", "refs/pull/1/merge", "", base)
        self.assertEqual(result["docs"], "true")

    def test_docs_protected_main_push_requires_full_ci(self):
        root, base = self.repository("docs/change.md", "docs\n")
        result = self.classify(root, "push", "refs/heads/main", base)
        self.assertEqual(result["docs"], "false")

    def test_source_pull_request_requires_full_ci(self):
        root, base = self.repository("internal/change.go", "package internal\n")
        result = self.classify(root, "pull_request", "refs/pull/2/merge", "", base)
        self.assertEqual(result["docs"], "false")

    def test_unknown_main_base_fails_closed(self):
        root, _ = self.repository("docs/change.md", "docs\n")
        result = self.classify(root, "push", "refs/heads/main", "0" * 40)
        self.assertEqual(result["docs"], "false")


if __name__ == "__main__":
    unittest.main()
