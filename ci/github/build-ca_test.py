"""Synthetic argument/scope regressions; real TLS requires cold image builds."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class BuildTrust(unittest.TestCase):
    def test_run_scope(self):
        for name in ('Dockerfile', 'Dockerfile.order'):
            text = (ROOT / name).read_text()
            dependency = text.split('RUN ', 1)[1].split('\nCOPY . .', 1)[0]
            self.assertIn('type=secret,id=build_ca', dependency)
            self.assertIn('required=false', dependency)
            self.assertIn('export SSL_CERT_FILE=/run/secrets/build_ca', dependency)
            self.assertEqual(text.count('type=secret'), 1)
            self.assertNotIn('build_ca', text.split('\nCOPY . .', 1)[1])
            self.assertNotIn('ENV SSL_CERT', text)
            # Execute the actual shell with only the mount path substituted.
            shell = dependency.split(' \\\n', 1)[1].replace('\\\n', '')
            for mode in ('missing', 'present', 'empty'):
                with tempfile.TemporaryDirectory() as tmp:
                    d = Path(tmp)
                    ca = d / 'bundle'
                    if mode != 'missing':
                        ca.write_text('synthetic trust fixture' if mode == 'present' else '')
                    (d / 'go').write_text('#!/bin/sh\n[ "$1 $2" = "mod download" ] || exit 9\nprintf "%s" "${SSL_CERT_FILE-unset}"\n')
                    (d / 'go').chmod(0o700)
                    env = dict(os.environ, PATH=tmp + ':' + os.environ['PATH'])
                    env.pop('SSL_CERT_FILE', None)
                    p = subprocess.run(['sh', '-c', shell.replace('/run/secrets/build_ca', str(ca))], env=env, capture_output=True, text=True)
                    self.assertEqual(p.returncode, 1 if mode == 'empty' else 0)
                    if mode != 'empty':
                        self.assertEqual(p.stdout, str(ca) if mode == 'present' else 'unset')

    def test_helper_optional_arguments(self):
        for service in ('backend', 'order'):
            for mode in ('missing', 'present', 'empty', 'nonexistent'):
                with tempfile.TemporaryDirectory() as tmp:
                    d = Path(tmp)
                    ca = d / 'existing bundle.pem'
                    if mode in ('present', 'empty'):
                        ca.write_text('synthetic trust fixture' if mode == 'present' else '')
                    (d / 'docker').write_text('#!/usr/bin/env python3\nimport json,sys\nfrom pathlib import Path\nPath("args.json").write_text(json.dumps(sys.argv[1:]))\nsys.exit(73)\n')
                    (d / 'docker').chmod(0o700)
                    env = dict(os.environ, PATH=tmp + ':' + os.environ['PATH'], RELEASE_SERVICE=service, GITHUB_SHA='a' * 40, GITHUB_REPOSITORY='syuhei0519/backend-app')
                    env.pop('OCI_BUILD_CA_FILE', None)
                    if mode != 'missing':
                        env['OCI_BUILD_CA_FILE'] = str(ca)
                    p = subprocess.run(['bash', str(ROOT / 'ci/github/oci.sh')], cwd=d, env=env, capture_output=True)
                    if mode in ('empty', 'nonexistent'):
                        self.assertNotEqual(p.returncode, 0)
                        self.assertFalse((d / 'args.json').exists())
                    else:
                        self.assertEqual(p.returncode, 73)
                        args = json.loads((d / 'args.json').read_text())
                        self.assertEqual(args.count('--secret'), mode == 'present')
                        if mode == 'present':
                            self.assertEqual(args[args.index('--secret') + 1], 'id=build_ca,src=' + str(ca))
                        self.assertEqual(args[args.index('-f') + 1], 'Dockerfile' if service == 'backend' else 'Dockerfile.order')


if __name__ == '__main__':
    unittest.main()
