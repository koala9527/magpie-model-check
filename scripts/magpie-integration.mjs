import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import assert from 'node:assert/strict';
import { nativeTarget, nativeTarCommand } from './package-check.mjs';
import { runInstallCheck } from './install-check.mjs';
import { verifyClientProtocols } from './client-check.mjs';

// A pinned official portable host, not a stand-in for native runtime testing.
const magpieVersion = 'v0.1.4361';
const bunVersion = '1.4.4';
const digests = Object.freeze({
  'windows/amd64': 'c67eba8a1a938d7e4196d712c7474d55b31b81e66d4681920d0a87c810b08e73',
  'windows/arm64': 'f79410da2046c0b90b2300c480ce823622cca074bc6f3d2ce09d7c9386579450',
  'darwin/amd64': 'e722fcc10a06787b8bf70ee9ae8cc3f164df91855a1dacdb3df0c057d3691de7',
  'darwin/arm64': '7aca6e9ca5e91b19247f7936c9d7b6a27947cb5319c0de4a8bc8c50cfa182901',
  'linux/amd64': '62144cd1e93102605820731fd7d5b5306ce98a3093fd3fc6ee4a2ea0210aadce',
  'linux/arm64': '73fd0118bbb21255834d1a98f00f59d602ba70072cf9ef1e50dddc4342ef308f',
});

export function magpieAsset({ goos, goarch }) {
  const sha256 = digests[`${goos}/${goarch}`];
  if (!sha256) throw Error(`Unsupported Magpie installation target: ${goos}/${goarch}`);
  const name = `magpie-cli-${goos}-${goarch}${goos === 'windows' ? '.exe' : ''}`;
  return { name, version: magpieVersion, sha256, url: `https://github.com/yetone/magpie/releases/download/${magpieVersion}/${name}` };
}

export async function downloadVerified(url, expectedSHA256, destination) {
  assert.match(expectedSHA256, /^[a-f0-9]{64}$/, 'An expected SHA-256 checksum is required');
  const response = await fetch(url, { signal: AbortSignal.timeout(180000) });
  assert.equal(response.ok, true, `Download HTTP ${response.status}: ${url}`);
  const bytes = Buffer.from(await response.arrayBuffer());
  const sha256 = createHash('sha256').update(bytes).digest('hex');
  assert.equal(sha256, expectedSHA256, `Download checksum mismatch: ${url}`);
  destination = path.resolve(destination); await fs.mkdir(path.dirname(destination), { recursive: true });
  const temporary = `${destination}.${randomUUID()}.tmp`;
  try { await fs.writeFile(temporary, bytes, { flag: 'wx', mode: 0o755 }); await fs.rename(temporary, destination); }
  finally { await fs.unlink(temporary).catch(error => { if (error.code !== 'ENOENT') throw error; }); }
  return { file: destination, url, sha256, size: bytes.length };
}

export async function downloadPortableMagpie(outputDir) {
  outputDir = path.resolve(outputDir); await fs.mkdir(outputDir, { recursive: true });
  const target = nativeTarget(), asset = magpieAsset(target);
  const magpie = await downloadVerified(asset.url, asset.sha256, path.join(outputDir, asset.name));
  const summary = { target, magpie_version: magpieVersion, magpie, runtime_validation: 'NOT_RUN' };
  const environment = { MAGPIE_BINARY: magpie.file };
  if (target.goos === 'windows') {
    const arch = target.goarch === 'amd64' ? 'x64' : 'aarch64', name = `bun-windows-${arch}.zip`;
    const base = `https://github.com/oven-sh/bun/releases/download/bun-v${bunVersion}`;
    const response = await fetch(`${base}/SHASUMS256.txt`, { signal: AbortSignal.timeout(30000) });
    assert.ok(response.ok, `Bun checksums HTTP ${response.status}`);
    const line = (await response.text()).split(/\r?\n/).find(line => line.trim().split(/\s+/).at(-1)?.replace(/^\*/, '') === name);
    assert.ok(line, `Bun checksum missing for ${name}`);
    const bun = await downloadVerified(`${base}/${name}`, line.trim().split(/\s+/)[0], path.join(outputDir, name));
    const unpacked = path.join(outputDir, 'bun'); await fs.mkdir(unpacked, { recursive: true });
    assert.ok(path.resolve(unpacked).startsWith(outputDir + path.sep));
    const list = spawnSync(nativeTarCommand(), ['-tf', bun.file], { encoding: 'utf8', windowsHide: true });
    assert.equal(list.status, 0, list.error?.message || list.stderr);
    for (const file of list.stdout.split(/\r?\n/).filter(Boolean)) {
      assert.ok(!file.startsWith('/') && !file.includes('\\') && !file.includes(':') && !file.split('/').includes('..'), `Invalid Bun archive path: ${file}`);
    }
    const extract = spawnSync(nativeTarCommand(), ['-xf', bun.file, '-C', unpacked], { encoding: 'utf8', windowsHide: true });
    assert.equal(extract.status, 0, extract.error?.message || extract.stderr);
    environment.MAGPIE_BUN = path.join(unpacked, `bun-windows-${arch}`, 'bun.exe');
    await fs.access(environment.MAGPIE_BUN);
    summary.bun_version = bunVersion; summary.bun = bun;
  }
  if (process.env.GITHUB_ENV) {
    for (const value of Object.values(environment)) assert.ok(!/[\r\n]/.test(value), 'Invalid environment path');
    await fs.appendFile(process.env.GITHUB_ENV, Object.entries(environment).map(([name, value]) => `${name}=${value}\n`).join(''));
  }
  await fs.writeFile(path.join(outputDir, 'host-download.json'), JSON.stringify({ ...summary, environment }, null, 2) + '\n');
  console.log(JSON.stringify({ ...summary, environment }, null, 2));
  return environment;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = { afterReady: verifyClientProtocols }, args = process.argv.slice(2);
    let download = false, outputDir;
    for (let i = 0; i < args.length; i++) {
      if (args[i] === '--download-magpie') { download = true; continue; }
      const flag = args[i], value = args[++i]; if (!value) throw Error(`Missing value for ${flag}`);
      if (flag === '--output-dir') outputDir = value;
      else if (flag === '--binary-dir') options.binaryDir = path.resolve(value);
      else if (flag === '--evidence-dir') options.evidenceDir = path.resolve(value);
      else if (flag === '--tarball') options.tarball = path.resolve(value);
      else throw Error(`Unknown Magpie integration option: ${flag}`);
    }
    if (download) { if (!outputDir) throw Error('--download-magpie requires --output-dir'); await downloadPortableMagpie(outputDir); }
    else { if (outputDir) throw Error('--output-dir is only used with --download-magpie'); await runInstallCheck(options); }
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
