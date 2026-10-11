"""GitHub-native publication and data-only manifest verification.

GitLab evidence is intentionally a separate protocol. This module does not
reinterpret a GitLab project/job identity as a GitHub identity.
"""
import base64
import datetime as dt
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import io
import zipfile
import traceback
import stat

SCHEMA='core-platform/github-release/v1'
ORDER_SCHEMA='core-platform/github-release/v2'
# Source repository is explicit. Never derive it from the registry image name.
SERVICES={'backend':('backend-app','backend-app',SCHEMA,'github-v1'),
          'frontend':('frontend-app','frontend-app',SCHEMA,'github-v1'),
          'order':('backend-app','backend-app-order',ORDER_SCHEMA,'github-v2')}
POSTGRES=('docker.io/library/postgres','17.10-bookworm','sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f')

def service_contract(repo,service=None):
    require(isinstance(repo,str) and REPO.fullmatch(repo))
    owner,source=repo.split('/')
    if service is None:
        service={'backend-app':'backend','frontend-app':'frontend'}.get(source)
    require(service in SERVICES)
    expected,image,schema,annotation=SERVICES[service]
    require(source==expected)
    return service,'ghcr.io/'+owner.lower()+'/'+image,schema,annotation

def positive(value):
    require(type(value) is int and value>0)
    return value

def annotation_id(value):
    require(isinstance(value,str) and re.fullmatch('[1-9][0-9]*',value))
    return int(value)

SHA=re.compile(r'[0-9a-f]{40}')
DIGEST=re.compile(r'sha256:[0-9a-f]{64}')
REPO=re.compile(r'[A-Za-z0-9][A-Za-z0-9-]{0,38}/(?:backend-app|frontend-app|application-manifest)')
PUBLIC=Path('.security/public')

def require(value):
    if not value: raise ValueError('GitHub migration contract refused')

def unique(pairs):
    out={}
    for key,value in pairs:
        require(key not in out);out[key]=value
    return out

def decode(data):
    def refuse(_): raise ValueError('nonfinite JSON refused')
    return json.loads(data,object_pairs_hook=unique,parse_constant=refuse)
def sha(data): return hashlib.sha256(data).hexdigest()
def load(path): return decode(Path(path).read_bytes())
def now(): return dt.datetime.now(dt.timezone.utc)
def stamp(): return now().isoformat()
def timestamp(text):return dt.datetime.fromisoformat(text.replace('Z','+00:00'))
def fresh(text,hours=24):
    value=timestamp(text)
    require(value.tzinfo is not None and dt.timedelta(0)<=now()-value<=dt.timedelta(hours=hours))

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args,**kwargs): raise ValueError('API redirect refused')

class PublicRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,req,fp,code,msg,headers,newurl):
        parsed=urllib.parse.urlsplit(newurl)
        require(parsed.scheme=='https' and parsed.hostname in ('github.com','objects.githubusercontent.com','release-assets.githubusercontent.com') and not parsed.username)
        return super().redirect_request(req,fp,code,msg,headers,newurl)

def api(path,method='GET',body=None,raw=None,upload=False):
    require(path.startswith('/repos/') and '\n' not in path)
    token=os.environ.get('GH_TOKEN','')
    target='/'.join(path.split('/')[2:4])
    if method=='GET' and target!=os.environ.get('GITHUB_REPOSITORY') and target.endswith(('/backend-app','/frontend-app')):
        token=os.environ.get('EVIDENCE_READ_TOKEN') or token
    require(token)
    host='https://uploads.github.com' if upload else 'https://api.github.com'
    headers={'Authorization':'Bearer '+token,'Accept':'application/vnd.github+json','X-GitHub-Api-Version':'2026-03-10','User-Agent':'core-platform-migration'}
    data=raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    if data is not None: headers['Content-Type']='application/octet-stream' if raw is not None else 'application/json'
    request=urllib.request.Request(host+path,data=data,headers=headers,method=method)
    with urllib.request.build_opener(NoRedirect).open(request,timeout=40) as response:
        payload=response.read(32*1024**2+1);require(len(payload)<=32*1024**2)
        return decode(payload) if payload else None

