# GitHub の比較元を確定し、文書だけの検査でよいか通常の CI が必要かを判定します。
"""Documentation-only classification: unknown/missing base always means full CI."""
import json, os, re, subprocess
from pathlib import Path
event=json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text())
name=os.environ['GITHUB_EVENT_NAME']; base=''; docs=False; release=False
default_ref='refs/heads/'+event['repository']['default_branch']
protected_main_push=name=='push' and os.environ['GITHUB_REF']==default_ref
retained_only={
    '.github/workflows/retained-runtime-refresh.yml',
    'ci/github/retained_publish.py',
    'ci/github/retained_runtime_refresh.py',
    'ci/github/retained_runtime_refresh_test.py',
    'tools/oci-input/cmd/oci-layout-push/main.go',
}
retained_delivery=retained_only|{'ci/github/classify.py','ci/github/classify_test.py'}
# One-time fail-closed bootstrap from the current protected main.  Later edits
# to CI controls are release-impacting again; only the three consumer files
# above remain a non-publishing change class.
bootstrap_base='635814f5354f0fe238694d35137b283576a9127f'
bootstrap_only=retained_only|{
    '.github/workflows/ci.yml',
    'ci/github/classify.py',
    'ci/github/classify_test.py',
    'ci/github/release_test.py',
}
if name=='pull_request': base=event['pull_request']['base']['sha']
elif name=='push':
    if protected_main_push:
        base=event.get('before','')
    else:
        p=subprocess.run(['git','merge-base','HEAD','origin/'+event['repository']['default_branch']],capture_output=True,text=True)
        if p.returncode==0: base=p.stdout.strip()
if re.fullmatch('[0-9a-f]{40}',base) and base!='0'*40:
    p=subprocess.run(['git','diff','--name-only','--no-renames','-z',base,'HEAD'],capture_output=True)
    if p.returncode==0:
        names=[x for x in p.stdout.decode().split('\0') if x]
        docs=not protected_main_push and bool(names) and all(re.fullmatch(r'(README\.md|CHANGELOG\.md|CONTRIBUTING\.md|docs/[^\r\n]*\.md)',x) for x in names)
        changed=set(names)
        retained=bool(changed) and (changed<=retained_only or changed==retained_delivery)
        bootstrap=base==bootstrap_base and bool(changed) and changed<=bootstrap_only
        release=protected_main_push and bool(changed) and not retained and not bootstrap
with open(os.environ['GITHUB_OUTPUT'],'a') as f:
    f.write('docs='+str(docs).lower()+'\nbase='+base+'\nrelease='+str(release).lower()+'\n')
