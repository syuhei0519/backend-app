#!/usr/bin/env python3
# 遅延を再現する試験サーバーと計測の処理を、ローカルで確認します。
import json
from pathlib import Path
import sys
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent
# Also works with Python -I, like the existing scenario tests.
sys.path.insert(0, str(ROOT))
import latency


class DelayedBody(unittest.TestCase):
    def test_server_observes_body_delay(self):
        received = []

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                started = time.monotonic()
                body = self.rfile.read(int(self.headers['Content-Length']))
                received.append((body, time.monotonic() - started))
                self.send_response(201)
                self.end_headers()
                self.wfile.write(b'{}')

            def log_message(self, *_):
                pass

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            status, _, _ = latency.request(server.server_port, 'POST', '/api/accounts', b'{}', 0.4)
            self.assertEqual(status, 201)
            self.assertEqual(received[0][0], b'{}')
            self.assertGreaterEqual(received[0][1], 0.25)
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_total_timeout_has_no_retry(self):
        received = []
        release = threading.Event()

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                received.append(1)
                release.wait(4)

            def log_message(self, *_):
                pass

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            start = time.monotonic()
            with self.assertRaises((OSError, latency.http.client.HTTPException)):
                latency.request(server.server_port, 'GET', '/api/accounts')
            self.assertGreaterEqual(time.monotonic() - start, 1.5)
            self.assertLess(time.monotonic() - start, 3)
            self.assertEqual(len(received), 1)
        finally:
            release.set()
            server.shutdown()
            server.server_close()
            thread.join()

    def test_bounded_load_and_exact_cleanup(self):
        calls = []
        rows = {}

        def request(port, method, path, body=None, delay=0, read_body=False):
            calls.append((method, path, delay))
            if method == 'POST':
                row = dict(json.loads(body), id=len(rows)+1)
                rows[row['id']] = row
                return 201, json.dumps(row).encode(), 401
            if method == 'GET' and path != '/api/accounts':
                return 200, json.dumps(rows[int(path.rsplit('/', 1)[1])]).encode(), 1
            if method == 'DELETE':
                del rows[int(path.rsplit('/', 1)[1])]
                return 204, b'', 1
            return 200, b'', 1

        with tempfile.TemporaryDirectory() as tmp, patch.object(latency, 'request', side_effect=request), patch.object(latency.time, 'sleep'):
            output = Path(tmp) / 'result'
            summary = latency.load('http://127.0.0.1:18080', 'a'*40, output)
            self.assertTrue(summary['completed'])
            self.assertEqual(len(calls), 30)
            self.assertEqual(sum(c[0] == 'GET' for c in calls), 27)
            self.assertEqual(sum(c[0] == 'POST' and c[2] == 0.4 for c in calls), 3)
            self.assertEqual((output / 'cleanup.json').stat().st_mode & 0o777, 0o600)
            latency.cleanup(output / 'cleanup.json')
            self.assertEqual(len(calls), 36)
            self.assertFalse(rows)

    def test_external_target_and_uncertain_outcome_have_no_requests(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(latency, 'request') as request:
            with self.assertRaises(ValueError):
                latency.load('http://example.test:18080', 'a'*40, Path(tmp) / 'result')
            path = Path(tmp) / 'cleanup.json'
            path.write_text(json.dumps(dict(target='http://127.0.0.1:18080', rows=[dict(name='learning-latency-'+'a'*24, email='learning-latency-'+'a'*24+'@example.test', id=None)])))
            with self.assertRaises(ValueError):
                latency.cleanup(path)
            request.assert_not_called()

    def test_cleanup_refuses_other_identity(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(latency, 'request', return_value=(200, b'{"id":1,"name":"unrelated","email":"keep@example.test"}',1)) as request:
            path = Path(tmp) / 'cleanup.json'
            marker = 'learning-latency-' + 'a'*24
            path.write_text(json.dumps(dict(target='http://127.0.0.1:18080', rows=[dict(name=marker,email=marker+'@example.test',id=1)])))
            with self.assertRaises(RuntimeError):
                latency.cleanup(path)
            self.assertEqual(request.call_count, 1)
            self.assertEqual(request.call_args.args[1], 'GET')


if __name__ == '__main__':
    unittest.main()
