#!/usr/bin/env python3
"""Build a deterministic Go proxy artifact and qualify it in fresh ordinary consumers."""
import argparse, hashlib, json, os, shutil, subprocess, time, zipfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1];MODULE='github.com/webmaniabr/webmania-go';VERSION='v0.1.0-alpha.5';MARKER='.webmania-go-qualify'
def owned_output(path):
 # Later steps remove and recreate consumer/ and module-cache/ inside --output, so the directory must
 # belong to this script: absent, empty or marked by an earlier run, and apart from the repository.
 out=path.resolve()
 if out==ROOT or ROOT in out.parents or out in ROOT.parents:raise SystemExit('--output must be outside the repository and must not contain it')
 if out.exists() and (not out.is_dir() or (any(out.iterdir()) and not (out/MARKER).is_file())):raise SystemExit('--output must be absent, empty or a directory created by an earlier qualify.py run')
 out.mkdir(parents=True,exist_ok=True);(out/MARKER).write_text('');return out
def main():
 p=argparse.ArgumentParser();p.add_argument('--go',default='go');p.add_argument('--output',type=Path,required=True);p.add_argument('--reuse-proxy',type=Path);p.add_argument('--no-race',action='store_true');p.add_argument('--build-only',action='store_true');p.add_argument('--dependency-proxy');a=p.parse_args();out=owned_output(a.output);proxy=out/'proxy';target=proxy/MODULE/'@v';target.mkdir(parents=True,exist_ok=True)
 archive=target/(VERSION+'.zip')
 if a.reuse_proxy:
  origin=a.reuse_proxy.resolve()/MODULE/'@v'
  for suffix in [VERSION+'.zip',VERSION+'.mod',VERSION+'.info','list']:shutil.copyfile(origin/suffix,target/suffix)
 else:
  paths=subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard'],cwd=ROOT,text=True).splitlines()
  with zipfile.ZipFile(archive,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=9)as z:
   for f in sorted(paths):
    if not (ROOT/f).is_file():continue
    parts=Path(f).parts
    public_root={'CHANGELOG.md','LICENSE','README.md','contracts.lock.json','go.mod','go.sum','.gitattributes','.gitignore'}
    public_dirs={'contracts','docs','examples','model','tests','value'}
    if any(part == '.DS_Store' or part.startswith('._') for part in parts):continue
    if not (f in public_root or (len(parts)==1 and f.endswith('.go')) or (parts and parts[0] in public_dirs)):continue
    info=zipfile.ZipInfo(MODULE+'@'+VERSION+'/'+f,(1980,1,1,0,0,0));info.compress_type=zipfile.ZIP_DEFLATED;info.external_attr=0o100644<<16;z.writestr(info,(ROOT/f).read_bytes())
  (target/(VERSION+'.mod')).write_bytes((ROOT/'go.mod').read_bytes());(target/(VERSION+'.info')).write_text(json.dumps({'Version':VERSION,'Time':'2026-10-31T00:00:00Z'})+'\n');(target/'list').write_text(VERSION+'\n')
 if a.build_only:
  print(json.dumps({'module':MODULE,'version':VERSION,'archive':str(archive),'sha256':hashlib.sha256(archive.read_bytes()).hexdigest()}));return
 work=out/'consumer';shutil.rmtree(work,ignore_errors=True);shutil.rmtree(out/'module-cache',ignore_errors=True);work.mkdir(exist_ok=True);(work/'go.mod').write_text('module qualification.example/consumer\n\ngo 1.26.0\n\nrequire '+MODULE+' '+VERSION+'\n')
 for f in ['dispatch_test.go','corpus_test.go','regressions_test.go','review_fixes_test.go','alpha3_formats_test.go','report_quota_test.go','reports_transport_test.go']:shutil.copyfile(ROOT/f,work/f)
 shutil.copytree(ROOT/'tests',work/'tests',dirs_exist_ok=True)
 env=os.environ.copy();env.update(GOPROXY=proxy.as_uri()+','+(a.dependency_proxy or 'https://proxy.golang.org'),GONOSUMDB=MODULE,GONOPROXY='',GOPRIVATE='',GOWORK='off',GOMODCACHE=str(out/'module-cache'),GOPATH=str(out/'gopath'),GOTOOLCHAIN='local')
 logs=[]
 def run(cmd,cwd=work,fail=False):
  start=time.monotonic();r=subprocess.run(cmd,cwd=cwd,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,check=False);logs.append({'command':cmd,'exit_code':r.returncode,'elapsed_seconds':round(time.monotonic()-start,3),'output':r.stdout});print(r.stdout,flush=True)
  if (r.returncode==0)==fail:raise SystemExit('qualification command did not have expected result')
  if fail and 'cannot use' not in r.stdout:raise SystemExit('negative consumer did not demonstrate the expected type mismatch')
  return r.stdout
 runtime=run([a.go,'version']);download=json.loads(run([a.go,'mod','download','-json',MODULE+'@'+VERSION]));assert hashlib.sha256(Path(download['Zip']).read_bytes()).digest()==hashlib.sha256(archive.read_bytes()).digest(),'downloaded module ZIP differs from candidate'
 with zipfile.ZipFile(archive)as z:
  prefix=MODULE+'@'+VERSION+'/'
  for entry in z.namelist():
   relative=entry.removeprefix(prefix);assert hashlib.sha256((Path(download['Dir'])/relative).read_bytes()).digest()==hashlib.sha256(z.read(entry)).digest(),'installed module file differs: '+relative
 run([a.go,'mod','tidy']);run([a.go,'test']+([] if a.no_race else ['-race'])+['-count=1','-json','./...']);run([a.go,'test','-count=1','-json','./...'],Path(download['Dir']))
 negatives={
 'alternative-field-type':'package negative\nimport (f \"'+MODULE+'/model/api1fiscal\";\"'+MODULE+'/value\")\nvar invalid=f.NfeGetSefazStatusResponse{Message:value.Some(map[string]string{\"new\":\"shape\"})}\n',
 'integer-field-type':'package negative\nimport (m \"'+MODULE+'/model/api2monitorreads\";\"'+MODULE+'/value\")\nvar invalid=m.MonitorCompany{Status:value.Some(1.0)}\n',
 'typed-body':'package negative\nimport ("context"; sdk "'+MODULE+'")\nfunc invalid(c *sdk.MonitorClient){c.CreateCompany(context.Background(),sdk.MonitorCreateCompanyParams{Body:"wrong"})}\n',
 'common-field-type':'package negative\nimport (f "'+MODULE+'/model/api1fiscal";"'+MODULE+'/value")\nvar invalid=f.NfeCancelRequest{Motivo:value.Some(123)}\n',
 'credential-scope':'package negative\nimport sdk "'+MODULE+'"\nfunc invalid(){sdk.NewMonitorClient(sdk.IssuerCredentials{Token:"synthetic"},sdk.Config{})}\n',
 'wrapper-body-collision':'package negative\nimport ("context";sdk "'+MODULE+'";m "'+MODULE+'/model/api2monitormanagement")\nfunc invalid(c *sdk.MonitorClient){c.UpdateCompany(context.Background(),m.MonitorUpdateCompanyRequest{})}\n'}
 for name,source in negatives.items():
  case=work/'negative'/name;case.mkdir(parents=True,exist_ok=True);(case/'negative.go').write_text(source);run([a.go,'test','./negative/'+name],fail=True)
 (out/'qualification-log.json').write_text(json.dumps(logs,indent=2)+'\n');receipt={'module':MODULE,'version':VERSION,'artifact':str(archive),'sha256':hashlib.sha256(archive.read_bytes()).hexdigest(),'runtime':runtime.strip(),'race_detector':not a.no_race,'module_download':download,'positive_consumers':'actual HTTP/TLS + regression suite imported downloaded module','negative_consumer_count':len(negatives),'remote_publication':False,'live_webmania_calls':False};(out/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps(receipt,indent=2))
if __name__=='__main__':main()
