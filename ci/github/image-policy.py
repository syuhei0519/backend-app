"""Mint image evidence only by evaluating the same private raw scan and SBOM.

This trusted CI step never seals an existing public summary. Raw scanner data
stays private; only validated identities, safe findings and fingerprints leave it.
"""
import importlib.util
from pathlib import Path
import tempfile

spec=importlib.util.spec_from_file_location('release',Path(__file__).with_name('release.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)

def check():
    public=m.PUBLIC
    # A failed invocation must not leave an earlier success available to record().
    for name in ('image-scan.json','image-proof.json','sbom-proof.json'):
        (public/name).unlink(missing_ok=True)
    repo,commit,run_id,attempt=m.identity()
    service,_,_,_=m.service_contract(repo,m.os.environ.get('RELEASE_SERVICE'))
    layout=m.load(public/'layout.json');reader=m.load(public/'reader.json')
    m.check_oci_identity(layout,reader,repo,commit,service)
    m.check_source(repo,commit,run_id,attempt,m.load('.validation/source-scan.json'))
    raw=Path('.security/private/image.json');sbom=Path('.security/private/sbom.cdx.json')
    raw_bytes=raw.read_bytes();sbom_bytes=sbom.read_bytes()
    # Both validators independently check raw Metadata.ImageID against this OCI.
    with tempfile.TemporaryDirectory(dir='.security/private',prefix='policy-') as temp:
        summary=Path(temp)/'summary.json'
        m.run(['.security/scan-gate','-policy','security/scan-policy.json','-db','.cache/trivy/db/metadata.json',
               '-report',str(raw),'-out',str(summary),'-scanner-exit','0','-image-config',reader['configDigest']])
        scan=m.load(summary)
        proof=m.decode(m.run(['.security/sbom-check',str(sbom),str(raw),reader['configDigest']]))
    m.require(raw.read_bytes()==raw_bytes and sbom.read_bytes()==sbom_bytes)
    m.require(proof['configDigest']==reader['configDigest'] and proof['sha256']==m.sha(sbom_bytes))
    identity=m.image_identity(repo,commit,run_id,attempt,service,layout['digest'],reader['configDigest'],
                              layout['archiveSha256'],m.sha(Path('.validation/source-scan.json').read_bytes()))
    raw_hash=m.sha(raw_bytes)
    scan.update(imageIdentity=identity,rawScanSha256=raw_hash,sbomSha256=m.sha(sbom_bytes))
    proof['rawScanSha256']=raw_hash
    def encoded(value):return (m.json.dumps(value,sort_keys=True,indent=2)+'\n').encode()
    scan_bytes=encoded(scan);proof_bytes=encoded(proof)
    (public/'sbom.cdx.json').write_bytes(sbom_bytes)
    (public/'sbom-proof.json').write_bytes(proof_bytes)
    (public/'image-scan.json').write_bytes(scan_bytes)
    # Write the final proof last, only after all independent checks succeeded.
    (public/'image-proof.json').write_bytes(encoded({'schema':'core-platform/github-image-proof/v1',
        'identity':identity,'rawScanSha256':raw_hash,'imageScanSha256':m.sha(scan_bytes),
        'sbomProofSha256':m.sha(proof_bytes)}))

if __name__=='__main__':
    try:check()
    except Exception:
        print('Image scan identity or policy refused; no release authorized.')
        raise SystemExit(1)
