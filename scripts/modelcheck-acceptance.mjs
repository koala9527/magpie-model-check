import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
import {startFixture,until,root} from './model-fixture.mjs';

const fixture=await startFixture({groups:true}),summary={status:'RUNNING',fixture:fixture.dir,steps:[],actual_model_reference_samples:0};
try{
  const listed=await until(async()=>{const result=await fixture.rpc('tools/list');return result.tools?.some(t=>t.name.endsWith('_check'))?result:null;},30000);
  await until(fixture.readSession);
  const cancel=listed.tools.find(t=>t.name.endsWith('_cancel'));
  const create=(name,extra={})=>fixture.api('targets','POST',{provider_id:'fixture',model_id:'fixture/solo',claimed_model:name,enabled:true,auto_check:false,interval_minutes:15,request_budget:1,...extra});
  const check=async target=>(await fixture.api(`targets/${target.id}/checks`,'POST',{})).batch_id;
  const report=id=>until(async()=>{const value=await fixture.api('batches/'+id);return value.batch.completed_at?value:null;});
  let settings=(await fixture.api('state')).settings;
  await fixture.api('settings','POST',{...settings,concurrency:1,timeout_seconds:1});
  const scheduled=await create('synthetic-scheduled',{auto_check:true});
  const scheduledBatch=await until(async()=>{const state=await fixture.api('state');return state.batches.find(b=>b.target_id===scheduled.id&&b.completed_at);});
  await fixture.api('targets/'+scheduled.id,'PUT',{...scheduled,auto_check:false});
  const scheduledReport=await fixture.api('batches/'+scheduledBatch.id);
  assert.equal(scheduledReport.result.identity_status,'INCONCLUSIVE');
  assert.equal(scheduledReport.samples[0].route_evidence.protocol_verified,false);
  summary.steps.push('OPT_IN_SCHEDULER_RAN_AND_RECORDED_ROUTE_WITHOUT_INVENTING_WIRE_PROOF');

  const held=await create('synthetic-cancel'),other=await create('synthetic-concurrency');
  const entered=fixture.hold(),heldID=await check(held);await entered;
  await assert.rejects(()=>check(other),/409/);
  const cancelled=await fixture.rpc('tools/call',{name:cancel.name,arguments:{batch_id:heldID}});
  assert.equal(JSON.parse(cancelled.content[0].text).cancelled,true);
  assert.equal((await report(heldID)).result.rejection_reason,'CANCELLED');
  summary.steps.push('GLOBAL_CONCURRENCY_ENFORCED_AND_EXPLICIT_MCP_CANCEL_STOPPED_IO');

  const timeoutTarget=await create('synthetic-timeout'),timeoutEntered=fixture.hold(),timeoutID=await check(timeoutTarget);await timeoutEntered;
  assert.equal((await report(timeoutID)).result.rejection_reason,'TIMEOUT');
  await assert.rejects(()=>check(timeoutTarget),/409/);
  summary.steps.push('CONFIGURED_TIMEOUT_PERSISTED_AND_RETRY_BACKOFF_ENFORCED');

  await fixture.api('settings','POST',{...(await fixture.api('state')).settings,timeout_seconds:15});

  const group=await create('synthetic-route-group',{model_id:'group/fixture-route',provider_id:'fixture-secondary'});
  fixture.errorNext=500;
  const groupReport=await report(await check(group));
  assert.equal(groupReport.batch.route_binding_status,'VERIFIED');
  assert.equal(groupReport.samples[0].route_evidence.provider_id,'fixture-secondary');
  assert.ok(groupReport.samples[0].route_evidence.attempt_count>=2);
  assert.ok(fixture.requestPaths.slice(-2).some(p=>p.includes('/primary/')));
  assert.ok(fixture.requestPaths.at(-1).includes('/secondary/'));
  summary.group_route=groupReport.samples[0].route_evidence;
  summary.steps.push('REAL_MAGPIE_GROUP_RETRY_BINDS_THE_FINAL_PROVIDER_NOT_THE_GROUP_LABEL');

  const failed=await create('synthetic-http-error');fixture.errorNext=400;
  const failedReport=await report(await check(failed));
  assert.equal(failedReport.result.identity_status,'UNAVAILABLE');
  assert.match(failedReport.result.rejection_reason,/^HTTP_\d{3}$/);
  summary.error_reason=failedReport.result.rejection_reason;
  summary.steps.push('GATEWAY_HTTP_ERROR_IS_UNAVAILABLE_NOT_A_MODEL_MISMATCH');

  const before=(await fixture.api('state')).usage,exhausted=await create('synthetic-budget');
  settings=(await fixture.api('state')).settings;
  await fixture.api('settings','POST',{...settings,daily_requests:before.requests});
  const count=fixture.requests.length;
  assert.equal((await report(await check(exhausted))).result.rejection_reason,'BUDGET_EXHAUSTED');
  assert.equal(fixture.requests.length,count);
  const after=(await fixture.api('state')).usage;
  assert.ok(after.requests>=before.requests&&after.tokens>=before.tokens);
  summary.steps.push('EXHAUSTED_DAILY_BUDGET_SENT_NO_UPSTREAM_REQUEST_AND_REFUNDED_NOTHING');
  assert.equal((await fixture.request()).reply.choices[0].message.content,'ordinary fixture reply');
  summary.steps.push('ORDINARY_REQUEST_STILL_PASSES_AFTER_ERRORS_TIMEOUT_AND_BUDGET_EXHAUSTION');
  summary.status='PASS';
}catch(error){summary.status='FAIL';summary.error=error.stack||String(error);process.exitCode=1;}
finally{await fixture.close();summary.requests=fixture.requests.length;await fs.writeFile(path.join(root,'artifacts/rebuild-work-20261117/magpie-acceptance.json'),JSON.stringify(summary,null,2));console.log(JSON.stringify(summary,null,2));}
