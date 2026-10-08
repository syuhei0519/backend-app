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

if __name__=='__main__':unittest.main()
