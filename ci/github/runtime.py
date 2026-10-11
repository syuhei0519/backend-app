"""Local disposable runtime check of the already checked OCI. No existing DB/volume.

Docker import converts transport only; image ID must equal the OCI config digest.
All cleanup is restricted to random names created by this invocation.
"""
import importlib.util
import json
import os
from pathlib import Path
import secrets
import subprocess
import time
import urllib.request
import urllib.error

spec=importlib.util.spec_from_file_location('release',Path(__file__).with_name('release.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
POSTGRES='postgres@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f'

def check():
    service,_,_,_=m.service_contract(os.environ['GITHUB_REPOSITORY'],os.environ.get('RELEASE_SERVICE'))
    layout=m.load('.security/public/layout.json');reader=m.load('.security/public/reader.json')
    m.require(m.sha(Path('.oci/image.tar').read_bytes())==layout['archiveSha256'])
    m.check_config(Path('.oci/layout'),reader['configDigest'],service)
    nonce=secrets.token_hex(12);prefix='order-b2-'+nonce;tag='docker.io/local/'+prefix+':checked'
    network=prefix+'-net';db=prefix+'-db';app=prefix+'-app';migration=prefix+'-migration'
    made=[];net=False;imported=False
    def docker(*args,input=None):
        # The local daemon only, no inherited remote context or TLS configuration.
        env={k:v for k,v in os.environ.items() if not k.startswith('DOCKER_') and k not in ('GH_TOKEN','GITHUB_TOKEN')}
        env['DOCKER_CONFIG']=str(Path('.security/private/docker').resolve())
        p=subprocess.run(['docker','--host','unix:///var/run/docker.sock',*args],input=input,capture_output=True,timeout=180,env=env)
        m.require(p.returncode==0);return p.stdout
    def request(method,path,body=None):
        data=None if body is None else json.dumps(body).encode()
        req=urllib.request.Request(url+path,data=data,method=method,headers={'Content-Type':'application/json'})
        try:
            with urllib.request.urlopen(req,timeout=2) as res:return res.status,res.read()
        except urllib.error.HTTPError as err:return err.code,err.read()
    try:
        m.run(['.security/oci-runtime-export','.oci/layout',layout['digest'],'.oci/runtime.tar',tag])
        docker('load','--input','.oci/runtime.tar');imported=True
        m.require(json.loads(docker('image','inspect',tag))[0]['Id']==reader['configDigest'])
        docker('network','create','--internal',network);net=True
        docker('create','--pull=never','--name',db,'--network',network,'--env','POSTGRES_PASSWORD='+nonce,POSTGRES);made.append(db)
        docker('start',db)
        for _ in range(60):
            try:docker('exec',db,'pg_isready','-U','postgres');break
            except ValueError:time.sleep(1)
        else:raise ValueError('fixture readiness')
        database,role=('orders','order_app') if service=='order' else ('accounts','account_app')
        sql=f"CREATE ROLE {role} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD '{nonce}';\nCREATE DATABASE {database} OWNER {role};\n"
        docker('exec','-i',db,'psql','-U','postgres','-v','ON_ERROR_STOP=1',input=sql.encode())
        dsn=f'postgres://{role}:{nonce}@{db}:5432/{database}?sslmode=disable'
        flags=['--pull=never','--network',network,'--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--user','10001:10001','--env','DATABASE_URL='+dsn,'--env','LOG_LEVEL=error']
        docker('create','--name',migration,*flags,tag,'migrate');made.append(migration)
        docker('start',migration);m.require(docker('wait',migration).strip()==b'0')
        docker('create','--name',app,*flags,'--publish','127.0.0.1::8080',tag);made.append(app)
        docker('start',app)
        port=docker('port',app,'8080/tcp').decode().strip();m.require(port.startswith('127.0.0.1:'))
        url='http://'+port
        for _ in range(60):
            try:
                if request('GET','/readyz')[0]==200:break
            except (OSError,urllib.error.URLError):pass
            time.sleep(1)
        else:raise ValueError('runtime readiness')
        m.require(request('GET','/livez')[0]==200)
        # Runtime process and migration command both used the same checked image.
        docker('restart',app)
        for _ in range(60):
            try:
                if request('GET','/readyz')[0]==200:break
            except (OSError,urllib.error.URLError):pass
            time.sleep(1)
        else:raise ValueError('runtime restart')
        result={'service':service,'digest':layout['digest'],'archiveSha256':layout['archiveSha256'],
                'configDigest':reader['configDigest'],'passed':True,'readOnly':True,'capDrop':'ALL','network':'internal'}
    finally:
        for name in reversed(made):docker('rm','--force','--volumes',name)
        if net:docker('network','rm',network)
        if imported:docker('image','rm',tag)
        Path('.oci/runtime.tar').unlink(missing_ok=True)
    (m.PUBLIC/'runtime.json').write_text(json.dumps(result,sort_keys=True)+'\n')

if __name__=='__main__':
    try:check()
    except Exception:
        print('Checked OCI runtime failed; no publication permitted.')
        raise SystemExit(1)