def public_asset(url,repo,tag,name):
    expected='https://github.com/'+repo+'/releases/download/'+tag+'/'+name
    require(url==expected)
    request=urllib.request.Request(url,headers={'User-Agent':'core-platform-migration'})
    with urllib.request.build_opener(PublicRedirect).open(request,timeout=40) as response:
        data=response.read(32*1024**2+1);require(len(data)<=32*1024**2);return data

def run(args,**kwargs):
    p=subprocess.run(args,capture_output=True,timeout=180,**kwargs)
    require(p.returncode==0);return p.stdout

def identity():
    repo=os.environ['GITHUB_REPOSITORY'];commit=os.environ['GITHUB_SHA']
    require(REPO.fullmatch(repo) and SHA.fullmatch(commit))
    run_id=os.environ['GITHUB_RUN_ID'];attempt=os.environ['GITHUB_RUN_ATTEMPT']
    require(run_id.isdecimal() and int(run_id)>0 and attempt.isdecimal() and int(attempt)>0)
    return repo,commit,int(run_id),int(attempt)

def tag_for(commit,run_id,attempt,service=None):
    require(SHA.fullmatch(commit) and type(run_id) is int and run_id>0 and type(attempt) is int and attempt>0)
    require(service in (None,'backend','frontend','order'))
    prefix='gha-order' if service=='order' else 'gha'
    return f'{prefix}-{commit}-{run_id}-{attempt}'

def record():
    repo,commit,run_id,attempt=identity()
    layout=load(PUBLIC/'layout.json');reader=load(PUBLIC/'reader.json')
    image=load(PUBLIC/'image-scan.json');source=load('.validation/source-scan.json')
    require(layout['sourceCommit']==commit and DIGEST.fullmatch(layout['digest']))
    require(layout['digest']==reader['digest'] and DIGEST.fullmatch(reader['configDigest']))
    require(reader['fullLayerValidation'] is True and source['pass'] is True and image['pass'] is True)
    for report in (image,source):
        require(report['trivyVersion']=='0.75.0');fresh(report['dbUpdatedAt']);fresh(report['scannedAt'])
    require(sha(Path('.oci/image.tar').read_bytes())==layout['archiveSha256'])
    sbom=Path(PUBLIC/'sbom.cdx.json').read_bytes();require(load(PUBLIC/'sbom-proof.json'))
    deadlines=[timestamp(report[key])+dt.timedelta(hours=24) for report in (source,image) for key in ('dbUpdatedAt','scannedAt')]
    deadlines.extend(timestamp(item['expiresAt']) for item in load('security/scan-policy.json')['exceptions'])
    result={'schema':SCHEMA,'repository':repo,'sourceCommit':commit,'runId':run_id,'runAttempt':attempt,
            'workflowRef':os.environ['GITHUB_WORKFLOW_REF'],'imageRepository':'ghcr.io/'+repo.lower(),
            'imageDigest':layout['digest'],'configDigest':reader['configDigest'],'archiveSha256':layout['archiveSha256'],
            'sbomSha256':sha(sbom),'sourceScanSha256':sha(Path('.validation/source-scan.json').read_bytes()),
            'imageScanSha256':sha(Path(PUBLIC/'image-scan.json').read_bytes()),
            'scannedAt':image['scannedAt'],'dbUpdatedAt':image['dbUpdatedAt'],'createdAt':stamp(),
            'validUntil':min(deadlines).isoformat(),
            'sourcePolicyPassed':True,'imagePolicyPassed':True,'sbomValidated':True,'isolatedRuntimePassed':True,
            'published':False,'deploymentAuthorized':False}
    (PUBLIC/'release-record.json').write_text(json.dumps(result,sort_keys=True,indent=2)+'\n')

