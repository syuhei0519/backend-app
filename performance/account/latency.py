#!/usr/bin/env python3
"""Synthetic delayed input, not an API performance regression or fault injection."""
import argparse
from datetime import datetime, timezone
import http.client
import json
from pathlib import Path
import secrets
import socket
import threading
import time

from run import validate


def request(port, method, path, body=None, delay=0, read_body=False):
    # A new direct TCP connection per request: no proxy, redirects or retries.
    # The timeout covers the whole request, including the intentional input delay.
    started = time.monotonic()
    connection = http.client.HTTPConnection('127.0.0.1', port, timeout=2)
    timer = None
    try:
        connection.connect()
        connected = connection.sock

        def expire():
            try:
                connected.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass

        timer = threading.Timer(max(0.001, 2 - (time.monotonic() - started)), expire)
        timer.daemon = True
        timer.start()
        connection.putrequest(method, path, skip_accept_encoding=True)
        connection.putheader('Connection', 'close')
        if body is not None:
            connection.putheader('Content-Type', 'application/json')
            connection.putheader('Content-Length', str(len(body)))
        connection.endheaders()
        if body is not None:
            time.sleep(delay)
            connection.sock.settimeout(max(0.001, 2 - (time.monotonic() - started)))
            connection.send(body)
        connection.sock.settimeout(max(0.001, 2 - (time.monotonic() - started)))
        response = connection.getresponse()
        # GET/list bodies can contain user data: never retain them.
        data = response.read(65537) if method == 'POST' or read_body else b''
        if len(data) > 65536:
            raise RuntimeError('unexpected POST response size')
        return response.status, data, (time.monotonic() - started) * 1000
    finally:
        if timer is not None:
            timer.cancel()
        connection.close()


def load(target, revision, output):
    config = validate(target, 1, 30, revision, 'normal', 'learning-latency')
    output.mkdir(parents=True, exist_ok=False)
    port = int(target.rsplit(':', 1)[1])
    owned = []
    # Local recovery metadata is deliberately separate from the public summary.
    recovery = output / 'cleanup.json'
    recovery.touch(mode=0o600)
    summary = dict(profile='synthetic-delayed-body', source_revision=revision,
                   started_at_utc=datetime.now(timezone.utc).isoformat(),
                   load_budget=30, max_concurrency=1, timeout_seconds=2, retries=0,
                   redirects=0, delay_ms=400, requests=[])
    error = None
    try:
        start = time.monotonic()
        for i in range(config['max_http_requests']):
            time.sleep(max(0, start + i - time.monotonic()))
            post = i % 10 == 9
            body = None
            if post:
                # Dedicated, unguessable synthetic name/email. No existing row is targeted.
                marker = 'learning-latency-' + secrets.token_hex(12)
                body = json.dumps(dict(name=marker, email=marker + '@example.test')).encode()
                # Persist before sending: uncertain responses require manual reconciliation,
                # never a retry or a broad list/delete sweep.
                owned.append(dict(name=marker, email=marker + '@example.test', id=None))
                recovery.write_text(json.dumps(dict(target=target, rows=owned), indent=2) + '\n')
            summary['http_attempts'] = i + 1
            status, data, elapsed = request(port, 'POST' if post else 'GET', '/api/accounts', body, 0.4 if post else 0)
            summary['requests'].append(dict(method='POST' if post else 'GET', status=status, client_ms=round(elapsed, 3)))
            if post:
                row = json.loads(data)
                if status != 201 or row.get('name') != marker or row.get('email') != marker + '@example.test' or type(row.get('id')) is not int or row['id'] <= 0:
                    raise RuntimeError('synthetic POST contract failed; inspect local cleanup metadata')
                owned[-1]['id'] = row['id']
                recovery.write_text(json.dumps(dict(target=target, rows=owned), indent=2) + '\n')
            elif status != 200:
                raise RuntimeError('GET contract failed')
    except Exception as exc:
        error = exc
    finally:
        summary['http_requests'] = len(summary['requests'])
        summary['completed'] = error is None and len(summary['requests']) == 30
        (output / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
    if error:
        raise error
    return summary


def cleanup(path):
    data = json.loads(path.read_text())
    # Validate every row before any request. This file is local and must not be edited.
    validate(data['target'], 1, 1, 'a' * 40, 'normal', 'learning-latency')
    rows = data['rows']
    if not isinstance(rows, list) or len(rows) > 3:
        raise ValueError('at most three owned synthetic rows required')
    import re
    for row in rows:
        if not re.fullmatch(r'learning-latency-[0-9a-f]{24}', row['name']) or row['email'] != row['name'] + '@example.test':
            raise ValueError('unexpected synthetic identity')
        if type(row['id']) is not int or row['id'] <= 0:
            raise ValueError('uncertain POST outcome: reconcile its exact synthetic identity manually; no retry')
    port = int(data['target'].rsplit(':', 1)[1])
    for row in list(rows):
        # Verify the exact returned id still belongs to this synthetic identity.
        status, body, _ = request(port, 'GET', '/api/accounts/' + str(row['id']), read_body=True)
        current = json.loads(body)
        if status != 200 or any(current.get(key) != row[key] for key in ('id', 'name', 'email')):
            raise RuntimeError('synthetic identity changed; deletion refused')
        status, _, _ = request(port, 'DELETE', '/api/accounts/' + str(row['id']))
        if status != 204:
            raise RuntimeError('cleanup failed; remaining local metadata retained')
        rows.remove(row)
        path.write_text(json.dumps(data, indent=2) + '\n')
    print('PASS synthetic row cleanup (at most 3 GET + 3 DELETE requests, separate from measured load)')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subs = parser.add_subparsers(dest='command', required=True)
    run = subs.add_parser('run')
    run.add_argument('--target', required=True)
    run.add_argument('--revision', required=True)
    run.add_argument('--output', type=Path, required=True)
    clean = subs.add_parser('cleanup')
    clean.add_argument('path', type=Path)
    args = parser.parse_args()
    if args.command == 'cleanup':
        cleanup(args.path)
    else:
        summary = load(args.target, args.revision, args.output)
        print('PASS 27 GET + 3 delayed POST; 30 load requests; cleanup is a separate step')
        assert summary['completed']


if __name__ == '__main__':
    main()
