import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createHash } from 'node:crypto';
import http from 'node:http';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';

const root = path.resolve(import.meta.dirname, '..');
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');

// Check for the import-safe public seam before importing legacy executables:
// the original build script rebuilds old binaries as an import side effect.
async function tooling(file, exported) {
  const source = await fs.readFile(path.join(root, 'scripts', file), 'utf8');
  for (const name of exported) assert.match(source, new RegExp(`export (?:async )?(?:function|const) ${name}\\b`), `${file}: missing ${name}`);
  return import(new URL(file, import.meta.url));
}

async function temporary(t) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'model-release-test-'));
  t.after(async () => {
    assert.ok(path.resolve(dir).startsWith(path.resolve(os.tmpdir()) + path.sep));
    await fs.rm(dir, { recursive: true, force: true });
  });
  return dir;
}

async function put(dir, file, text = 'fixture\n') {
  const target = path.join(dir, file);
  await fs.mkdir(path.dirname(target), { recursive: true });
  await fs.writeFile(target, text);
}

test('test discovery retains old tests and discovers both frontend trees without shell globs', async t => {
  const { discoverNodeTests, testPlan } = await tooling('test.mjs', ['discoverNodeTests', 'testPlan']);
  const dir = await temporary(t);
  const expected = ['plugin/old.test.mjs', 'scripts/answer-analysis.test.mjs', 'scripts/tooling.test.mjs', 'internal/check/web/assets/original.test.mjs', 'internal/modelcheck/web/ui-forms.test.mjs'];
  for (const file of [...expected, 'plugin/.runtime/private.test.mjs', 'scripts/node_modules/dependency.test.mjs']) await put(dir, file);
  await put(dir, 'cmd/quality-check/main.go');
  await put(dir, 'internal/modelstore/store.go');
  assert.deepEqual((await discoverNodeTests(dir)).map(file => path.relative(dir, file).replaceAll('\\', '/')), expected.toSorted());
  const plan = await testPlan(dir);
  assert.ok(plan.some(step => step.command === 'go' && step.args.join(' ') === 'test ./cmd/... ./internal/...'));
  assert.ok(plan.some(step => step.command === 'go' && step.args.join(' ') === 'vet ./cmd/... ./internal/...'));
  assert.ok(plan.some(step => step.args.includes(path.join(dir, 'internal/modelcheck/web/ui-forms.test.mjs'))));
  assert.ok(plan.some(step => step.args.includes(path.join(dir, 'scripts/package-check.mjs'))));
  assert.ok(plan.every(step => !step.args.some(arg => arg.includes('*'))));
});

test('the race gate explicitly enables CGO and propagates a missing compiler failure', async t => {
  const { testPlan, runTestPlan } = await tooling('test.mjs', ['testPlan', 'runTestPlan']);
  const dir = await temporary(t);
  await put(dir, 'cmd/model-check/main.go');
  await put(dir, 'internal/check/service_test.go');
  const plan = await testPlan(dir, 'race');
  assert.equal(plan.length, 1);
  assert.deepEqual(plan[0].args, ['test', '-race', './cmd/...', './internal/...']);
  assert.equal(plan[0].env.CGO_ENABLED, '1');
  assert.equal(runTestPlan(plan, { cwd: dir, run: () => ({ status: 19 }) }), 19);
});

test('Go discovery retains every source tree but excludes evidence, caches and nested modules', async t => {
  const { discoverGoPatterns, testPlan } = await tooling('test.mjs', ['discoverGoPatterns', 'testPlan']);
  const dir = await temporary(t);
  for (const file of ['root_test.go', 'cmd/quality-check/main.go', 'cmd/model-check/main.go', 'internal/check/service_test.go', 'internal/modelstore/store_test.go', 'new-source/api/service.go']) await put(dir, file);
  for (const file of ['artifacts/review/broken_test.go', 'node_modules/dependency/broken.go', '.runtime/broken.go', 'dist/snapshot/broken.go', 'bin/broken.go', 'testdata/broken.go', 'vendor/broken.go', '_scratch/broken.go', 'nested/go.mod', 'nested/broken.go', 'internal/nested/go.mod', 'internal/nested/broken.go']) await put(dir, file);
  const patterns = ['.', './cmd/...', './internal/...', './new-source/...'];
  assert.deepEqual(await discoverGoPatterns(dir), patterns);
  for (const mode of ['go', 'vet', 'race']) {
    const [step] = await testPlan(dir, mode);
    assert.deepEqual(step.args, [mode === 'vet' ? 'vet' : 'test', ...(mode === 'race' ? ['-race'] : []), ...patterns]);
    assert.equal(step.env.CGO_ENABLED, mode === 'race' ? '1' : '0');
  }
});

