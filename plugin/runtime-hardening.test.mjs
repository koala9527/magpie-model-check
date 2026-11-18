import test from 'node:test';
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { EventEmitter } from 'node:events';
import { PassThrough, Writable } from 'node:stream';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { promisify } from 'node:util';
import { randomBytes, createHash } from 'node:crypto';
import { privateDirectory, provisionRouteKey, resolveGateway, watchConfiguration, prepareExecutable } from './runtime.mjs';

const runFile = promisify(childProcess.execFile);
const sourceRoot = fileURLToPath(new URL('../', import.meta.url));
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
};
async function bounded(promise, code = 'FIXTURE_DEADLINE', ms = 8000) {
  let timer;
  try { return await Promise.race([promise, new Promise((_, reject) => { timer = setTimeout(() => reject(Error(code)), ms); })]); }
  finally { clearTimeout(timer); }
}

async function fixture(t) {
  const parent = await fs.realpath(os.tmpdir());
  const dir = await fs.mkdtemp(path.join(parent, 'model-runtime-hardening-'));
  const cleanups = [];
  await fs.writeFile(path.join(dir, '.owned-runtime-test'), 'runtime-hardening');
  t.after(async () => {
    for (const cleanup of cleanups.reverse()) await cleanup();
    const resolved = await fs.realpath(dir);
    const relative = path.relative(parent, resolved);
    assert.ok(relative && !relative.startsWith('..') && !path.isAbsolute(relative), 'FIXTURE_CLEANUP_OUTSIDE_TEMP');
    assert.ok(await fs.readFile(path.join(resolved, '.owned-runtime-test'), 'utf8') === 'runtime-hardening', 'FIXTURE_OWNERSHIP_MISSING');
    await fs.rm(resolved, { recursive: true, force: true });
  });
  const packageRoot = path.join(dir, 'package');
  const configDir = path.join(dir, 'config');
  await fs.mkdir(path.join(packageRoot, 'plugin'), { recursive: true });
  await fs.mkdir(configDir);
  await fs.writeFile(path.join(packageRoot, 'package.json'), JSON.stringify({ name: 'magpie-model-check', type: 'module', magpie: { middleware: './plugin/route.middleware.mjs' } }));
  await fs.copyFile(path.join(sourceRoot, 'plugin/runtime.mjs'), path.join(packageRoot, 'plugin/runtime.mjs'));
  await fs.writeFile(path.join(packageRoot, 'plugin/route.middleware.mjs'), 'export function onRequest(body){return body;}\n');
  const module = await import(pathToFileURL(path.join(packageRoot, 'plugin/runtime.mjs')).href);
  const options = { dataDir: path.join(dir, 'data'), legacyDB: path.join(dir, 'missing.db'), binaryPath: path.join(dir, 'fixture-model-check'), gatewayURL: 'http://127.0.0.1:9', port: 34791 };
  await fs.writeFile(options.binaryPath, 'fake-child fixture');
  const file = path.join(configDir, 'plugins.json');
  const writeConfig = (plugins = [{ spec: packageRoot, options }]) => fs.writeFile(file, JSON.stringify({ plugins }));
  await writeConfig();
  return { dir, packageRoot, configDir, file, options, module, writeConfig, cleanup: fn => cleanups.push(fn) };
}

function fakeChild(f, { autoReady = false, exitOnEnd = true } = {}) {
  const child = new EventEmitter();
  const boot = deferred(), exited = deferred();
  let calls = 0, spawnOptions, protocolGuard;
  child.exitCode = null;
  child.signalCode = null;
  child.killed = false;
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.stdout.on('error', () => {});
  child.stderr.on('error', () => {});
  child.finish = (code = 0, signal = null) => {
    if (child.exitCode !== null || child.signalCode !== null) return;
    child.exitCode = code;
    child.signalCode = signal;
    child.emit('exit', code, signal);
    child.stdout.end(); child.stderr.end();
    child.emit('close', code, signal);
    exited.resolve();
  };
  child.ready = () => {
    if (child.exitCode !== null || child.signalCode !== null) return;
    child.stdout.write(JSON.stringify({ origin: `http://127.0.0.1:${f.options.port}`, control: randomBytes(32).toString('hex') }) + '\n');
  };
  child.stdin = new Writable({ write(chunk, _encoding, done) { protocolGuard = JSON.parse(String(chunk)).protocol_guard; done(); boot.resolve(); if (autoReady) queueMicrotask(child.ready); } });
  child.stdin.on('finish', () => { if (exitOnEnd) queueMicrotask(() => child.finish()); });
  child.kill = () => { child.killed = true; child.finish(null, 'SIGTERM'); return true; };
  const original = childProcess.spawn;
  childProcess.spawn = (_binary, _args, options) => { calls++; spawnOptions = options; return child; };
  syncBuiltinESMExports();
  f.cleanup(() => { childProcess.spawn = original; syncBuiltinESMExports(); });
  f.cleanup(async () => { child.stdin.end(); if (child.exitCode === null && child.signalCode === null) child.kill(); await bounded(exited.promise); });
  return { child, boot: boot.promise, exited: exited.promise, get calls() { return calls; }, get spawnOptions() { return spawnOptions; }, get protocolGuard() { return protocolGuard; } };
}

