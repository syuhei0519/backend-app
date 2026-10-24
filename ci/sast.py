"""Offline Semgrep CE scan and fail-closed policy evaluation (stdlib only).

Keep this file identical in backend-app and frontend-app until shared-tools has
a reviewed Python distribution contract. Existing pinned Go modules are unchanged.
"""
import datetime as dt
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
import uuid

IMAGE = 'semgrep/semgrep:1.182.0@sha256:4a34048c04fef049f71665047f074cffb00cc3d89482f965a5cd151d589237af'
VERSION = '1.182.0'
UTC = dt.timezone.utc


def require(condition):
    if not condition:
        raise ValueError('SAST contract rejected')


def pairs(items):
    result = {}
    for key, value in items:
        require(key not in result)
        result[key] = value
    return result


def decode(data):
    return json.loads(data, object_pairs_hook=pairs)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def timestamp(value):
    require(isinstance(value, str) and re.fullmatch(r'\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ', value))
    return dt.datetime.fromisoformat(value.replace('Z', '+00:00'))


def safe_path(value):
    require(isinstance(value, str) and bool(value) and '\\' not in value)
    require(not value.startswith('/') and not any(p in ('', '.', '..') for p in value.split('/')))
    require(not re.search(r'[\x00-\x1f\x7f:*?\[\]]', value))
    return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        raise ValueError('Approval redirect rejected')


def approval_comment(repo, comment):
    # Only this exact public repository API path is accepted. No arbitrary URLs.
    headers = {'Accept': 'application/vnd.github+json', 'User-Agent': 'sast-policy'}
    token = os.environ.get('GITHUB_TOKEN')
    if token:
        headers['Authorization'] = 'Bearer ' + token
    request = urllib.request.Request(f'https://api.github.com/repos/{repo}/issues/comments/{comment}', headers=headers)
    with urllib.request.build_opener(NoRedirect).open(request, timeout=20) as response:
        return decode(response.read(1024 * 1024))


def policy_check(policy, repo, now, fetch=approval_comment):
    require(set(policy) == {'schemaVersion', 'mode', 'blockingSeverities', 'exceptions'})
    require(type(policy['schemaVersion']) is int and policy['schemaVersion'] == 1)
    require(policy['mode'] in ('observe', 'enforce'))
    require(policy['blockingSeverities'] == ['ERROR', 'WARNING'])
    require(isinstance(policy['exceptions'], list))
    require(re.fullmatch(r'syuhei0519/(backend-app|frontend-app)', repo))
    ids, fingerprints = set(), set()
    for item in policy['exceptions']:
        require(set(item) == {'id', 'fingerprint', 'owner', 'reason', 'createdAt', 'expiresAt', 'approval'})
        require(isinstance(item['id'], str) and re.fullmatch(r'[a-z0-9][a-z0-9-]{2,63}', item['id']))
        require(item['id'] not in ids and item['fingerprint'] not in fingerprints)
        ids.add(item['id']); fingerprints.add(item['fingerprint'])
        require(isinstance(item['fingerprint'], str) and re.fullmatch('[0-9a-f]{64}', item['fingerprint']))
        for key in ('owner', 'reason'):
            require(isinstance(item[key], str) and 3 <= len(item[key].strip()) <= 500)
        created, expires = timestamp(item['createdAt']), timestamp(item['expiresAt'])
        require(created <= now < expires and expires - created <= dt.timedelta(days=14))
        match = re.fullmatch(r'https://github\.com/' + re.escape(repo) + r'/issues/[1-9][0-9]*#issuecomment-([1-9][0-9]*)', item['approval'])
        require(match is not None)
        evidence = fetch(repo, match[1])
        digest = sha(canonical({k: v for k, v in item.items() if k != 'approval'}))
        require(evidence['user']['login'] == 'syuhei0519')
        require(evidence['html_url'] == item['approval'])
        require(evidence['body'].strip() == 'SAST-EXCEPTION-APPROVED sha256:' + digest)
    return {x['fingerprint']: x['id'] for x in policy['exceptions']}


def evaluate(raw, exit_code, targets, rules, policy, repo, now, fetch=approval_comment):
    # scan (without --error) must exit zero, even when findings exist. Any other
    # code, parser warning, timeout, missing target or unknown severity fails.
    require(type(exit_code) is int and exit_code == 0)
    require(raw['version'] == VERSION and raw['errors'] == [])
    require(isinstance(raw['results'], list) and isinstance(raw['paths']['scanned'], list))
    scanned = [safe_path(p.removeprefix('./')) for p in raw['paths']['scanned']]
    require(set(scanned) == set(targets) and len(scanned) == len(set(scanned)) and bool(scanned))
    exceptions = policy_check(policy, repo, now, fetch)
    findings = []
    for item in raw['results']:
        rule, path = item['check_id'], safe_path(item['path'].removeprefix('./'))
        require(rule in rules and path in targets)
        severity = item['extra']['severity']
        require(severity in ('ERROR', 'WARNING', 'INFO') and severity == rules[rule])
        require(item['extra'].get('is_ignored', False) is False)
        start, end = item['start']['line'], item['end']['line']
        require(type(start) is int and type(end) is int and 1 <= start <= end <= len(targets[path].splitlines()))
        finding = {'rule': rule, 'path': path, 'startLine': start, 'endLine': end,
                   'severity': severity, 'sourceSha256': sha(targets[path])}
        finding['fingerprint'] = sha(canonical(finding))
        finding['exceptionId'] = exceptions.get(finding['fingerprint'])
        finding['blocking'] = severity in policy['blockingSeverities'] and finding['exceptionId'] is None
        findings.append(finding)
    would_pass = not any(f['blocking'] for f in findings)
    return {'schema': 'core-platform/sast/v1', 'scanner': 'semgrep-ce', 'version': VERSION,
            'mode': policy['mode'], 'pass': would_pass or policy['mode'] == 'observe',
            'wouldPass': would_pass, 'scannedFiles': sorted(targets), 'findings': findings,
            'policySha256': sha(canonical(policy)), 'scannedAt': now.strftime('%Y-%m-%dT%H:%M:%SZ')}


