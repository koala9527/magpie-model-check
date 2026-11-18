import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createHash, randomUUID } from 'node:crypto';
import assert from 'node:assert/strict';
import { startFixture, until, root } from './model-fixture.mjs';
import { packPackage, inspectPackage, runNpm, auditPackageEntries, nativeTarget } from './package-check.mjs';

const digest = value => createHash('sha256').update(JSON.stringify(value)).digest('hex');

export function fixtureEnvironment(fixture) {
  const dir = path.resolve(fixture.dir);
  return {
    ...process.env, HOME: dir, USERPROFILE: dir,
    XDG_CONFIG_HOME: path.join(dir, 'config'), XDG_CACHE_HOME: path.join(dir, 'cache'), XDG_STATE_HOME: path.join(dir, 'state'),
    APPDATA: path.join(dir, 'appdata'), LOCALAPPDATA: path.join(dir, 'local'),
    MAGPIE_ADDR: new URL(fixture.gateway).host,
    ...(process.platform === 'win32' ? { MAGPIE_BUN: path.join(path.dirname(fixture.binary), 'fixture-bun.exe') } : {}),
    [fixture.options.gatewayKeyEnv]: 'magpie',
  };
}

export async function replacePluginRegistration(fixture, installed) {
  const file = path.join(fixture.config, 'plugins.json');
  const saved = JSON.parse(await fs.readFile(file, 'utf8'));
  assert.ok(saved.plugins.some(plugin => plugin.spec === fixture.spec), 'The isolated source registration is missing');
  saved.plugins = saved.plugins.map(plugin => plugin.spec === fixture.spec ? { ...plugin, spec: installed, options: fixture.options, off: true } : plugin);
  const temporary = `${file}.${randomUUID()}.tmp`;
  try { await fs.writeFile(temporary, JSON.stringify(saved, null, 2) + '\n', { flag: 'wx', mode: 0o600 }); await fs.rename(temporary, file); }
  finally { await fs.unlink(temporary).catch(error => { if (error.code !== 'ENOENT') throw error; }); }
}

async function stopProcess(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, 'exit');
  child.stdin?.end(); child.kill();
  let timer;
  const force = setTimeout(() => child.kill('SIGKILL'), 3000);
  try { await Promise.race([exited, new Promise((_, reject) => { timer = setTimeout(() => reject(Error('Portable Magpie did not stop')), 5000); })]); }
  finally { clearTimeout(timer); clearTimeout(force); }
}

export async function closeInstallation(fixture, installed, child) {
  const errors = [];
  if (fixture && installed) {
    try { await fixture.command(['plugin', 'off', installed]); } catch (error) { errors.push(String(error)); }
  }
  try { await stopProcess(child); } catch (error) { errors.push(String(error)); }
  if (fixture) { try { await fixture.close(); } catch (error) { errors.push(String(error)); } }
  return errors;
}

async function installedFiles(directory) {
  const files = [];
  async function visit(dir) {
    for (const entry of await fs.readdir(dir, { withFileTypes: true })) {
      assert.equal(entry.isSymbolicLink(), false, `Unexpected installed symlink: ${entry.name}`);
      const file = path.join(dir, entry.name);
      if (entry.isDirectory()) await visit(file);
      else files.push(path.relative(directory, file).replaceAll('\\', '/'));
    }
  }
  await visit(directory); return files;
}

