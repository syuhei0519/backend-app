"""Synthetic producer/unchanged pinned M0 consumer interoperability. No network."""
import copy
import datetime as dt
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

HERE=Path(__file__).resolve().parent

def module(name,path):
    spec=importlib.util.spec_from_file_location(name,path)
    m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
p=module('producer',HERE/'release.py')
c=module('consumer',HERE/'fixtures/m0/release.py')
a=module('aggregate',HERE/'matrix-result.py')

class Interop(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.old=os.getcwd();os.chdir(self.temp.name);self.addCleanup(os.chdir,self.old)
        self.clock=dt.datetime(2030,1,1,1,tzinfo=dt.timezone.utc)
        for m in (p,c):
            patcher=patch.object(m,'now',return_value=self.clock);patcher.start();self.addCleanup(patcher.stop)
        self.repo='syuhei0519/backend-app';self.commit='a'*40
        self.env={'GITHUB_REPOSITORY':self.repo,'GITHUB_SHA':self.commit,'GITHUB_RUN_ID':'123','GITHUB_RUN_ATTEMPT':'2',
                  'GITHUB_WORKFLOW_REF':self.repo+'/.github/workflows/ci.yml@refs/heads/main',
                  'GITHUB_EVENT_NAME':'push','GITHUB_REF':'refs/heads/main','GITHUB_REF_PROTECTED':'true',
                  'RELEASE_SERVICE':'order','GITHUB_ACTOR':'synthetic','GH_TOKEN':'synthetic-no-credential'}
        patcher=patch.dict(os.environ,self.env);patcher.start();self.addCleanup(patcher.stop)
        self.fixture=HERE/'fixtures/m0/fixtures'
        self.order=c.load(self.fixture/'order-record.json')
        config=c.load(self.fixture/'order-config.json');config['config']['User']='10001:10001'
        self.config=json.dumps(config).encode();self.order['configDigest']='sha256:'+p.sha(self.config)
        for path in ('.oci/layout','.security/public','.validation'):Path(path).mkdir(parents=True)
        Path('.oci/image.tar').write_bytes(b'synthetic checked OCI bytes; not a real image')
        self.scan={'pass':True,'trivyVersion':'0.75.0','dbUpdatedAt':'2030-01-01T00:00:00Z','scannedAt':'2030-01-01T00:00:00Z'}
        self.write('.validation/source-scan.json',self.scan)
        self.write('.validation/source-identity.json',{'repository':self.repo,'sourceCommit':self.commit,'runId':123,'runAttempt':2,'sourceScanSha256':p.sha(Path('.validation/source-scan.json').read_bytes())})
        self.write('.security/public/image-scan.json',self.scan)
        sbom=(self.fixture/'sbom.cdx.json').read_bytes();Path('.security/public/sbom.cdx.json').write_bytes(sbom)
        self.write('.security/public/sbom-proof.json',{'sha256':p.sha(sbom),'configDigest':self.order['configDigest']})
        self.layout={'sourceCommit':self.commit,'digest':self.order['imageDigest'],'archiveSha256':p.sha(Path('.oci/image.tar').read_bytes())}
        self.write('.security/public/layout.json',self.layout)
        self.write('.security/public/reader.json',{'digest':self.order['imageDigest'],'configDigest':self.order['configDigest'],'fullLayerValidation':True})
        self.write('.security/public/runtime.json',{'service':'order','digest':self.order['imageDigest'],'configDigest':self.order['configDigest'],'archiveSha256':self.layout['archiveSha256'],'passed':True,'readOnly':True,'capDrop':'ALL','network':'internal'})
        Path('security').mkdir();self.write('security/scan-policy.json',{'exceptions':[]})
    def write(self,path,value):Path(path).write_text(json.dumps(value))
    def published(self):
        p.record();record=p.load('.security/public/release-record.json')
        return {**record,'published':True,'protectedMain':True}
    def test_exact_fixed_consumer_and_shared_fixtures(self):
        root=HERE/'fixtures/m0';source=c.load(root/'SOURCE.json')
        self.assertEqual(source['commit'],'bc6adaedcd645a4663c23284b977a3dd36c0796d')
        for name,digest in source['files'].items():self.assertEqual(p.sha((root/name).read_bytes()),digest)
        for name,service in [('order','order'),('backend','backend')]:
            record=c.load(self.fixture/(name+'-record.json'))
            p.validate_record(record,self.repo,self.commit,123,2,service)
            c.validate_record(record,self.repo,self.commit,123,2,service)
    def test_generated_record_consumed_by_m0_and_values_match(self):
        record=self.published();c.validate_record(record,self.repo,self.commit,123,2,'order')
        self.assertEqual(set(record),set(self.order))
        text='enabled: false\nimage:\n  repository: old\n  tag: old\n  digest: old\nrelease:\n  schemaVersion: old\nconfig:\n  dbName: orders\n'
        urls={'release-record.json':'record','sbom.cdx.json':'sbom'}
        self.assertEqual(p.patch_values(text,self.repo,self.commit,record,urls,'0'*64),c.patch_values(text,self.repo,self.commit,record,urls,'0'*64))
        self.assertIn('enabled: false',p.patch_values(text,self.repo,self.commit,record,urls,'0'*64))
    def test_record_negative_binding_and_expiry(self):
        good=self.published()
        changes={'service':'backend','schema':p.SCHEMA,'repository':'syuhei0519/frontend-app','sourceCommit':'b'*40,
                 'runId':124,'runAttempt':3,'imageRepository':'ghcr.io/syuhei0519/backend-app','workflowRef':'untrusted',
                 'configDigest':'invalid','archiveSha256':'invalid','sbomSha256':'invalid','imageDigest':'invalid',
                 'imagePolicyPassed':False,'validUntil':'2000-01-01T00:00:00Z','unknown':'unknown'}
        for key,value in changes.items():
            for m in (p,c):
                with self.subTest(key=key,module=m.__name__),self.assertRaises((ValueError,KeyError)):
                    m.validate_record({**good,key:value},self.repo,self.commit,123,2,'order')
        for key in good:
            bad=dict(good);del bad[key]
            with self.assertRaises((ValueError,KeyError)):c.validate_record(bad,self.repo,self.commit,123,2,'order')
    def test_producer_rejects_swapped_inputs_and_rebuild(self):
        for path,key,value in [('.validation/source-identity.json','sourceCommit','b'*40),('.validation/source-identity.json','runId',124),
                               ('.validation/source-identity.json','runAttempt',3),('.security/public/runtime.json','service','backend'),
                               ('.security/public/runtime.json','configDigest','sha256:'+'0'*64),('.security/public/runtime.json','readOnly',False),
                               ('.security/public/sbom-proof.json','sha256','0'*64),('.security/public/sbom-proof.json','configDigest','sha256:'+'0'*64),
                               ('.security/public/layout.json','archiveSha256','0'*64)]:
            before=Path(path).read_bytes();value_doc=p.decode(before);value_doc[key]=value;self.write(path,value_doc)
            with self.subTest(path=path,key=key),self.assertRaises(ValueError):p.record()
            Path(path).write_bytes(before)
        Path('.oci/image.tar').write_bytes(b'rebuilt')
        with self.assertRaises(ValueError):p.record()
    def test_publish_same_tar_and_service_release_no_rebuild(self):
        p.record();calls=[];assets={}
        config=self.config
        # M0 fixture config is exact real JSON bytes with a checksum in the record.
        def run(args,**kwargs):
            calls.append(args)
            if args[0]=='github-oci-check':
                dest=Path(args[3])/'blobs/sha256';dest.mkdir(parents=True)
                (dest/self.order['configDigest'][7:]).write_bytes(config)
                return json.dumps(self.layout).encode()
            if args[:2]==['crane','digest']:return self.order['imageDigest'].encode()
            if args[:2]==['crane','config']:return config
            return b''
        def api(path,method='GET',body=None,raw=None,upload=False):
            if '/git/ref/heads/main' in path:return {'object':{'sha':self.commit}}
            if method=='POST' and path.endswith('/releases'):
                self.assertEqual(body['tag_name'],'gha-order-'+self.commit+'-123-2');return {'id':42}
            if raw is not None:assets[path.split('name=')[1]]=raw
            return {}
        # Actual push transport mocked; all local checksum/config/runtime guards execute.
        with patch.object(p,'run',side_effect=run),patch.object(p,'api',side_effect=api),patch.object(p.subprocess,'run',return_value=type('Result',(),{'returncode':1,'stderr':b'404 Not Found'})()):p.publish()
        record=p.decode(assets['release-record.json']);c.validate_record(record,self.repo,self.commit,123,2,'order')
        self.assertIn(['crane','push','.oci/publish-layout','ghcr.io/syuhei0519/backend-app-order:'+self.commit],calls)
        self.assertFalse(any('build' in arg for call in calls for arg in call))
    def test_guard_before_publish_and_propose_side_effects(self):
        for field,value in [('GITHUB_EVENT_NAME','workflow_dispatch'),('GITHUB_REF','refs/heads/feature'),('GITHUB_REF_PROTECTED','false'),('RELEASE_SERVICE','unknown')]:
            with patch.dict(os.environ,{field:value}),patch.object(p,'api') as api:
                for operation in (p.publish,p.propose):
                    with self.assertRaises((ValueError,FileNotFoundError)):operation()
                if field!='RELEASE_SERVICE':api.assert_not_called()
    def test_matrix_missing_failure_swaps(self):
        order=self.published();order.pop('protectedMain');order['published']=False
        root=Path('.checked');root.mkdir()
        for service in ('backend','order'):
            dest=root/('checked-image-'+('order-' if service=='order' else '')+'2');public=dest/'.security/public';public.mkdir(parents=True)
            (dest/'.oci').mkdir();archive=b'synthetic '+service.encode();(dest/'.oci/image.tar').write_bytes(archive)
            record=copy.deepcopy(order);record['archiveSha256']=p.sha(archive)
            if service=='backend':
                del record['service'];record['schema']=p.SCHEMA;record['imageRepository']='ghcr.io/'+self.repo
                record['imageDigest']='sha256:'+'e'*64;record['configDigest']='sha256:'+'f'*64
            for name in ('source-scan.json','source-identity.json','image-scan.json','sbom.cdx.json'):(public/name).write_bytes((Path('.security/public')/name).read_bytes())
            self.write(public/'release-record.json',record)
            self.write(public/'runtime.json',{'service':service,'digest':record['imageDigest'],'configDigest':record['configDigest'],'archiveSha256':record['archiveSha256'],'passed':True,'readOnly':True,'capDrop':'ALL','network':'internal'})
        a.check(root)
        target=root/'checked-image-order-2/.security/public/release-record.json';raw=target.read_bytes()
        for key,value in [('runId',124),('sourceCommit','b'*40),('runAttempt',3),('service','backend'),('imagePolicyPassed',False),('sourceScanSha256','0'*64)]:
            self.write(target,{**p.decode(raw),key:value})
            with self.subTest(key=key),self.assertRaises(ValueError):a.check(root)
        target.write_bytes(raw)
        (root/'checked-image-order-2').rename(root/'missing')
        with self.assertRaises(ValueError):a.check(root)

if __name__=='__main__':unittest.main()
