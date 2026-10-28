# 公開・配備提案で、由来や権限の条件を満たさない入力が拒否されることを確認します。
import importlib.util
from pathlib import Path
import unittest
import datetime as dt
import io,zipfile
import re
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
        record={'imageRepository':'ghcr.io/owner/backend-app','imageDigest':'sha256:'+'b'*64,'runId':123,'runAttempt':1,
                'workflowRef':'owner/backend-app/.github/workflows/ci.yml@refs/heads/main','sbomSha256':'c'*64}
        result=m.patch_values(text,'owner/backend-app','a'*40,record,{'release-record.json':'record-url','sbom.cdx.json':'sbom-url'},'d'*64)
        self.assertIn('telemetry:\n  enabled: true',result);self.assertIn('config:\n  dbName: account',result)
        self.assertIn('schemaVersion: "github-v1"',result);self.assertNotIn('sourceProjectId',result)
        self.assertIn('event: "push"',result);self.assertIn('ref: "refs/heads/main"',result)
        self.assertIn('workflowRef: "owner/backend-app/.github/workflows/ci.yml@refs/heads/main"',result)
    def test_current_runtime_profile_patch_is_finite(self):
        fixture=Path(__file__).with_name('fixtures')/'account-runtime-profile.yaml'
        before=fixture.read_text(encoding='utf-8')
        commit='d'*40;digest='sha256:'+'c'*64;run=12;attempt=3
        base=f'https://github.com/owner/backend-app/releases/download/gha-{commit}-{run}-{attempt}/'
        urls={'release-record.json':base+'release-record.json','sbom.cdx.json':base+'sbom.cdx.json'}
        record={'sourceCommit':commit,'workflowRef':'owner/backend-app/.github/workflows/ci.yml@refs/heads/main',
                'runId':run,'runAttempt':attempt,'imageRepository':'ghcr.io/owner/backend-app',
                'imageDigest':digest,'sbomSha256':'e'*64}
        after=m.patch_runtime_profile(before,'owner/backend-app',commit,record,urls,'f'*64)
        for expected in ('sourceCommit: "'+commit+'"','imageRevision: "sha256-'+'c'*56+'"',
                         '  repository: "ghcr.io/owner/backend-app"','  tag: "'+commit+'"',
                         '  digest: "'+digest+'"','  schemaVersion: "github-v1"',
                         '  event: "push"','  ref: "refs/heads/main"',
                         '  workflowRef: "owner/backend-app/.github/workflows/ci.yml@refs/heads/main"',
                         '  runId: "12"','  runAttempt: "3"','  recordUrl: "'+urls['release-record.json']+'"',
                         '  recordSha256: "'+'f'*64+'"','  sbomUrl: "'+urls['sbom.cdx.json']+'"',
                         '  sbomSha256: "'+'e'*64+'"','  published: true'):
            self.assertIn(expected,after)
        for preserved in ('owner: "plat170-account"','strategy: "Canary"','promotionMode: "auto"',
                          'analysisPluginEnabled: true'):
            self.assertIn(preserved,after)
        self.assertNotIn('image: ghcr.io/',after)
    def test_runtime_profile_patch_rejects_malformed_or_unapproved_input(self):
        fixture=(Path(__file__).with_name('fixtures')/'account-runtime-profile.yaml').read_text(encoding='utf-8')
        invalid=(fixture+'owner: plat170-account\n',
                 fixture+'unknown: value\n',
                 fixture.replace('owner: plat170-account\n',''),
                 fixture.replace('owner: plat170-account','owner: another-owner'),
                 fixture.replace('strategy: Canary','strategy: Rolling'),
                 fixture.replace('analysisPluginEnabled: true','analysisPluginEnabled: false'),
                 fixture.replace('image: ghcr.io/owner/backend-app@','image:\n  repository: ghcr.io/owner/backend-app@'))
        commit='d'*40;run=12;attempt=3
        base=f'https://github.com/owner/backend-app/releases/download/gha-{commit}-{run}-{attempt}/'
        urls={'release-record.json':base+'release-record.json','sbom.cdx.json':base+'sbom.cdx.json'}
        record={'sourceCommit':commit,'workflowRef':'owner/backend-app/.github/workflows/ci.yml@refs/heads/main',
                'runId':run,'runAttempt':attempt,'imageRepository':'ghcr.io/owner/backend-app',
                'imageDigest':'sha256:'+'c'*64,'sbomSha256':'e'*64}
        for text in invalid:
            with self.subTest(text=text),self.assertRaises(ValueError):
                m.patch_runtime_profile(text,'owner/backend-app',commit,record,urls,'f'*64)
        for changed in ({'imageRepository':'ghcr.io/other/backend-app'},
                        {'imageDigest':'sha256:bad'},
                        {'service':'order'},
                        {'runAttempt':0},
                        {'workflowRef':'owner/backend-app/.github/workflows/other.yml@refs/heads/main'}):
            with self.subTest(changed=changed),self.assertRaises(ValueError):
                m.patch_runtime_profile(fixture,'owner/backend-app',commit,{**record,**changed},urls,'f'*64)
        bad_urls={**urls,'release-record.json':urls['release-record.json'].replace('-12-3/','-13-3/')}
        with self.assertRaises(ValueError):
            m.patch_runtime_profile(fixture,'owner/backend-app',commit,record,bad_urls,'f'*64)
    def test_proposal_targets_only_current_profile(self):
        self.assertEqual(m.proposal_path('backend'),'learning/account-release/runtime-profile.yaml')
        for service in ('order','frontend','unknown'):
            with self.subTest(service=service),self.assertRaises(ValueError):m.proposal_path(service)
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