function attempt(f) {
  const result = f.module.startRuntime({ directory: f.configDir }, f.options).then(runtime => ({ runtime }), error => ({ error }));
  f.cleanup(async () => { const outcome = await bounded(result); await outcome.runtime?.stop(); });
  return result;
}

const ps = path.join(process.env.SystemRoot || 'C:\\Windows', 'System32/WindowsPowerShell/v1.0/powershell.exe');
async function acl(file, operation) {
  const script = `$ErrorActionPreference='Stop';$p=$env:MODEL_CHECK_TEST_PATH;$sid=[System.Security.Principal.WindowsIdentity]::GetCurrent().User;
$directory=([System.IO.File]::GetAttributes($p) -band [System.IO.FileAttributes]::Directory) -ne 0;
$sections=[System.Security.AccessControl.AccessControlSections]'Owner,Access';
$a=if($directory){[System.IO.Directory]::GetAccessControl($p,$sections)}else{[System.IO.File]::GetAccessControl($p,$sections)};
${operation}
`;
  const systemRoot=process.env.SystemRoot||'C:\\Windows';
  const { stdout } = await runFile(ps, ['-NoLogo', '-NoProfile', '-NonInteractive', '-Command', script], { windowsHide: true, timeout: 10000, env: { SystemRoot:systemRoot,MODEL_CHECK_TEST_PATH: file } });
  return stdout.trim() ? JSON.parse(stdout) : undefined;
}
const publicAcl = `$all=[System.Security.Principal.SecurityIdentifier]::new('S-1-1-0');$inherit=if($directory){[System.Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'}else{[System.Security.AccessControl.InheritanceFlags]::None};$a.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new($all,'FullControl',$inherit,'None','Allow'));if($directory){[System.IO.Directory]::SetAccessControl($p,$a)}else{[System.IO.File]::SetAccessControl($p,$a)};`;
const inspectAcl = `$rules=@($a.GetAccessRules($true,$true,[System.Security.Principal.SecurityIdentifier]));$unexpected=0;foreach($rule in $rules){if($rule.IdentityReference.Value -ne $sid.Value){$unexpected++}};[Console]::WriteLine(('{{"protected":{0},"unexpected":{1},"owner":{2}}}' -f $a.AreAccessRulesProtected.ToString().ToLowerInvariant(),$unexpected,($a.GetOwner([System.Security.Principal.SecurityIdentifier]).Value -eq $sid.Value).ToString().ToLowerInvariant()));`;

test('configured gateway environment variables reject whitespace-only values', () => {
  for (const blank of ['', ' ', '\t\n']) {
    assert.throws(() => resolveGateway({ gatewayKeyEnv: 'RUNTIME_TEST_KEY' }, { RUNTIME_TEST_KEY: blank }), { message: 'GATEWAY_KEY_ENV_MISSING' });
  }
});

test('Windows ACL helpers avoid module autoloading and do not inherit application secrets',async t=>{
  const f=await fixture(t),execFile=childProcess.execFile,platform=Object.getOwnPropertyDescriptor(process,'platform'),calls=[];
  Object.defineProperty(process,'platform',{...platform,value:'win32'});
  childProcess.execFile=(_binary,_args,options,callback)=>{calls.push(options.env);queueMicrotask(()=>callback(null,'',''));return new EventEmitter();};
  syncBuiltinESMExports();
  f.cleanup(()=>{Object.defineProperty(process,'platform',platform);childProcess.execFile=execFile;syncBuiltinESMExports();});
  const module=await import(pathToFileURL(path.join(f.packageRoot,'plugin/runtime.mjs')).href+'?acl-environment');
  await module.privateDirectory(path.join(f.dir,'private'));
  assert.equal(calls.length,2);
  for(const env of calls){
    assert.deepEqual(Object.keys(env).toSorted(),['MODEL_CHECK_PRIVATE_PATH','MODEL_CHECK_PRIVATE_TREE','SystemRoot'].toSorted());
  }
  const source=await fs.readFile(path.join(f.packageRoot,'plugin/runtime.mjs'),'utf8');
  const script=source.match(/const windowsAclScript=`([\s\S]*?)`;/)[1];
  assert.doesNotMatch(script,/\b(?:Get-Item|Get-Acl|Set-Acl)\b/,'PRIVATE_ACL_DEPENDS_ON_MODULE_AUTOLOAD');
});

