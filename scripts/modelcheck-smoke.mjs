import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
import { createHmac, createHash } from 'node:crypto';
import { startFixture,until,root } from './model-fixture.mjs';
import {requestBodySHA256} from '../plugin/route-crypto.mjs';

const fixture=await startFixture({web:process.argv.includes('--web')}),summary={fixture:fixture.dir,steps:[],actual_model_reference_samples:0,independent_calibration:'NOT_MEASURED'};
try{
  const listed=await until(async()=>{const result=await fixture.rpc('tools/list');return result.tools?.some(tool=>tool.name.endsWith('_check'))?result:null;},30000);
  const tool=listed.tools.find(tool=>tool.name.endsWith('_check'));summary.steps.push('REAL_MAGPIE_FACTORY_AND_MCP_REGISTERED');
  await until(fixture.readSession);summary.steps.push('PAGE_SERVICE_READY');
  const ordinary=await fixture.request();assert.equal(ordinary.reply.choices[0].message.content,'ordinary fixture reply');assert.equal(ordinary.reply._magpie_model_check_route.provider_id,'upstream-self-claim');summary.steps.push('ORDINARY_REQUEST_UNCHANGED');
  const proofSource=await fs.readFile(path.join(fixture.spec,'plugin/.runtime/route-secret.mjs'),'utf8');const key=proofSource.match(/routeKey = "([a-f0-9]{64})"/)?.[1];assert.ok(key);
  for(const stream of [false,true]){
    const {reply,id}=await fixture.request({probe:true,stream});const route=reply._magpie_model_check_route;assert.equal(route.provider_id,'fixture');assert.equal(route.provider_model,'solo');assert.equal(route.probe_id,id);
    const choice=reply.choices[0],text=(choice.message||choice.delta).content,finish=choice.finish_reason||'',digest=createHash('sha256').update(text+'\n'+finish).digest('hex'),requestDigest=requestBodySHA256(fixture.requests.at(-1));const payload=['model-check-route-v3',id,'fixture/solo','fixture','solo',String(route.attempt_count),digest,requestDigest,route.envelope_sha256,route.stream_sha256].join('\n');
    assert.equal(route.version,3);assert.equal(route.request_sha256,requestDigest);
    assert.equal(route.mac,createHmac('sha256',key).update(payload).digest('hex'));assert.equal('_magpie_model_check_probe' in fixture.requests.at(-1),false);
  }
  summary.steps.push('AUTHENTICATED_NONSTREAM_AND_SSE_ROUTE');
  const target=await fixture.api('targets','POST',{provider_id:'fixture',model_id:'fixture/solo',claimed_model:'UNVALIDATED_FIXTURE_CLAIM',enabled:true,auto_check:false,interval_minutes:15,request_budget:1});
  const result=JSON.parse((await fixture.rpc('tools/call',{name:tool.name,arguments:{target_id:target.id}})).content[0].text);
  assert.equal(result.identity_status,'INCONCLUSIVE');assert.equal(result.calibrated_probability,null);
  const report=await fixture.api('batches/'+result.batch_id);assert.equal(report.batch.route_binding_status,'VERIFIED');assert.equal(report.samples.length,1);assert.equal(report.samples[0].valid,true);assert.ok(report.samples[0].raw_response);assert.equal(report.samples[0].route_evidence.authenticated,true);summary.steps.push('MANUAL_MCP_CHECK_PERSISTED_WITH_CONSERVATIVE_RESULT');
  const backup=await fixture.api('backup');assert.equal(JSON.stringify(backup).includes(key),false);assert.equal(JSON.stringify(backup).includes('fixture-upstream'),false);assert.equal(fixture.log.join('').includes(key),false);assert.equal(JSON.stringify(fixture.requests).includes(key),false);summary.steps.push('KEY_ABSENT_FROM_REPORT_BACKUP_LOG_AND_UPSTREAM');
  fixture.pkg.magpie.middleware='';await fs.writeFile(path.join(fixture.spec,'package.json'),JSON.stringify(fixture.pkg));fixture.forgeRoute=true;await new Promise(resolve=>setTimeout(resolve,1300));
  const forged=JSON.parse((await fixture.rpc('tools/call',{name:tool.name,arguments:{target_id:target.id}})).content[0].text);assert.equal(forged.identity_status,'ROUTE_UNVERIFIED');summary.steps.push('FORGED_UPSTREAM_WITH_MISSING_MIDDLEWARE_REJECTED');
  fixture.forgeRoute=false;fixture.pkg.magpie.middleware='./plugin/route.middleware.mjs';await fs.writeFile(path.join(fixture.spec,'package.json'),JSON.stringify(fixture.pkg));await new Promise(resolve=>setTimeout(resolve,1300));
  const entered=fixture.hold();const pending=fixture.rpc('tools/call',{name:tool.name,arguments:{target_id:target.id}});await entered;const started=Date.now();await fixture.command(['plugin','off',fixture.spec]);
  const cancelled=JSON.parse((await pending).content[0].text);assert.equal(cancelled.identity_status,'UNAVAILABLE');summary.disable_elapsed_ms=Date.now()-started;assert.ok(summary.disable_elapsed_ms<5000,`disable took ${summary.disable_elapsed_ms}ms`);
  await until(async()=>{try{await fetch(fixture.service+'/',{signal:AbortSignal.timeout(300)});return false;}catch{return true;}});summary.steps.push('DISABLE_CANCELLED_INFLIGHT_REQUEST_AND_CLOSED_SERVICE');
  await fixture.command(['plugin','on',fixture.spec]);await until(async()=>{const result=await fixture.rpc('tools/list');return result.tools?.some(t=>t.name===tool.name);},30000);await until(fixture.readSession);
  const restored=await fixture.api('state');assert.ok(restored.targets.some(t=>t.id===target.id));assert.ok(restored.batches.some(b=>b.id===result.batch_id));summary.steps.push('REENABLE_RETAINED_TARGETS_RAW_SAMPLES_HISTORY_AND_USAGE');
  const before=restored.usage.requests;await fixture.api('restore','POST',JSON.stringify(backup));const after=(await fixture.api('state')).usage.requests;assert.ok(after>=before);summary.steps.push('BACKUP_RESTORE_DID_NOT_REFUND_BUDGET');
  summary.status='PASS';
  if(process.argv.includes('--hold')){await fs.writeFile(path.join(root,'artifacts/rebuild-work-20261117/active-fixture.json'),JSON.stringify({fixture:fixture.dir,gateway:fixture.gateway,service:fixture.service,webURL:fixture.webURL,web:process.argv.includes('--web'),status:'READY'},null,2));console.log('UI_FIXTURE_READY '+fixture.webURL+' SERVICE '+fixture.service);await new Promise(resolve=>process.stdin.once('data',resolve));}
}catch(error){summary.status='FAIL';summary.error=String(error);process.exitCode=1;}
finally{await fixture.close();summary.requests=fixture.requests.length;await fs.writeFile(path.join(root,'artifacts/rebuild-work-20261117/magpie-smoke.json'),JSON.stringify(summary,null,2));console.log(JSON.stringify(summary,null,2));}