class WorkflowPublication(unittest.TestCase):
    """Evaluate the actual workflow's closed conjunction grammar, not a copied gate.

    This is a contract test, not a GitHub scheduler implementation. Unknown YAML
    layout or expression syntax fails rather than being silently approximated.
    """
    @classmethod
    def setUpClass(cls):
        cls.workflow=(Path(__file__).resolve().parents[2]/'.github/workflows/ci.yml').read_text(encoding='utf-8')

    def block(self,name):
        matches=re.findall(r'^  '+re.escape(name)+r':\n(.*?)(?=^  [a-z][a-z-]*:|\Z)',self.workflow,re.M|re.S)
        self.assertEqual(len(matches),1)
        return matches[0]

    def condition(self,name):
        matches=re.findall(r'^    if: >-\n((?:      [^\n]+\n)+)',self.block(name),re.M)
        self.assertEqual(len(matches),1)
        return ' '.join(line.strip() for line in matches[0].splitlines())

    def accepts(self,name,context):
        values=[]
        for term in self.condition(name).split('&&'):
            term=term.strip()
            if term=='!cancelled()':
                values.append(not context['cancelled'])
                continue
            match=re.fullmatch(r"([a-zA-Z_][a-zA-Z0-9_.-]*)(?: == '([^']*)')?",term)
            self.assertIsNotNone(match,'Unsupported workflow expression: '+term)
            key,expected=match.groups()
            actual=context[key]
            if expected is None:
                self.assertIs(type(actual),bool)
                values.append(actual)
            else:
                self.assertIs(type(actual),str)
                values.append(actual==expected)
        return all(values)

    def allowed(self):
        return {'cancelled':False,'github.event_name':'push','github.ref':'refs/heads/main',
                'github.ref_protected':True,'vars.ENABLE_GHCR_PUBLISH':'true',
                'vars.ENABLE_MANIFEST_PR':'true','needs.classify.outputs.release':'true',
                **{'needs.'+job+'.result':'success'
                    for job in ('classify','validate','image','ci-result','publish')}}

    def test_full_ci_and_explicit_status(self):
        # The graph has a skipped docs ancestor. Only test expression semantics;
        # protected-main scheduler/publication remains an integration check.
        self.assertIn('needs: [classify, docs, validate, sast, image]',self.block('ci-result'))
        self.assertIn('if: always()',self.block('ci-result'))
        for job in ('publish','propose'):
            with self.subTest(job=job):
                self.assertIn('!cancelled()',self.condition(job))
                self.assertTrue(self.accepts(job,self.allowed()))

    def test_required_results_and_docs_only(self):
        for job,dependencies in (('publish',('classify','validate','image','ci-result')),('propose',('publish',))):
            for dependency in dependencies:
                for result in ('failure','skipped','cancelled'):
                    with self.subTest(job=job,dependency=dependency,result=result):
                        self.assertFalse(self.accepts(job,{**self.allowed(),'needs.'+dependency+'.result':result}))
        # docs-only ci-result may succeed, but no checked image was produced.
        context={**self.allowed(),'needs.validate.result':'skipped','needs.image.result':'skipped'}
        self.assertFalse(self.accepts('publish',context))
        self.assertFalse(self.accepts('propose',{**context,'needs.publish.result':'skipped'}))

    def test_context_and_variable_refusals(self):
        for job,variable in (('publish','ENABLE_GHCR_PUBLISH'),('propose','ENABLE_MANIFEST_PR')):
            changes=[('cancelled',True),('github.event_name','pull_request'),
                     ('github.event_name','workflow_dispatch'),('github.ref','refs/heads/feature'),
                     ('github.ref','refs/tags/release'),('github.ref_protected',False)]
            changes.extend(('vars.'+variable,value) for value in ('false',''))
            for key,value in changes:
                with self.subTest(job=job,key=key,value=value):
                    self.assertFalse(self.accepts(job,{**self.allowed(),key:value}))
        self.assertFalse(self.accepts('publish',{
            **self.allowed(),'needs.classify.outputs.release':'false'}))

    def test_dependency_environment_and_permission_contract(self):
        publish=self.block('publish');propose=self.block('propose')
        self.assertIn('needs: [classify, validate, image, ci-result]',publish)
        self.assertIn('needs: publish',propose)
        self.assertIn('environment: ghcr-publish',publish)
        self.assertIn('environment: manifest-update',propose)
        for block in (publish,propose):
            self.assertIn('service: [backend, order]',block)
        self.assertIn('    permissions:\n      contents: write\n      packages: write\n',publish)
        self.assertIn('    permissions:\n      contents: read\n',propose)

    def test_required_validation_is_not_serialized_by_publication_approval(self):
        # A protected-main publish may wait indefinitely for its environment
        # approval. Workflow-wide concurrency would make that wait block the
        # required checks for every later main commit, so only the publication
        # jobs may be serialized.
        workflow_header=self.workflow.split('jobs:\n',1)[0]
        self.assertNotRegex(workflow_header,r'(?m)^concurrency:')
        publish=self.block('publish')
        self.assertIn('environment: ghcr-publish',publish)
        self.assertIn('    concurrency:\n      group: ghcr-publication-${{ matrix.service }}\n      cancel-in-progress: false\n',publish)
        self.assertIn("github.event_name == 'push' && github.ref == 'refs/heads/main'",publish)
        self.assertIn('github.ref_protected',publish)
        self.assertIn("vars.ENABLE_GHCR_PUBLISH == 'true'",publish)
        self.assertIn('run: python3 -I ci/github/release.py publish',publish)


if __name__=='__main__':unittest.main()
