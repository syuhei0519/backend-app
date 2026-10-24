"""Operator-run Community Build analysis of the exact, clean remote main only.

No server deployment, port-forward, token creation, PR checkout or local runner
registration is performed here. Supply an approved local server and project token.
"""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import urllib.parse
import uuid

IMAGE = 'sonarsource/sonar-scanner-cli:12.3.0.4285_8.1.0@sha256:47956b669bf486bdad919e1cbd0533223515aa0b6d1d31763e5a3754942df6d7'


def require(value):
    if not value:
        raise ValueError('Sonar main contract rejected')


def git(*args):
    return subprocess.run(['git', *args], check=True, capture_output=True, text=True, timeout=60).stdout.strip()


def preflight(env):
    require(env.get('SONAR_TOKEN'))
    url = urllib.parse.urlsplit(env.get('SONAR_HOST_URL', ''))
    require(url.scheme == 'https' and url.hostname and not url.username and not url.password)
    require(not url.query and not url.fragment)
    require(git('branch', '--show-current') == 'main' and git('status', '--porcelain') == '')
    commit = git('rev-parse', 'HEAD')
    require(re.fullmatch('[0-9a-f]{40}', commit))
    remote = git('remote', 'get-url', 'origin')
    require(remote in ('https://github.com/syuhei0519/backend-app.git', 'https://github.com/syuhei0519/frontend-app.git'))
    require(git('ls-remote', 'origin', 'refs/heads/main').split() == [commit, 'refs/heads/main'])
    return commit


def run_container(command, timeout):
    name = 'plat-sonar-' + uuid.uuid4().hex
    try:
        return subprocess.run(command[:2] + ['--name', name] + command[2:],
                              capture_output=True, timeout=timeout)
    finally:
        cleanup = subprocess.run(['docker', 'rm', '--force', name], capture_output=True, timeout=20)
        require(cleanup.returncode == 0)


def execute(commit, temporary):
    source = Path(temporary) / 'source'; source.mkdir()
    archive = Path(temporary) / 'source.tar'
    subprocess.run(['git', 'archive', '--format=tar', '-o', str(archive), commit], check=True, capture_output=True)
    with tarfile.open(archive) as tar:
        for item in tar:
            path = Path(item.name)
            require(not path.is_absolute() and '..' not in path.parts and not item.issym() and not item.islnk())
        tar.extractall(source, filter='data')
    uid, gid = (os.getuid(), os.getgid()) if hasattr(os, 'getuid') else (1000, 1000)
    result = run_container(['docker', 'run', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
        '--user', f'{uid}:{gid}', '-e', 'SONAR_HOST_URL', '-e', 'SONAR_TOKEN', '-e', 'SONAR_USER_HOME=/tmp/sonar',
        '-v', str(source.resolve()) + ':/usr/src', IMAGE, '-Dsonar.qualitygate.wait=true',
        '-Dsonar.qualitygate.timeout=300', '-Dsonar.scm.revision=' + commit],
        timeout=900)
    # Never forward scanner logs which can include source/URL details.
    require(result.returncode == 0)


def main():
    report = {'scanner': 'sonar-scanner-cli', 'version': '8.1.0.6389', 'image': IMAGE, 'pass': False}
    try:
        require(len(sys.argv) == 1)
        commit = preflight(os.environ)
        report['sourceCommit'] = commit
        with tempfile.TemporaryDirectory(prefix='sonar-main-') as temporary:
            execute(commit, temporary)
        report['pass'] = True
    except Exception:
        report['reason'] = 'Main identity, credentials, connectivity, scanner or quality gate failed'
    output = Path('.security/sonar'); output.mkdir(parents=True, exist_ok=True)
    (output / 'main.json').write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
    print('SonarQube main analysis: ' + ('passed' if report['pass'] else 'failed; no success claimed'))
    return 0 if report['pass'] else 1


if __name__ == '__main__':
    sys.exit(main())
