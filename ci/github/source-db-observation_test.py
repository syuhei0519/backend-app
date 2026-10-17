"""Source-scan wiring only; fake tools, no network or real scan."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[1] / 'source-scan.sh'


class DBObservationWiring(unittest.TestCase):
    def run_source(self, cached=False, download_failure=False, observation_failure=False, gate_exit=1):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'bin').mkdir()
            (root / '.security').mkdir()
            def script(path, text):
                path.write_text('#!/bin/sh\n' + text)
                path.chmod(0o700)
            script(root / 'bin/git', 'exit 0\n')
            script(root / 'bin/tar', 'exit 0\n')
            script(root / 'bin/sleep', 'exit 0\n')
            script(root / 'bin/trivy', '''
printf '%s\n' "$*" >> calls
if [ "$1" = image ]; then
  if [ "$DOWNLOAD_FAILURE" = true ]; then exit 1; fi
  mkdir -p .cache/trivy/db
  printf '%s\n' '{"Version":2,"UpdatedAt":"2026-10-16T01:00:00Z"}' > .cache/trivy/db/metadata.json
  echo 'INFO Artifact successfully downloaded repo="ghcr.io/aquasecurity/trivy-db:2"'
else
  printf '%s\n' '{}' > .security/private/raw.json
fi
''')
            script(root / '.security/scan-gate', f'exit {gate_exit}\n')
            script(root / '.security/source-db-observation', '''
printf '%s\n' "$*" > observation-args
if [ "$OBSERVATION_FAILURE" = true ]; then exit 1; fi
printf '%s\n' '{"sourceStatus":"fixture","metadata":{"gateReason":"expired"}}'
''')
            if cached:
                (root / '.cache/trivy/db').mkdir(parents=True)
                (root / '.cache/trivy/db/metadata.json').write_text('{}')
            env = {**os.environ, 'PATH': str(root/'bin') + ':' + os.environ['PATH'],
                   'DOWNLOAD_FAILURE': str(download_failure).lower(),
                   'OBSERVATION_FAILURE': str(observation_failure).lower(),
                   'SECURITY_RENDER_REQUIRED': 'false', 'AT11_CASE': ''}
            result = subprocess.run(['sh', str(SOURCE)], cwd=root, env=env, capture_output=True)
            self.assertFalse((root / '.security/private').exists())
            calls = (root / 'calls').read_text().splitlines()
            args = (root / 'observation-args').read_text().strip()
            observation = (root / '.security/public/db-observation.json').read_bytes()
            return result.returncode, calls, args, observation

    def test_gate_failure_retains_observation(self):
        code, calls, args, raw = self.run_source()
        self.assertEqual(code, 1)
        self.assertEqual(len(calls), 2)
        self.assertIn('--db-repository ghcr.io/aquasecurity/trivy-db:2 --db-repository public.ecr.aws/aquasecurity/trivy-db:2', calls[0])
        self.assertNotIn('--quiet', calls[0])
        self.assertIn('--skip-db-update', calls[1])
        self.assertEqual(args, '-cache-metadata-existed=false')
        self.assertEqual(json.loads(raw)['metadata']['gateReason'], 'expired')

    def test_download_failure_retains_cache_observation_without_scan(self):
        code, calls, args, raw = self.run_source(cached=True, download_failure=True)
        self.assertEqual(code, 2)
        self.assertEqual(len(calls), 2)  # Existing bounded transport retry only.
        self.assertTrue(all(c.startswith('image --download-db-only ') for c in calls))
        self.assertEqual(args, '-cache-metadata-existed=true')
        self.assertTrue(raw)

    def test_optional_observation_does_not_change_gate_acceptance(self):
        code, _, _, _ = self.run_source(observation_failure=True, gate_exit=0)
        self.assertEqual(code, 0)


if __name__ == '__main__':
    unittest.main()