test('native binaries unpacked from a Windows-created tarball become executable before spawn', async t => {
  const f = await fixture(t), file = path.join(f.dir, 'native-binary');
  await fs.writeFile(file, 'not executed by this test', {mode:0o600});
  const before=await fs.stat(file),probe=await fs.open(file,'r');
  const prototype=Object.getPrototypeOf(probe);await probe.close();
  const original=prototype.chmod,calls=[];
  prototype.chmod=async function(mode){calls.push({ino:(await this.stat()).ino,mode});return original.call(this,mode);};
  f.cleanup(()=>{prototype.chmod=original;});
  assert.equal(await prepareExecutable(file),file);
  assert.deepEqual(calls,[{ino:before.ino,mode:0o700}]);
  const hardlink = path.join(f.dir, 'binary-hardlink');
  await fs.link(file, hardlink);
  await assert.rejects(prepareExecutable(hardlink), {message:'PRIVATE_BINARY_COPY_REQUIRED'});
  await assert.rejects(prepareExecutable(f.dir), {message:'PRIVATE_FILE_REQUIRED'});
  assert.equal(calls.length,1,'UNSAFE_BINARY_PERMISSIONS_CHANGED');
});

test('Bun hardlinked package binaries execute from a private copy without chmoding the cache', async t => {
  const f=await fixture(t);
  const source=f.options.binaryPath;
  const cacheAlias=path.join(f.dir,'bun-cache-binary');
  await fs.chmod(source,0o644);
  await fs.link(source,cacheAlias);
  const original=await fs.readFile(source);
  const before=await fs.stat(source);
  assert.equal(before.nlink,2,'The fixture must reproduce actual Bun install links');
  const copyDir=path.join(f.dir,'private-executables');
  const prepared=await prepareExecutable(source,copyDir);
  assert.notEqual(prepared,source);
  assert.equal(path.dirname(prepared),copyDir);
  assert.deepEqual(await fs.readFile(prepared),original);
  assert.equal((await fs.stat(prepared)).nlink,1);
  if(process.platform!=='win32')assert.equal((await fs.stat(prepared)).mode&0o777,0o700);
  assert.equal((await fs.stat(cacheAlias)).mode,before.mode,'Package cache mode must stay unchanged');
  assert.equal(await prepareExecutable(source,copyDir),prepared,'Unchanged source reuses the same checked private executable');
  await fs.link(prepared,path.join(f.dir,'unsafe-private-cache-alias'));
  await assert.rejects(prepareExecutable(source,copyDir),{message:'PRIVATE_FILE_REQUIRED'});
});

test('executable preparation checks cancellation after closing the source handle', async t => {
  const f=await fixture(t),controller=new AbortController(),open=fs.open;
  fs.open=async function(file,...args){const handle=await open.call(this,file,...args);if(file===f.options.binaryPath){const close=handle.close.bind(handle);handle.close=async()=>{await close();controller.abort();};}return handle;};
  f.cleanup(()=>{fs.open=open;});
  await assert.rejects(prepareExecutable(f.options.binaryPath,undefined,controller.signal),{name:'AbortError'});
});

test('a hardlinked source never becomes chmod-safe when an alias disappears before open',async t=>{
  const f=await fixture(t),source=f.options.binaryPath,alias=path.join(f.dir,'cache-alias');
  await fs.chmod(source,0o644);await fs.link(source,alias);
  const before=await fs.stat(source),open=fs.open;
  fs.open=async function(file,...args){if(file===source)await fs.unlink(alias);return open.call(this,file,...args);};
  f.cleanup(()=>{fs.open=open;});
  const prepared=await prepareExecutable(source,path.join(f.dir,'executables'));
  assert.notEqual(prepared,source,'SHARED_SOURCE_BECAME_CHMOD_SAFE');
  assert.equal((await fs.stat(source)).mode,before.mode,'SHARED_SOURCE_MODE_CHANGED');
});

