#!/usr/bin/env python3
"""Supported entrypoint: validate everything before k6 can send HTTP."""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent
VERSION = 'k6 v2.3.0 '


def validate(target, rate, duration, revision, profile, label):
    # Literal loopback only: no DNS, credentials, path, query, fragment or TLS proxy.
    match = re.fullmatch(r'http://127\.0\.0\.1:([1-9][0-9]{3,4})', target)
    if not match or not 1024 <= int(match[1]) <= 65535:
        raise ValueError('target must be http://127.0.0.1:<1024..65535> (dedicated test target)')
    if type(rate) is not int or not 1 <= rate <= 5:
        raise ValueError('rate must be an integer from 1 to 5 HTTP requests/second')
    if type(duration) is not int or not 1 <= duration <= 60:
        raise ValueError('duration must be integer seconds from 1 to 60')
    if not re.fullmatch('[0-9a-f]{40}', revision):
        raise ValueError('revision must be the confirmed target source commit (40 lowercase hex)')
    if profile not in ('normal', 'mixed-4xx'):
        raise ValueError('unknown profile')
    if not re.fullmatch('[a-z][a-z0-9-]{0,39}', label):
        raise ValueError('label must be a public, non-secret learning label, up to 40 characters')
    return dict(target=target, rate=rate, duration=duration, revision=revision,
                profile=profile, label=label, max_http_requests=rate * duration,
                max_vus=5, setup_requests=0, cleanup_requests=0)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target', required=True)
    parser.add_argument('--revision', required=True)
    parser.add_argument('--rate', type=int, default=1)
    parser.add_argument('--duration', type=int, default=30)
    parser.add_argument('--profile', choices=['normal', 'mixed-4xx'], default='normal')
    parser.add_argument('--label', default='learning-smoke')
    parser.add_argument('--output', type=Path, required=True, help='new artifact directory')
    args = parser.parse_args()
    try:
        config = validate(args.target, args.rate, args.duration, args.revision, args.profile, args.label)
    except ValueError as error:
        parser.error(str(error))
    executable = shutil.which('k6')
    if not executable:
        parser.error('k6 v2.3.0 required; no request sent')
    # Strip proxy/K6_* environment and user config, preventing load/redirect overrides.
    env = {'PATH': os.environ.get('PATH', ''), 'K6_NO_USAGE_REPORT': 'true'}
    version = subprocess.check_output([executable, 'version'], env=env, text=True).strip()
    if not version.startswith(VERSION):
        parser.error('k6 v2.3.0 required; no request sent')
    args.output.mkdir(parents=True, exist_ok=False)
    config['k6_version'] = version
    config['started_at_utc'] = datetime.now(timezone.utc).isoformat()
    (args.output / 'input.json').write_text(json.dumps(config, indent=2) + '\n')
    # A private local config is transient; only reproducible input and summary are artifacts.
    with tempfile.TemporaryDirectory(prefix='account-k6-') as tmp:
        sealed = Path(tmp) / 'config.json'
        sealed.write_text('{}')
        env.update(ACCOUNT_CONFIG=json.dumps(config), ACCOUNT_SUMMARY=str((args.output / 'summary.json').resolve()))
        result = subprocess.run([executable, 'run', '--config', str(sealed), '--no-color',
                                 '--quiet', str(ROOT / 'scenario.js')], env=env)
    raise SystemExit(result.returncode)


if __name__ == '__main__':
    main()
