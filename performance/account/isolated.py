#!/usr/bin/env python3
"""Disposable real Account API/Postgres integration; local Docker socket only."""
import argparse
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
POSTGRES = 'postgres@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f'
RUNTIME = 'gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True, help='Account cmd/server binary built from this checkout')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    # Non-public random resource names stay transient, never in artifacts.
    prefix = 'account-k6-' + secrets.token_hex(8)
    network, db, app, migration = [prefix + suffix for suffix in ('-net', '-db', '-app', '-migration')]
    created = []
    net_created = False
    env = {k: v for k, v in os.environ.items() if not k.startswith('DOCKER_')}
    with tempfile.TemporaryDirectory(prefix='account-k6-docker-') as config:
        env['DOCKER_CONFIG'] = config

        def docker(*command):
            result = subprocess.run(['docker', '--host', 'unix:///var/run/docker.sock', *command],
                                    env=env, capture_output=True, text=True, timeout=180)
            if result.returncode:
                raise RuntimeError('isolated Docker step failed: ' + command[0])
            return result.stdout

        def sql(query):
            return docker('exec', db, 'psql', '-U', 'postgres', '-At', '-v', 'ON_ERROR_STOP=1', '-c', query).strip()

        try:
            for image in [POSTGRES, RUNTIME]:
                docker('pull', image)
            # Dedicated bridge: Docker's --internal suppresses the loopback publication.
            # No external target or telemetry exporter is configured in this fixture.
            docker('network', 'create', network)
            net_created = True
            docker('create', '--name', db, '--network', network, '--memory', '256m',
                   '--env', 'POSTGRES_HOST_AUTH_METHOD=trust', POSTGRES)
            created.append(db)
            docker('start', db)
            for _ in range(30):
                try:
                    docker('exec', db, 'pg_isready', '-U', 'postgres')
                    break
                except RuntimeError:
                    time.sleep(1)
            else:
                raise RuntimeError('disposable DB readiness failed (not skipped)')
            flags = ['--network', network, '--read-only', '--cap-drop=ALL',
                     '--security-opt=no-new-privileges', '--user', '10001:10001', '--memory', '256m',
                     '--mount', f'type=bind,source={binary},target=/server,readonly',
                     '--env', f'DATABASE_URL=postgres://postgres@{db}:5432/postgres?sslmode=disable',
                     '--env', 'LOG_LEVEL=error', '--entrypoint', '/server']
            docker('create', '--name', migration, *flags, RUNTIME, 'migrate')
            created.append(migration)
            docker('start', migration)
            if docker('wait', migration).strip() != '0':
                raise RuntimeError('disposable migration failed (not skipped)')
            # Pre-existing sentinel verifies no writes/deletes from the load scenario.
            sql("INSERT INTO accounts (name,email) VALUES ('fixture-preserve','preserve@example.test')")
            before = sql('SELECT id,name,email FROM accounts ORDER BY id')
            docker('create', '--name', app, '--publish', '127.0.0.1::8080', *flags, RUNTIME)
            created.append(app)
            docker('start', app)
            inspected = json.loads(docker('inspect', app))[0]
            if '8080/tcp' not in inspected['NetworkSettings']['Ports']:
                raise RuntimeError('API port unavailable: running=' + str(inspected['State']['Running']) + ', exit=' + str(inspected['State']['ExitCode']))
            binding = inspected['NetworkSettings']['Ports']['8080/tcp'][0]
            if binding['HostIp'] != '127.0.0.1':
                raise RuntimeError('non-loopback binding refused')
            port = int(binding['HostPort'])
            # TCP readiness only: zero setup HTTP requests.
            for _ in range(30):
                try:
                    with socket.create_connection(('127.0.0.1', port), timeout=1):
                        break
                except OSError:
                    time.sleep(1)
            else:
                raise RuntimeError('disposable API readiness failed (not skipped)')
            args.output.mkdir(parents=True, exist_ok=False)
            for profile in ['normal', 'mixed-4xx']:
                subprocess.run([sys.executable, str(ROOT / 'performance/account/run.py'),
                                '--target', f'http://127.0.0.1:{port}', '--revision', revision,
                                '--rate', '1', '--duration', '30', '--profile', profile,
                                '--output', str(args.output / profile)], check=True)
            if sql('SELECT id,name,email FROM accounts ORDER BY id') != before:
                raise RuntimeError('sentinel data changed')
        finally:
            # Remove only resources this process created; volumes belong to the disposable DB.
            for name in reversed(created):
                docker('rm', '--force', '--volumes', name)
            if net_created:
                docker('network', 'rm', network)
    print('PASS real Account/Postgres: normal + mixed-4xx, sentinel preserved, owned fixtures removed')


if __name__ == '__main__':
    main()
