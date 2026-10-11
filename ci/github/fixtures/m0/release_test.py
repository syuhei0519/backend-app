import importlib.util
from pathlib import Path
import unittest
import datetime as dt
import io,zipfile
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('release',Path(__file__).with_name('release.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)

class Boundary(unittest.TestCase):
    def test_duplicate_json(self):
        with self.assertRaises(ValueError):m.decode('{"pass":false,"pass":true}')
    def test_stale_and_future_scan(self):
        for time in (m.now()-dt.timedelta(hours=25),m.now()+dt.timedelta(hours=1)):
            with self.assertRaises(ValueError):m.fresh(time.isoformat())
    def test_asset_origin(self):
        with self.assertRaises(ValueError):m.public_asset('https://example.org/record','owner/backend-app','tag','release-record.json')
    def test_api_redirect(self):
        with self.assertRaises(ValueError):m.NoRedirect().redirect_request(None,None,302,'',{},'https://example.org')
    def test_canonical_artifact_integrity_and_members(self):
        for name,accepted in [('release-record.json',True),('../release-record.json',False)]:
            stream=io.BytesIO()
            with zipfile.ZipFile(stream,'w') as archive:archive.writestr(name,b'{"test":true}')
            data=stream.getvalue()
            if accepted:self.assertEqual(m.artifact_record_bytes(data,'sha256:'+m.sha(data)),b'{"test":true}')
            else:
                with self.assertRaises(ValueError):m.artifact_record_bytes(data,'sha256:'+m.sha(data))
            with self.assertRaises(ValueError):m.artifact_record_bytes(data,'sha256:'+'0'*64)
    def test_server_run_identity_and_revocation(self):
        run={'id':123,'repository':{'full_name':'owner/backend-app'},'head_sha':'a'*40,'head_branch':'main','event':'push',
            'status':'completed','conclusion':'success','run_attempt':1,'path':'.github/workflows/ci.yml','created_at':'2026-10-08T10:00:00Z'}
        m.validate_run(run,'owner/backend-app','a'*40,123,1)
        for change in ({'event':'pull_request'},{'head_branch':'feature'},{'conclusion':'failure'},{'run_attempt':2},{'head_sha':'b'*40}):
            with self.assertRaises(ValueError):m.validate_run({**run,**change},'owner/backend-app','a'*40,123,1)
        later={**run,'id':124,'conclusion':'failure','created_at':'2026-10-08T11:00:00Z'}
        with patch.object(m,'api',side_effect=[run,{'workflow_runs':[later,run]}]):
            with self.assertRaises(ValueError):m.verify_latest_run('owner/backend-app','a'*40,123,1)
    def test_values_patch_preserves_configuration(self):
        text='telemetry:\n  enabled: true\nimage:\n  repository: "old"\n  tag: "old"\n  digest: "old"\n# comment\nrelease:\n  sourceProjectId: "old"\n  sourceCommit: "old"\nconfig:\n  dbName: account\n'
        record={'imageRepository':'ghcr.io/owner/backend-app','imageDigest':'sha256:'+'b'*64,'runId':123,'runAttempt':1,'sbomSha256':'c'*64}
        result=m.patch_values(text,'owner/backend-app','a'*40,record,{'release-record.json':'record-url','sbom.cdx.json':'sbom-url'},'d'*64)
        self.assertIn('telemetry:\n  enabled: true',result);self.assertIn('config:\n  dbName: account',result)
        self.assertIn('schemaVersion: "github-v1"',result);self.assertNotIn('sourceProjectId',result)
    def test_record_rejects_untrusted_workflow_and_failed_scan(self):
        record={'schema':m.SCHEMA,'repository':'owner/backend-app','sourceCommit':'a'*40,'runId':123,'runAttempt':1,
          'workflowRef':'owner/backend-app/.github/workflows/ci.yml@refs/heads/main','imageRepository':'ghcr.io/owner/backend-app',
          'imageDigest':'sha256:'+'b'*64,'configDigest':'sha256:'+'c'*64,'deploymentAuthorized':False}
        for k in ('sourcePolicyPassed','imagePolicyPassed','sbomValidated','isolatedRuntimePassed'):record[k]=True
        for k in ('archiveSha256','sbomSha256','sourceScanSha256','imageScanSha256'):record[k]='d'*64
        for k in ('scannedAt','dbUpdatedAt','createdAt'):record[k]=(m.now()-dt.timedelta(seconds=1)).isoformat()
        record['validUntil']=(m.now()+dt.timedelta(hours=23)).isoformat()
        m.validate_record(record,'owner/backend-app','a'*40,123,1)
        for change in ({'workflowRef':'owner/backend-app/.github/workflows/ci.yml@refs/heads/untrusted'},{'imagePolicyPassed':False},{'repository':'other/backend-app'},{'runAttempt':2},{'imageRepository':'registry.gitlab.com/owner/backend-app'}, {'validUntil':(m.now()-dt.timedelta(minutes=1)).isoformat()}, {'validUntil':(m.now()+dt.timedelta(hours=25)).isoformat()}):
            with self.assertRaises(ValueError):m.validate_record({**record,**change},'owner/backend-app','a'*40,123,1)



class OrderContract(unittest.TestCase):
    def setUp(self):
        import copy
        self.fixtures=Path(__file__).with_name('fixtures')
        self.order=m.load(self.fixtures/'order-record.json')
        self.account=m.load(self.fixtures/'backend-record.json')
        self.repo=self.order['repository'];self.commit=self.order['sourceCommit']
        self.clock=patch.object(m,'now',return_value=dt.datetime(2030,1,1,1,tzinfo=dt.timezone.utc));self.clock.start();self.addCleanup(self.clock.stop)
    def validate(self,value,service='order'):
        m.validate_record(value,self.repo,self.commit,123,2,service)
    def test_shared_v1_v2_positive_and_schema(self):
        self.validate(self.order);self.validate(self.account,'backend')
        schema=m.load(Path(__file__).with_name('order-release.schema.json'))
        self.assertEqual(set(schema['required']),set(self.order))
        frontend={**self.account,'repository':'syuhei0519/frontend-app','imageRepository':'ghcr.io/syuhei0519/frontend-app','workflowRef':'syuhei0519/frontend-app/.github/workflows/ci.yml@refs/heads/main'}
        m.validate_record(frontend,'syuhei0519/frontend-app',self.commit,123,2,'frontend')
    def test_record_negative_bindings(self):
        cases={'schema':m.SCHEMA,'service':'backend','repository':'syuhei0519/frontend-app','sourceCommit':'0'*40,
               'runId':124,'runAttempt':True,'workflowRef':self.repo+'/.github/workflows/other.yml@refs/heads/main',
               'imageRepository':'ghcr.io/'+self.repo,'imageDigest':'sha256:bad','configDigest':'bad',
               'archiveSha256':'bad','sbomSha256':'bad','sourceScanSha256':'bad','imageScanSha256':'bad',
               'validUntil':'2030-01-01T00:30:00Z','sourcePolicyPassed':False,'published':False,
               'protectedMain':False,'deploymentAuthorized':True,'unknown':True}
        for key,value in cases.items():
            with self.subTest(key=key),self.assertRaises((ValueError,KeyError)):
                self.validate({**self.order,key:value})
        for key in self.order:
            candidate=dict(self.order);del candidate[key]
            with self.subTest(missing=key),self.assertRaises((ValueError,KeyError)):self.validate(candidate)
        with self.assertRaises(ValueError):self.validate(self.account)
        with self.assertRaises(ValueError):m.service_contract(self.repo,'unknown')
        with self.assertRaises(ValueError):m.service_contract('syuhei0519/frontend-app','order')
    def jobs(self):
        return [{'name':n,'run_id':123,'head_sha':self.commit,'status':'completed','conclusion':'success'} for n in ('validate','image (backend)','image (order)','ci-result')]
    def artifacts(self):
        return [{'name':n,'expired':False,'workflow_run':{'id':123,'head_sha':self.commit},'digest':'sha256:'+'1'*64} for n in ('checked-image-2','checked-image-order-2','published-evidence-2','published-evidence-order-2')]
    def test_matrix_and_artifact_closure(self):
        def verify(jobs,artifacts):
            with patch.object(m,'api',side_effect=[{'jobs':jobs},{'artifacts':artifacts}]):m.verify_order_matrix(self.repo,self.commit,123,2)
        verify(self.jobs(),self.artifacts())
        for index in range(4):
            jobs=self.jobs();jobs.pop(index)
            with self.assertRaises(ValueError):verify(jobs,self.artifacts())
            artifacts=self.artifacts();artifacts.pop(index)
            with self.assertRaises(ValueError):verify(self.jobs(),artifacts)
        for field,value in (('conclusion','failure'),('status','in_progress'),('run_id',124),('head_sha','0'*40)):
            jobs=self.jobs();jobs[2][field]=value
            with self.assertRaises(ValueError):verify(jobs,self.artifacts())
        for field,value in (('expired',True),('workflow_run',{'id':124,'head_sha':self.commit}),('digest','bad')):
            artifacts=self.artifacts();artifacts[1][field]=value
            with self.assertRaises(ValueError):verify(self.jobs(),artifacts)
        with self.assertRaises(ValueError):verify(self.jobs()+[self.jobs()[0]],self.artifacts())
    def asset(self,url,repo,tag,name):
        self.assertEqual(url,f'https://github.com/{repo}/releases/download/{tag}/{name}')
        if name=='release-record.json':return (self.fixtures/('order-record.json' if tag.startswith('gha-order-') else 'backend-record.json')).read_bytes()
        return (self.fixtures/name).read_bytes()
    def api(self,path):
        if '/releases/tags/' in path:
            tag=path.rsplit('/',1)[1]
            return {'draft':False,'prerelease':False,'tag_name':tag,'assets':[{'name':n,'browser_download_url':f'https://github.com/{self.repo}/releases/download/{tag}/{n}'} for n in ('release-record.json','sbom.cdx.json','image-scan.json')]}
        if '/git/ref/' in path:return {'object':{'type':'commit','sha':self.commit}}
        self.fail('unexpected API path '+path)
    def entry(self):
        record=self.order;tag=m.tag_for(self.commit,123,2,'order');root=f'https://github.com/{self.repo}/releases/download/{tag}/'
        annotations={'release-schema-version':'github-v2','service':'order','source-repository':self.repo,'source-commit':self.commit,
          'github-run-id':'123','github-run-attempt':'2','image-digest':record['imageDigest'],'release-record-url':root+'release-record.json',
          'release-record-sha256':m.sha((self.fixtures/'order-record.json').read_bytes()),'sbom-url':root+'sbom.cdx.json','sbom-sha256':record['sbomSha256']}
        return {'repository':record['imageRepository'],'tag':self.commit,'digest':record['imageDigest'],'release':{'account.lab/'+k:v for k,v in annotations.items()}}
    def crane(self,args,**kwargs):
        self.assertTrue(set(kwargs['env']).isdisjoint({'GH_TOKEN','GITHUB_TOKEN','EVIDENCE_READ_TOKEN','ACTIONS_RUNTIME_TOKEN'}))
        if args[1]=='digest':return self.order['imageDigest'].encode()
        return (self.fixtures/('order-config.json' if args[1]=='config' else 'order-manifest.json')).read_bytes()
    def canonical(self,repo,commit,run,attempt,service=None):
        return m.sha((self.fixtures/('order-record.json' if service=='order' else 'backend-record.json')).read_bytes())
    def verify(self,entry=None):
        with patch.object(m,'api',side_effect=self.api),patch.object(m,'public_asset',side_effect=self.asset),patch.object(m,'verify_latest_run'),patch.object(m,'canonical_record_hash',side_effect=self.canonical),patch.object(m,'run',side_effect=self.crane):
            return m.verify_entry(entry or self.entry(),'syuhei0519')
    def test_complete_selection_and_anonymous_registry(self):
        self.assertEqual(self.verify()['service'],'order')
    def test_selection_spoof_and_wrong_checksums(self):
        for key,value in (('service','backend'),('source-repository','syuhei0519/backend-app-order'),('release-schema-version','github-v1'),('release-record-sha256','0'*64),('sbom-sha256','0'*64),('github-run-id','+123'),('source-commit','0'*40)):
            entry=self.entry();entry['release']['account.lab/'+key]=value
            with self.subTest(key=key),self.assertRaises(ValueError):self.verify(entry)
        entry=self.entry();entry['repository']='ghcr.io/'+self.repo
        with self.assertRaises(ValueError):self.verify(entry)
    def test_release_moved_tag_and_scan_or_sbom_bytes(self):
        original=self.api
        def moved(path):
            if '/git/ref/tags/' in path:return {'object':{'type':'commit','sha':'0'*40}}
            return original(path)
        with patch.object(m,'api',side_effect=moved),patch.object(m,'public_asset',side_effect=self.asset):
            with self.assertRaises(ValueError):m.release_data(self.repo,self.commit,123,2,'order')
        for damaged in ('sbom.cdx.json','image-scan.json'):
            def asset(url,repo,tag,name):return b'{}' if name==damaged else self.asset(url,repo,tag,name)
            with patch.object(m,'api',side_effect=self.api),patch.object(m,'public_asset',side_effect=asset):
                with self.assertRaises(ValueError):m.release_data(self.repo,self.commit,123,2,'order')
    def test_platform_image_separate_allowlist(self):
        entry={'repository':m.POSTGRES[0],'tag':m.POSTGRES[1],'digest':m.POSTGRES[2],'release':{},'kind':'StatefulSet','name':'order-postgresql','containerType':'containers','containerName':'order-postgresql'}
        self.assertIsNone(m.verify_entry(entry,'syuhei0519'))
        for change in ({'digest':'sha256:'+'0'*64},{'tag':'latest'},{'release':{'account.lab/service':'order'}},{'kind':'Deployment'},{'name':'unknown'}):
            with self.assertRaises(ValueError):m.verify_entry({**entry,**change},'syuhei0519')
    def test_order_values_and_service_tag(self):
        text='image:\n  repository: old\n  tag: old\n  digest: old\nrelease:\n  schemaVersion: old\nconfig:\n  dbName: orders\n'
        result=m.patch_values(text,self.repo,self.commit,self.order,{'release-record.json':'record','sbom.cdx.json':'sbom'},'0'*64)
        self.assertIn('schemaVersion: "github-v2"',result);self.assertIn('service: "order"',result)
        self.assertIn('dbName: orders',result)
        self.assertEqual(m.tag_for(self.commit,123,2,'order'),'gha-order-'+self.commit+'-123-2')
    def test_zip_duplicate_extra_symlink_and_nonfinite(self):
        import stat,warnings
        for names in (['release-record.json','other'],['release-record.json','release-record.json'],['/release-record.json'],['dir/release-record.json']):
            stream=io.BytesIO()
            with warnings.catch_warnings():
                warnings.simplefilter('ignore')
                with zipfile.ZipFile(stream,'w') as z:
                    for name in names:z.writestr(name,b'{}')
            with self.assertRaises(ValueError):m.artifact_record_bytes(stream.getvalue(),'sha256:'+m.sha(stream.getvalue()))
        stream=io.BytesIO();info=zipfile.ZipInfo('release-record.json');info.external_attr=(stat.S_IFLNK|0o777)<<16
        with zipfile.ZipFile(stream,'w') as z:z.writestr(info,b'{}')
        with self.assertRaises(ValueError):m.artifact_record_bytes(stream.getvalue(),'sha256:'+m.sha(stream.getvalue()))
        for data in ('{"value":NaN}','{"value":Infinity}'):
            with self.assertRaises(ValueError):m.decode(data)



    def test_registry_main_archive_and_peer_binding_fail_closed(self):
        from contextlib import ExitStack
        for fault in ('main','digest','config','index','architecture','canonical','peer-source','peer-archive'):
            with self.subTest(fault=fault),ExitStack() as stack:
                original_api=self.api
                def api(path):
                    if fault=='main' and '/git/ref/heads/' in path:return {'object':{'sha':'0'*40}}
                    return original_api(path)
                def crane(args,**kwargs):
                    raw=self.crane(args,**kwargs)
                    if fault=='digest' and args[1]=='digest':return b'sha256:'+b'0'*64
                    if fault=='config' and args[1]=='config':return b'{}'
                    if fault=='index' and args[1]=='manifest':return b'{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}'
                    return raw
                def canonical(*args):return '0'*64 if fault=='canonical' else self.canonical(*args)
                original_release=m.release_data
                def release(*args):
                    result,urls,checksum=original_release(*args)
                    if args[-1]=='backend':
                        if fault=='peer-source':result['sourceScanSha256']='0'*64
                        if fault=='peer-archive':result['archiveSha256']=self.order['archiveSha256']
                    return result,urls,checksum
                stack.enter_context(patch.object(m,'api',side_effect=api));stack.enter_context(patch.object(m,'public_asset',side_effect=self.asset))
                stack.enter_context(patch.object(m,'verify_latest_run'));stack.enter_context(patch.object(m,'canonical_record_hash',side_effect=canonical))
                stack.enter_context(patch.object(m,'run',side_effect=crane));stack.enter_context(patch.object(m,'release_data',side_effect=release))
                if fault=='architecture':
                    # A changed config also changes its bound digest; both must be refused.
                    stack.enter_context(patch.object(m,'run',side_effect=lambda args,**kw: b'{}' if args[1]=='config' else crane(args,**kw)))
                with self.assertRaises((ValueError,KeyError)):m.verify_entry(self.entry(),'syuhei0519')
    def test_canonical_service_artifact_download(self):
        from unittest.mock import Mock
        import urllib.error
        raw=(self.fixtures/'order-record.json').read_bytes();stream=io.BytesIO()
        with zipfile.ZipFile(stream,'w') as z:z.writestr('release-record.json',raw)
        payload=stream.getvalue()
        artifact={'id':42,'name':'published-evidence-order-2','expired':False,'workflow_run':{'id':123,'head_sha':self.commit},'digest':'sha256:'+m.sha(payload)}
        response=Mock();response.read.return_value=payload;response.__enter__=Mock(return_value=response);response.__exit__=Mock(return_value=False)
        signed='https://example.blob.core.windows.net/synthetic'
        first=Mock();first.open.side_effect=urllib.error.HTTPError('https://api.github.com',302,'',{'Location':signed},None)
        second=Mock();second.open.return_value=response
        with patch.dict(m.os.environ,{'EVIDENCE_READ_TOKEN':'synthetic-no-credential'}),patch.object(m,'api',return_value={'artifacts':[artifact]}),patch.object(m.urllib.request,'build_opener',side_effect=[first,second]):
            self.assertEqual(m.canonical_record_hash(self.repo,self.commit,123,2,'order'),m.sha(raw))
        self.assertIsNone(second.open.call_args.args[0].get_header('Authorization'))
        for artifacts in ([],[artifact,artifact],[{**artifact,'expired':True}],[{**artifact,'workflow_run':{'id':124,'head_sha':self.commit}}],[{**artifact,'name':'published-evidence-2'}]):
            with patch.object(m,'api',return_value={'artifacts':artifacts}),self.assertRaises(ValueError):m.canonical_record_hash(self.repo,self.commit,123,2,'order')

if __name__=='__main__':unittest.main()
