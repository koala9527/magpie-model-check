import { spawnSync } from 'node:child_process';
import fs from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const defaultRoot = path.resolve(import.meta.dirname, '..');
const hash = bytes => createHash('sha256').update(bytes).digest('hex');

export const RELEASE_TARGETS = Object.freeze(
  ['windows', 'darwin', 'linux'].flatMap(goos => ['amd64', 'arm64'].map(goarch => Object.freeze({ goos, goarch }))),
);

export function binaryName({ goos, goarch }) {
  if (!RELEASE_TARGETS.some(target => target.goos === goos && target.goarch === goarch)) throw Error(`Unsupported build target: ${goos}/${goarch}`);
  return `magpie-model-check-${goos}-${goarch}${goos === 'windows' ? '.exe' : ''}`;
}

async function atomicCopy(source, destination) {
  const temporary = `${destination}.${randomUUID()}.tmp`;
  await fs.copyFile(source, temporary);
  await fs.rename(temporary, destination);
}

async function snapshotBinaries(binDir, outputDir) {
  const snapshot = { format: 'magpie-model-check-binary-regression', created_at: new Date().toISOString(), files: [] };
  let entries;
  try { entries = await fs.readdir(binDir, { withFileTypes: true }); }
  catch (error) { if (error.code !== 'ENOENT') throw error; entries = []; }
  const relative = `binary-regression/${randomUUID()}`;
  for (const entry of entries.toSorted((a, b) => a.name.localeCompare(b.name))) {
    if (!entry.isFile()) continue;
    const bytes = await fs.readFile(path.join(binDir, entry.name));
    await fs.mkdir(path.join(outputDir, relative), { recursive: true });
    await fs.writeFile(path.join(outputDir, relative, entry.name), bytes);
    snapshot.files.push({ name: entry.name, size: bytes.length, sha256: hash(bytes), snapshot: `${relative}/${entry.name}` });
  }
  await fs.writeFile(path.join(outputDir, 'binary-regression.json'), JSON.stringify(snapshot, null, 2) + '\n');
}

export async function buildRelease({ root = defaultRoot, outputDir = path.join(root, 'dist'), binDir = path.join(root, 'bin'), targets = RELEASE_TARGETS, run = spawnSync } = {}) {
  root = path.resolve(root); outputDir = path.resolve(outputDir); binDir = path.resolve(binDir);
  if (!targets.length || new Set(targets.map(binaryName)).size !== targets.length) throw Error('Expected distinct supported build targets');
  const pkg = JSON.parse(await fs.readFile(path.join(root, 'package.json'), 'utf8'));
  if (pkg.name !== 'magpie-model-check' || typeof pkg.version !== 'string') throw Error('Expected magpie-model-check package metadata');
  await fs.mkdir(outputDir, { recursive: true });
  // Preserve existing model and legacy binaries before any publication. Nothing
  // here removes old bin files; their retirement belongs to the main migration.
  await snapshotBinaries(binDir, outputDir);
  const staging = await fs.mkdtemp(path.join(outputDir, '.compile-'));
  const manifest = { format: 'magpie-model-check-release', schema: 1, version: pkg.version, command: './cmd/model-check', cgo_enabled: false, artifacts: [] };
  try {
    for (const target of targets) {
      const name = binaryName(target), output = path.join(staging, name);
      const args = ['build', '-mod=readonly', '-trimpath', '-buildvcs=false', '-ldflags=-s -w', '-o', output, './cmd/model-check'];
      console.log(`COMPILE: ${target.goos}/${target.goarch} CGO_ENABLED=0`);
      const result = run('go', args, { cwd: root, env: { ...process.env, CGO_ENABLED: '0', GOOS: target.goos, GOARCH: target.goarch }, stdio: 'inherit', windowsHide: true });
      if (result.error || result.status !== 0) throw Error(`Build failed: ${name} (exit ${result.status ?? 1}${result.error ? `; ${result.error.message}` : ''})`);
      const bytes = await fs.readFile(output);
      if (!bytes.length) throw Error(`Build failed: empty ${name}`);
      manifest.artifacts.push({ name, ...target, size: bytes.length, sha256: hash(bytes), compile_status: 'PASS', runtime_validation: 'NOT_RUN' });
    }
    // A failed/interrupted compiler does not publish a partial set of binaries.
    await fs.mkdir(binDir, { recursive: true });
    for (const artifact of manifest.artifacts) {
      await atomicCopy(path.join(staging, artifact.name), path.join(outputDir, artifact.name));
      await atomicCopy(path.join(staging, artifact.name), path.join(binDir, artifact.name));
    }
    await fs.writeFile(path.join(staging, 'checksums.txt'), manifest.artifacts.map(item => `${item.sha256}  ${item.name}\n`).join(''));
    await fs.writeFile(path.join(staging, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
    await atomicCopy(path.join(staging, 'checksums.txt'), path.join(outputDir, 'checksums.txt'));
    await atomicCopy(path.join(staging, 'manifest.json'), path.join(outputDir, 'manifest.json'));
  } finally {
    if (!path.resolve(staging).startsWith(outputDir + path.sep)) throw Error('Compile staging directory escaped output directory');
    await fs.rm(staging, { recursive: true, force: true });
  }
  console.log(`PASS: ${manifest.artifacts.length} CGO0 binaries and SHA-256 checksums; runtime validation NOT_RUN (native installation is a separate gate)`);
  return manifest;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = {}, selected = [];
    const args = process.argv.slice(2);
    for (let i = 0; i < args.length; i += 2) {
      const value = args[i + 1];
      if (!value) throw Error(`Missing value for ${args[i]}`);
      if (args[i] === '--output-dir') options.outputDir = value;
      else if (args[i] === '--bin-dir') options.binDir = value;
      else if (args[i] === '--target') { const [goos, goarch] = value.split('/'); const target = { goos, goarch }; binaryName(target); selected.push(target); }
      else throw Error(`Unknown build option: ${args[i]}`);
    }
    if (selected.length) options.targets = selected;
    await buildRelease(options);
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
