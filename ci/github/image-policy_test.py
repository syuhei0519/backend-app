"""Real scan/SBOM validators with synthetic raw inputs; no network or registry."""
import datetime as dt
import json
import os
from pathlib import Path
import shutil
import unittest

import importlib.util
HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('fixture',HERE/'order-release_test.py')
f=importlib.util.module_from_spec(spec);spec.loader.exec_module(f)
policy=f.module('image_policy',HERE/'image-policy.py')

class ScanProducer(unittest.TestCase):
    def setUp(self):
        self.fx=f.Interop();self.fx.setUp();self.addCleanup(self.fx.doCleanups)
        Path('.security/private').mkdir();Path('.cache/trivy/db').mkdir(parents=True)
        for name in ('scan-gate','sbom-check'):
            shutil.copy2(HERE.parents[1]/'.security'/name,Path('.security')/name)
        now=dt.datetime.now(dt.timezone.utc).isoformat()
        self.fx.write('security/scan-policy.json',{'schemaVersion':1,'trivyVersion':'0.75.0','dbMaxAgeHours':24,'exceptions':[]})
        self.fx.write('.cache/trivy/db/metadata.json',{'Version':2,'UpdatedAt':now})
        self.raw={'SchemaVersion':2,'Trivy':{'Version':'0.75.0'},'ArtifactType':'container_image',
                  'ArtifactName':'.oci/layout','Metadata':{'ImageID':self.fx.order['configDigest']},
                  'Results':[{'Target':'.oci/layout (debian 13)','Packages':[{'Name':'nginx','Version':'1.29.0'}]}]}
        self.bom={'bomFormat':'CycloneDX','specVersion':'1.7','version':1,
            'metadata':{'timestamp':now,'tools':{'components':[{'type':'application','name':'trivy','version':'0.75.0'}]},
                'component':{'type':'container','name':'.oci/layout','bom-ref':'root','properties':[
                    {'name':'aquasecurity:trivy:ImageID','value':self.fx.order['configDigest']}]}},
            'components':[{'type':'library','name':'nginx','version':'1.29.0','bom-ref':'nginx'}],
            'dependencies':[{'ref':'root','dependsOn':['nginx']},{'ref':'nginx','dependsOn':[]}]}
        self.write_inputs()
    def write_inputs(self):
        self.fx.write('.security/private/image.json',self.raw)
        self.fx.write('.security/private/sbom.cdx.json',self.bom)
    def test_real_validators_mint_identity_and_replace_untrusted_summary(self):
        Path('.security/public/image-scan.json').write_bytes(b'untrusted passing summary')
        policy.check()
        m=policy.m;scan=m.load(m.PUBLIC/'image-scan.json')
        self.assertEqual(scan['imageIdentity']['configDigest'],self.raw['Metadata']['ImageID'])
        self.assertEqual(scan['imageIdentity']['service'],'order')
        self.assertEqual(scan['rawScanSha256'],m.sha(Path('.security/private/image.json').read_bytes()))
        m.check_image(scan['imageIdentity'])
    def test_raw_scan_rejection_removes_prior_success(self):
        original=json.dumps(self.raw)
        mutations=[{'Metadata':{'ImageID':'sha256:'+'f'*64}}, {'ArtifactType':'filesystem'},
                   {'Results':[]}, {'Trivy':{'Version':'0.1'}},
                   {'Results':[{'Target':'image','Secrets':[{'RuleID':'synthetic','Severity':'HIGH','Match':'private-sentinel'}]}]}]
        for change in mutations:
            self.raw=json.loads(original);self.write_inputs();policy.check()
            self.raw.update(change);self.write_inputs()
            with self.subTest(change=change),self.assertRaises(ValueError):policy.check()
            for name in ('image-proof.json','image-scan.json','sbom-proof.json'):
                self.assertFalse((Path('.security/public')/name).exists())
    def test_independent_sbom_inventory_and_config_rejection(self):
        for kind in ('config','inventory'):
            if kind=='config':self.bom['metadata']['component']['properties'][0]['value']='sha256:'+'f'*64
            else:
                self.bom['metadata']['component']['properties'][0]['value']=self.fx.order['configDigest']
                self.bom['components'][0]['version']='1.28.0'
            self.write_inputs()
            with self.subTest(kind=kind),self.assertRaises(ValueError):policy.check()
            self.assertFalse(Path('.security/public/image-proof.json').exists())

if __name__=='__main__':unittest.main()