def validate_record(result,repo,commit,run_id,attempt,service=None):
    service,image,schema,_=service_contract(repo,service)
    positive(run_id);positive(attempt)
    require(type(result['runId']) is int and type(result['runAttempt']) is int)
    require(SHA.fullmatch(commit))
    require(result.get('service')=='order' if service=='order' else 'service' not in result)
    if service=='order':
        required={'schema','service','repository','sourceCommit','runId','runAttempt','workflowRef','imageRepository',
                  'imageDigest','configDigest','archiveSha256','sbomSha256','sourceScanSha256','imageScanSha256',
                  'scannedAt','dbUpdatedAt','createdAt','validUntil','sourcePolicyPassed','imagePolicyPassed',
                  'sbomValidated','isolatedRuntimePassed','published','protectedMain','deploymentAuthorized'}
        require(set(result)==required and result['published'] is True and result['protectedMain'] is True)

    require(result['schema']==schema and result['repository']==repo and result['sourceCommit']==commit)
    require(result['runId']==run_id and result['runAttempt']==attempt)
    require(result['workflowRef']==repo+'/.github/workflows/ci.yml@refs/heads/main')
    require(result['imageRepository']==image and DIGEST.fullmatch(result['imageDigest']) and DIGEST.fullmatch(result['configDigest']))
    for key in ('sourcePolicyPassed','imagePolicyPassed','sbomValidated','isolatedRuntimePassed'):
        require(result[key] is True)
    for key in ('archiveSha256','sbomSha256','sourceScanSha256','imageScanSha256'):
        require(re.fullmatch('[0-9a-f]{64}',result[key]))
    for key in ('scannedAt','dbUpdatedAt','createdAt'):fresh(result[key])
    deadline=timestamp(result['validUntil'])
    require(now()<deadline<=min(timestamp(result[key])+dt.timedelta(hours=24) for key in ('scannedAt','dbUpdatedAt')))
    require(result['deploymentAuthorized'] is False)

def publish():
    repo,commit,run_id,attempt=identity()
    require(os.environ['GITHUB_EVENT_NAME']=='push' and os.environ['GITHUB_REF']=='refs/heads/main' and os.environ['GITHUB_REF_PROTECTED']=='true')
    result=load(PUBLIC/'release-record.json');validate_record(result,repo,commit,run_id,attempt)
    require(result['published'] is False)
    require(sha(Path('.oci/image.tar').read_bytes())==result['archiveSha256'])
    require(sha(Path(PUBLIC/'sbom.cdx.json').read_bytes())==result['sbomSha256'])
    require(sha(Path(PUBLIC/'image-scan.json').read_bytes())==result['imageScanSha256'])
    proof=decode(run(['github-oci-check','.oci/image.tar',result['archiveSha256'],'.oci/publish-layout',commit,repo]))
    require(proof['digest']==result['imageDigest'])
    require(api('/repos/'+repo+'/git/ref/heads/main')['object']['sha']==commit)
    image=result['imageRepository']+':'+commit
    with tempfile.TemporaryDirectory(prefix='gha-registry-') as auth:
        env={**os.environ,'DOCKER_CONFIG':auth};env.pop('GH_TOKEN',None)
        run(['crane','auth','login','ghcr.io','--username',os.environ['GITHUB_ACTOR'],'--password-stdin'],input=os.environ['GH_TOKEN'].encode(),env=env)
        lookup=subprocess.run(['crane','digest',image],env=env,capture_output=True,timeout=40)
        if lookup.returncode==0:
            require(lookup.stdout.decode().strip()==result['imageDigest'])
        else:
            # Transport/auth failures are not evidence that a SHA tag is absent.
            require(b'404' in lookup.stderr and b'Not Found' in lookup.stderr)
            run(['crane','push','.oci/publish-layout',image],env=env)
        require(run(['crane','digest',image],env=env).decode().strip()==result['imageDigest'])
        config=decode(run(['crane','config',result['imageRepository']+'@'+result['imageDigest']],env=env))
        require(config['config']['Labels']['org.opencontainers.image.revision']==commit)
        require(config['config']['Labels']['org.opencontainers.image.source']=='https://github.com/'+repo)
        require(sha(run(['crane','config',result['imageRepository']+'@'+result['imageDigest']],env=env).rstrip(b'\n'))==result['configDigest'][7:])
    result['published']=True;result['protectedMain']=True
    record_bytes=(json.dumps(result,sort_keys=True,indent=2)+'\n').encode()
    (PUBLIC/'release-record.json').write_bytes(record_bytes)
    tag=tag_for(commit,run_id,attempt)
    release=api('/repos/'+repo+'/releases','POST',{'tag_name':tag,'target_commitish':commit,'name':tag,'draft':True,'prerelease':False,'body':'Checked GHCR image, runtime policy and same-input SBOM. Owner approval is required for deployment.'})
    for name,data in [('release-record.json',record_bytes),('sbom.cdx.json',Path(PUBLIC/'sbom.cdx.json').read_bytes()),('image-scan.json',Path(PUBLIC/'image-scan.json').read_bytes())]:
        api(f'/repos/{repo}/releases/{release["id"]}/assets?name={name}','POST',raw=data,upload=True)
    api(f'/repos/{repo}/releases/{release["id"]}','PATCH',{'draft':False})
    print('Published checked image and GitHub release evidence: '+tag)