test('native source opens reject symlink traversal at the filesystem boundary',async t=>{
  const f=await fixture(t),open=fs.open;
  const {constants}=await import('node:fs');
  // Windows has no O_NOFOLLOW; the production helper runs only on POSIX.
  if(!constants.O_NOFOLLOW){assert.equal(process.platform,'win32');return;}
  fs.open=async function(file,flags,...args){
    if(file===f.options.binaryPath)assert.ok((flags&constants.O_NOFOLLOW)!==0,'NATIVE_SOURCE_OPEN_FOLLOWS_SYMLINKS');
    return open.call(this,file,flags,...args);
  };
  f.cleanup(()=>{fs.open=open;});
  await prepareExecutable(f.options.binaryPath);
});

test('private tree hardening keeps explicitly named executable copies runnable across startups',async t=>{
  const f=await fixture(t),dir=path.join(f.dir,'runtime'),file=path.join(dir,'binary');
  await fs.mkdir(dir);await fs.writeFile(file,'fixture');await fs.chmod(file,0o755);
  const platform=Object.getOwnPropertyDescriptor(process,'platform'),lstat=fs.lstat,chmod=fs.chmod,calls=[];
  Object.defineProperty(process,'platform',{...platform,value:'linux'});
  fs.lstat=async(...args)=>{const stat=await lstat(...args);if(args[0]===file)stat.mode=0o100600;return stat;};
  fs.chmod=async(target,mode)=>{if(target===file)calls.push(mode);return chmod(target,mode);};
  f.cleanup(()=>{Object.defineProperty(process,'platform',platform);fs.lstat=lstat;fs.chmod=chmod;});
  await privateDirectory(dir,undefined,dir);
  assert.deepEqual(calls,[0o700],'EXECUTABLE_INVALIDATED_BY_ANOTHER_STARTUP');
});

test('cancellation before native spawn cannot create an unmanaged child',async t=>{
  const f=await fixture(t),fake=fakeChild(f,{autoReady:true});
  const platform=Object.getOwnPropertyDescriptor(process,'platform');
  Object.defineProperty(process,'platform',{...platform,value:'linux'});
  f.cleanup(()=>Object.defineProperty(process,'platform',platform));
  const open=fs.open;
  fs.open=async function(file,...args){const handle=await open.call(this,file,...args);if(file===f.options.binaryPath){const close=handle.close.bind(handle);handle.close=async()=>{await close();process.stdin.emit('end');};}return handle;};
  f.cleanup(()=>{fs.open=open;});
  const outcome=await bounded(attempt(f));
  assert.equal(outcome.error?.message,'SERVICE_RELOADING');
  assert.equal(fake.calls,0,'CANCELLED_RUNTIME_SPAWNED_UNOWNED_CHILD');
});

test('disabled, missing, duplicate, and stale own entries fail before spawning', async t => {
  for (const mode of ['disabled', 'missing', 'duplicate', 'stale']) await t.test(mode, async t => {
    const f = await fixture(t), fake = fakeChild(f, { autoReady: true });
    const own = { spec: f.packageRoot, options: f.options };
    const entries = mode === 'disabled' ? [{ ...own, off: true }] : mode === 'missing' ? [{ spec: 'unrelated-plugin' }] : mode === 'duplicate' ? [own, own] : [{ ...own, options: { ...f.options, port: f.options.port + 1 } }];
    await f.writeConfig(entries);
    const outcome = await bounded(attempt(f));
    assert.ok(!!outcome.error && fake.calls === 0, 'OWN_ENTRY_NOT_VALIDATED_BEFORE_SPAWN');
  });
});

test('repository registrations recognize this package but not a lookalike Git repo', async t => {
  for (const mode of ['github', 'ssh', 'lookalike']) await t.test(mode, async t => {
    const f = await fixture(t), manifestFile = path.join(f.packageRoot, 'package.json');
    const manifest = JSON.parse(await fs.readFile(manifestFile, 'utf8'));
    manifest.repository = { type: 'git', url: 'git+ssh://git@github.com/koala9527/magpie-model-check.git' };
    await fs.writeFile(manifestFile, JSON.stringify(manifest));
    const spec = mode === 'github' ? 'github:koala9527/magpie-model-check#main' : mode === 'ssh' ? 'git@github.com:koala9527/magpie-model-check.git' : 'github:someone-else/magpie-model-check';
    await f.writeConfig([{ spec, options: f.options }]);
    const fake = fakeChild(f, { autoReady: true }), outcome = await bounded(attempt(f));
    assert.equal(!!outcome.runtime, mode !== 'lookalike', 'REPOSITORY_REGISTRATION_IDENTITY_MISMATCH');
    if (outcome.runtime) { outcome.runtime.stop(); await bounded(fake.exited); }
    else assert.equal(fake.calls, 0);
  });
});

