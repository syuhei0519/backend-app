"""Boundary + real k6/HTTP fixture tests. No DB/cluster or persistent data."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('account_run', ROOT / 'run.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
SHA = 'a' * 40


class Guard(unittest.TestCase):
    def test_boundaries(self):
        for rate, duration in [(1, 1), (5, 60)]:
            c = module.validate('http://127.0.0.1:18080', rate, duration, SHA, 'normal', 'fixture')
            self.assertEqual(c['max_http_requests'], rate * duration)
        for key, values in {
            'target': ['https://example.com', 'http://127.0.0.2:18080', 'http://localhost:18080',
                       'http://127.0.0.1:80', 'http://127.0.0.1:65536',
                       'http://127.0.0.1:18080/', 'http://127.0.0.1:18080?x=1',
                       'http://user@127.0.0.1:18080', 'http://127.0.0.1:18080#x'],
            'rate': [0, 6, 1.5, True], 'duration': [0, 61, 1.5],
            'revision': ['', 'main', 'a' * 39], 'profile': ['crud'], 'label': ['private/id'],
        }.items():
            for value in values:
                args = dict(target='http://127.0.0.1:18080', rate=1, duration=1,
                            revision=SHA, profile='normal', label='fixture')
                args[key] = value
                with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                    module.validate(**args)

    def test_http(self):
        requests = []
        mode = ['normal']

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                requests.append(self.path)
                status = 302 if mode[0] == 'redirect' else 500 if mode[0] == 'failure' else 400 if self.path.endswith('/0') else 200
                self.send_response(status)
                if status == 302:
                    self.send_header('Location', 'http://192.0.2.1:18080/must-not-follow')
                self.end_headers()
                self.wfile.write(b'[]')

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        target = f'http://127.0.0.1:{server.server_port}'
        try:
            with tempfile.TemporaryDirectory() as tmp:
                for profile, failure in [('normal', False), ('mixed-4xx', False), ('redirect', True), ('failure', True)]:
                    mode[0] = profile
                    requests.clear()
                    out = Path(tmp) / profile
                    cmd = [sys.executable, str(ROOT / 'run.py'), '--target', target,
                           '--revision', SHA, '--rate', '2', '--duration', '2',
                           '--profile', 'mixed-4xx' if profile == 'mixed-4xx' else 'normal', '--output', str(out)]
                    # These must not override configured limits or redirect policy.
                    env = dict(os.environ, K6_RATE='1000', K6_DURATION='1h', K6_MAX_REDIRECTS='10',
                               HTTP_PROXY='http://192.0.2.1:9999')
                    result = subprocess.run(cmd, env=env, capture_output=True, text=True)
                    self.assertEqual(result.returncode != 0, failure, result.stderr + result.stdout)
                    self.assertEqual(len(requests), 4)
                    self.assertEqual(requests.count('/api/accounts/0'), 2 if profile == 'mixed-4xx' else 0)
                    summary = json.loads((out / 'summary.json').read_text())
                    metrics = summary['metrics']
                    self.assertEqual(metrics['http_reqs']['values']['count'], 4)
                    self.assertEqual(metrics['iterations']['values']['count'],
                                     4 + metrics['budget_skipped_iterations']['values']['count'])
                    self.assertLessEqual(metrics['vus_max']['values']['max'], 5)
                    self.assertEqual(metrics['http_req_failed']['values']['rate'],
                                     1 if failure else .5 if profile == 'mixed-4xx' else 0)
                    self.assertIn('p(95)', metrics['http_req_duration']['values'])
                before = len(requests)
                for extra in [['--rate', '6'], ['--duration', '61'], ['--target', target + '/']]:
                    result = subprocess.run(cmd + extra, capture_output=True)
                    self.assertNotEqual(result.returncode, 0)
                self.assertEqual(len(requests), before, 'guard must reject before HTTP')
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == '__main__':
    unittest.main()