def release_data(repo,commit,run_id,attempt,service=None):
    service_contract(repo,service)
    tag=tag_for(commit,run_id,attempt,service)
    release=api('/repos/'+repo+'/releases/tags/'+tag)
    require(not release['draft'] and not release['prerelease'] and release['tag_name']==tag)
    assets={a['name']:a for a in release['assets']}
    require(len(assets)==len(release['assets']) and 'release-record.json' in assets and 'sbom.cdx.json' in assets)
    urls={name:assets[name]['browser_download_url'] for name in ('release-record.json','sbom.cdx.json')}
    record_bytes=public_asset(urls['release-record.json'],repo,tag,'release-record.json')
    result=decode(record_bytes);validate_record(result,repo,commit,run_id,attempt,service)
    require(result['published'] is True and result['protectedMain'] is True)
    sbom=public_asset(urls['sbom.cdx.json'],repo,tag,'sbom.cdx.json')
    require(sha(sbom)==result['sbomSha256'])
    if service=='order':
        # Release names and mutable target_commitish are not a Git tag binding.
        obj=api('/repos/'+repo+'/git/ref/tags/'+tag)['object']
        if obj['type']=='tag':obj=api('/repos/'+repo+'/git/tags/'+obj['sha'])['object']
        require(obj['type']=='commit' and obj['sha']==commit)
        require('image-scan.json' in assets)
        scan=public_asset(assets['image-scan.json']['browser_download_url'],repo,tag,'image-scan.json')
        require(sha(scan)==result['imageScanSha256'] and decode(scan)['pass'] is True)
    return result,urls,sha(record_bytes)

def artifact_record_bytes(data,expected_digest):
    require(expected_digest=='sha256:'+sha(data))
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        require(archive.namelist()==['release-record.json'])
        item=archive.getinfo('release-record.json')
        require(not item.flag_bits & 1 and 0<item.file_size<=8*1024**2)
        mode=item.external_attr >> 16
        require(stat.S_IFMT(mode) in (0,stat.S_IFREG) and not item.is_dir())
        record_bytes=archive.read(item)
    decode(record_bytes)
    return record_bytes