test('startup compares the original configuration after delayed child readiness', async t => {
  for (const mode of ['disable', 'remove', 'options']) await t.test(mode, async t => {
    const f = await fixture(t), fake = fakeChild(f), pending = attempt(f);
    await bounded(fake.boot);
    const own = { spec: f.packageRoot, options: f.options };
    await f.writeConfig(mode === 'disable' ? [{ ...own, off: true }] : mode === 'remove' ? [] : [{ ...own, options: { ...f.options, port: f.options.port + 1 } }]);
    fake.child.ready();
    const outcome = await bounded(pending);
    assert.ok(outcome.error?.message === 'SERVICE_RELOADING', 'STARTUP_CONFIGURATION_CHANGE_WAS_MISSED');
    await bounded(fake.exited);
    assert.ok(!await fs.access(path.join(f.packageRoot, 'plugin/.runtime/route-secret.mjs')).then(() => true, () => false), 'CANCELLED_STARTUP_PUBLISHED_A_ROUTE_KEY');
  });
});

test('a rapid disable and re-enable cannot hide a startup configuration generation change', async t => {
  const f = await fixture(t), fake = fakeChild(f);
  const original = globalThis.setInterval;
  // Suppress polling so this deterministically exercises the startup snapshot,
  // rather than depending on whether a timer happens to see the short off-state.
  globalThis.setInterval = () => ({ unref() {} });
  f.cleanup(() => { globalThis.setInterval = original; });
  const pending = attempt(f);
  await bounded(fake.boot);
  await f.writeConfig([{ spec: f.packageRoot, options: f.options, off: true }]);
  await pause(20);
  await f.writeConfig();
  fake.child.ready();
  const outcome = await bounded(pending);
  assert.ok(outcome.error?.message === 'SERVICE_RELOADING', 'STARTUP_CONFIGURATION_GENERATION_WAS_MISSED');
  await bounded(fake.exited);
});

test('configuration changes during atomic route provisioning cancel startup and clean material', async t => {
  const f = await fixture(t), fake = fakeChild(f), entered = deferred(), release = deferred();
  const original = fs.rename;
  fs.rename = async (from, to) => {
    if (to === path.join(f.packageRoot, 'plugin/.runtime/route-secret.mjs')) { entered.resolve(); await release.promise; }
    return original(from, to);
  };
  f.cleanup(() => { release.resolve(); fs.rename = original; });
  const pending = attempt(f);
  await bounded(fake.boot); fake.child.ready();
  await bounded(entered.promise);
  await f.writeConfig([{ spec: f.packageRoot, options: f.options, off: true }]);
  await pause(350); release.resolve();
  const outcome = await bounded(pending);
  assert.ok(outcome.error?.message === 'SERVICE_RELOADING', 'PROVISION_CONFIGURATION_CHANGE_WAS_MISSED');
  await bounded(fake.exited);
  const files = await fs.readdir(path.join(f.packageRoot, 'plugin/.runtime'));
  assert.ok(files.length === 0, 'CANCELLED_PROVISION_LEFT_PRIVATE_MATERIAL');
});

test('a final configuration change rolls back a just-published startup key', async t => {
  for (const mode of ['absent', 'existing']) await t.test(mode, async t => {
    const f = await fixture(t);
    const target = path.join(f.packageRoot, 'plugin/.runtime/route-secret.mjs');
    if (mode === 'existing') await provisionRouteKey(f.packageRoot, randomBytes(32).toString('hex'));
    const before = await fs.readFile(target).catch(error => { if (error.code !== 'ENOENT') throw error; });
    const interval = globalThis.setInterval, utimes = fs.utimes;
    globalThis.setInterval = () => ({ unref() {} });
    f.cleanup(() => { globalThis.setInterval = interval; fs.utimes = utimes; });
    fs.utimes = async (file, ...times) => {
      await utimes(file, ...times);
      if (file === path.join(f.packageRoot, 'plugin/route.middleware.mjs')) {
        await f.writeConfig([{ spec: f.packageRoot, options: f.options, off: true }]);
      }
    };
    const fake = fakeChild(f, { autoReady: true });
    const outcome = await bounded(attempt(f));
    assert.equal(outcome.error?.message, 'SERVICE_RELOADING', 'FINAL_CONFIGURATION_CHANGE_WAS_MISSED');
    await bounded(fake.exited);
    const after = await fs.readFile(target).catch(error => { if (error.code !== 'ENOENT') throw error; });
    assert.ok(before ? after?.equals(before) : after === undefined, 'FAILED_STARTUP_DID_NOT_ROLL_BACK_PUBLICATION');
    const files = await fs.readdir(path.dirname(target));
    assert.ok(files.length === (before ? 1 : 0), 'FAILED_STARTUP_LEFT_TEMP_MATERIAL');
  });
});

