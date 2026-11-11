#!/usr/bin/env python3
"""Deterministic Go generator v2; Python 3 stdlib only, pinned Git inputs.

The canonical contract is pinned by its contract_digest: the SHA-256 of the canonical JSON of
the sorted "module path -> SHA-256" map of the manifest modules.
"""
import argparse, hashlib, json, re, subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONTRACT_DIGEST='371a499f74012bb6b41f23642869dee4954e8394dcba7642edc701aedb3dd59e'
def name(v):
 v=''.join(s[:1].upper()+s[1:] for s in re.split('[^a-zA-Z0-9]',v) if s)
 return ('N'+v if v[:1].isdigit() else v) or 'Value'
def main(source,check,revision='HEAD'):
 outputs={}; records=[]; ops={}; methods=[]; types=0
 def read(p):return subprocess.check_output(['git','-C',str(source),'show',revision+':'+p])
 manifest=json.loads(read('contracts/manifest.json'))
 digest=hashlib.sha256(json.dumps(dict(sorted((p,hashlib.sha256(read(p)).hexdigest()) for p in manifest['contracts'])),separators=(',',':'),ensure_ascii=False).encode('utf-8')).hexdigest()
 if digest!=CONTRACT_DIGEST:raise SystemExit('Canonical contract_digest '+digest+' differs from the pinned '+CONTRACT_DIGEST)
 for p in manifest['contracts']:
  raw=read(p); doc=json.loads(raw); mod=p.removeprefix('contracts/').replace('/','_').replace('-','_').replace('.openapi.json',''); pkg=mod.replace('_',''); schemas=doc['components']['schemas']; created={}; defs=[]
  outputs['contracts/'+mod+'.json']=raw.decode('utf-8'); records.append(dict(contract=p,module=mod,sha256=hashlib.sha256(raw).hexdigest()))
  def resolve(s,seen=()):
   if '$ref'in s:
    k=s['$ref'].rsplit('/',1)[-1]
    if k in seen:return {}
    s={**resolve(schemas[k],seen+(k,)),**{k:v for k,v in s.items() if k!='$ref'}}
   out={k:v for k,v in s.items() if k not in ['allOf','anyOf','oneOf']}; props=dict(out.get('properties',{})); req=set(out.get('required',[]))
   for k in ['allOf','anyOf','oneOf']:
    for sub in s.get(k,[]):
     sub=resolve(sub,seen)
     for field,child in sub.get('properties',{}).items():
      if field not in props:props[field]=child
      elif props[field]!=child:props[field]={'anyOf':[props[field],child]}
     if k=='allOf':req.update(sub.get('required',[]))
   if props:out['type']='object';out['properties']=props;out['required']=sorted(req)
   return out
  def common_properties(s,seen=()):
   own=set(s.get('properties',{}))
   if '$ref' in s:
    key=s['$ref'].rsplit('/',1)[-1]
    if key not in seen:own.update(common_properties(schemas[key],seen+(key,)))
   for child in s.get('allOf',[]):own.update(common_properties(child,seen))
   for kind in ['anyOf','oneOf']:
    if s.get(kind):own.update(set.intersection(*(common_properties(child,seen) for child in s[kind])))
   return own
  def typ(s,n,top=False):
   if not top and '$ref'in s and len(s)==1:return name(s['$ref'].rsplit('/',1)[-1])
   def alternatives(x,seen=()):
    if '$ref' in x:
     key=x['$ref'].rsplit('/',1)[-1]
     if key in seen:return [x]
     return alternatives(schemas[key],seen+(key,))
    if not any(k in x for k in ['properties','allOf']):
     for union in ['anyOf','oneOf']:
      if union in x:return [z for c in x[union] for z in alternatives(c,seen)]
    return [x]
   choices=alternatives(s); kinds=[resolve(x).get('type') for x in choices]; active=[(x,k) for x,k in zip(choices,kinds) if k!='null']
   if len(active)>1 and any(k=='object' or 'properties' in resolve(x) for x,k in active) and any(k!='object' and 'properties' not in resolve(x) for x,k in active):
    ts=[typ(x,n+'Alternative'+str(i)) for i,(x,k) in enumerate(active)]
    def union(ts):return ts[0] if len(ts)==1 else 'value.Union['+ts[0]+','+union(ts[1:])+']'
    return union(ts)
   shape=resolve(s); k=shape.get('type')
   if k is None and ('enum' in shape or 'const' in shape):
    ev=shape.get('enum',[shape.get('const')]); kinds=set('boolean' if isinstance(v,bool) else 'string' if isinstance(v,str) else 'integer' if isinstance(v,int) else 'number' if isinstance(v,float) else 'null' for v in ev)
    kinds.discard('null')
    if kinds=={'string'}:return 'string'
    if kinds=={'integer'}:return 'int64'
    if kinds=={'boolean'}:return 'bool'
    if kinds:return 'value.Scalar'
   if k=='object' or 'properties'in shape:
    if n in created:return n
    created[n]=True; fields=[]; keys=[]; strict=common_properties(s)
    for key,child in shape.get('properties',{}).items():
     fn=name(key)
     if fn in ['Extras','MarshalJSON','UnmarshalJSON']:fn+='Field'
     if fn in keys:fn+='Field'+str(len(keys))
     keys.append(fn); fields.append('\t'+fn+' value.Field['+typ(child,n+fn)+'] `json:'+json.dumps(key+',omitzero')+(' wm:"alternative"' if key not in strict else '')+'`')
    fields.append('\tExtras map[string]json.RawMessage `json:"-"`')
    defs.append('type '+n+' struct {\n'+'\n'.join(fields)+'\n}\nfunc(m '+n+') MarshalJSON()([]byte,error){ type plain '+n+'; return value.MarshalObject(plain(m),m.Extras) }\nfunc(m *'+n+') UnmarshalJSON(b []byte)error{ type plain '+n+'; return value.UnmarshalObject(b,(*plain)(m),&m.Extras) }')
    return n
   if k=='array':return '[]'+typ(shape.get('items',{}),n+'Item')
   if s.get('x-sdk-scalar')=='decimal-input' or shape.get('x-sdk-scalar')=='decimal-input':return 'value.Scalar'
   if isinstance(k,list):
    non=[x for x in k if x!='null']
    if len(non)==1:return typ({**shape,'type':non[0]},n)
    return 'value.Scalar'
   if k is None:
    variants=s.get('oneOf',s.get('anyOf',[])); kinds=[resolve(x).get('type') for x in variants]; non=[x for x in kinds if x!='null'];
    if non and all(x=='string' for x in non):return 'string'
    if non and all(isinstance(x,str) and x in ['string','number','integer','boolean'] for x in non):return 'value.Scalar'
    ts=[]
    for sub in variants:
     if resolve(sub).get('type')=='null':continue
     t=typ(sub,n+'Variant'+str(len(ts)))
     if t not in ts:ts.append(t)
    if len(ts)==1:return ts[0]
    return 'value.Scalar' if ts and all(t in ['string','bool','int64','json.Number','value.Scalar'] for t in ts) else 'json.RawMessage'
   return {'string':'string','integer':'int64','number':'json.Number','boolean':'bool','null':'json.RawMessage'}.get(k,'json.RawMessage')
  for n,s in schemas.items():
   gn=name(n); t=typ(s,gn,True)
   if gn not in created:created[gn]=True;defs.append('type '+gn+' = '+t)
  for path,item in doc['paths'].items():
   for verb,spec in item.items():
    if not isinstance(spec,dict) or 'operationId'not in spec:continue
    oid=spec['operationId']; on=name(oid)
    service=('Nfe' if mod=='api1_fiscal' else 'NfeFiles' if mod=='api1_files' else 'Reseller' if mod=='api1_b2b' else 'EmitterFiles' if mod=='api2_emitter_files' else 'ReportFiles' if mod=='api2_reports' else 'Monitor' if mod.startswith('api2_monitor') else 'Emitter')
    method=re.sub('^(nfe|fiscal|b2bV1|file|monitor|emitter)','',oid); params=spec.get('parameters',[])+item.get('parameters',[]); q={'type':'object','properties':{x['name']:x['schema'] for x in params if x['in']=='query'},'additionalProperties':False,'required':[x['name']for x in params if x['in']=='query' and x.get('required')]}; body=spec.get('requestBody',{}).get('content',{}).get('application/json',{}).get('schema'); binary=any(x.get('schema',{}).get('format')=='binary' for k,v in spec['responses'].items() if k.startswith('2') for x in v.get('content',{}).values()); rs=[v['content']['application/json']['schema'] for k,v in spec['responses'].items() if k.startswith('2') and 'application/json'in v.get('content',{})]
    # Response projections allow future fields/enums; runtime schema remains authoritative.
    rt=typ({'anyOf':rs},on+'Response',True) if rs and not binary else 'json.RawMessage'; qt=typ(q,on+'Query',True)
    if on+'Query'not in created:defs.append('type '+on+'Query = '+qt);created[on+'Query']=True
    bt=typ(body,on+'Body') if body is not None else None
    if body is not None and on+'Body'not in created:defs.append('type '+on+'Body = '+bt);created[on+'Body']=True
    if not binary and on+'Response'not in created:defs.append('type '+on+'Response = '+rt);created[on+'Response']=True
    ops[oid]=dict(module=mod,path=path,verb=verb.upper(),service=service,query=q,spec=spec,parameters=params)
    fields=[];pathFields=[]
    for x in params:
     if x['in']=='path':
      t=typ(x['schema'],on+name(x['name'])+'Parameter');fields.append(name(x['name'])+' '+('m.'+t if re.fullmatch('[A-Z][a-zA-Z0-9]*',t) else t));pathFields.append(json.dumps(x['name'])+':p.'+name(x['name']))
    fields.append('Query m.'+on+'Query')
    if bt:fields.append('Body m.'+on+'Body')
    if service.endswith('Files'):fields.append('Auth FileAuth')
    result='FileResponse' if binary else '*Result[m.'+on+'Response]'
    call='c.base.execute(ctx,'+json.dumps(oid)+',map[string]any{'+','.join(pathFields)+'},p.Query,'+('p.Body'if bt else 'nil')+','+('p.Auth'if service.endswith('Files')else 'FileAuth{}')+')'
    code='// '+method+' implements '+oid+'.\ntype '+on+'Params struct { '+ '; '.join(fields)+' }\nfunc(c *'+service+'Client) '+method+'(ctx context.Context,p '+on+'Params)('+result+',error){ r,e:='+call+'; if e!=nil {return '+('FileResponse{}'if binary else 'nil')+',e};'
    code+=('return r.file,nil'if binary else 'var v m.'+on+'Response; if e:=json.Unmarshal(r.raw,&v);e!=nil{return nil,r.decodeError()}; return &Result[m.'+on+'Response]{Value:v,Status:r.status},nil')+' }\n'
    methods.append((mod,pkg,oid,code))
  header='// Code generated by scripts/generate.py v2 from '+p+'; DO NOT EDIT.\npackage '+pkg+'\nimport("encoding/json";"github.com/webmaniabr/webmania-go/value")\nvar _ = json.RawMessage{}\nvar _ = value.Some(0)\n'
  outputs['model/'+pkg+'/models.go']=header+'\n\n'.join(defs)+'\n'; types+=len(defs)
 for mod in sorted(set(x[0]for x in methods)):
  pkg=mod.replace('_',''); codes=[x[3]for x in methods if x[0]==mod]; outputs['methods_'+mod+'.go']='// Code generated by scripts/generate.py v2; DO NOT EDIT.\npackage webmania\nimport("context";"encoding/json"; m "github.com/webmaniabr/webmania-go/model/'+pkg+'";"github.com/webmaniabr/webmania-go/value")\nvar _=json.RawMessage{}\nvar _=value.Some(0)\n'+'\n'.join(codes)
 outputs['contracts/operations.json']=json.dumps(ops,ensure_ascii=False,indent=2)+'\n'; outputs['contracts.lock.json']=json.dumps(dict(generator='scripts/generate.py v2',contract_digest=digest,operation_count=len(ops),model_count=types,inputs=records),indent=2)+'\n'
 for p,s in outputs.items():
  f=ROOT/p
  if p.endswith('.go'):
   proc=subprocess.run(['gofmt'],input=s.encode(),stdout=subprocess.PIPE,stderr=subprocess.PIPE)
   if proc.returncode:raise ValueError(p+' '+proc.stderr.decode())
   s=proc.stdout.decode()
  if check:
   if not f.exists()or f.read_bytes()!=s.encode('utf-8'):raise SystemExit('Generated differs: '+p)
  else:f.parent.mkdir(parents=True,exist_ok=True);f.write_bytes(s.encode('utf-8'))
 print(json.dumps(dict(operations=len(ops),models=types,check=check)))
if __name__=='__main__':
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--source',type=Path,required=True,help='webmania-openapi Git checkout');p.add_argument('--revision',default='HEAD',help='Git revision of the checkout to read (default HEAD)');p.add_argument('--check',action='store_true');a=p.parse_args();main(a.source,a.check,a.revision)
