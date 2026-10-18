// Offline tests of Renovate's own extractor. Requires a separately installed Renovate 44.167.1.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.RENOVATE_PACKAGE_DIR;
assert(root, 'Set RENOVATE_PACKAGE_DIR to the installed renovate package');
const pkg = JSON.parse(await fs.readFile(path.join(root, 'package.json'), 'utf8'));
assert.equal(pkg.version, '44.167.1');
const { GlobalConfig } = await import(pathToFileURL(path.join(root, 'dist/config/global.js')));
GlobalConfig.set({ localDir: '/tmp', platform: 'github' });
const { extractPackageFile } = await import(pathToFileURL(path.join(root, 'dist/modules/manager/github-actions/extract.js')));
const sha = 'a'.repeat(40);
const fixture = (use) => `jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: ${use}\n`;
const extract = async (content) => ((await extractPackageFile(content, '.github/workflows/ci.yml'))?.deps ?? []).filter(d => d.depType !== 'github-runner');
let deps = await extract(fixture(`syuhei0519/backend-app/.github/actions/shared-tools@${sha} # main`));
assert.equal(deps.length, 1);
assert.equal(deps[0].currentDigest, sha);
assert.equal(deps[0].currentValue, 'main');
assert.equal(deps[0].datasource, 'github-digest');
assert.match(deps[0].autoReplaceStringTemplate, /\/\.github\/actions\/shared-tools@/);
deps = await extract(fixture(`syuhei0519/backend-app/.github/actions/shared-tools@${sha}`));
assert.equal(deps[0].enabled, false);
assert.equal(deps[0].skipReason, 'unversioned-reference');
assert.deepEqual(await extract(fixture('./.github/actions/shared-tools')), []);
assert(!(await extract(fixture('actions/checkout@v4'))).some(d => d.depName === 'syuhei0519/backend-app'));
for (const filename of process.argv.slice(2)) {
  const found = (await extract(await fs.readFile(filename, 'utf8'))).filter(d => d.depName === 'syuhei0519/backend-app');
  assert(found.length > 0, filename);
  assert.equal(new Set(found.map(d => d.currentDigest)).size, 1, filename);
  for (const d of found) {
    assert.match(d.currentDigest, /^[0-9a-f]{40}$/);
    assert.equal(d.currentValue, 'main');
    assert.equal(d.datasource, 'github-digest');
  }
}
process.stdout.write('PASS: official extractor positive/negative fixtures and supplied workflows; no remote writes\n');