test('a child exiting after readiness is a failed startup, not a closed runtime return', async t => {
  const f = await fixture(t), fake = fakeChild(f), pending = attempt(f);
  await bounded(fake.boot); fake.child.ready(); fake.child.finish(1);
  const outcome = await bounded(pending);
  assert.ok(outcome.error?.message === 'MODEL_CHECK_START_FAILED', 'EXIT_DURING_STARTUP_WAS_ACCEPTED');
  assert.ok(!await fs.access(path.join(f.packageRoot, 'plugin/.runtime/route-secret.mjs')).then(() => true, () => false), 'FAILED_STARTUP_PUBLISHED_A_ROUTE_KEY');
});

test('runtime children are hidden and receive an explicitly blank environment', async t => {
  const f = await fixture(t), fake = fakeChild(f, { autoReady: true });
  const outcome = await bounded(attempt(f));
  assert.ok(!!outcome.runtime, 'FIXTURE_START_FAILED');
  assert.ok(fake.spawnOptions.windowsHide === true, 'CHILD_WINDOW_NOT_HIDDEN');
  assert.ok(!!fake.spawnOptions.env && Object.keys(fake.spawnOptions.env).length === 0, 'CHILD_ENVIRONMENT_NOT_BLANK');
  outcome.runtime.stop();
  await bounded(fake.exited);
  assert.ok(outcome.runtime.closed && outcome.runtime.lifetime.aborted, 'STOP_DID_NOT_ABORT_LIFETIME');
});

test('runtime stop resolves only after the owned child has exited',async t=>{
  const f=await fixture(t),fake=fakeChild(f,{autoReady:true,exitOnEnd:false});
  const outcome=await bounded(attempt(f));assert.ok(outcome.runtime);
  let completed=false;
  const stopped=Promise.resolve(outcome.runtime.stop()).then(()=>{completed=true;});
  try{await pause(30);assert.equal(completed,false,'STOP_COMPLETED_BEFORE_CHILD_EXIT');}
  finally{fake.child.finish();}
  await bounded(stopped);assert.equal(completed,true);
});

test('boot protocol guard is private and declines unknown enabled metadata', async t => {
  for (const mode of ['known', 'unknown']) await t.test(mode, async t => {
    const f = await fixture(t), own = { spec: f.packageRoot, options: f.options };
    await f.writeConfig(mode === 'known' ? [{ spec: 'unreadable-but-off', off: true }, own] : [own, { spec: path.join(f.dir, 'unreadable-package') }]);
    const fake = fakeChild(f, { autoReady: true }), outcome = await bounded(attempt(f));
    assert.ok(!!outcome.runtime, 'PROTOCOL_GUARD_FIXTURE_START_FAILED');
    assert.equal(fake.protocolGuard, false, 'OBSERVER_METADATA_CANNOT_ATTEST_FINAL_WIRE_PACKET');
    assert.ok(!Object.hasOwn(outcome.runtime, 'protocol_guard'), 'BOOT_PROTOCOL_GUARD_BECAME_PUBLIC');
    outcome.runtime.stop(); await bounded(fake.exited);
  });
});

test('removing the configuration file closes an established watcher', async t => {
  const f = await fixture(t);
  let changed = 0;
  const stop = await watchConfiguration(f.file, () => { changed++; }, 20);
  f.cleanup(stop);
  await fs.unlink(f.file);
  await pause(120);
  assert.ok(changed === 1, 'REMOVED_CONFIGURATION_WAS_IGNORED');
});

test('private directories reject symlink or junction ancestors before creating a leaf', async t => {
  const f = await fixture(t), outside = path.join(f.dir, 'outside'), link = path.join(f.dir, 'redirect');
  await fs.mkdir(outside);
  await fs.symlink(outside, link, process.platform === 'win32' ? 'junction' : 'dir');
  const leaf = path.join(link, 'nested/private');
  await assert.rejects(privateDirectory(leaf), 'REPARSE_ANCESTOR_ACCEPTED');
  assert.ok(!await fs.access(path.join(outside, 'nested')).then(() => true, () => false), 'PRIVATE_DIRECTORY_FOLLOWED_A_LINK');
});

