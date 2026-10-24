"""Policy boundaries plus real, never-executed vulnerability fixtures."""
import copy
import datetime as dt
import importlib.util
import json
from pathlib import Path
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace

spec = importlib.util.spec_from_file_location('sast', Path(__file__).with_name('sast.py'))
s = importlib.util.module_from_spec(spec); spec.loader.exec_module(s)


class Gate(unittest.TestCase):
    def setUp(self):
        self.now = dt.datetime(2030, 1, 2, tzinfo=s.UTC)
        self.repo = 'syuhei0519/backend-app'
        self.targets = {'app.go': b'package app\nfunc example() {}\n'}
        self.rules = {'test-rule': 'ERROR'}
        self.policy = {'schemaVersion': 2, 'mode': 'enforce', 'blockingSeverities': ['ERROR', 'WARNING'],
                       'ruleModes': {'test-rule': {'mode': 'enforce'}}, 'exceptions': []}
        self.raw = {'version': s.VERSION, 'errors': [], 'paths': {'scanned': ['app.go']}, 'results': []}

    def finding(self):
        self.raw['results'] = [{'check_id': 'test-rule', 'path': 'app.go', 'start': {'line': 2}, 'end': {'line': 2},
                                'extra': {'severity': 'ERROR', 'message': 'SENSITIVE-MARKER', 'lines': 'SENSITIVE-MARKER'}}]

    def evaluate(self, **kwargs):
        return s.evaluate(self.raw, kwargs.pop('exit_code', 0), self.targets, self.rules, self.policy, self.repo, self.now, **kwargs)

    def exception(self):
        self.finding()
        item = {'id': 'reviewed-fixture', 'fingerprint': self.evaluate()['findings'][0]['fingerprint'],
                'owner': 'Lab operator', 'reason': 'Synthetic exception test only',
                'createdAt': '2030-01-01T00:00:00Z', 'expiresAt': '2030-01-03T00:00:00Z'}
        evidence = {'user': {'login': 'syuhei0519'}, 'body': 'SAST-EXCEPTION-APPROVED sha256:' + s.sha(s.canonical(item)),
                    'html_url': 'https://github.com/syuhei0519/backend-app/issues/36#issuecomment-123'}
        item['approval'] = evidence['html_url']; self.policy['exceptions'] = [item]
        return item, evidence

    def test_clean_detection_and_observation(self):
        self.assertTrue(self.evaluate()['pass'])
        self.finding()
        result = self.evaluate(); self.assertFalse(result['pass']); self.assertFalse(result['wouldPass'])
        self.assertNotIn('SENSITIVE-MARKER', json.dumps(result))
        self.policy['mode'] = 'observe'
        result = self.evaluate(); self.assertTrue(result['pass']); self.assertFalse(result['wouldPass'])

    def test_scanner_failure_and_partial_analysis_fail_in_both_modes(self):
        for mode in ('observe', 'enforce'):
            self.policy['mode'] = mode
            for code in (1, 2, 3, 124, 137):
                with self.assertRaises(ValueError): self.evaluate(exit_code=code)
            for key, value in [('errors', [{'message': 'parse failed'}]), ('version', 'unknown'), ('paths', {'scanned': []})]:
                original = self.raw[key]; self.raw[key] = value
                with self.assertRaises(ValueError): self.evaluate()
                self.raw[key] = original

    def test_exact_approved_exception_and_changed_code(self):
        _, evidence = self.exception()
        self.assertTrue(self.evaluate(fetch=lambda *_: evidence)['pass'])
        self.targets['app.go'] += b'// changed\n'
        self.assertFalse(self.evaluate(fetch=lambda *_: evidence)['pass'])

    def test_expired_unused_exception_and_approval_forgery(self):
        item, evidence = self.exception()
        self.raw['results'] = []
        item['expiresAt'] = '2030-01-02T00:00:00Z'
        with self.assertRaises(ValueError): self.evaluate(fetch=lambda *_: evidence)
        item['expiresAt'] = '2030-01-03T00:00:00Z'
        for bad in ({**evidence, 'user': {'login': 'someone-else'}}, {**evidence, 'body': 'approved'},
                    {**evidence, 'html_url': 'https://github.com/other/repo/issues/1#issuecomment-123'}):
            with self.assertRaises(ValueError): self.evaluate(fetch=lambda *_: bad)

    def test_long_future_and_malformed_policy(self):
        item, evidence = self.exception()
        for key, value in [('expiresAt', '2030-02-01T00:00:00Z'), ('createdAt', '2030-01-03T00:00:00Z'),
                           ('approval', 'https://evil.example/'), ('fingerprint', '*')]:
            old = item[key]; item[key] = value
            with self.assertRaises(ValueError): self.evaluate(fetch=lambda *_: evidence)
            item[key] = old
        with self.assertRaises(ValueError): s.decode('{"pass":false,"pass":true}')
        self.policy['extra'] = True
        with self.assertRaises(ValueError): self.evaluate(fetch=lambda *_: evidence)

    def test_unrecognized_finding_cannot_be_dropped(self):
        self.finding()
        for key, value in [('path', '../private'), ('check_id', 'unknown')]:
            old = self.raw['results'][0][key]; self.raw['results'][0][key] = value
            with self.assertRaises(ValueError): self.evaluate()
            self.raw['results'][0][key] = old
        self.raw['results'][0]['extra']['severity'] = 'UNKNOWN'
        with self.assertRaises(ValueError): self.evaluate()

    def test_rule_selection_is_complete_and_cannot_observe_injection_rules(self):
        for modes in ({}, {'unknown': {'mode': 'enforce'}}, {'test-rule': {'mode': 'observe'}},
                      {'test-rule': {'mode': 'enforce', 'ignored': True}}):
            self.policy['ruleModes'] = modes
            with self.assertRaises(ValueError): self.evaluate()

    def test_bounded_audit_review_remains_visible_and_expiry_fails(self):
        self.finding()
        self.rules['dangerous-exec-command'] = 'ERROR'
        self.raw['results'][0]['check_id'] = 'dangerous-exec-command'
        self.policy['ruleModes']['dangerous-exec-command'] = dict(mode='observe', owner='syuhei0519',
            reason='Independently reviewed test harness false positives; retain all findings.',
            createdAt='2030-01-01T00:00:00Z', expiresAt='2030-01-03T00:00:00Z')
        result = self.evaluate()
        self.assertTrue(result['pass'])
        self.assertEqual(len(result['findings']), 1)
        self.assertFalse(result['findings'][0]['blocking'])
        self.assertEqual(result['findings'][0]['ruleMode'], 'observe')
        self.raw['results'].append({**self.raw['results'][0], 'check_id': 'test-rule'})
        self.assertFalse(self.evaluate()['pass'], 'selected injection finding was hidden by audit')
        self.policy['ruleModes']['dangerous-exec-command']['expiresAt'] = '2030-01-02T00:00:00Z'
        for mode in ('enforce', 'observe'):
            self.policy['mode'] = mode
            with self.assertRaises(ValueError): self.evaluate()


