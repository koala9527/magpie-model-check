import fs from 'node:fs/promises';
import { existsSync, realpathSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import assert from 'node:assert/strict';
import { RELEASE_TARGETS, binaryName } from './build-release.mjs';

const root = path.resolve(import.meta.dirname, '..');
const hash = bytes => createHash('sha256').update(bytes).digest('hex');

export const PACKAGE_FILES = Object.freeze([
  'plugin/index.mjs', 'plugin/runtime.mjs', 'plugin/route.middleware.mjs', 'plugin/route-crypto.mjs',
  'plugin/page/index.html', 'plugin/page/loader.js', 'plugin/page/page.css', 'plugin/page/icon.svg',
  ...RELEASE_TARGETS.map(target => `bin/${binaryName(target)}`),
  'third_party/modeltrace/LICENSE', 'third_party/modeltrace/README.md',
  'third_party/lucide/LICENSE', 'third_party/fonts/SpaceGrotesk-OFL.txt', 'third_party/fonts/SpaceMono-OFL.txt',
  'README.md', 'LICENSE',
]);

export function nativeTarget() {
  const goos = { win32: 'windows', darwin: 'darwin', linux: 'linux' }[process.platform];
  const goarch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
  const target = { goos, goarch }; binaryName(target); return target;
}

export function auditPackageEntries(entries, pkg, { requireAllTargets = false } = {}) {
  assert.equal(pkg.name, 'magpie-model-check', 'Unexpected package name');
  assert.equal(pkg.main, './plugin/index.mjs', 'Unexpected plugin factory');
  assert.equal(pkg.magpie?.middleware, './plugin/route.middleware.mjs', 'Unexpected middleware');
  assert.equal(pkg.magpie?.page?.file, './plugin/page/index.html', 'Unexpected plugin page');
  const allowed = new Set([...PACKAGE_FILES, 'package.json']), found = new Set();
  for (const entry of entries) {
    assert.ok(typeof entry === 'string' && !entry.includes('\\') && !entry.startsWith('/') && !entry.split('/').some(part => !part || part === '.' || part === '..'), `Invalid package path: ${entry}`);
    assert.ok(allowed.has(entry), `Excluded or unexpected package file: ${entry}`);
    assert.ok(!found.has(entry), `Invalid duplicate package file: ${entry}`);
    found.add(entry);
  }
  const required = [...PACKAGE_FILES.filter(file => !file.startsWith('bin/')), 'package.json',
    ...(requireAllTargets ? RELEASE_TARGETS : [nativeTarget()]).map(target => `bin/${binaryName(target)}`)];
  for (const file of required) assert.ok(found.has(file), `Missing package file: ${file}`);
  return { file_count: found.size, binary_targets: RELEASE_TARGETS.filter(target => found.has(`bin/${binaryName(target)}`)) };
}

function npmCLI() {
  const nodeDir = path.dirname(process.execPath);
  const candidates = [process.env.npm_execpath, path.join(nodeDir, 'node_modules/npm/bin/npm-cli.js'), path.join(nodeDir, '../lib/node_modules/npm/bin/npm-cli.js')];
  for (const directory of (process.env.PATH || '').split(path.delimiter)) {
    const npm = path.join(directory, process.platform === 'win32' ? 'npm.cmd' : 'npm');
    if (existsSync(npm)) {
      const resolved = realpathSync(npm);
      candidates.push(resolved, path.join(path.dirname(resolved), 'node_modules/npm/bin/npm-cli.js'), path.join(directory, '../lib/node_modules/npm/bin/npm-cli.js'));
    }
  }
  const cli = candidates.find(candidate => candidate && /npm-cli\.js$/.test(candidate) && existsSync(candidate));
  if (!cli) throw Error('npm-cli.js was not found alongside Node or on PATH');
  return cli;
}

export function runNpm(args, { cwd = root, env = process.env, timeout = 120000 } = {}) {
  const result = spawnSync(process.execPath, [npmCLI(), ...args], { cwd, env, timeout, encoding: 'utf8', windowsHide: true, maxBuffer: 16 * 1024 * 1024 });
  if (result.error || result.status !== 0) throw Error(`npm ${args.join(' ')} failed (exit ${result.status ?? 1}): ${result.error?.message || result.stderr || result.stdout}`);
  return result;
}

export function nativeTarCommand(platform=process.platform,env=process.env){
  // Git Bash's GNU tar interprets C:\\... archive arguments as remote hosts.
  // Use Windows' native bsdtar for native Windows paths, not whichever tar
  // happens to be first in the runner's shell PATH.
  return platform==='win32'?path.win32.join(env.SystemRoot||'C:\\Windows','System32','tar.exe'):'tar';
}

function tar(args) {
  const result = spawnSync(nativeTarCommand(), args, { encoding: 'utf8', windowsHide: true, maxBuffer: 16 * 1024 * 1024 });
  if (result.error || result.status !== 0) throw Error(`tar failed (exit ${result.status ?? 1}): ${result.error?.message || result.stderr}`);
  return result.stdout;
}

export async function inspectPackage(tarball, options = {}) {
  const files = tar(['-tzf', tarball]).split(/\r?\n/).filter(file => file && !file.endsWith('/')).map(file => {
    assert.ok(file.startsWith('package/'), `Invalid archive prefix: ${file}`);
    return file.slice('package/'.length);
  });
  const pkg = JSON.parse(tar(['-xOzf', tarball, 'package/package.json']));
  const audit = auditPackageEntries(files, pkg, options);
  const bytes = await fs.readFile(tarball);
  return { ...audit, files: files.toSorted(), pkg, sha256: hash(bytes), size: bytes.length };
}

export async function verifyRelease(directory, { targets = RELEASE_TARGETS } = {}) {
  const manifest = JSON.parse(await fs.readFile(path.join(directory, 'manifest.json'), 'utf8'));
  assert.equal(manifest.format, 'magpie-model-check-release', 'Unexpected release manifest');
  assert.equal(manifest.schema, 1);
  assert.equal(manifest.command, './cmd/model-check');
  assert.equal(manifest.cgo_enabled, false, 'Release binaries must use modernc with CGO0');
  assert.equal(manifest.artifacts.length, targets.length, 'Missing build targets');
  const expected = new Set(targets.map(binaryName));
  for (const item of manifest.artifacts) {
    assert.ok(expected.delete(item.name), `Unexpected/duplicate release artifact: ${item.name}`);
    assert.equal(item.name, binaryName(item));
    assert.equal(item.compile_status, 'PASS');
    assert.equal(item.runtime_validation, 'NOT_RUN', 'Compilation must not claim native runtime validation');
    const bytes = await fs.readFile(path.join(directory, item.name));
    assert.equal(bytes.length, item.size, `Size mismatch: ${item.name}`);
    assert.equal(hash(bytes), item.sha256, `Checksum mismatch: ${item.name}`);
  }
  assert.equal(expected.size, 0, 'Missing release target');
  const checksums = await fs.readFile(path.join(directory, 'checksums.txt'), 'utf8');
  assert.equal(checksums, manifest.artifacts.map(item => `${item.sha256}  ${item.name}\n`).join(''), 'Checksum file does not match manifest');
  console.log(`PASS: ${manifest.artifacts.length} release binary sizes and SHA-256 checksums; compile-only, native runtime NOT_RUN`);
  return manifest;
}

export async function packPackage({ root: directory = root, outputDir, binaryDir, requireAllTargets = false } = {}) {
  assert.ok(outputDir, 'A destination for npm pack is required');
  directory = path.resolve(directory); outputDir = path.resolve(outputDir);
  await fs.mkdir(outputDir, { recursive: true });
  let staging;
  try {
    if (binaryDir) {
      // A release can be packed from independently built binaries without
      // replacing a main agent's in-use working-tree binary.
      staging = await fs.mkdtemp(path.join(outputDir, '.package-source-'));
      for (const file of [...PACKAGE_FILES, 'package.json']) {
        const source = file.startsWith('bin/') ? path.join(binaryDir, path.basename(file)) : path.join(directory, file);
        let stat;
        try { stat = await fs.lstat(source); } catch (error) { if (error.code === 'ENOENT' && file.startsWith('bin/')) continue; throw error; }
        assert.ok(stat.isFile() && !stat.isSymbolicLink(), `Invalid package source: ${file}`);
        await fs.mkdir(path.dirname(path.join(staging, file)), { recursive: true });
        await fs.copyFile(source, path.join(staging, file));
      }
    }
    const result = runNpm(['pack', '--json', '--ignore-scripts', '--pack-destination', outputDir], { cwd: staging || directory });
    const metadata = JSON.parse(result.stdout);
    assert.equal(metadata.length, 1, 'Expected one npm package');
    assert.equal(path.basename(metadata[0].filename), metadata[0].filename, 'Invalid npm package filename');
    const tarball = path.join(outputDir, metadata[0].filename);
    const inspected = await inspectPackage(tarball, { requireAllTargets });
    assert.deepEqual(inspected.files, metadata[0].files.map(file => file.path).toSorted(), 'npm metadata differs from the actual archive');
    const bytes = await fs.readFile(tarball);
    const packed = { tarball, files: inspected.files, pkg: inspected.pkg, sha256: hash(bytes), size: bytes.length, binary_targets: inspected.binary_targets, command: ['npm', 'pack', '--json', '--ignore-scripts', '--pack-destination', outputDir], exit_status: result.status };
    console.log(`PASS: actual npm pack, ${packed.files.length} allowlisted files, ${packed.binary_targets.length} binary targets; no private runtime/data/artifacts/retired helpers`);
    return packed;
  } finally {
    if (staging) {
      assert.ok(path.resolve(staging).startsWith(outputDir + path.sep));
      await fs.rm(staging, { recursive: true, force: true });
    }
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  let temporary;
  try {
    const options = {}, args = process.argv.slice(2);
    let releaseDir;
    for (let i = 0; i < args.length; i++) {
      if (args[i] === '--require-all-targets') options.requireAllTargets = true;
      else if (['--output-dir', '--binary-dir', '--release-dir'].includes(args[i])) {
        const flag = args[i], value = args[++i]; if (!value) throw Error(`Missing value for ${flag}`);
        if (flag === '--output-dir') options.outputDir = value;
        else if (flag === '--binary-dir') options.binaryDir = value;
        else releaseDir = value;
      } else throw Error(`Unknown package-check option: ${args[i]}`);
    }
    if (releaseDir) { await verifyRelease(path.resolve(releaseDir)); options.requireAllTargets = true; }
    if (!options.outputDir) options.outputDir = temporary = await fs.mkdtemp(path.join(os.tmpdir(), 'model-package-check-'));
    const packed = await packPackage(options);
    if (!temporary) await fs.writeFile(path.join(options.outputDir, 'package-check.json'), JSON.stringify(packed, null, 2) + '\n');
  } catch (error) { console.error(error.message); process.exitCode = 1; }
  finally {
    if (temporary) { assert.ok(path.resolve(temporary).startsWith(path.resolve(os.tmpdir()) + path.sep)); await fs.rm(temporary, { recursive: true, force: true }); }
  }
}