test('native Windows reparse verification runs before any directory creation', { skip: process.platform !== 'win32' }, async t => {
  const f = await fixture(t), leaf = path.join(f.dir, 'uncreated/private');
  const original = childProcess.execFile;
  childProcess.execFile = (_binary, _args, _options, callback) => {
    queueMicrotask(() => callback(Error('INJECTED_NATIVE_REPARSE_FAILURE')));
    return new EventEmitter();
  };
  syncBuiltinESMExports();
  f.cleanup(() => { childProcess.execFile = original; syncBuiltinESMExports(); });
  const module = await import(pathToFileURL(path.join(f.packageRoot, 'plugin/runtime.mjs')).href + '?native-path-check');
  await assert.rejects(module.privateDirectory(leaf));
  assert.ok(!await fs.access(path.join(f.dir, 'uncreated')).then(() => true, () => false), 'DIRECTORY_CREATED_BEFORE_NATIVE_REPARSE_VERIFICATION');
});

test('private directories repair explicit ACLs on existing directories and files', { skip: process.platform !== 'win32' }, async t => {
  const f = await fixture(t), dir = path.join(f.dir, 'private');
  await fs.mkdir(dir);
  const file = path.join(dir, 'existing.db');
  await fs.writeFile(file, 'fixture');
  await acl(dir, publicAcl); await acl(file, publicAcl);
  await privateDirectory(dir);
  for (const item of [dir, file]) {
    const state = await acl(item, inspectAcl);
    assert.ok(state.protected && state.unexpected === 0 && state.owner, 'EXPLICIT_OR_INHERITED_ACL_REMAINS_PUBLIC');
  }
});

test('route provisioning rejects nonregular or hardlinked existing key files without temp remnants', async t => {
  for (const mode of ['directory', 'hardlink']) await t.test(mode, async t => {
    const f = await fixture(t), dir = path.join(f.packageRoot, 'plugin/.runtime'), target = path.join(dir, 'route-secret.mjs');
    await fs.mkdir(dir);
    if (mode === 'directory') await fs.mkdir(target);
    else { const other = path.join(f.dir, 'other-file'); await fs.writeFile(other, 'existing'); await fs.link(other, target); }
    await assert.rejects(provisionRouteKey(f.packageRoot, randomBytes(32).toString('hex')), 'UNSAFE_EXISTING_KEY_FILE_ACCEPTED');
    const files = await fs.readdir(dir);
    assert.ok(files.length === 1 && files[0] === 'route-secret.mjs', 'FAILED_PROVISION_LEFT_TEMP_FILE');
  });
});

test('failed atomic replacement preserves the existing key and removes the temp file', async t => {
  const f = await fixture(t);
  await provisionRouteKey(f.packageRoot, randomBytes(32).toString('hex'));
  const dir = path.join(f.packageRoot, 'plugin/.runtime'), target = path.join(dir, 'route-secret.mjs');
  const before = createHash('sha256').update(await fs.readFile(target)).digest('hex');
  const original = fs.rename;
  fs.rename = async (_from, to) => { if (to === target) throw Error('INJECTED_REPLACE_FAILURE'); return original(_from, to); };
  f.cleanup(() => { fs.rename = original; });
  await assert.rejects(provisionRouteKey(f.packageRoot, randomBytes(32).toString('hex')));
  assert.ok(createHash('sha256').update(await fs.readFile(target)).digest('hex') === before, 'FAILED_REPLACE_CHANGED_EXISTING_KEY');
  const files = await fs.readdir(dir);
  assert.ok(files.length === 1 && files[0] === 'route-secret.mjs', 'FAILED_REPLACE_LEFT_TEMP_FILE');
});

test('route provisioning atomically replaces an existing private key without temp remnants', async t => {
  const f = await fixture(t), target = path.join(f.packageRoot, 'plugin/.runtime/route-secret.mjs');
  await provisionRouteKey(f.packageRoot, randomBytes(32).toString('hex'));
  const before = await fs.readFile(target);
  await provisionRouteKey(f.packageRoot, randomBytes(32).toString('hex'));
  assert.ok(!(await fs.readFile(target)).equals(before), 'REPLACEMENT_DID_NOT_PUBLISH_NEW_MATERIAL');
  const files = await fs.readdir(path.dirname(target));
  assert.ok(files.length === 1 && files[0] === 'route-secret.mjs', 'SUCCESSFUL_REPLACE_LEFT_TEMP_FILE');
});