def rule_lock(root):
    lock = decode((root / 'security/semgrep.lock.json').read_bytes())
    require(lock['version'] == VERSION and lock['image'] == IMAGE)
    directory = root / 'security/semgrep'
    files = {p.relative_to(directory).as_posix() for p in directory.rglob('*') if p.suffix in ('.yaml', '.yml')}
    require(files == set(lock['files']))
    for name, digest in lock['files'].items():
        require(sha((root / 'security/semgrep' / safe_path(name)).read_bytes()) == digest)
    require(bool(lock['rules']))
    return lock


def snapshot(root):
    language = decode((root / 'security/semgrep.lock.json').read_bytes())['language']
    suffixes = {'.go'} if language == 'go' else {'.js', '.jsx', '.ts', '.tsx', '.mjs', '.cjs'}
    data = subprocess.run(['git', 'archive', '--format=tar', 'HEAD'], cwd=root, check=True, capture_output=True).stdout
    targets = {}
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        for member in archive:
            path = safe_path(member.name)
            if PurePosixPath(path).suffix not in suffixes:
                continue
            require(member.isfile() and member.size <= 10 * 1024 * 1024)
            targets[path] = archive.extractfile(member).read()
    require(bool(targets))
    return targets


def run_container(command, timeout):
    # A unique name identifies only this invocation, including a startup timeout
    # before a CID could be written. Do not rely on --rm after killing Docker CLI.
    name = 'plat-sast-' + uuid.uuid4().hex
    try:
        return subprocess.run(command[:2] + ['--name', name] + command[2:],
                              capture_output=True, timeout=timeout)
    finally:
        cleanup = subprocess.run(['docker', 'rm', '--force', name], capture_output=True, timeout=20)
        require(cleanup.returncode == 0)


def scan(root, targets, timeout=300):
    # Snapshot contains only tracked language inputs; no git config, credentials,
    # ignore files, node_modules, build output or scanner state is exposed.
    with tempfile.TemporaryDirectory(prefix='sast-') as temporary:
        source = Path(temporary)
        for name, data in targets.items():
            path = source / safe_path(name); path.parent.mkdir(parents=True, exist_ok=True); path.write_bytes(data)
        # Override Semgrep's built-in test/vendor exclusions, not just gitignore.
        (source / '.semgrepignore').write_text('# All selected tracked language files are required.\n')
        uid, gid = (os.getuid(), os.getgid()) if hasattr(os, 'getuid') else (1000, 1000)
        command = ['docker', 'run', '--network', 'none', '--read-only', '--cap-drop', 'ALL',
                   '--security-opt', 'no-new-privileges', '--tmpfs', '/tmp:rw,nosuid,nodev,size=512m',
                   '--user', f'{uid}:{gid}', '-e', 'HOME=/tmp',
                   '-v', str(source.resolve()) + ':/src:ro',
                   '-v', str((root / 'security/semgrep').resolve()) + ':/rules:ro', '-w', '/src', IMAGE,
                   'semgrep', 'scan', '--oss-only', '--config', '/rules', '--metrics', 'off',
                   '--disable-version-check', '--disable-nosem', '--strict', '--no-git-ignore',
                   '--no-rewrite-rule-ids', '--max-target-bytes', '0', '--timeout', '20',
                   '--timeout-threshold', '1', '--json', '--quiet', '.']
        started = time.monotonic()
        result = run_container(command, timeout)
        return decode(result.stdout), result.returncode, round(time.monotonic() - started, 3)


def main():
    root = Path.cwd()
    public = root / '.security/sast'; public.mkdir(parents=True, exist_ok=True)
    output = public / 'semgrep.json'
    result = {'schema': 'core-platform/sast/v1', 'scanner': 'semgrep-ce', 'pass': False,
              'reason': 'scanner, input, rule or exception validation failed'}
    try:
        require(len(sys.argv) == 1)
        lock = rule_lock(root)
        targets = snapshot(root)
        policy = decode((root / 'security/sast-policy.json').read_bytes())
        repo = os.environ['GITHUB_REPOSITORY']
        raw, code, elapsed = scan(root, targets)
        result = evaluate(raw, code, targets, lock['rules'], policy, repo, dt.datetime.now(UTC))
        result.update({'image': IMAGE, 'rulesSha256': sha(canonical(lock)), 'durationSeconds': elapsed,
                       'sourceCommit': subprocess.run(['git', 'rev-parse', 'HEAD'], check=True, capture_output=True, text=True).stdout.strip(),
                       'repository': repo, 'runId': os.environ.get('GITHUB_RUN_ID'),
                       'runAttempt': os.environ.get('GITHUB_RUN_ATTEMPT')})
    except Exception:
        # Do not publish raw matches, source snippets, stderr, URLs or credentials.
        pass
    output.write_text(json.dumps(result, indent=2, sort_keys=True) + '\n', encoding='utf-8')
    print('Semgrep: ' + ('completed (' + result['mode'] + ')' if result['pass'] else 'failed; inspect sanitized artifact'))
    return 0 if result['pass'] else 1


if __name__ == '__main__':
    sys.exit(main())