class ContainerBoundary(unittest.TestCase):
    def test_archive_attributes_cannot_hide_tracked_sources(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for command in (['git', 'init', '-q', str(root)],):
                subprocess.run(command, check=True, capture_output=True)
            (root / 'security').mkdir()
            (root / 'security/semgrep.lock.json').write_text('{"language":"go"}')
            (root / '.gitattributes').write_text('hidden.go export-ignore\nvisible.go export-subst\n')
            (root / 'hidden.go').write_bytes(b'package hidden\n')
            (root / 'visible.go').write_bytes(b'package visible\n// $Format:%H$\n')
            subprocess.run(['git', 'add', '.'], cwd=root, check=True, capture_output=True)
            subprocess.run(['git', '-c', 'user.name=SAST fixture', '-c', 'user.email=sast@example.invalid',
                            'commit', '-qm', 'tracked source fixture'], cwd=root, check=True, capture_output=True)
            (root / 'untracked.go').write_bytes(b'package untracked\n')
            files = s.snapshot(root)
            self.assertEqual(set(files), {'hidden.go', 'visible.go'})
            self.assertEqual(files['visible.go'], b'package visible\n// $Format:%H$\n')
            self.assertIn('hidden.go', s._source.tracked_files(root))

    def test_timeout_stops_only_owned_container(self):
        with patch.object(s.subprocess, 'run', side_effect=[subprocess.TimeoutExpired('docker', 1),
                subprocess.CompletedProcess([], 0)]) as run:
            with self.assertRaises(subprocess.TimeoutExpired):
                s.run_container(['docker', 'run', 'scanner'], 1)
        invocation = run.call_args_list[0].args[0]
        name = invocation[invocation.index('--name') + 1]
        self.assertRegex(name, r'^plat-sast-[0-9a-f]{32}$')
        self.assertEqual(run.call_args_list[1].args[0], ['docker', 'rm', '--force', name])
        self.assertEqual(run.call_args_list[1].kwargs['timeout'], 20)

    def test_cleanup_failure_never_reports_scanner_success(self):
        with patch.object(s.subprocess, 'run', side_effect=[subprocess.CompletedProcess([], 0),
                subprocess.CompletedProcess([], 1)]):
            with self.assertRaises(ValueError): s.run_container(['docker', 'run', 'scanner'], 1)

    def test_unpinned_nested_and_yml_rules_are_refused(self):
        root = Path.cwd()
        with tempfile.TemporaryDirectory() as temporary:
            target = Path(temporary)
            shutil.copytree(root / 'security', target / 'security')
            for name in ('unpinned.yml', 'nested/unpinned.yaml', 'nested/unpinned.yml'):
                file = target / 'security/semgrep' / name
                file.parent.mkdir(parents=True, exist_ok=True)
                file.write_text('rules: []\n')
                with self.assertRaises(ValueError): s.rule_lock(target)
                file.unlink()


@unittest.skipUnless(os.environ.get('SAST_REAL_TESTS') == '1', 'set SAST_REAL_TESTS=1 for pinned engine tests')
class Engine(unittest.TestCase):
    def test_separate_merge_gate_proof_sources_without_executing_them(self):
        root = Path.cwd(); lock = s.rule_lock(root)
        language = lock['language']
        extension, repo = ('go', 'syuhei0519/backend-app') if language == 'go' else ('tsx', 'syuhei0519/frontend-app')
        policy = s.decode((root / 'security/sast-policy.json').read_bytes())
        for case, expected in [('semgrep', False), ('codeql', True)]:
            targets = {'sast-verification.' + extension:
                (root / 'security/sast-fixtures' / (case + '.' + extension + '.txt')).read_bytes()}
            raw, code, _ = s.scan(root, targets)
            result = s.evaluate(raw, code, targets, lock['rules'], policy, repo, dt.datetime.now(s.UTC))
            self.assertIs(result['pass'], expected)
            if expected:
                self.assertEqual(result['findings'], [])

    def test_real_timeout_removes_owned_container(self):
        identifier = s.uuid.uuid4().hex
        name = 'plat-sast-' + identifier
        with patch.object(s.uuid, 'uuid4', return_value=SimpleNamespace(hex=identifier)):
            with self.assertRaises(subprocess.TimeoutExpired):
                s.run_container(['docker', 'run', '--network', 'none', '--entrypoint', 'sh',
                                 s.IMAGE, '-c', 'sleep 30'], timeout=2)
        result = subprocess.run(['docker', 'container', 'inspect', name], capture_output=True, timeout=20)
        self.assertNotEqual(result.returncode, 0, 'Timed-out scanner container still exists')

    def test_real_clean_findings_nosem_and_parse_failure(self):
        root = Path.cwd(); lock = s.rule_lock(root)
        if lock['language'] == 'go':
            repo = 'syuhei0519/backend-app'
            clean = {'sample.go': b'package demo\nfunc safe() string { return "safe" }\n'}
            bad = {'sample.go': b'package demo\nimport "os/exec"\nfunc run(input string) {\n exec.Command("sh", "-c", input) // nosemgrep\n}\n'}
            bad['pool.go'] = b'package demo\nimport "github.com/jackc/pgx/v5/pgxpool"\nfunc query(ctx context.Context, db *pgxpool.Pool, input string) { db.QueryRow(ctx, "SELECT " + input) }\n'
            bad['conn.go'] = b'package demo\nimport "github.com/jackc/pgx/v5"\nfunc query(ctx context.Context, db *pgx.Conn, input string) { db.Query(ctx, "SELECT " + input) }\n'
            parse = {'sample.go': b'package demo\nimport "os/exec"\nfunc invalid() { exec.Command("sh", "-c", input); @@@ }\n'}
        else:
            repo = 'syuhei0519/frontend-app'
            clean = {'sample.tsx': b'export function Safe({text}: {text: string}) { return <div>{text}</div>; }\n'}
            bad = {'sample.tsx': b'export function Bad({text}: {text: string}) {\n return <div dangerouslySetInnerHTML={{__html: text}} />; // nosemgrep\n}\n'}
            bad['props.tsx'] = b'function Bad(props: {text: string}) { return <div dangerouslySetInnerHTML={{__html: props.text}} />; }\n'
            bad['browser.js'] = b'document.getElementById("demo").innerHTML = location.hash;\neval(location.hash);\n'
            parse = {'sample.tsx': b'export function Bad({text}) { return <div dangerouslySetInnerHTML={{__html: text}} />; @@@ }\n'}
        policy = {'schemaVersion': 2, 'mode': 'enforce', 'blockingSeverities': ['ERROR', 'WARNING'],
                  'ruleModes': {rule: {'mode': 'enforce'} for rule in lock['rules']}, 'exceptions': []}
        evidence = []
        for name, targets, expected in [('clean', clean, True), ('finding-and-nosem', bad, False), ('parse-error', parse, None)]:
            raw, code, elapsed = s.scan(root, targets)
            if expected is None:
                with self.assertRaises((ValueError, KeyError)):
                    s.evaluate(raw, code, targets, lock['rules'], policy, repo, dt.datetime.now(s.UTC))
            else:
                result = s.evaluate(raw, code, targets, lock['rules'], policy, repo, dt.datetime.now(s.UTC))
                self.assertIs(result['pass'], expected)
                if not expected:
                    self.assertEqual({f['rule'] for f in result['findings']}, set(lock['rules']))
            evidence.append({'case': name, 'expectedPass': expected, 'scannerExit': code, 'durationSeconds': elapsed})
        target = root / '.security/sast'; target.mkdir(parents=True, exist_ok=True)
        (target / 'fixtures.json').write_text(json.dumps(evidence, indent=2) + '\n')


if __name__ == '__main__':
    unittest.main()
