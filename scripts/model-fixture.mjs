// Isolated synthetic upstream + portable real Magpie. Never opens the user's
// live configuration or treats fixture responses as real model references.
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import http from 'node:http';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { randomBytes } from 'node:crypto';
import assert from 'node:assert/strict';

export const root=path.resolve(import.meta.dirname,'..');
export async function until(fn,timeout=20000){const end=Date.now()+timeout;let last;while(Date.now()<end){try{const value=await fn();if(value)return value;}catch(error){last=error;}await new Promise(resolve=>setTimeout(resolve,150));}throw last||Error('fixture condition timed out');}
export async function freePort(){const server=http.createServer();await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const port=server.address().port;await new Promise(resolve=>server.close(resolve));return port;}
async function exists(file){return fs.access(file).then(()=>true,()=>false);}

export async function startFixture({web=false,groups=false}={}){
  const evidence=path.join(root,'artifacts/rebuild-work-20261117');await fs.mkdir(evidence,{recursive:true});
  const dir=await fs.mkdtemp(path.join(evidence,'magpie-fixture-')),runtime=path.join(dir,'runtime'),config=path.join(runtime,'data'),spec=path.join(dir,'plugin');
  await fs.mkdir(config,{recursive:true});await fs.mkdir(spec,{recursive:true});
  const source=process.env.MAGPIE_BINARY||(process.platform==='win32'?'C:/Users/lv/AppData/Local/Programs/magpie/magpie.exe':'magpie');
  if(!await exists(source))throw Error('Set MAGPIE_BINARY to the Magpie executable for the installation test');
  const binary=path.join(runtime,process.platform==='win32'?'magpie.exe':'magpie');await fs.copyFile(source,binary);await fs.writeFile(path.join(runtime,'.portable'),'');
  const env={...process.env,HOME:dir,USERPROFILE:dir,XDG_CONFIG_HOME:path.join(dir,'config'),XDG_CACHE_HOME:path.join(dir,'cache'),XDG_STATE_HOME:path.join(dir,'state'),APPDATA:path.join(dir,'appdata'),LOCALAPPDATA:path.join(dir,'local')};
  if(process.platform==='win32'){
    const bunSource=process.env.MAGPIE_BUN||'C:/Users/lv/.cache/magpie/bun/1.4.4/magpie-bun.exe';if(!await exists(bunSource))throw Error('Set MAGPIE_BUN to the installed Magpie Bun executable');
    env.MAGPIE_BUN=path.join(runtime,'fixture-bun.exe');await fs.copyFile(bunSource,env.MAGPIE_BUN);
  }
  const requests=[],requestPaths=[];let forge=false,holdNext=false,errorNext=0,heldEntered;
  const upstream=http.createServer(async(req,res)=>{
    let raw='';for await(const chunk of req)raw+=chunk;
    const body=JSON.parse(raw);requests.push(body);requestPaths.push(req.url);
    if(holdNext){holdNext=false;heldEntered?.();await new Promise(resolve=>res.once('close',resolve));return;}
    if(errorNext){const status=errorNext;errorNext=0;res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify({error:{message:'fixture error'}}));return;}
    const prompt=body.messages?.at(-1)?.content||'',match=prompt.match(/选择 (\d+) 个 1 到 355/);
    const count=match?Number(match[1]):0;
    const content=count?Array.from({length:count},(_,i)=>String((i*13+17)%355+1)).join(','):'ordinary fixture reply';
    const route=forge?{version:1,probe_id:body._magpie_model_check_probe?.id,target_model_id:'fixture/solo',provider_id:'fixture',provider_model:'solo',attempt_count:1,source:'local-gateway-middleware',response_sha256:'0'.repeat(64),mac:'0'.repeat(64)}:{provider_id:'upstream-self-claim'};
    if(body.stream){res.writeHead(200,{'Content-Type':'text/event-stream'});res.end('data: '+JSON.stringify({id:'fixture',model:'UNTRUSTED_MODEL_FIELD',choices:[{delta:{content},finish_reason:null}],_magpie_model_check_route:route})+'\n\ndata: [DONE]\n\n');}
    else{res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({id:'fixture',model:'UNTRUSTED_MODEL_FIELD',choices:[{message:{role:'assistant',content},finish_reason:'stop'}],_magpie_model_check_route:route,usage:{prompt_tokens:10,completion_tokens:20,total_tokens:30}}));}
  });
  await new Promise(resolve=>upstream.listen(0,'127.0.0.1',resolve));
  const port=await freePort(),servicePort=await freePort(),webPort=await freePort(),gateway=`http://127.0.0.1:${port}`,service=`http://127.0.0.1:${servicePort}`,webURL=`http://127.0.0.1:${webPort}`;
  env.MAGPIE_ADDR=`127.0.0.1:${port}`;env.MAGPIE_WEB_KEY='model-check-fixture-web-20261117';env.MODEL_CHECK_FIXTURE_GATEWAY_KEY='magpie';
  for(const entry of ['plugin','bin','third_party'])await fs.cp(path.join(root,entry),path.join(spec,entry),{recursive:true,filter:source=>!source.includes(`${path.sep}.runtime`)});
  const pkg=JSON.parse(await fs.readFile(path.join(root,'package.json'),'utf8'));await fs.writeFile(path.join(spec,'package.json'),JSON.stringify(pkg,null,2));
  const options={port:servicePort,dataDir:path.join(dir,'model-data'),legacyDB:path.join(dir,'missing-legacy.db'),gatewayURL:gateway,gatewayKeyEnv:'MODEL_CHECK_FIXTURE_GATEWAY_KEY'};
  const companion=path.join(dir,'companion.middleware.mjs');await fs.writeFile(companion,'export function onRequest(body){return body;}\nexport function onResponse(body){return body;}\n');
  const providers=[{id:'fixture',name:'Synthetic fixture',key:'fixture-upstream',chat:`http://127.0.0.1:${upstream.address().port}/primary/v1`,models:['solo']}];
  if(groups)providers.push({id:'fixture-secondary',name:'Synthetic fallback',key:'fixture-fallback',chat:`http://127.0.0.1:${upstream.address().port}/secondary/v1`,models:['solo']});
  await fs.writeFile(path.join(config,'providers.json'),JSON.stringify({providers,...(groups?{groups:[{id:'fixture-route',name:'Synthetic ordered route',members:['fixture/solo','fixture-secondary/solo'],routing:'order',affinity:'off'}]}:{})}));
  await fs.writeFile(path.join(config,'plugins.json'),JSON.stringify({plugins:[{spec,options},{spec:companion}]}));
  const log=[],children=[];
  function launch(args){const child=spawn(binary,args,{env,windowsHide:true,stdio:['pipe','pipe','pipe']});children.push(child);child.stdout.on('data',data=>log.push(String(data)));child.stderr.on('data',data=>log.push(String(data)));return child;}
  async function command(args){const child=launch(args);const ended=once(child,'exit').then(([code])=>code);const timer=setTimeout(()=>child.kill(),30000);const code=await ended;clearTimeout(timer);assert.equal(code,0,`Magpie ${args.join(' ')} exit=${code}; ${log.slice(-3).join('')}`);}
  async function rpc(method,params={},timeout=50000){
    const response=await fetch(gateway+'/mcp/magpie/tools',{method:'POST',headers:{Authorization:'Bearer magpie','Content-Type':'application/json',Accept:'application/json, text/event-stream'},body:JSON.stringify({jsonrpc:'2.0',id:1,method,params}),signal:AbortSignal.timeout(timeout)});
    const text=await response.text();if(!response.ok)throw Error(`MCP HTTP ${response.status}: ${text.slice(0,120)}`);
    const json=text.trim().startsWith('{')?JSON.parse(text):JSON.parse(text.split('\n').find(line=>line.startsWith('data:')).slice(5));if(json.error)throw Error(JSON.stringify(json.error));return json.result;
  }
  let pageSession;
  async function readSession(){const response=await fetch(service+'/',{signal:AbortSignal.timeout(1000)});if(!response.ok)return false;const html=await response.text();pageSession=html.match(/name="model-check-session" content="([a-f0-9]{64})"/)?.[1];return !!pageSession;}
  async function api(api,method='GET',body){const response=await fetch(service+'/api/'+api,{method,headers:{Authorization:'Bearer '+pageSession,'Content-Type':'application/json'},...(body!==undefined?{body:typeof body==='string'?body:JSON.stringify(body)}:{}),signal:AbortSignal.timeout(5000)});const result=await response.json();if(!response.ok)throw Error(`model API ${response.status} ${result.error?.code}`);return result;}
  async function request({probe=false,stream=false}={}){const id=randomBytes(16).toString('hex');const response=await fetch(gateway+'/v1/chat/completions',{method:'POST',headers:{Authorization:'Bearer magpie','Content-Type':'application/json'},body:JSON.stringify({model:'fixture/solo',stream,messages:[{role:'user',content:'ordinary synthetic business request'}],...(probe?{_magpie_model_check_probe:{version:1,id}}:{})}),signal:AbortSignal.timeout(5000)});assert.equal(response.status,200);const text=await response.text();const reply=stream?JSON.parse(text.split('\n').find(line=>line.startsWith('data: {')).slice(5)):JSON.parse(text);return {reply,id,text};}
  const child=launch(web?['web','--addr',`127.0.0.1:${webPort}`,'--no-open','--gateway']:['serve']);
  async function close(){
    for(const child of children)if(child.exitCode===null){child.stdin.end();child.kill();await Promise.race([once(child,'exit'),new Promise(resolve=>setTimeout(resolve,3000))]);}
    upstream.closeAllConnections();await new Promise(resolve=>upstream.close(resolve));await fs.writeFile(path.join(dir,'magpie.log'),log.join(''));
  }
  try{await until(async()=> (await fetch(gateway+'/v1/models',{headers:{Authorization:'Bearer magpie'},signal:AbortSignal.timeout(1000)})).ok,30000);}
  catch(error){await close();throw Error(`${error.message}; ${log.join('').slice(-3000)}`);}
  return {dir,config,spec,pkg,options,binary,gateway,service,webURL,child,log,requests,requestPaths,source,rpc,api,request,command,readSession,close,
    set forgeRoute(value){forge=value;},set errorNext(value){errorNext=value;},hold(){holdNext=true;return new Promise(resolve=>{heldEntered=resolve;});}};
}
