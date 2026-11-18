import test from 'node:test';
import assert from 'node:assert/strict';
import {referenceReviewPayload,normaliseBankImport,metricText} from './ui-reference.mjs';

const detail={bank:{sha256:'bank-hash'},validation:{corpus_sha256:'corpus-hash'},provenance_sha256:'records-hash',provenance:[{record:{id:'record-1'}},{record:{id:'record-2'}}]};
const values={reviewer:'local auditor',review_note:'I checked independent source evidence',confirm_independent_source_review:true,confidence:'0.95',max_false_match_rate:'0.01',max_false_replacement_rate:'0.02',min_unknown_recall:'0.8',max_known_rejection_rate:'0.2',min_top1_accuracy:'0.8',min_family_accuracy:'0.9'};

test('human review binds exact loaded hashes and all record ids; no imported trusted flag',()=>{
  const input=referenceReviewPayload(detail,values);
  assert.equal(input.bank_sha256,'bank-hash');
  assert.equal(input.corpus_sha256,'corpus-hash');
  assert.equal(input.provenance_sha256,'records-hash');
  assert.deepEqual(input.approved_record_ids,['record-1','record-2']);
  assert.equal(input.policy.confidence,0.95);
  assert.equal('trusted' in input,false);
  for(const field of ['reviewer','review_note','confidence','max_false_match_rate'])assert.throws(()=>referenceReviewPayload(detail,{...values,[field]:''}));
  assert.throws(()=>referenceReviewPayload(detail,{...values,confirm_independent_source_review:false}));
  assert.throws(()=>referenceReviewPayload(detail,{...values,confidence:'1'}));
});

test('bare reference bundle imports preserve bound inner strings and remain untrusted',()=>{
  const manifest='{\n "version":"v1", "algorithm_version":"alg-v1", "models":[{"id":"A"},{"id":"B"}]\n}\n';
  const bundle={format:'magpie-model-check.reference-bundle',bank_json:manifest,corpus_json:'{ "raw":"测试 < >" }\n'};
  const transfer=normaliseBankImport(bundle);
  assert.equal(transfer.version,'v1');
  assert.equal(transfer.algorithm_version,'alg-v1');
  assert.equal(transfer.trusted,false);
  assert.deepEqual(JSON.parse(transfer.data),bundle);
  assert.equal(JSON.parse(transfer.data).bank_json,manifest);
  assert.equal(normaliseBankImport(transfer).data,transfer.data);
});

test('metrics retain grouped counts and explicit missing confidence/probability',()=>{
  assert.match(metricText({events:1,total:2,value:0.5,wilson_ci:{lower:0.1,upper:0.9},confidence:0.95}),/1 \/ 2/);
  assert.match(metricText({events:1,total:2,value:0.5,wilson_ci:null}),/置信水平未指定/);
  assert.equal(metricText({events:0,total:0,value:null}),'未测量（0 组）');
});
