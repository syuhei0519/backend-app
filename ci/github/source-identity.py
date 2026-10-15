"""Bind the shared source scan to the exact CI source/run/attempt."""
import importlib.util
from pathlib import Path
spec=importlib.util.spec_from_file_location('release',Path(__file__).with_name('release.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
repo,commit,run_id,attempt=m.identity()
scan=m.PUBLIC/'source-scan.json'
m.require(m.load(scan)['pass'] is True)
(m.PUBLIC/'source-identity.json').write_text(m.json.dumps({'repository':repo,'sourceCommit':commit,'runId':run_id,
    'runAttempt':attempt,'sourceScanSha256':m.sha(scan.read_bytes())},sort_keys=True)+'\n')
