import importlib.util
import json
import pathlib
import unittest

MODULE = pathlib.Path(__file__).with_name("four_service_db.py")
SPEC = importlib.util.spec_from_file_location("four_service_db", MODULE)
four_service_db = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(four_service_db)


def event(package, test, action):
    return json.dumps({"Package": package, "Test": test, "Action": action})


class GateTests(unittest.TestCase):
    def complete(self):
        return [
            event(package, test, "pass")
            for package, tests in four_service_db.EXPECTED.items()
            for test in tests
        ]

    def test_complete_passes(self):
        self.assertEqual(four_service_db.evaluate(self.complete(), 0), [])

    def test_real_suite_is_race_checked_and_bounded(self):
        command = four_service_db.build_command()
        self.assertEqual(sum(map(len, four_service_db.EXPECTED.values())), 12)
        self.assertIn("-race", command)
        self.assertIn("-timeout=6m", command)
        self.assertIn("github.com/syuhei0519/backend-app/internal/ordertest", command)
        self.assertIn("TestDisposableStartupFailureCleansUp", " ".join(command))
        self.assertIn("TestDisposableCancellationCleanup", " ".join(command))
        self.assertIn("TestDisposableAccountE1CompatibilityMatrix", " ".join(command))

    def test_skip_fails(self):
        lines = self.complete()
        package, test = next(
            (package, test)
            for package, tests in four_service_db.EXPECTED.items()
            for test in tests
        )
        lines.append(event(package, test, "skip"))
        self.assertIn("got skip", "\n".join(four_service_db.evaluate(lines, 0)))

    def test_unrun_fails(self):
        self.assertIn("got unrun", "\n".join(four_service_db.evaluate([], 0)))

    def test_assertion_or_process_failure_fails(self):
        lines = self.complete()
        package, test = next(
            (package, test)
            for package, tests in four_service_db.EXPECTED.items()
            for test in tests
        )
        lines.append(event(package, test, "fail"))
        failures = "\n".join(four_service_db.evaluate(lines, 1))
        self.assertIn("got fail", failures)
        self.assertIn("go test exited 1", failures)

    def test_required_ci_runs_and_cleans_the_gate(self):
        workflow = (MODULE.parents[2] / ".github" / "workflows" / "ci.yml").read_text(
            encoding="utf-8"
        )
        self.assertIn("python3 -I ci/github/four_service_db_test.py -v", workflow)
        self.assertIn("python3 -I ci/github/four_service_db.py", workflow)
        self.assertIn("label=backend-four-service-run=$FOUR_SERVICE_RUN_ID", workflow)
        self.assertIn("if: always()", workflow)


if __name__ == "__main__":
    unittest.main()