test('a checkout containing only evidence Go files does not report an empty passing Go gate', async t => {
  const { testPlan } = await tooling('test.mjs', ['testPlan']);
  const dir = await temporary(t);
  await put(dir, 'artifacts/not-a-source-package/broken_test.go');
  await assert.rejects(testPlan(dir, 'go'), /No Go source packages/);
});

test('a failed quality gate prevents later gates from reporting success', async t => {
  const { runTestPlan } = await tooling('test.mjs', ['runTestPlan']);
  const dir = await temporary(t);
  let runs = 0;
  assert.equal(runTestPlan([{ name: 'Go', command: 'go', args: [] }, { name: 'Node', command: process.execPath, args: [] }], { cwd: dir, run: () => { runs++; return { status: 7 }; } }), 7);
  assert.equal(runs, 1);
});

test('the build contract covers all six model-check targets including Windows arm64', async () => {
  const { RELEASE_TARGETS, binaryName } = await tooling('build-release.mjs', ['RELEASE_TARGETS', 'binaryName']);
  assert.deepEqual(RELEASE_TARGETS.map(({ goos, goarch }) => `${goos}/${goarch}`).toSorted(), ['darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64', 'windows/amd64', 'windows/arm64']);
  assert.equal(binaryName({ goos: 'windows', goarch: 'arm64' }), 'magpie-model-check-windows-arm64.exe');
});

test('builds use the new command, CGO0, checked hashes and no invented runtime pass', async t => {
  const { buildRelease, RELEASE_TARGETS } = await tooling('build-release.mjs', ['buildRelease', 'RELEASE_TARGETS']);
  const dir = await temporary(t);
  await put(dir, 'package.json', JSON.stringify({ name: 'magpie-model-check', version: '0.4.0' }));
  await put(dir, 'bin/quality-check-windows-amd64.exe', 'ORIGINAL_BINARY\n');
  await put(dir, 'bin/magpie-model-check-windows-amd64.exe', 'ORIGINAL_MODEL_BINARY\n');
  const calls = [];
  const manifest = await buildRelease({ root: dir, run(command, args, options) {
    calls.push({ command, args, env: options.env });
    writeFileSync(args[args.indexOf('-o') + 1], `COMPILE_FIXTURE:${options.env.GOOS}/${options.env.GOARCH}\n`);
    return { status: 0 };
  } });
  assert.equal(calls.length, 6);
  for (const call of calls) {
    assert.equal(call.command, 'go');
    assert.equal(call.args.at(-1), './cmd/model-check');
    assert.ok(call.args.includes('-mod=readonly'), 'Tooling must not rewrite the main agent\'s Go module');
    assert.equal(call.env.CGO_ENABLED, '0');
  }
  assert.equal(manifest.artifacts.length, RELEASE_TARGETS.length);
  for (const artifact of manifest.artifacts) {
    const bytes = await fs.readFile(path.join(dir, 'dist', artifact.name));
    assert.equal(artifact.sha256, sha256(bytes));
    assert.equal(artifact.size, bytes.length);
    assert.equal(artifact.compile_status, 'PASS');
    assert.equal(artifact.runtime_validation, 'NOT_RUN');
  }
  assert.equal(await fs.readFile(path.join(dir, 'bin/quality-check-windows-amd64.exe'), 'utf8'), 'ORIGINAL_BINARY\n');
  const snapshot = JSON.parse(await fs.readFile(path.join(dir, 'dist/binary-regression.json'), 'utf8'));
  assert.equal(snapshot.files.find(file => file.name === 'quality-check-windows-amd64.exe').sha256, sha256('ORIGINAL_BINARY\n'));
  assert.equal(await fs.readFile(path.join(dir, 'dist', snapshot.files.find(file => file.name === 'magpie-model-check-windows-amd64.exe').snapshot), 'utf8'), 'ORIGINAL_MODEL_BINARY\n');
});

