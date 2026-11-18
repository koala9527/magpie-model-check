import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { failureExcerpt, workflowAnnotation, diagnosticExit } from './ci-test-diagnostics.mjs';

test('CI diagnostics preserve failing TAP/Go context rather than only the final passing test', () => {
  const log = ['TAP version 13', 'not ok 3 - private runtime', '  error: PRIVATE_PATH_REQUIRED', '  stack: |', '    at runtime.mjs:25', ...Array.from({length:100}, (_,i)=>`ok ${i+4} - unrelated`), '# tests 104', '# fail 1'].join('\n');
  const excerpt = failureExcerpt(log);
  assert.match(excerpt, /not ok 3 - private runtime/);
  assert.match(excerpt, /PRIVATE_PATH_REQUIRED/);
  assert.match(excerpt, /# fail 1/);
  assert.match(failureExcerpt('--- FAIL: TestNative (0.1s)\n    fixture.go:9: failure\nFAIL package'), /fixture.go:9: failure/);
});

test('CI diagnostics retain later direct errors after many early startup timeouts', () => {
  const groups=Array.from({length:9},(_,i)=>[
    `not ok ${i+1} - case ${i+1}`,
    `  error: ${i===8?'WINDOWS_PRIVATE_ACL_FAILED':'FIXTURE_DEADLINE'}`,
    ...Array.from({length:40},()=> 'ok - unrelated passing context'),
  ].join('\n')).join('\n');
  assert.match(failureExcerpt(groups),/WINDOWS_PRIVATE_ACL_FAILED/);
});

test('CI annotation encoding cannot introduce additional workflow commands', () => {
  assert.equal(workflowAnnotation('x%\r\n::warning::payload'), '::error title=Model-check test failure::x%25%0D%0A::warning::payload');
  assert.ok(workflowAnnotation('x'.repeat(100000)).length < 20000);
});

test('CI diagnostics retain the actual mandatory suite exit status', () => {
  assert.equal(diagnosticExit('0'), 0);
  assert.equal(diagnosticExit('1'), 1);
  assert.equal(diagnosticExit('7'), 7);
  for (const status of ['-1', '256', 'false', '1; exit 0']) assert.throws(()=>diagnosticExit(status));
});

test('the actual diagnostic CLI emits context and exits with the suite failure', async t => {
  const parent=await fs.realpath(os.tmpdir());
  const dir=await fs.mkdtemp(path.join(parent,'model-ci-diagnostic-'));
  const log=path.join(dir,'suite.log');
  t.after(async()=>{
    assert.equal(path.dirname(await fs.realpath(dir)),parent);
    await fs.unlink(log); await fs.rmdir(dir);
  });
  await fs.writeFile(log,'not ok 1 - isolated generated failure\n  error: test error\n# fail 1\n');
  const cli=fileURLToPath(new URL('./ci-test-diagnostics.mjs',import.meta.url));
  const failed=spawnSync(process.execPath,[cli,log,'7'],{encoding:'utf8',windowsHide:true});
  assert.equal(failed.status,7);
  assert.match(failed.stderr,/::error title=Model-check test failure::/);
  assert.match(failed.stderr,/isolated generated failure/);
  const passed=spawnSync(process.execPath,[cli,log,'0'],{encoding:'utf8',windowsHide:true});
  assert.equal(passed.status,0); assert.equal(passed.stderr,'');
});
