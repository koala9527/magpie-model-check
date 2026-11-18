import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { resolveGateway, watchConfiguration, provisionRouteKey } from './runtime.mjs';
import * as runtimeModule from './runtime.mjs';

async function temporary(t,prefix,base=os.tmpdir()){
  const parent=await fs.realpath(base);
  const dir=await fs.mkdtemp(path.join(parent,prefix));
  t.after(async()=>{
    const resolved=await fs.realpath(dir),relative=path.relative(parent,resolved);
    assert.ok(relative&&!relative.startsWith('..')&&!path.isAbsolute(relative),'FIXTURE_CLEANUP_OUTSIDE_TEMP');
    await fs.rm(resolved,{recursive:true,force:true});
  });
  return dir;
}

test('OS temporary directory aliases resolve before testing private route provisioning',async t=>{
  const cleanups=[],owned={after:cleanup=>cleanups.push(cleanup)};
  t.after(async()=>{for(const cleanup of cleanups.reverse())await cleanup();});
  const outer=await temporary(owned,'model-temp-alias-');
  const physical=path.join(outer,'physical'),alias=path.join(outer,'alias');
  await fs.mkdir(physical);await fs.symlink(physical,alias,process.platform==='win32'?'junction':'dir');
  const created=await temporary(owned,'model-proof-',alias);
  assert.ok(created.startsWith(physical+path.sep),'OS_TEMP_ALIAS_WAS_NOT_CANONICALIZED');
});

test('gateway configuration is loopback-only and credentials come from the environment',()=>{
  assert.equal(resolveGateway({gatewayURL:'http://127.0.0.1:4567',gatewayKeyEnv:'TEST_GATEWAY_KEY'},{TEST_GATEWAY_KEY:'private'}).gateway_key,'private');
  assert.equal(resolveGateway({}, {MAGPIE_ADDR:'0.0.0.0:1234'}).gateway_url,'http://127.0.0.1:1234');
  for(const url of ['https://outside.example','http://outside.example','http://key:secret@127.0.0.1:1234','http://127.0.0.1:1234/?secret=key'])assert.throws(()=>resolveGateway({gatewayURL:url},{}));
  assert.throws(()=>resolveGateway({gatewayKeyEnv:'MISSING'},{ }));
});
test('a real configuration change stops the old service while invalid partial writes do not',async t=>{
  const dir=await temporary(t,'model-watch-');
  const file=path.join(dir,'plugins.json');await fs.writeFile(file,JSON.stringify({plugins:[{spec:'local-model-check'}]}));
  let stopped=0;const stop=await watchConfiguration(file,()=>{stopped++;},20);t.after(stop);
  await fs.writeFile(file,'{');await new Promise(r=>setTimeout(r,60));assert.equal(stopped,0);
  await fs.writeFile(file,JSON.stringify({plugins:[{spec:'local-model-check',off:true}]}));await new Promise(r=>setTimeout(r,100));assert.equal(stopped,1);
});
test('private route provisioning touches only the runtime module outside the page directory',async t=>{
  const dir=await temporary(t,'model-proof-');
  await fs.mkdir(path.join(dir,'plugin'));await fs.mkdir(path.join(dir,'plugin/page'));await fs.writeFile(path.join(dir,'plugin/route.middleware.mjs'),'export function onRequest(body){return body;}\n');
  const before=await fs.readFile(path.join(dir,'plugin/route.middleware.mjs'),'utf8');await provisionRouteKey(dir,'a'.repeat(64));
  const source=await fs.readFile(path.join(dir,'plugin/.runtime/route-secret.mjs'),'utf8');assert.ok(/a{64}/.test(source),'PRIVATE_ROUTE_MODULE_MISSING');
  assert.equal(await fs.readFile(path.join(dir,'plugin/route.middleware.mjs'),'utf8'),before);assert.deepEqual(await fs.readdir(path.join(dir,'plugin/page')),[]);
});

test('protocol guard derives conservatively from enabled middleware order and hook metadata',async t=>{
  const own={spec:'ours'},other={spec:'other'},disabled={spec:'disabled',off:true};
  const observer={name:'ours',magpie:{middleware:'./observer.mjs'}};
  const middleware={name:'other',magpie:{middleware:'./rewrite.mjs'}};
  const hooks={name:'other',magpie:{hooks:'./async-hooks.mjs'}};
  const cases=[
    ['ours alone',[own],0,[observer],true],
    ['middleware before ours',[other,own],1,[middleware,observer],true],
    ['middleware after ours',[own,other],0,[observer,middleware],false],
    ['gateway alias after ours',[own,other],0,[observer,{name:'other',magpie:{gateway:'./rewrite.mjs'}}],false],
    ['hooks before ours',[other,own],1,[hooks,observer],false],
    ['hooks after ours',[own,other],0,[observer,hooks],false],
    ['off middleware and hooks',[own,disabled],0,[observer,{name:'disabled',magpie:{middleware:'./rewrite.mjs',hooks:'./hooks.mjs'}}],true],
    ['off unknown entry',[disabled,own],1,[undefined,observer],true],
    ['off own entry',[{...own,off:true}],0,[observer],false],
    ['unknown or unreadable metadata',[other,own],1,[undefined,observer],false],
    ['invalid package metadata',[other,own],1,[[],observer],false],
    ['invalid middleware metadata',[own,other],0,[observer,{name:'other',magpie:{middleware:[]}}],false],
    ['invalid hook metadata',[own,other],0,[observer,{name:'other',magpie:{hooks:null}}],false],
    ['invalid on state',[own,{...other,off:'false'}],0,[observer,middleware],false],
    ['own observer missing',[own],0,[{name:'ours'}],false],
    ['own entry missing',[other],-1,[middleware],false],
    ['known tool-only plugin after ours',[own,other],0,[observer,{name:'other'}],true],
  ];
  for(const [name,plugins,ownIndex,metadata,expected] of cases)await t.test(name,()=>{
    assert.equal(typeof runtimeModule.deriveProtocolGuard,'function','PROTOCOL_GUARD_HELPER_MISSING');
    const before=JSON.stringify({plugins,metadata});
    assert.equal(runtimeModule.deriveProtocolGuard(plugins,ownIndex,metadata),expected);
    assert.equal(JSON.stringify({plugins,metadata}),before,'PROTOCOL_GUARD_CHANGED_CONFIGURATION');
  });
});