test('an interrupted compilation leaves installed binaries and the prior manifest unchanged', async t => {
  const { buildRelease } = await tooling('build-release.mjs', ['buildRelease']);
  const dir = await temporary(t);
  await put(dir, 'package.json', JSON.stringify({ name: 'magpie-model-check', version: '0.4.0' }));
  await put(dir, 'bin/magpie-model-check-windows-amd64.exe', 'ORIGINAL\n');
  await put(dir, 'dist/manifest.json', 'PRIOR_MANIFEST\n');
  let runs = 0;
  await assert.rejects(buildRelease({ root: dir, run(_command, args) {
    if (++runs === 3) return { status: 23 };
    writeFileSync(args[args.indexOf('-o') + 1], 'compile fixture\n');
    return { status: 0 };
  } }), /build failed/i);
  assert.equal(await fs.readFile(path.join(dir, 'bin/magpie-model-check-windows-amd64.exe'), 'utf8'), 'ORIGINAL\n');
  assert.equal(await fs.readFile(path.join(dir, 'dist/manifest.json'), 'utf8'), 'PRIOR_MANIFEST\n');
});

test('package auditing rejects credentials, histories, old binaries and retired helper code', async () => {
  const { PACKAGE_FILES, auditPackageEntries } = await tooling('package-check.mjs', ['PACKAGE_FILES', 'auditPackageEntries']);
  const pkg = JSON.parse(await fs.readFile(path.join(root, 'package.json'), 'utf8'));
  const entries = [...PACKAGE_FILES, 'package.json'];
  assert.doesNotThrow(() => auditPackageEntries(entries, pkg, { requireAllTargets: true }));
  for (const file of ['plugin/.runtime/route-secret.mjs', 'plugin/page/.runtime/key.mjs', 'data/model-check.db', 'plugin/page/history.sqlite', 'artifacts/check.json', 'bin/quality-check-linux-amd64', 'plugin/cloudflared-runtime.mjs', 'plugin/gate.middleware.js', 'plugin/gateway-discovery.mjs', 'plugin/console-readiness.mjs', 'third_party/cloudflared/LICENSE', '../escaped']) {
    assert.throws(() => auditPackageEntries([...entries, file], pkg), /excluded|unexpected|invalid/i, file);
  }
  assert.throws(() => auditPackageEntries(entries.filter(file => file !== 'bin/magpie-model-check-windows-arm64.exe'), pkg, { requireAllTargets: true }), /missing/i);
});

test('Windows archive checks select native tar instead of Git Bash remote-colon parsing',async()=>{
  const {nativeTarCommand}=await tooling('package-check.mjs',['nativeTarCommand']);
  assert.equal(nativeTarCommand('win32',{SystemRoot:'C:\\Windows'}),'C:\\Windows\\System32\\tar.exe');
  assert.equal(nativeTarCommand('win32',{}),'C:\\Windows\\System32\\tar.exe');
  assert.equal(nativeTarCommand('linux',{}),'tar');assert.equal(nativeTarCommand('darwin',{}),'tar');
});

test('actual npm pack excludes planted private files and retains a complete new runtime', async t => {
  const { PACKAGE_FILES, packPackage, inspectPackage } = await tooling('package-check.mjs', ['PACKAGE_FILES', 'packPackage', 'inspectPackage']);
  const dir = await temporary(t);
  const pkg = JSON.parse(await fs.readFile(path.join(root, 'package.json'), 'utf8'));
  pkg.files = PACKAGE_FILES;
  pkg.scripts = {};
  await put(dir, 'package.json', JSON.stringify(pkg));
  for (const file of PACKAGE_FILES) await put(dir, file, `PACKAGING_FIXTURE:${file}\n`);
  for (const file of ['plugin/.runtime/route-secret.mjs', 'plugin/page/leak.db', 'bin/quality-check-linux-amd64', 'artifacts/raw-response.json', 'plugin/cloudflared-runtime.mjs']) await put(dir, file, 'PRIVATE_PACK_SENTINEL\n');
  const packed = await packPackage({ root: dir, outputDir: path.join(dir, 'packed'), requireAllTargets: true });
  assert.ok((await fs.stat(packed.tarball)).size > 0);
  assert.deepEqual(packed.files.toSorted(), [...PACKAGE_FILES, 'package.json'].toSorted());
  const inspected = await inspectPackage(packed.tarball, { requireAllTargets: true });
  assert.equal(inspected.sha256, packed.sha256, 'An externally supplied tarball must retain checksum evidence too');
  assert.equal(inspected.size, packed.size);
  assert.equal(packed.files.some(file => /runtime|\.db|quality-check|artifacts|cloudflared/.test(file) && !file.endsWith('/runtime.mjs')), false);
});