def canonical_record_hash(repo,commit,run_id,attempt,service=None):
    """Bind release bytes to a server-owned artifact of the actual CI run."""
    service_contract(repo,service)
    name=f'published-evidence-order-{attempt}' if service=='order' else f'published-evidence-{attempt}'
    matches=[]
    for page in range(1,11):
        data=api(f'/repos/{repo}/actions/runs/{run_id}/artifacts?per_page=100&page={page}')
        items=data['artifacts']
        matches.extend(item for item in items if item['name']==name)
        if len(items)<100:break
    else:raise ValueError('artifact pagination refused')
    require(len(matches)==1);artifact=matches[0]
    require(not artifact['expired'] and artifact['workflow_run']['id']==run_id and artifact['workflow_run']['head_sha']==commit)
    token=os.environ.get('EVIDENCE_READ_TOKEN','');require(token)
    class StopRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self,*args,**kwargs):return None
    request=urllib.request.Request(f'https://api.github.com/repos/{repo}/actions/artifacts/{int(artifact["id"])}/zip',
        headers={'Authorization':'Bearer '+token,'Accept':'application/vnd.github+json','User-Agent':'core-platform-migration','X-GitHub-Api-Version':'2026-03-10'})
    try:
        urllib.request.build_opener(StopRedirect).open(request,timeout=40)
        raise ValueError('artifact redirect expected')
    except urllib.error.HTTPError as error:
        require(error.code==302);location=error.headers['Location']
    parsed=urllib.parse.urlsplit(location)
    require(parsed.scheme=='https' and parsed.username is None and parsed.hostname is not None)
    require(parsed.hostname.endswith(('.blob.core.windows.net','.actions.githubusercontent.com')))
    # Follow a signed URL with no PAT, and do not follow a second redirect.
    request=urllib.request.Request(location,headers={'User-Agent':'core-platform-migration'})
    with urllib.request.build_opener(NoRedirect).open(request,timeout=40) as response:
        payload=response.read(32*1024**2+1);require(len(payload)<=32*1024**2)
    return sha(artifact_record_bytes(payload,artifact['digest']))

def validate_run(workflow,repo,commit,run_id,attempt):
    require(workflow['id']==run_id and workflow['repository']['full_name']==repo and workflow['head_sha']==commit and workflow['head_branch']=='main' and workflow['event']=='push')
    require(workflow['status']=='completed' and workflow['conclusion']=='success' and workflow['run_attempt']==attempt and workflow['path']=='.github/workflows/ci.yml')

def verify_latest_run(repo,commit,run_id,attempt,service=None):
    validate_run(api(f'/repos/{repo}/actions/runs/{run_id}'),repo,commit,run_id,attempt)
    runs=[]
    for page in range(1,11):
        result=api(f'/repos/{repo}/actions/workflows/ci.yml/runs?head_sha={commit}&branch=main&event=push&per_page=100&page={page}')
        items=result['workflow_runs'];runs.extend(items)
        if len(items)<100:break
    else:raise ValueError('run pagination refused')
    require(runs)
    latest=max(runs,key=lambda item:(item['created_at'],item['id']))
    validate_run(latest,repo,commit,run_id,attempt)
    if service=='order':
        verify_order_matrix(repo,commit,run_id,attempt)

def verify_order_matrix(repo,commit,run_id,attempt):
    # B2 contract: matrix display names, both same-run checked artifacts and
    # canonical published records. A green aggregate alone is insufficient.
    jobs=[];artifacts=[]
    for collection,path,key in ((jobs,f'actions/runs/{run_id}/attempts/{attempt}/jobs','jobs'),
                                (artifacts,f'actions/runs/{run_id}/artifacts','artifacts')):
        for page in range(1,11):
            items=api(f'/repos/{repo}/{path}?per_page=100&page={page}')[key]
            collection.extend(items)
            if len(items)<100:break
        else:raise ValueError('matrix pagination refused')
    for name in ('validate','image (backend)','image (order)','ci-result'):
        matches=[j for j in jobs if j['name']==name]
        require(len(matches)==1)
        job=matches[0]
        require(job['run_id']==run_id and job['head_sha']==commit and
                job['status']=='completed' and job['conclusion']=='success')
    for name in (f'checked-image-{attempt}',f'checked-image-order-{attempt}',
                 f'published-evidence-{attempt}',f'published-evidence-order-{attempt}'):
        matches=[a for a in artifacts if a['name']==name]
        require(len(matches)==1)
        artifact=matches[0]
        require(not artifact['expired'] and artifact['workflow_run']['id']==run_id and
                artifact['workflow_run']['head_sha']==commit and DIGEST.fullmatch(artifact['digest']))

