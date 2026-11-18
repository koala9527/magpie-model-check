import test from 'node:test';
import assert from 'node:assert/strict';
import {createRenderGuard} from './ui-refresh.mjs';

test('unchanged poll data keeps open details and focus by skipping DOM replacement',()=>{
  const shouldRender=createRenderGuard();
  const data={batch:{id:'saved-batch',completed_at:'2026-11-17'},samples:[{id:'raw-1',raw_response:'17,17'}]};
  let replacements=0;
  if(shouldRender(data))replacements++;
  if(shouldRender(JSON.parse(JSON.stringify(data))))replacements++;
  assert.equal(replacements,1);
  assert.equal(shouldRender({...data,samples:[...data.samples,{id:'raw-2'}]}),true);
  assert.equal(shouldRender({...data,batch:{id:'another-batch',completed_at:'2026-11-17'}}),true);
});

test('independent state and report guards do not suppress each other',()=>{
  const state=createRenderGuard(),report=createRenderGuard();
  assert.equal(state({targets:[]}),true);
  assert.equal(report({batch:{id:'one'}}),true);
  assert.equal(state({targets:[]}),false);
  assert.equal(report({batch:{id:'one'}}),false);
});
