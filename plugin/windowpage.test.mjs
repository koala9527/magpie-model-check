import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';

test('the sandbox bootstrap uses the official service navigation, not a forbidden nested iframe',async()=>{
  const html=await fs.readFile(new URL('page/index.html',import.meta.url),'utf8');
  const source=await fs.readFile(new URL('page/loader.js',import.meta.url),'utf8');
  assert.match(html,/_magpie\/bridge\.js/);
  assert.doesNotMatch(html,/<iframe/i);
  assert.match(source,/location\.replace/);
  assert.match(source,/magpie\.service/);
  assert.doesNotMatch(source,/fetch\(|gatewayKey|routeKey|localStorage/);
});