def patch_values(text,repo,commit,result,urls,record_hash):
    service,_,_,annotation=service_contract(repo,result.get('service'))
    image=re.search(r'^image:\s*\n(?:[ \t]+[^\n]*\n)*',text,re.M);require(image)
    block=image[0]
    for key,value in {'repository':result['imageRepository'],'tag':commit,'digest':result['imageDigest']}.items():
        block,count=re.subn(r'^  '+key+r':[^\n]*$', '  '+key+': '+json.dumps(value),block,flags=re.M);require(count==1)
    text=text[:image.start()]+block+text[image.end():]
    release=re.search(r'^release:\s*\n(?:[ \t]+[^\n]*\n)*',text,re.M);require(release)
    values={'schemaVersion':annotation,'sourceRepository':repo,'sourceCommit':commit,'runId':str(result['runId']),
            'runAttempt':str(result['runAttempt']),'recordUrl':urls['release-record.json'],'recordSha256':record_hash,
            'sbomUrl':urls['sbom.cdx.json'],'sbomSha256':result['sbomSha256']}
    if service=='order':values['service']='order'
    block='release:\n'+''.join('  '+key+': '+json.dumps(value)+'\n' for key,value in values.items())
    return text[:release.start()]+block+text[release.end():]

def propose():
    repo,commit,run_id,attempt=identity()
    service,_,_,_=service_contract(repo,os.environ.get('RELEASE_SERVICE'))
    result,urls,record_hash=release_data(repo,commit,run_id,attempt,service)
    owner=repo.split('/')[0];target=owner+'/application-manifest'
    base=api('/repos/'+target+'/git/ref/heads/main')['object']['sha']
    branch=f'github/{service}-{commit}-{run_id}-{attempt}'
    path='environments/local/'+service+'.yaml'
    file=api('/repos/'+target+'/contents/'+path+'?ref='+base)
    before=base64.b64decode(file['content']).decode()
    after=patch_values(before,repo,commit,result,urls,record_hash)
    api('/repos/'+target+'/git/refs','POST',{'ref':'refs/heads/'+branch,'sha':base})
    api('/repos/'+target+'/contents/'+path,'PUT',{'message':f'Update {service} to checked GitHub image {commit}',
        'content':base64.b64encode(after.encode()).decode(),'sha':file['sha'],'branch':branch,
        'committer':{'name':'github-actions[bot]','email':'41898282+github-actions[bot]@users.noreply.github.com'}})
    pr=api('/repos/'+target+'/pulls','POST',{'title':f'Update {service} to {commit[:12]}','head':branch,'base':'main',
        'body':'GHCR digest, release record and same-input SBOM are bound to the GitHub CI run. Run Trusted manifest verification on protected main for this PR before owner review. No automatic merge or cluster apply.'})
    print('Manifest proposal: '+pr['html_url'])

def pr_snapshot(repo,number):
    pr=api(f'/repos/{repo}/pulls/{number}')
    require(pr['state']=='open' and pr['base']['ref']=='main' and pr['base']['repo']['full_name']==repo and pr['head']['repo']['full_name']==repo)
    head=pr['head']['sha'];base=api('/repos/'+repo+'/git/ref/heads/main')['object']['sha']
    require(SHA.fullmatch(head) and SHA.fullmatch(base));return base,head