test('release checks reject changed bytes instead of trusting a manifest hash', async t => {
  const { buildRelease } = await tooling('build-release.mjs', ['buildRelease']);
  const { verifyRelease } = await tooling('package-check.mjs', ['verifyRelease']);
  const dir = await temporary(t);
  await put(dir, 'package.json', JSON.stringify({ name: 'magpie-model-check', version: '0.4.0' }));
  await buildRelease({ root: dir, run(_command, args) { writeFileSync(args[args.indexOf('-o') + 1], 'CHECKSUM_FIXTURE\n'); return { status: 0 }; } });
  await verifyRelease(path.join(dir, 'dist'));
  await fs.appendFile(path.join(dir, 'dist/magpie-model-check-linux-amd64'), 'tampered');
  await assert.rejects(verifyRelease(path.join(dir, 'dist')), /checksum|size/i);
});

test('installed-package registration is atomic and preserves companion plugins and options', async t => {
  const { replacePluginRegistration } = await tooling('install-check.mjs', ['replacePluginRegistration']);
  const dir = await temporary(t);
  const fixture = { config: path.join(dir, 'config'), spec: path.join(dir, 'root-copy'), options: { port: 32123, dataDir: path.join(dir, 'history') } };
  const companion = { spec: path.join(dir, 'companion.mjs'), options: { keep: true } };
  await put(dir, 'config/plugins.json', JSON.stringify({ plugins: [{ spec: fixture.spec, options: fixture.options, off: true }, companion], retained: true }));
  const installed = path.join(dir, 'node_modules/magpie-model-check');
  await replacePluginRegistration(fixture, installed);
  const saved = JSON.parse(await fs.readFile(path.join(fixture.config, 'plugins.json'), 'utf8'));
  assert.deepEqual(saved.plugins, [{ spec: installed, options: fixture.options, off: true }, companion]);
  assert.equal(saved.retained, true);
  assert.deepEqual(await fs.readdir(fixture.config), ['plugins.json']);
});

test('restart launches use only isolated portable fixture directories', async t => {
  const { fixtureEnvironment } = await tooling('install-check.mjs', ['fixtureEnvironment']);
  const dir = await temporary(t);
  const env = fixtureEnvironment({ dir, binary: path.join(dir, 'runtime/magpie.exe'), gateway: 'http://127.0.0.1:32123', options: { gatewayKeyEnv: 'MODEL_CHECK_FIXTURE_GATEWAY_KEY' } });
  assert.equal(env.HOME, dir);
  assert.equal(env.USERPROFILE, dir);
  assert.equal(env.MAGPIE_ADDR, '127.0.0.1:32123');
  for (const name of ['APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME']) assert.ok(env[name].startsWith(dir + path.sep));
});

