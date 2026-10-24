"""Check that scanner failures cannot skip the existing image/release gate."""
from pathlib import Path
import re
import subprocess
import unittest


class Workflow(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = (Path(__file__).resolve().parents[1] / '.github/workflows/ci.yml').read_text()

    def block(self, name):
        matches = re.findall(r'^  ' + re.escape(name) + r':\n(.*?)(?=^  [a-z][a-z-]*:|\Z)', self.workflow, re.M | re.S)
        self.assertEqual(len(matches), 1)
        return matches[0]

    def test_graph_and_no_failure_suppression(self):
        self.assertIn('needs: [validate, sast]', self.block('image'))
        self.assertNotRegex(self.block('image'), r'^    if:', 'image must inherit dependency success')
        aggregate = self.block('ci-result')
        self.assertIn('needs: [classify, docs, validate, sast, image]', aggregate)
        self.assertIn('if: always()', aggregate)
        self.assertIn('SAST: ${{ needs.sast.result }}', aggregate)
        self.assertIn('test "$SAST" = success', aggregate)
        self.assertRegex(self.block('publish'), r'needs: \[validate, image(?:, ci-result)?\]')
        self.assertIn('needs: publish', self.block('propose'))
        sast = self.block('sast')
        self.assertIn('run: python3 -I ci/sast.py', sast)
        self.assertNotIn('continue-on-error', sast)
        self.assertNotIn('|| true', sast)
        self.assertIn('if: always()', sast)

    def test_actual_required_check_rejects_every_non_success_sast(self):
        aggregate = self.block('ci-result')
        code = aggregate.split('        run: |\n')[1]
        code = '\n'.join(line[10:] for line in code.splitlines())
        # The existing matrix consumer has its own tests. Stub only that command;
        # execute the workflow's actual shell status checks, including docs bypass.
        code = 'python3() { return 0; };\n' + code
        for sast, expected in [('success', 0), ('failure', 1), ('cancelled', 1), ('skipped', 1), ('', 1)]:
            command = ['bash', '-e', '-c', code]
            import os
            env = {**os.environ, 'CLASSIFY': 'success', 'DOCS': 'false', 'VALIDATE': 'success', 'IMAGE': 'success', 'SAST': sast}
            result = subprocess.run(command, env=env, capture_output=True)
            self.assertEqual(result.returncode, expected, result.stderr.decode(errors='replace'))


if __name__ == '__main__':
    unittest.main()