def verify_entry(entry,owner):
    repository=entry['repository'];commit=entry['tag'];digest=entry['digest'];annotations=entry['release']
    if repository==POSTGRES[0]:
        require((repository,commit,digest)==POSTGRES and annotations=={})
        require(entry['kind']=='StatefulSet' and entry['name'] in ('postgresql','order-postgresql') and
                entry['containerType']=='containers' and entry['containerName']==entry['name'])
        return None  # Platform image, never an application release.
    require(SHA.fullmatch(commit) and DIGEST.fullmatch(digest))
    matches=[(name,source,schema,version) for name,(source,image,schema,version) in SERVICES.items()
             if repository=='ghcr.io/'+owner.lower()+'/'+image]
    require(len(matches)==1)
    service,source,schema,version=matches[0];repo=owner+'/'+source
    require(annotations.get('account.lab/release-schema-version')==version and
            annotations.get('account.lab/source-repository')==repo and annotations.get('account.lab/source-commit')==commit)
    require(annotations.get('account.lab/service')=='order' if service=='order' else 'account.lab/service' not in annotations)
    if service=='order':
        require(set(annotations)=={'account.lab/'+key for key in ('release-schema-version','service','source-repository',
            'source-commit','github-run-id','github-run-attempt','image-digest','release-record-url','release-record-sha256','sbom-url','sbom-sha256')})
    run_id=annotation_id(annotations['account.lab/github-run-id']);attempt=annotation_id(annotations['account.lab/github-run-attempt'])
    result,urls,record_hash=release_data(repo,commit,run_id,attempt,service)
    require(result['imageDigest']==digest and annotations.get('account.lab/image-digest')==digest)
    require(annotations.get('account.lab/release-record-url')==urls['release-record.json'] and annotations.get('account.lab/release-record-sha256')==record_hash)
    require(annotations.get('account.lab/sbom-url')==urls['sbom.cdx.json'] and annotations.get('account.lab/sbom-sha256')==result['sbomSha256'])
    verify_latest_run(repo,commit,run_id,attempt,service)
    require(canonical_record_hash(repo,commit,run_id,attempt,service)==record_hash)
    if service=='order':
        account,_,account_hash=release_data(repo,commit,run_id,attempt,'backend')
        require(canonical_record_hash(repo,commit,run_id,attempt,'backend')==account_hash)
        require(account['sourceScanSha256']==result['sourceScanSha256'])
        require(account['archiveSha256']!=result['archiveSha256'] and account['imageDigest']!=digest)
    require(api('/repos/'+repo+'/git/ref/heads/main')['object']['sha']==commit)
    ref=repository+':'+commit
    # Public GHCR reads receive no API/registry credentials, including the reader token.
    with tempfile.TemporaryDirectory(prefix='gha-read-') as auth:
        env={**os.environ,'DOCKER_CONFIG':auth}
        for key in ('GH_TOKEN','GITHUB_TOKEN','EVIDENCE_READ_TOKEN','ACTIONS_RUNTIME_TOKEN'):env.pop(key,None)
        require(run(['crane','digest',ref],env=env).decode().strip()==digest)
        data=run(['crane','config',repository+'@'+digest],env=env).rstrip(b'\n')
        config=decode(data);require('sha256:'+sha(data)==result['configDigest'])
        require(config['config']['Labels']['org.opencontainers.image.revision']==commit and config['config']['Labels']['org.opencontainers.image.source']=='https://github.com/'+repo)
        if service=='order':
            manifest=decode(run(['crane','manifest',repository+'@'+digest],env=env))
            require(manifest['mediaType'] in ('application/vnd.oci.image.manifest.v1+json','application/vnd.docker.distribution.manifest.v2+json'))
            require('manifests' not in manifest and manifest['config']['digest']==result['configDigest'])
            require(config['architecture']=='amd64' and config['os']=='linux' and config['config']['Entrypoint']==['/order-server'])
    return {'repository':repo,'service':service,'sourceCommit':commit,'runId':run_id,'runAttempt':attempt,'digest':digest,'recordSha256':record_hash,'validUntil':result['validUntil']}

