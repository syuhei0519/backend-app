# Order の公開証跡が Account と混ざらず、必須の由来情報を持つことを確認します。
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
        for m in (p,c,a.m):
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
        self.manifest=json.dumps({'config':{'digest':self.order['configDigest']}}).encode()
        self.order['imageDigest']='sha256:'+p.sha(self.manifest)
        for path in ('.oci/layout','.security/public','.validation'):Path(path).mkdir(parents=True)
        blobs=Path('.oci/layout/blobs/sha256');blobs.mkdir(parents=True)
        (blobs/self.order['configDigest'][7:]).write_bytes(self.config)
        (blobs/self.order['imageDigest'][7:]).write_bytes(self.manifest)
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
        self.image_evidence(Path('.security/public'),'order',self.order['imageDigest'],self.order['configDigest'],self.layout['archiveSha256'])
    def write(self,path,value):Path(path).write_text(json.dumps(value))
    def image_evidence(self,public,service,digest,config,archive):
        # Synthetic scanner-produced evidence; the real producer is tested separately.
        source_hash=p.sha(Path('.validation/source-scan.json').read_bytes())
        identity=p.image_identity(self.repo,self.commit,123,2,service,digest,config,archive,source_hash)
        raw_hash=p.sha(('synthetic raw '+service).encode())
        sbom_hash=p.sha((public/'sbom.cdx.json').read_bytes())
        self.write(public/'image-scan.json',{**self.scan,'imageIdentity':identity,'rawScanSha256':raw_hash,'sbomSha256':sbom_hash})
        self.write(public/'sbom-proof.json',{'sha256':sbom_hash,'configDigest':config,'rawScanSha256':raw_hash})
        self.write(public/'image-proof.json',{'schema':'core-platform/github-image-proof/v1','identity':identity,
            'rawScanSha256':raw_hash,'imageScanSha256':p.sha((public/'image-scan.json').read_bytes()),
            'sbomProofSha256':p.sha((public/'sbom-proof.json').read_bytes())})
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
                               ('.validation/source-identity.json','runAttempt',3),('.validation/source-identity.json','runAttempt',2.0),
                               ('.validation/source-identity.json','runId',123.0),('.security/public/runtime.json','service','backend'),
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
                (dest/self.order['imageDigest'][7:]).write_bytes(self.manifest)
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
    def test_pre_record_scan_substitution_and_missing_proof(self):
        scan=Path('.security/public/image-scan.json');good=scan.read_bytes()
        proof=Path('.security/public/image-proof.json');sealed=proof.read_bytes()
        mutations=[b'{}',b'{"pass":true,"pass":true}',b'{"pass":NaN}',good+b'changed']
        foreign=p.decode(good);foreign['imageIdentity']['service']='backend'
        foreign['imageIdentity']['configDigest']='sha256:'+'f'*64
        mutations.append(json.dumps(foreign).encode())
        for raw in mutations:
            scan.write_bytes(raw)
            with self.assertRaises((ValueError,KeyError)):p.record()
        scan.write_bytes(good)
        proof.unlink()
        with self.assertRaises(FileNotFoundError):p.record()
        proof.write_bytes(sealed)
        p.record()
    def test_scan_identity_rejects_mixing_even_with_rehashed_summary(self):
        scan=Path('.security/public/image-scan.json');proof=Path('.security/public/image-proof.json')
        good=scan.read_bytes();seal=proof.read_bytes()
        changes={'repository':'syuhei0519/frontend-app','sourceCommit':'b'*40,'runId':124,
                 'runAttempt':3,'service':'backend','configDigest':'sha256:'+'f'*64,
                 'imageDigest':'sha256:'+'e'*64,'archiveSha256':'0'*64,'sourceScanSha256':'1'*64}
        for key,value in changes.items():
            image=p.decode(good);image['imageIdentity'][key]=value
            self.write(scan,image)
            forged=p.decode(seal);forged['identity']=image['imageIdentity'];forged['imageScanSha256']=p.sha(scan.read_bytes())
            self.write(proof,forged)
            with self.subTest(key=key),self.assertRaises(ValueError):p.record()
        scan.write_bytes(good);proof.write_bytes(seal)
        for key in p.decode(good)['imageIdentity']:
            image=p.decode(good);del image['imageIdentity'][key];self.write(scan,image)
            with self.subTest(missing=key),self.assertRaises((ValueError,KeyError)):p.record()
        scan.write_bytes(good)
    def test_scan_sbom_raw_fingerprint_coupling(self):
        scan=Path('.security/public/image-scan.json');proof=Path('.security/public/image-proof.json')
        good=scan.read_bytes();seal=proof.read_bytes()
        for field,value in [('rawScanSha256','0'*64),('rawScanSha256','invalid'),('sbomSha256','0'*64)]:
            image=p.decode(good);image[field]=value;self.write(scan,image)
            forged=p.decode(seal);forged['rawScanSha256']=image['rawScanSha256'];forged['imageScanSha256']=p.sha(scan.read_bytes());self.write(proof,forged)
            with self.subTest(field=field),self.assertRaises(ValueError):p.record()
        scan.write_bytes(good);proof.write_bytes(seal)
    def test_publish_rechecks_pre_record_scan_binding_before_api(self):
        p.record();scan=Path('.security/public/image-scan.json');image=p.load(scan)
        image['imageIdentity']['runId']=124;self.write(scan,image)
        record=p.load(p.PUBLIC/'release-record.json');record['imageScanSha256']=p.sha(scan.read_bytes())
        self.write(p.PUBLIC/'release-record.json',record)
        def validator(args,**kwargs):
            self.assertEqual(args[0],'github-oci-check')
            dest=Path(args[3])/'blobs/sha256';dest.mkdir(parents=True)
            (dest/self.order['configDigest'][7:]).write_bytes(self.config)
            (dest/self.order['imageDigest'][7:]).write_bytes(self.manifest)
            return json.dumps(self.layout).encode()
        with patch.object(p,'run',side_effect=validator),patch.object(p,'api') as api,self.assertRaises(ValueError):p.publish()
        api.assert_not_called()
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
            self.image_evidence(public,service,record['imageDigest'],record['configDigest'],record['archiveSha256'])
            record['imageScanSha256']=p.sha((public/'image-scan.json').read_bytes())
            self.write(public/'release-record.json',record)
            self.write(public/'runtime.json',{'service':service,'digest':record['imageDigest'],'configDigest':record['configDigest'],'archiveSha256':record['archiveSha256'],'passed':True,'readOnly':True,'capDrop':'ALL','network':'internal'})
        a.check(root)
        scan=root/'checked-image-order-2/.security/public/image-scan.json'
        recordpath=scan.parent/'release-record.json';scanraw=scan.read_bytes();recordraw=recordpath.read_bytes()
        foreign=(root/'checked-image-2/.security/public/image-scan.json').read_bytes();scan.write_bytes(foreign)
        forged=p.decode(recordraw);forged['imageScanSha256']=p.sha(foreign);self.write(recordpath,forged)
        with self.assertRaises(ValueError):a.check(root)
        scan.write_bytes(scanraw);recordpath.write_bytes(recordraw)
        target=root/'checked-image-order-2/.security/public/release-record.json';raw=target.read_bytes()
        for key,value in [('runId',124),('runId',123.0),('sourceCommit','b'*40),('runAttempt',3),('runAttempt',2.0),('service','backend'),('imagePolicyPassed',False),('sourceScanSha256','0'*64)]:
            self.write(target,{**p.decode(raw),key:value})
            with self.subTest(key=key),self.assertRaises(ValueError):a.check(root)
        target.write_bytes(raw)
        (root/'checked-image-order-2').rename(root/'missing')
        with self.assertRaises(ValueError):a.check(root)

if __name__=='__main__':unittest.main()
