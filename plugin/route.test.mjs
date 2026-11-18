import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createHmac, createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';

const id = '1'.repeat(32), key = 'a'.repeat(64);
async function middleware(t) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'model-route-'));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  await fs.mkdir(path.join(dir, '.runtime'));
  for (const f of ['route.middleware.mjs', 'route-crypto.mjs'])
    await fs.copyFile(new URL(f, import.meta.url), path.join(dir, f));
  await fs.writeFile(path.join(dir, '.runtime/route-secret.mjs'), `export const routeKey = ${JSON.stringify(key)};\n`);
  return import(pathToFileURL(path.join(dir, 'route.middleware.mjs')));
}
test('ordinary requests and responses are unchanged and never rejected', async t => {
  const m = await middleware(t), body = { model: 'channel/solo', messages: [{ role: 'user', content: '日常业务' }] };
  const ctx = { state: {}, reject() { throw Error('business request rejected'); } };
  assert.deepEqual(m.onRequest(structuredClone(body), ctx), body);
  assert.deepEqual(m.onProvider(structuredClone(body), ctx), body);
  const response = { choices: [{ message: { content: '日常回答' } }] };
  assert.deepEqual(m.onResponse(structuredClone(response), ctx), response);
  assert.deepEqual(ctx.state, {});
});
test('controlled probe strips only its marker and authenticates the last real route', async t => {
  const m = await middleware(t), ctx = { state: {} };
  const body = { model: 'channel/solo', stream: false, _magpie_model_check_probe: { version: 1, id }, messages: [{ role: 'user', content: '17,29,41' }] };
  const sent = m.onRequest(body, ctx);
  assert.equal(sent.model, 'channel/solo');
  assert.equal('_magpie_model_check_probe' in sent, false);
  ctx.provider = { id: 'first', model: 'other' }; m.onProvider(sent, ctx);
  ctx.provider = { id: 'channel', model: 'solo' }; m.onProvider(sent, ctx);
  const reply = m.onResponse({ choices: [{ message: { content: '17,29,41' }, finish_reason: 'stop' }], _magpie_model_check_route: { provider_id: 'forged' } }, ctx);
  const route = reply._magpie_model_check_route;
  assert.equal(route.provider_id, 'channel'); assert.equal(route.provider_model, 'solo'); assert.equal(route.attempt_count, 2);
  const digest = createHash('sha256').update('17,29,41\nstop').digest('hex');
  const requestDigest=createHash('sha256').update('{"messages":[{"content":"17,29,41","role":"user"}],"stream":false}').digest('hex');
  assert.equal(route.version,3);
  assert.equal(route.request_sha256,requestDigest);
  const payload = ['model-check-route-v3', id, 'channel/solo', 'channel', 'solo', '2', digest, requestDigest,route.envelope_sha256,''].join('\n');
  assert.equal(route.mac, createHmac('sha256', key).update(payload).digest('hex'));
  assert.equal(route.response_sha256, digest);
  assert.equal(JSON.stringify(reply).includes(key), false);
});
test('the final provider packet, not the original prompt, is bound to the MAC',async t=>{
  const m=await middleware(t),ctx={state:{}};
  const sent=m.onRequest({model:'channel/solo',messages:[{role:'user',content:'original'}],_magpie_model_check_probe:{version:1,id}},ctx);
  ctx.provider={id:'channel',model:'solo'};
  m.onProvider({...sent,messages:[{role:'user',content:'modified by another extension'}]},ctx);
  const route=m.onResponse({choices:[{message:{content:'17'},finish_reason:'stop'}]},ctx)._magpie_model_check_route;
  const expected=createHash('sha256').update('{"messages":[{"content":"modified by another extension","role":"user"}]}').digest('hex');
  assert.equal(route.request_sha256,expected);
  assert.notEqual(route.request_sha256,createHash('sha256').update('{"messages":[{"content":"original","role":"user"}]}').digest('hex'));
});
test('invalid markers never turn an ordinary request into a probe', async t => {
  const m = await middleware(t);
  for (const marker of [null, { version: 1, id: 'wrong' }, { version: 1, id, secret: 'extra' }, []]) {
    const body = { model: 'channel/solo', _magpie_model_check_probe: marker }, ctx = { state: {} };
    assert.deepEqual(m.onRequest(structuredClone(body), ctx), body);
    assert.deepEqual(ctx.state, {});
  }
});

test('a failed retry observer never authenticates the previous provider',async t=>{
  const m=await middleware(t),ctx={state:{},provider:{id:'first',model:'solo'}};
  const sent=m.onRequest({model:'solo',_magpie_model_check_probe:{version:1,id}},ctx);
  m.onProvider(sent,ctx);
  // Magpie updates ctx.provider even if the next hook fails open before its
  // function is entered. State from the first attempt must then be declined.
  ctx.provider={id:'fallback',model:'solo'};
  const response=m.onResponse({choices:[{message:{content:'17'},finish_reason:'stop'}]},ctx);
  assert.equal(response._magpie_model_check_route,undefined);
});

test('route proof covers consumed usage, self label and tool content',async t=>{
  const m=await middleware(t);
  function sign(change){const ctx={state:{},provider:{id:'channel',model:'solo'}},sent=m.onRequest({model:'channel/solo',_magpie_model_check_probe:{version:1,id}},ctx);m.onProvider(sent,ctx);const reply={model:'self-label',usage:{prompt_tokens:1,completion_tokens:2},choices:[{message:{content:'17',tool_calls:[{function:{arguments:'{}'}}]},finish_reason:'stop'}]};change?.(reply);return m.onResponse(reply,ctx)._magpie_model_check_route;}
  const original=sign();
  for(const change of [r=>r.usage.prompt_tokens=999999,r=>r.model='changed',r=>r.choices[0].message.tool_calls[0].function.arguments='{"value":1}'])assert.notEqual(sign(change).mac,original.mac);
});

test('terminal stream proof binds previous event content',async t=>{
  const m=await middleware(t);
  function transcript(text){const ctx={state:{},provider:{id:'channel',model:'solo'}},sent=m.onRequest({model:'channel/solo',_magpie_model_check_probe:{version:1,id}},ctx);m.onProvider(sent,ctx);m.onEvent({choices:[{delta:{content:text},finish_reason:null}]},ctx);return m.onEvent({choices:[{delta:{},finish_reason:'stop'}]},ctx)._magpie_model_check_route;}
  assert.notEqual(transcript('17,29').mac,transcript('1,2').mac);
});