def verify():
    repo,verifier,run_id,attempt=identity();require(repo.endswith('/application-manifest'))
    require(os.environ['GITHUB_EVENT_NAME']=='workflow_dispatch' and os.environ['GITHUB_REF']=='refs/heads/main' and os.environ['GITHUB_REF_PROTECTED']=='true')
    number=os.environ['VERIFY_PR'];require(re.fullmatch('[1-9][0-9]*',number))
    if not os.environ.get('EVIDENCE_READ_TOKEN'):
        print('Configure EVIDENCE_READ_TOKEN in the manifest-verification Environment.',file=sys.stderr)
        raise ValueError('read credential missing')
    base,head=pr_snapshot(repo,number);require(base==verifier)
    run(['git','fetch','--no-tags','origin','refs/pull/'+number+'/head'])
    require(run(['git','rev-parse','FETCH_HEAD']).decode().strip()==head)
    # All executed code is the protected-main checkout. PR charts/values are data
    # only in the existing bounded, networkless render/inventory sandbox.
    clean={key:value for key,value in os.environ.items() if key not in ('GH_TOKEN','GITHUB_TOKEN','EVIDENCE_READ_TOKEN','ACTIONS_RUNTIME_TOKEN')}
    Path('.trusted').mkdir(exist_ok=True)
    run(['docker','pull','alpine/helm:4.2.4@sha256:76c375eed56144c68d6197c55bc5a4552fb42002190b796729901cbab3ae6e51'],env=clean)
    run(['docker','pull','golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b'],env=clean)
    run(['python3','-I','ci/trusted-render.py','--repo','.', '--target',base,'--source',head,'--verifier',verifier,
         '--producer-job-id',str(run_id),'--output','.trusted/github-render.json','--test-only'],env=clean)
    render=load('.trusted/github-render.json');require(render['targetSha']==base and render['sourceSha']==head and render['verifierSha']==verifier)
    before=render['renders']['before']['inventory']['entries'];after=render['renders']['candidate']['inventory']['entries']
    def key(e):return tuple(e[k] for k in ('namespace','kind','name','podSpecPath','containerType','containerName'))
    old={key(e):e for e in before};verified=[];cache={}
    for entry in after:
        if old.get(key(entry))==entry:continue
        selection=json.dumps({k:entry[k] for k in ('repository','tag','digest','release')},sort_keys=True)
        if selection not in cache:cache[selection]=verify_entry(entry,repo.split('/')[0])
        if cache[selection] is not None:verified.append(cache[selection])
    require(pr_snapshot(repo,number)==(base,head))
    for item in verified:
        require(api('/repos/'+item['repository']+'/git/ref/heads/main')['object']['sha']==item['sourceCommit'])
        verify_latest_run(item['repository'],item['sourceCommit'],item['runId'],item['runAttempt'],item['service'])
    deadline=min([now()+dt.timedelta(minutes=15)]+[timestamp(item['validUntil']) for item in verified]);require(deadline>now())
    report={'schema':'core-platform/github-manifest-verification/v1','repository':repo,'prNumber':int(number),
        'baseSha':base,'headSha':head,'verifierSha':verifier,'candidateTree':render['candidateTree'],
        'runId':run_id,'runAttempt':attempt,'verifiedImages':verified,'renderSha256':sha(Path('.trusted/github-render.json').read_bytes()),
        'createdAt':stamp(),'validUntil':deadline.isoformat(),'mergeAuthorized':False}
    Path('.trusted/github-verification.json').write_text(json.dumps(report,sort_keys=True,indent=2)+'\n')
    print('Trusted verification passed. Owner review still required; any PR/main update invalidates the report.')

if __name__=='__main__':
    try:
        require(len(sys.argv)==2 and sys.argv[1] in ('record','publish','propose','verify'))
        globals()[sys.argv[1]]()
    except Exception as error:
        # Server bodies, credential-bearing URLs, and raw scanner output stay private.
        frames=traceback.extract_tb(error.__traceback__)
        print('Failure location: '+' > '.join(f.name+':'+str(f.lineno) for f in frames)
              +' ('+type(error).__name__+((' HTTP '+str(error.code)) if isinstance(error,urllib.error.HTTPError) else '')+')',file=sys.stderr)
        print('GitHub migration operation failed; publication/merge is not authorized.',file=sys.stderr)
        sys.exit(1)
