import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const root = path.resolve(import.meta.dirname, '..');
const ignored = new Set(['.git', '.runtime', 'node_modules', 'artifacts']);
const goIgnored = new Set([...ignored, 'dist', 'bin', 'vendor', 'testdata']);
const frontendRoots = ['internal/check/web', 'internal/modelcheck/web'];

export async function discoverGoPatterns(directory = root) {
  const sourceEntry = entry => !entry.isSymbolicLink() && !goIgnored.has(entry.name) && !/^[._]/.test(entry.name);
  async function containsGo(dir) {
    const entries = await fs.readdir(dir, { withFileTypes: true });
    // Match Go's module boundary: nested modules are not part of this suite.
    if (entries.some(entry => entry.name === 'go.mod')) return false;
    for (const entry of entries.filter(sourceEntry)) {
      if (entry.isFile() && entry.name.endsWith('.go')) return true;
      if (entry.isDirectory() && await containsGo(path.join(dir, entry.name))) return true;
    }
    return false;
  }
  const entries = (await fs.readdir(directory, { withFileTypes: true })).filter(sourceEntry);
  const patterns = entries.some(entry => entry.isFile() && entry.name.endsWith('.go')) ? ['.'] : [];
  for (const entry of entries) {
    if (entry.isDirectory() && await containsGo(path.join(directory, entry.name))) patterns.push(`./${entry.name}/...`);
  }
  // Keep Go's build-tag/GOOS filtering rather than listing individual files.
  return patterns.toSorted();
}

export async function discoverNodeTests(directory = root, trees = ['plugin', 'scripts', ...frontendRoots]) {
  const files = [];
  async function visit(dir) {
    let entries;
    try { entries = await fs.readdir(dir, { withFileTypes: true }); }
    catch (error) { if (error.code === 'ENOENT') return; throw error; }
    for (const entry of entries) {
      if (ignored.has(entry.name) || entry.isSymbolicLink()) continue;
      const file = path.join(dir, entry.name);
      if (entry.isDirectory()) await visit(file);
      else if (/\.test\.(?:mjs|js)$/.test(entry.name)) files.push(file);
    }
  }
  for (const tree of trees) await visit(path.join(directory, tree));
  return files.toSorted();
}

export async function testPlan(directory = root, mode = 'all') {
  if (!['all', 'go', 'vet', 'race', 'node', 'plugin', 'frontend'].includes(mode)) throw Error(`Unknown test mode: ${mode}`);
  const patterns = ['all', 'go', 'vet', 'race'].includes(mode) ? await discoverGoPatterns(directory) : [];
  if (['all', 'go', 'vet', 'race'].includes(mode) && !patterns.length) throw Error('No Go source packages found');
  const go = (name, args, cgo = '0') => ({ name, command: 'go', args, env: { CGO_ENABLED: cgo } });
  if (mode === 'race') return [go('Go race (required; CGO/compiler failures are fatal)', ['test', '-race', ...patterns], '1')];
  if (mode === 'go') return [go('Retained and new Go tests', ['test', ...patterns])];
  if (mode === 'vet') return [go('Go vet', ['vet', ...patterns])];
  const trees = mode === 'plugin' ? ['plugin'] : mode === 'frontend' ? frontendRoots : undefined;
  const tests = await discoverNodeTests(directory, trees);
  if (!tests.length) throw Error(`No Node tests found for ${mode}`);
  const node = { name: 'Retained Node and frontend module tests', command: process.execPath, args: ['--test', '--test-concurrency=2', ...tests] };
  if (mode !== 'all') return [node];
  return [
    go('Retained and new Go tests', ['test', ...patterns]),
    go('Go vet', ['vet', ...patterns]),
    node,
    { name: 'Actual npm packaged-content check', command: process.execPath, args: [path.join(directory, 'scripts/package-check.mjs')] },
  ];
}

export function runTestPlan(plan, { cwd = root, run = spawnSync } = {}) {
  for (const step of plan) {
    console.log(`GATE: ${step.name}\n$ ${step.command} ${step.args.join(' ')}`);
    const result = run(step.command, step.args, { cwd, env: { ...process.env, ...step.env }, stdio: 'inherit', windowsHide: true });
    if (result.error) console.error(result.error.message);
    const status = result.status ?? 1;
    if (status !== 0) { console.error(`FAIL: ${step.name} (exit ${status}${result.signal ? `, signal ${result.signal}` : ''})`); return status; }
    console.log(`PASS: ${step.name}`);
  }
  return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const args = process.argv.slice(2);
    if (args.length > 1 || (args[0] && !/^--(?:race|go|vet|node|plugin|frontend)$/.test(args[0]))) throw Error('Expected one of --race, --go, --vet, --node, --plugin, --frontend');
    process.exitCode = runTestPlan(await testPlan(root, args[0]?.slice(2) || 'all'));
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
