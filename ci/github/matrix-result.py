"""Require complete same-source/run/attempt image artifacts, beyond matrix status."""
import importlib.util
from pathlib import Path
spec=importlib.util.spec_from_file_location('release',Path(__file__).with_name('release.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)

def check(root=Path('.checked')):
    repo,commit,run_id,attempt=m.identity()
    expected={'checked-image-'+str(attempt):'backend','checked-image-order-'+str(attempt):'order'}
    m.require({p.name for p in root.iterdir()}==set(expected))
    records=[]
    for name,service in expected.items():
        base=root/name;public=base/'.security/public';record=m.load(public/'release-record.json')
        _,image,schema,_=m.service_contract(repo,service)
        m.require(record['repository']==repo and record['sourceCommit']==commit and record['runId']==run_id and record['runAttempt']==attempt)
        m.require(record['workflowRef']==m.os.environ['GITHUB_WORKFLOW_REF'])
        m.require(record['schema']==schema and record['imageRepository']==image)
        m.require(record.get('service')=='order' if service=='order' else 'service' not in record)
        m.require(record['published'] is False and record['deploymentAuthorized'] is False)
        for key in ('sourcePolicyPassed','imagePolicyPassed','sbomValidated','isolatedRuntimePassed'):m.require(record[key] is True)
        for filename,key in (('sbom.cdx.json','sbomSha256'),('image-scan.json','imageScanSha256'),('source-scan.json','sourceScanSha256')):
            m.require(m.sha((public/filename).read_bytes())==record[key])
        m.require(m.sha((base/'.oci/image.tar').read_bytes())==record['archiveSha256'])
        m.check_source(repo,commit,run_id,attempt,m.load(public/'source-scan.json'),public)
        proof=m.load(public/'runtime.json')
        m.require(proof=={'service':service,'digest':record['imageDigest'],'archiveSha256':record['archiveSha256'],
                         'configDigest':record['configDigest'],'passed':True,'readOnly':True,'capDrop':'ALL','network':'internal'})
        records.append(record)
    m.require(records[0]['sourceScanSha256']==records[1]['sourceScanSha256'])
    for key in ('archiveSha256','imageDigest','configDigest'):m.require(records[0][key]!=records[1][key])

if __name__=='__main__':
    try:check()
    except Exception:
        print('Incomplete or inconsistent image matrix; CI refused.')
        raise SystemExit(1)