export async function runInstallCheck({ binaryDir, tarball, evidenceDir = path.join(root, 'artifacts/rebuild-work-20261117/release-tooling-install'), afterReady } = {}) {
  evidenceDir = path.resolve(evidenceDir); await fs.mkdir(evidenceDir, { recursive: true });
  const evidence = await fs.mkdtemp(path.join(evidenceDir, 'run-'));
  const summary = { status: 'RUNNING', evidence, native_target: nativeTarget(), native_runtime_validation: 'NOT_RUN', upstream: 'ISOLATED_SYNTHETIC_FIXTURE', actual_model_reference_samples: 0, steps: [], commands: [] };
  let fixture, restarted, installed;
  try {
    const packed = tarball ? { tarball: path.resolve(tarball), ...await inspectPackage(path.resolve(tarball)) } : await packPackage({ outputDir: evidence, binaryDir, requireAllTargets: !!binaryDir });
    summary.package = { tarball: packed.tarball, sha256: packed.sha256, files: packed.files, binary_targets: packed.binary_targets };
    if (packed.command) summary.commands.push({ command: packed.command, exit_status: packed.exit_status });
    fixture = await startFixture(); summary.fixture = fixture.dir; summary.magpie_source = fixture.source;
    await fixture.command(['plugin', 'off', fixture.spec]);
    const prefix = path.join(fixture.dir, 'npm-install'); await fs.mkdir(prefix);
    await fs.writeFile(path.join(prefix, 'package.json'), '{"private":true,"type":"module"}\n');
    const args = ['install', '--ignore-scripts', '--no-audit', '--no-fund', '--package-lock=false', packed.tarball];
    const installation = runNpm(args, { cwd: prefix, env: fixtureEnvironment(fixture) });
    summary.commands.push({ command: ['npm', ...args], cwd: prefix, exit_status: installation.status, output: installation.stdout + installation.stderr });
    installed = path.join(prefix, 'node_modules/magpie-model-check'); summary.installed = installed;
    const pkg = JSON.parse(await fs.readFile(path.join(installed, 'package.json'), 'utf8'));
    auditPackageEntries(await installedFiles(installed), pkg, { requireAllTargets: !!binaryDir });
    await replacePluginRegistration(fixture, installed);
    summary.steps.push('ACTUAL_NPM_PACK_AND_ISOLATED_NPM_INSTALL');

    async function command(args) {
      const before = fixture.log.length; await fixture.command(args);
      summary.commands.push({ command: [fixture.binary, ...args], exit_status: 0, output: fixture.log.slice(before).join('') });
    }
    async function ready() {
      const listed = await until(async () => { const result = await fixture.rpc('tools/list'); return result.tools?.some(tool => tool.name.endsWith('_check')) ? result : null; }, 30000);
      await until(fixture.readSession); return listed.tools.find(tool => tool.name.endsWith('_check'));
    }
    async function closed() {
      await until(async () => { try { await fetch(fixture.service + '/', { signal: AbortSignal.timeout(300) }); return false; } catch { return true; } });
    }
    async function ordinary(label) {
      const response = await fixture.request(); assert.equal(response.reply.choices[0].message.content, 'ordinary fixture reply'); summary.steps.push(label);
    }
    await command(['--version']);
    await command(['plugin', 'on', installed]);
    const tool = await ready();
    await fs.access(path.join(installed, 'plugin/.runtime/route-secret.mjs'));
    const registered = JSON.parse(await fs.readFile(path.join(fixture.config, 'plugins.json'), 'utf8'));
    assert.ok(registered.plugins.some(plugin => plugin.spec === installed && !plugin.off));
    assert.equal(registered.plugins.some(plugin => plugin.spec === fixture.spec), false, 'Root-copy smoke must not masquerade as installed-package validation');
    summary.steps.push('NPM_INSTALLED_FACTORY_AND_REAL_MAGPIE_MCP_READY');
    await ordinary('ENABLED_ORDINARY_REQUEST_UNCHANGED');
    if (afterReady) summary.client_protocols = await afterReady(fixture);
    const target = await fixture.api('targets', 'POST', { provider_id: 'fixture', model_id: 'fixture/solo', claimed_model: 'UNVALIDATED_FIXTURE_CLAIM', enabled: true, auto_check: false, interval_minutes: 15, request_budget: 1 });
    const called = await fixture.rpc('tools/call', { name: tool.name, arguments: { target_id: target.id } });
    const result = JSON.parse(called.content[0].text); assert.ok(result.batch_id, JSON.stringify(result));
    const report = await fixture.api('batches/' + result.batch_id);
    assert.ok(report.batch.completed_at); assert.ok(report.samples.length > 0);
    assert.ok(report.samples.every(sample => sample.raw_response && sample.route_evidence?.authenticated));
    assert.notEqual(result.identity_status, 'MATCHED', 'Synthetic fixture is not a real reference identity');
    const reportHash = digest(report), before = (await fixture.api('state')).usage;
    assert.ok(before.requests >= 1); assert.ok(before.tokens > 0);
    summary.batch_id = result.batch_id; summary.report_sha256 = reportHash; summary.usage_before = before;
    summary.steps.push('CONTROLLED_CHECK_RAW_SAMPLES_RESULTS_AND_LEDGER_PERSISTED');
    const backup = await fixture.api('backup');
    const repacked = await packPackage({ root: installed, outputDir: path.join(evidence, 'after-runtime'), requireAllTargets: !!binaryDir });
    assert.deepEqual(repacked.files, packed.files);
    summary.steps.push('REPACK_EXCLUDES_GENERATED_PRIVATE_RUNTIME');

    await command(['plugin', 'off', installed]); await closed();
    await until(async () => !(await fixture.rpc('tools/list')).tools?.some(item => item.name.endsWith('_check')));
    await ordinary('DISABLED_ORDINARY_REQUEST_UNCHANGED');
    await command(['plugin', 'on', installed]); await ready();
    assert.equal(digest(await fixture.api('batches/' + result.batch_id)), reportHash);
    summary.steps.push('REENABLE_RETAINS_RAW_HISTORY');

    await command(['plugin', 'off', installed]); await closed(); await stopProcess(fixture.child);
    restarted = spawn(fixture.binary, ['serve'], { env: fixtureEnvironment(fixture), windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
    restarted.stdout.on('data', bytes => fixture.log.push(String(bytes))); restarted.stderr.on('data', bytes => fixture.log.push(String(bytes)));
    let launchError; restarted.once('error', error => { launchError = error; });
    await until(async () => { if (launchError) throw launchError; return (await fetch(fixture.gateway + '/v1/models', { headers: { Authorization: 'Bearer magpie' }, signal: AbortSignal.timeout(1000) })).ok; }, 30000);
    await ordinary('FULL_MAGPIE_RESTART_DISABLED_ORDINARY_REQUEST_UNCHANGED');
    await command(['plugin', 'on', installed]); await ready();
    assert.equal(digest(await fixture.api('batches/' + result.batch_id)), reportHash);
    await ordinary('FULL_MAGPIE_RESTART_ENABLED_ORDINARY_REQUEST_UNCHANGED');
    const restored = await fixture.api('state'); assert.ok(restored.targets.some(item => item.id === target.id));
    assert.ok(restored.usage.requests >= before.requests && restored.usage.tokens >= before.tokens);
    summary.steps.push('FULL_PROCESS_RESTART_RETAINS_TARGETS_HISTORY_AND_USAGE');
    await fixture.api('restore', 'POST', JSON.stringify(backup));
    const usage = (await fixture.api('state')).usage;
    assert.ok(usage.requests >= restored.usage.requests && usage.tokens >= restored.usage.tokens);
    summary.usage_after = usage; summary.steps.push('BACKUP_RESTORE_DOES_NOT_REFUND_LEDGER');
    summary.native_runtime_validation = 'PASS'; summary.status = 'PASS';
  } catch (error) { summary.status = 'FAIL'; summary.error = error.stack || String(error); }
  finally {
    const errors = await closeInstallation(fixture, installed, restarted);
    if (errors.length) { summary.status = 'FAIL'; summary.cleanup_error = errors.join('; '); }
    await fs.writeFile(path.join(evidence, 'install-check.json'), JSON.stringify(summary, null, 2) + '\n');
    console.log(JSON.stringify(summary, null, 2));
  }
  if (summary.status !== 'PASS') throw Error(`Installation gate failed; evidence: ${path.join(evidence, 'install-check.json')}; ${summary.error || summary.cleanup_error}`);
  return summary;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = {}, args = process.argv.slice(2);
    for (let i = 0; i < args.length; i += 2) {
      const value = args[i + 1]; if (!value) throw Error(`Missing value for ${args[i]}`);
      if (args[i] === '--binary-dir') options.binaryDir = path.resolve(value);
      else if (args[i] === '--tarball') options.tarball = path.resolve(value);
      else if (args[i] === '--evidence-dir') options.evidenceDir = path.resolve(value);
      else throw Error(`Unknown install-check option: ${args[i]}`);
    }
    await runInstallCheck(options);
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