test('failed plugin disable still closes the fixture and stops the real restarted process', async t => {
  const { closeInstallation } = await tooling('install-check.mjs', ['closeInstallation']);
  const dir = await temporary(t);
  const child = spawn(process.execPath, ['-e', 'process.on("SIGTERM",()=>{}); console.log("READY"); setInterval(()=>{},1000)'], { windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
  t.after(() => { if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL'); });
  await once(child.stdout, 'data');
  const fixture = { async command() { throw Error('plugin off fixture failure'); }, async close() { await fs.writeFile(path.join(dir, 'closed'), 'CLOSED\n'); } };
  const errors = await closeInstallation(fixture, path.join(dir, 'installed'), child);
  assert.ok(errors.some(error => error.includes('plugin off fixture failure')));
  assert.equal(await fs.readFile(path.join(dir, 'closed'), 'utf8'), 'CLOSED\n');
  assert.ok(child.exitCode !== null || child.signalCode !== null, 'Restarted native process leaked');
});

test('client checks send ordinary requests on all supported wire APIs, not quality-gate errors', async t => {
  const { verifyClientProtocols } = await tooling('client-check.mjs', ['verifyClientProtocols']);
  const seen = [];
  const server = http.createServer(async (request, response) => {
    let raw = ''; for await (const chunk of request) raw += chunk;
    const body = JSON.parse(raw); seen.push({ path: request.url, body });
    if (body.stream) { response.writeHead(200, { 'Content-Type': 'text/event-stream' }); response.end('data: {"choices":[{"delta":{"content":"ordinary fixture reply"}}]}\n\ndata: [DONE]\n\n'); }
    else { response.writeHead(200, { 'Content-Type': 'application/json' }); response.end(JSON.stringify({ text: 'ordinary fixture reply' })); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); return new Promise(resolve => server.close(resolve)); });
  const verified = await verifyClientProtocols({ gateway: `http://127.0.0.1:${server.address().port}` });
  assert.equal(verified.length, 4);
  assert.deepEqual([...new Set(seen.map(item => item.path))].toSorted(), ['/v1/chat/completions', '/v1/messages', '/v1/responses']);
  assert.ok(seen.every(item => item.body.model === 'fixture/solo' && !('_magpie_model_check_probe' in item.body)));
});

test('client validation treats a non-200 response as a failing gate', async t => {
  const { verifyClientProtocols } = await tooling('client-check.mjs', ['verifyClientProtocols']);
  const server = http.createServer((_request, response) => { response.writeHead(403, { 'Content-Type': 'application/json' }); response.end('{"error":{"message":"unexpected rejection"}}'); });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); return new Promise(resolve => server.close(resolve)); });
  await assert.rejects(verifyClientProtocols({ gateway: `http://127.0.0.1:${server.address().port}` }), /403|200/);
});

test('portable downloads verify bytes before publishing and cover Windows arm64', async t => {
  const { magpieAsset, downloadVerified } = await tooling('magpie-integration.mjs', ['magpieAsset', 'downloadVerified']);
  const asset = magpieAsset({ goos: 'windows', goarch: 'arm64' });
  assert.match(asset.url, /magpie-cli-windows-arm64\.exe$/);
  assert.match(asset.sha256, /^[a-f0-9]{64}$/);
  const dir = await temporary(t), bytes = Buffer.from('DOWNLOAD_INTEGRITY_FIXTURE\n');
  const server = http.createServer((_request, response) => response.end(bytes));
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); return new Promise(resolve => server.close(resolve)); });
  const url = `http://127.0.0.1:${server.address().port}/binary`;
  await assert.rejects(downloadVerified(url, 'f'.repeat(64), path.join(dir, 'corrupt.bin')), /checksum/i);
  await assert.rejects(fs.access(path.join(dir, 'corrupt.bin')));
  await downloadVerified(url, sha256(bytes), path.join(dir, 'verified.bin'));
  assert.deepEqual(await fs.readFile(path.join(dir, 'verified.bin')), bytes);
});

test('CI has native supported-OS installs, CGO0 builds and a mandatory explicit race gate', async () => {
  const source = await fs.readFile(path.join(root, '.github/workflows/ci.yml'), 'utf8');
  for (const runner of ['ubuntu-24.04', 'macos-15', 'windows-2025']) assert.ok(source.includes(runner), runner);
  for (const command of ['npm test', 'npm run test:race', 'npm run build:release', 'npm run test:install', 'npm run test:clients']) assert.ok(source.includes(command), command);
  assert.match(source, /CGO_ENABLED: ['"]?0/);
  assert.match(source, /MAGPIE_BINARY/);
  assert.match(source, /magpie-cli-/);
  assert.doesNotMatch(source, /continue-on-error|race[^\n]*\|\|\s*(?:true|echo)/);
});

test('the aggregate CI gate fails for failed, cancelled, skipped or missing mandatory jobs', async () => {
  const source = await fs.readFile(path.join(root, '.github/workflows/ci.yml'), 'utf8');
  const aggregate = source.slice(source.indexOf('\n  release-gate:'));
  assert.match(aggregate, /if:\s*\$\{\{\s*always\(\)\s*\}\}/, 'A skipped aggregate can otherwise count as a passing required check');
  assert.match(aggregate, /NEEDS_JSON:\s*\$\{\{\s*toJSON\(needs\)\s*\}\}/);
  const script = aggregate.match(/node --input-type=module -e '([\s\S]*?)'/)?.[1];
  assert.ok(script, 'The mandatory job results must be checked, not just echoed');
  const success = { race: { result: 'success' }, 'cross-compile': { result: 'success' }, 'native-install': { result: 'success' } };
  const run = needs => spawnSync(process.execPath, ['--input-type=module', '-e', script], { env: { ...process.env, NEEDS_JSON: JSON.stringify(needs) }, encoding: 'utf8', windowsHide: true });
  const passed = run(success);
  assert.equal(passed.status, 0, passed.stderr);
  assert.match(passed.stdout, /^PASS: race, six CGO0 builds/);
  for (const name of Object.keys(success)) {
    for (const result of ['failure', 'cancelled', 'skipped']) {
      const failed = run({ ...success, [name]: { result } });
      assert.equal(failed.status, 1, `${name}/${result} was accepted`);
    }
  }
  assert.equal(run({}).status, 1, 'Missing mandatory jobs were accepted');
});