test('concurrent publishers cannot interleave a failed publication rollback', async t => {
  const f = await fixture(t), target = path.join(f.packageRoot, 'plugin/.runtime/route-secret.mjs');
  await provisionRouteKey(f.packageRoot, randomBytes(32).toString('hex'));
  const before = await fs.readFile(target), entered = deferred(), release = deferred();
  const original = fs.utimes, controller = new AbortController();
  let paused = false;
  fs.utimes = async (file, ...times) => {
    await original(file, ...times);
    if (!paused && file === path.join(f.packageRoot, 'plugin/route.middleware.mjs')) {
      paused = true; entered.resolve(); await release.promise;
    }
  };
  f.cleanup(() => { release.resolve(); fs.utimes = original; });
  const first = provisionRouteKey(f.packageRoot, randomBytes(32).toString('hex'), controller.signal).then(() => null, error => error);
  await bounded(entered.promise);
  let second;
  try {
    second = await provisionRouteKey(f.packageRoot, randomBytes(32).toString('hex')).then(() => null, error => error);
  } finally { controller.abort(); release.resolve(); }
  assert.ok(await bounded(first), 'CANCELLED_PUBLICATION_SUCCEEDED');
  assert.equal(second?.message, 'ROUTE_PUBLICATION_BUSY', 'CONCURRENT_PUBLISHER_ENTERED_ROLLBACK_TRANSACTION');
  assert.ok((await fs.readFile(target)).equals(before), 'FAILED_TRANSACTION_DID_NOT_RESTORE_PRIOR_KEY');
  await provisionRouteKey(f.packageRoot, randomBytes(32).toString('hex'));
  assert.ok(!(await fs.readFile(target)).equals(before), 'NEXT_PUBLICATION_DID_NOT_ACQUIRE_RELEASED_LOCK');
  assert.deepEqual(await fs.readdir(path.dirname(target)), ['route-secret.mjs']);
});

test('route key replacement has a private explicit Windows ACL', { skip: process.platform !== 'win32' }, async t => {
  const f = await fixture(t), dir = path.join(f.packageRoot, 'plugin/.runtime');
  await fs.mkdir(dir);
  await acl(dir, publicAcl);
  await provisionRouteKey(f.packageRoot, randomBytes(32).toString('hex'));
  const state = await acl(path.join(dir, 'route-secret.mjs'), inspectAcl);
  assert.ok(state.protected && state.unexpected === 0 && state.owner, 'PUBLISHED_ROUTE_KEY_ACL_NOT_PRIVATE');
});

test('actual packaged binary starts with blank parent environment and stops on disable', async t => {
  const platform = { win32: 'windows', darwin: 'darwin', linux: 'linux' }[process.platform];
  const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
  const source = path.join(sourceRoot, 'bin', `magpie-model-check-${platform}-${arch}${process.platform === 'win32' ? '.exe' : ''}`);
  if (!await fs.access(source).then(() => true, () => false)) { t.skip('PACKAGED_BINARY_NOT_PRESENT'); return; }
  const f = await fixture(t);
  f.options.binaryPath = path.join(f.dir, path.basename(source));
  await fs.copyFile(source, f.options.binaryPath);
  await fs.chmod(f.options.binaryPath, 0o700);
  t.diagnostic('PACKAGED_BINARY_SHA256=' + createHash('sha256').update(await fs.readFile(f.options.binaryPath)).digest('hex'));
  const server = net.createServer();
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  f.options.port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  await f.writeConfig();
  const previous = process.env;
  process.env = {};
  f.cleanup(() => { process.env = previous; });
  const outcome = await bounded(attempt(f), 'ACTUAL_BINARY_START_DEADLINE', 12000);
  assert.ok(!!outcome.runtime, 'ACTUAL_BINARY_START_FAILED: '+(outcome.error?.message||'no diagnostic'));
  const state = await outcome.runtime.request('state');
  assert.ok(Array.isArray(state.targets), 'ACTUAL_BINARY_STATE_CONTRACT_CHANGED');
  const moduleSource = await fs.readFile(path.join(f.packageRoot, 'plugin/.runtime/route-secret.mjs'), 'utf8');
  assert.ok(!JSON.stringify(state).includes(moduleSource.match(/routeKey = "([a-f0-9]{64})"/)?.[1] || 'not-a-key'), 'ROUTE_KEY_LEAKED_IN_STATE');
  await f.writeConfig([{ spec: f.packageRoot, options: f.options, off: true }]);
  for (let i = 0; i < 40 && !outcome.runtime.closed; i++) await pause(50);
  assert.ok(outcome.runtime.closed && outcome.runtime.lifetime.aborted, 'ACTUAL_BINARY_DISABLE_NOT_OBSERVED');
  let unreachable = false;
  for (let i = 0; i < 40 && !unreachable; i++) {
    try { await fetch(outcome.runtime.origin + '/', { signal: AbortSignal.timeout(200) }); await pause(50); }
    catch { unreachable = true; }
  }
  assert.ok(unreachable, 'ACTUAL_BINARY_LISTENER_REMAINED_OPEN');
});
