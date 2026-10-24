import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('sonar', Path(__file__).with_name('sonar-main.py'))
s = importlib.util.module_from_spec(spec); spec.loader.exec_module(s)


class MainOnly(unittest.TestCase):
    def setUp(self):
        self.env = {'SONAR_TOKEN': 'synthetic-not-a-credential', 'SONAR_HOST_URL': 'https://sonarqube.platform.test'}
        self.values = ['main', '', 'a' * 40, 'https://github.com/syuhei0519/backend-app.git', 'a' * 40 + '\trefs/heads/main']

    def test_exact_main(self):
        with patch.object(s, 'git', side_effect=self.values):
            self.assertEqual(s.preflight(self.env), 'a' * 40)

    def test_feature_dirty_stale_or_foreign_repository_refused(self):
        for index, value in [(0, 'feature'), (1, 'M application.go'), (3, 'https://github.com/other/repo.git'),
                             (4, 'b' * 40 + '\trefs/heads/main')]:
            values = self.values.copy(); values[index] = value
            with patch.object(s, 'git', side_effect=values), self.assertRaises(ValueError):
                s.preflight(self.env)

    def test_no_credentials_or_plaintext_endpoint(self):
        for change in ({'SONAR_TOKEN': ''}, {'SONAR_HOST_URL': 'http://server'},
                       {'SONAR_HOST_URL': 'https://user:secret@server'}, {'SONAR_HOST_URL': 'https://server/?token=value'}):
            with patch.object(s, 'git') as git, self.assertRaises(ValueError):
                s.preflight({**self.env, **change})
            git.assert_not_called()

    def test_timeout_stops_only_owned_container(self):
        with patch.object(s.subprocess, 'run', side_effect=[subprocess.TimeoutExpired('docker', 1),
                subprocess.CompletedProcess([], 0)]) as run:
            with self.assertRaises(subprocess.TimeoutExpired):
                s.run_container(['docker', 'run', 'scanner'], 1)
        invocation = run.call_args_list[0].args[0]
        name = invocation[invocation.index('--name') + 1]
        self.assertRegex(name, r'^plat-sonar-[0-9a-f]{32}$')
        self.assertEqual(run.call_args_list[1].args[0], ['docker', 'rm', '--force', name])
        self.assertEqual(run.call_args_list[1].kwargs['timeout'], 20)

    def test_cleanup_failure_never_reports_scanner_success(self):
        with patch.object(s.subprocess, 'run', side_effect=[subprocess.CompletedProcess([], 0),
                subprocess.CompletedProcess([], 1)]):
            with self.assertRaises(ValueError): s.run_container(['docker', 'run', 'scanner'], 1)

    def test_explicit_truststore_mount_and_missing_file_refusal(self):
        self.assertEqual(s.truststore_arguments({}), [])
        with tempfile.TemporaryDirectory() as temporary:
            file = Path(temporary) / 'truststore.p12'
            with self.assertRaises(ValueError): s.truststore_arguments({'SONAR_TRUSTSTORE_PATH': str(file)})
            file.write_bytes(b'public certificate truststore fixture')
            self.assertEqual(s.truststore_arguments({'SONAR_TRUSTSTORE_PATH': str(file)}),
                             ['-v', str(file.resolve()) + ':/tmp/sonar/ssl/truststore.p12:ro'])


if __name__ == '__main__':
    unittest.main()
