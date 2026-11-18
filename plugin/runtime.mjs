import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import fs from 'node:fs/promises';
import { constants } from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { randomBytes, createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';

const runFile=promisify(execFile),root=fileURLToPath(new URL('../',import.meta.url));
export function resolveGateway(options={},env=process.env){
  let address=env.MAGPIE_ADDR||'127.0.0.1:3425';
  if(/^0\.0\.0\.0:\d+$/.test(address))address=address.replace('0.0.0.0','127.0.0.1');
  if(/^\[::\]:\d+$/.test(address))address=address.replace('[::]','[::1]');
  if(/^:\d+$/.test(address))address='127.0.0.1'+address;
  const url=new URL(options.gatewayURL||`http://${address}`);
  if(url.protocol!=='http:'||!['127.0.0.1','localhost','[::1]'].includes(url.hostname)||url.username||url.password||url.search||url.hash||!['','/','/v1','/v1/'].includes(url.pathname))throw Error('LOCAL_GATEWAY_REQUIRED');
  if(url.hostname==='localhost')url.hostname='127.0.0.1';
  const variable=options.gatewayKeyEnv;
  if(variable!==undefined&&(typeof variable!=='string'||!/^[A-Z][A-Z0-9_]*$/.test(variable)||typeof env[variable]!=='string'||!env[variable].trim()))throw Error('GATEWAY_KEY_ENV_MISSING');
  return {gateway_url:url.href.replace(/\/$/,'').replace(/\/v1$/,''),gateway_key:variable?env[variable]:'magpie'};
}
async function directoryAncestors(dir, allowMissing=false){
  const absolute=path.resolve(dir),base=path.parse(absolute).root;
  let current=base;
  for(const part of ['',...absolute.slice(base.length).split(path.sep).filter(Boolean)]){
    current=path.join(current,part);
    let stat;try{stat=await fs.lstat(current);}catch(error){if(allowMissing&&error.code==='ENOENT')return;throw error;}
    if(stat.isSymbolicLink())throw Error('PRIVATE_PATH_REQUIRED');
    if(!stat.isDirectory())throw Error('PRIVATE_DIRECTORY_REQUIRED');
  }
}
async function privateTree(dir){
  const entries=[],pending=[dir];
  while(pending.length){
    const file=pending.pop(),stat=await fs.lstat(file);
    if(stat.isSymbolicLink())throw Error('PRIVATE_PATH_REQUIRED');
    if(!stat.isDirectory()&&(!stat.isFile()||stat.nlink!==1))throw Error('PRIVATE_FILE_REQUIRED');
    if(process.getuid&&stat.uid!==process.getuid())throw Error('PRIVATE_OWNER_REQUIRED');
    entries.push({file,directory:stat.isDirectory()});
    if(stat.isDirectory())for(const name of await fs.readdir(file))pending.push(path.join(file,name));
  }
  return entries;
}

// A new protected DACL replaces inherited AND explicit grants. DACL-only
// framework persistence avoids the provider's SACL privilege request on an
// already-protected path. Data paths stay separate from static shell code.
// Use .NET filesystem/ACL APIs directly: cmdlet module autoloading hangs with
// a minimal environment on native hosted runners. No profile/modules needed.
const windowsAclScript=`$ErrorActionPreference='Stop'
$p=[System.IO.Path]::GetFullPath($env:MODEL_CHECK_PRIVATE_PATH)
$sid=[System.Security.Principal.WindowsIdentity]::GetCurrent().User
$cursor=$p
while($cursor){
 try{if(([System.IO.File]::GetAttributes($cursor) -band [System.IO.FileAttributes]::ReparsePoint) -ne 0){throw 'PRIVATE_PATH_REQUIRED'}}
 catch{
  $cause=$_.Exception;while($cause.InnerException){$cause=$cause.InnerException}
  if($cause -isnot [System.IO.FileNotFoundException] -and $cause -isnot [System.IO.DirectoryNotFoundException]){throw}
 }
 $parent=[System.IO.Directory]::GetParent($cursor)
 $cursor=if($parent){$parent.FullName}else{$null}
}
if($env:MODEL_CHECK_PRIVATE_TREE -eq 'check'){exit 0}
$pending=[System.Collections.Generic.Stack[string]]::new();$pending.Push($p)
$items=[System.Collections.Generic.List[System.IO.FileSystemInfo]]::new()
$sections=[System.Security.AccessControl.AccessControlSections]'Owner,Access'
while($pending.Count){
 $name=$pending.Pop()
 $item=if(([System.IO.File]::GetAttributes($name) -band [System.IO.FileAttributes]::Directory) -ne 0){[System.IO.DirectoryInfo]::new($name)}else{[System.IO.FileInfo]::new($name)}
 if(($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0){throw 'PRIVATE_PATH_REQUIRED'}
 $old=if($item -is [System.IO.DirectoryInfo]){[System.IO.Directory]::GetAccessControl($item.FullName,$sections)}else{[System.IO.File]::GetAccessControl($item.FullName,$sections)}
 if($old.GetOwner([System.Security.Principal.SecurityIdentifier]).Value -ne $sid.Value){throw 'PRIVATE_OWNER_REQUIRED'}
 $items.Add($item)
 if($env:MODEL_CHECK_PRIVATE_TREE -eq '1' -and $item -is [System.IO.DirectoryInfo]){foreach($child in $item.EnumerateFileSystemInfos()){$pending.Push($child.FullName)}}
}
foreach($item in $items){
 $a=if($item -is [System.IO.DirectoryInfo]){[System.Security.AccessControl.DirectorySecurity]::new()}else{[System.Security.AccessControl.FileSecurity]::new()}
 $inherit=if($item -is [System.IO.DirectoryInfo]){[System.Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'}else{[System.Security.AccessControl.InheritanceFlags]::None}
 $a.SetAccessRuleProtection($true,$false)
 $a.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new($sid,'FullControl',$inherit,'None','Allow'))
 if($item -is [System.IO.DirectoryInfo]){[System.IO.Directory]::SetAccessControl($item.FullName,$a)}else{[System.IO.File]::SetAccessControl($item.FullName,$a)}
 $verified=if($item -is [System.IO.DirectoryInfo]){[System.IO.Directory]::GetAccessControl($item.FullName,$sections)}else{[System.IO.File]::GetAccessControl($item.FullName,$sections)}
 $rules=@($verified.GetAccessRules($true,$true,[System.Security.Principal.SecurityIdentifier]))
 if($verified.GetOwner([System.Security.Principal.SecurityIdentifier]).Value -ne $sid.Value -or -not $verified.AreAccessRulesProtected -or $rules.Count -ne 1 -or $rules[0].IdentityReference.Value -ne $sid.Value -or $rules[0].IsInherited -or $rules[0].AccessControlType -ne 'Allow' -or $rules[0].FileSystemRights -ne [System.Security.AccessControl.FileSystemRights]::FullControl){throw 'WINDOWS_PRIVATE_ACL_FAILED'}
}`;
async function windowsPrivateAcl(file,tree,signal){
  const systemRoot=process.env.SystemRoot||'C:\\Windows';
  try{
    await runFile(path.join(systemRoot,'System32/WindowsPowerShell/v1.0/powershell.exe'),['-NoLogo','-NoProfile','-NonInteractive','-Command',windowsAclScript],{
      windowsHide:true,timeout:10000,signal,
      env:{SystemRoot:systemRoot,MODEL_CHECK_PRIVATE_PATH:path.resolve(file),MODEL_CHECK_PRIVATE_TREE:tree==='check'?'check':tree?'1':'0'},
    });
  }catch{signal?.throwIfAborted();throw Error('WINDOWS_PRIVATE_ACL_FAILED');}
}
export async function privateDirectory(dir,signal,executableDirectory){
  signal?.throwIfAborted();
  dir=path.resolve(dir);
  await directoryAncestors(dir,true);
  if(process.platform==='win32')await windowsPrivateAcl(dir,'check',signal);
  await fs.mkdir(dir,{recursive:true,mode:0o700});
  await directoryAncestors(dir);
  const entries=await privateTree(dir);
  signal?.throwIfAborted();
  if(process.platform==='win32')await windowsPrivateAcl(dir,true,signal);
  // The explicitly named executable directory always uses owner-only 0700.
  // Never derive that from an earlier mode snapshot: another startup may
  // publish a runnable copy between traversal and chmod. Data files stay 0600.
  else for(const entry of entries)await fs.chmod(entry.file,entry.directory||executableDirectory&&path.dirname(entry.file)===path.resolve(executableDirectory)?0o700:0o600);
  signal?.throwIfAborted();
}
async function privateFile(file,signal,mode=0o600){
  await directoryAncestors(path.dirname(file));
  const stat=await fs.lstat(file);
  if(!stat.isFile()||stat.isSymbolicLink()||stat.nlink!==1)throw Error('PRIVATE_FILE_REQUIRED');
  if(process.getuid&&stat.uid!==process.getuid())throw Error('PRIVATE_OWNER_REQUIRED');
  if(process.platform==='win32')await windowsPrivateAcl(file,false,signal);
  else await fs.chmod(file,mode);
  signal?.throwIfAborted();
}
export async function prepareExecutable(file,copyDir,signal){
  signal?.throwIfAborted();
  await directoryAncestors(path.dirname(file));
  const before=await fs.lstat(file);
  if(!before.isFile()||before.isSymbolicLink())throw Error('PRIVATE_FILE_REQUIRED');
  const source=await fs.open(file,constants.O_RDONLY|(constants.O_NOFOLLOW||0));
  try{
    const stat=await source.stat();
    if(!stat.isFile()||stat.nlink<1||stat.dev!==before.dev||stat.ino!==before.ino)throw Error('PRIVATE_FILE_REQUIRED');
    if(process.getuid&&stat.uid!==process.getuid())throw Error('PRIVATE_OWNER_REQUIRED');
    if(before.nlink===1&&stat.nlink===1){
      // Windows-created archives can carry 0644 on POSIX. Change only this
      // opened single-link file, not a path that could have been replaced.
      await source.chmod(0o700);signal?.throwIfAborted();return file;
    }
    // Bun installs Git packages with hardlinks into its cache. Never chmod
    // that shared inode: execute a separately owned, checked private copy.
    if(typeof copyDir!=='string'||!copyDir)throw Error('PRIVATE_BINARY_COPY_REQUIRED');
    copyDir=path.resolve(copyDir);
    await privateDirectory(copyDir,signal,copyDir);
    const bytes=await source.readFile(),digest=createHash('sha256').update(bytes).digest('hex');
    signal?.throwIfAborted();
    const target=path.join(copyDir,`binary-${digest}${path.extname(file)==='.exe'?'.exe':''}`);
    try{
      await privateFile(target,signal,0o700);
      if(createHash('sha256').update(await fs.readFile(target)).digest('hex')!==digest)throw Error('PRIVATE_BINARY_CACHE_INVALID');
    }catch(error){if(error.code!=='ENOENT')throw error;await replacePrivateFile(target,bytes,signal);}
    await privateFile(target,signal,0o700);
    signal?.throwIfAborted();return target;
  }finally{await source.close();signal?.throwIfAborted();}
}
async function replacePrivateFile(target,source,signal){
  const dir=path.dirname(target),temp=path.join(dir,`key-${randomBytes(8).toString('hex')}.tmp`);
  let handle;
  try{
    signal?.throwIfAborted();
    handle=await fs.open(temp,'wx',0o600);
    await privateFile(temp,signal);
    await handle.writeFile(source);await handle.sync();
    await handle.close();handle=undefined;
    await directoryAncestors(dir);
    try{await privateFile(target,signal);}catch(error){if(error.code!=='ENOENT')throw error;}
    signal?.throwIfAborted();
    await fs.rename(temp,target);
  }finally{
    try{if(handle)await handle.close();}
    finally{await fs.unlink(temp).catch(error=>{if(error.code!=='ENOENT')throw error;});}
  }
}
export async function provisionRouteKey(packageRoot,key,signal){
  return publishRouteKey(packageRoot,key,signal);
}
async function publishRouteKey(packageRoot,key,signal,validate){
  if(typeof key!=='string'||!/^[a-f0-9]{64}$/.test(key))throw Error('INVALID_ROUTE_KEY');
  const dir=path.resolve(packageRoot,'plugin/.runtime'),target=path.join(dir,'route-secret.mjs');
  await privateDirectory(dir,signal);
  // Hold a cross-process lock across snapshot, publication, validation AND
  // rollback. Comparing the key before an asynchronous rollback is not a CAS.
  // A stale lock fails closed; never reclaim another publisher's lock by age.
  const lock=path.join(dir,'route-publication.lock');
  let handle;
  try{handle=await fs.open(lock,'wx',0o600);}
  catch(error){if(error.code==='EEXIST')throw Error('ROUTE_PUBLICATION_BUSY');throw error;}
  try{
  await privateFile(lock,signal);
  await handle.writeFile(JSON.stringify({pid:process.pid,created_at:new Date().toISOString()}));
  await handle.sync();
  let previous;
  try{await privateFile(target,signal);previous=await fs.readFile(target);}catch(error){if(error.code!=='ENOENT')throw error;}
  const middleware=path.resolve(packageRoot,'plugin/route.middleware.mjs');
  await directoryAncestors(path.dirname(middleware));
  const stat=await fs.lstat(middleware);
  if(!stat.isFile()||stat.isSymbolicLink())throw Error('PRIVATE_FILE_REQUIRED');
  const source=Buffer.from(`// Private per-service material; excluded from package exports.\nexport const routeKey = ${JSON.stringify(key)};\n`);
  let published=false;
  try{
    await replacePrivateFile(target,source,signal);published=true;
    signal?.throwIfAborted();
    const stamp=new Date();await fs.utimes(middleware,stamp,stamp);
    // Keep the final startup check inside the publication transaction: an
    // immediate config change must restore the prior key, not leave an orphan.
    if(validate)await validate();
    signal?.throwIfAborted();
  }catch(error){
    // The publication lock prevents a newer cooperating runtime from entering
    // between this comparison and the rollback operation.
    if(published){
      const current=await fs.readFile(target).catch(()=>undefined);
      if(current?.equals(source)){
        if(previous)await replacePrivateFile(target,previous);
        else await fs.unlink(target);
      }
    }
    throw error;
  }
  }finally{
    try{await handle.close();}
    finally{await fs.unlink(lock).catch(error=>{if(error.code!=='ENOENT')throw error;});}
  }
}

function canonical(value){
  if(Array.isArray(value))return value.map(canonical);
  if(value&&typeof value==='object')return Object.fromEntries(Object.keys(value).sort().map(key=>[key,canonical(value[key])]));
  return value;
}
async function configuration(file){
  const version=stat=>[stat.dev,stat.ino,stat.mtimeNs,stat.ctimeNs].map(String).join(':');
  let parsed,generation;
  for(let attempt=0;attempt<2;attempt++){
    const before=await fs.stat(file,{bigint:true}),text=await fs.readFile(file,'utf8'),after=await fs.stat(file,{bigint:true});
    if(version(before)!==version(after)){if(attempt===1)throw Error('PLUGIN_CONFIGURATION_UNSTABLE');continue;}
    parsed=JSON.parse(text);generation=version(after);break;
  }
  if(!parsed||!Array.isArray(parsed.plugins))throw Error('INVALID_PLUGIN_LIST');
  // Include valid-file generations, so off/on or remove/reinstall cannot hide
  // a reload by restoring the same JSON before the next polling tick.
  return {parsed,stamp:createHash('sha256').update(JSON.stringify([canonical(parsed),generation])).digest('hex')};
}
function githubRepository(spec){
  if(typeof spec!=='string')return undefined;
  let value=spec.split('#',1)[0].replace(/^git\+/,'');
  if(value.startsWith('github:'))value='https://github.com/'+value.slice(7);
  else if(value.startsWith('git@github.com:'))value='ssh://git@github.com/'+value.slice(15);
  else if(/^[\w.-]+\/[\w.-]+$/.test(value))value='https://github.com/'+value;
  try{
    const url=new URL(value);
    if(url.hostname!=='github.com'||!['https:','ssh:'].includes(url.protocol)||url.password||url.search||url.port)return undefined;
    if(url.username&&(url.protocol!=='ssh:'||url.username!=='git'))return undefined;
    const slug=url.pathname.replace(/^\//,'').replace(/\/$/,'').replace(/\.git$/,'');
    return /^[\w.-]+\/[\w.-]+$/.test(slug)?slug.toLowerCase():undefined;
  }catch{return undefined;}
}
function ownSpec(spec,packageName,directory,repository){
  if(typeof spec!=='string')return false;
  const npm=spec.replace(/^npm:/,'');
  if(npm===packageName||npm.startsWith(packageName+'@'))return true;
  const source=githubRepository(typeof repository==='string'?repository:repository?.url);
  if(source&&githubRepository(spec)===source)return true;
  let local;
  try{local=spec.startsWith('file:')?fileURLToPath(new URL(spec)):path.resolve(directory,spec);}catch{return false;}
  const normalize=p=>process.platform==='win32'?path.resolve(p).toLowerCase():path.resolve(p);
  return normalize(local)===normalize(root)||normalize(local)===normalize(path.join(root,'plugin/index.mjs'));
}
export function deriveProtocolGuard(plugins,ownIndex,metadata=[]){
  const object=value=>value!==null&&typeof value==='object'&&!Array.isArray(value);
  if(!Array.isArray(plugins)||!Array.isArray(metadata)||!Number.isInteger(ownIndex)||ownIndex<0||ownIndex>=plugins.length)return false;
  let lastMiddleware=-1;
  for(let i=0;i<plugins.length;i++){
    const entry=plugins[i];if(!object(entry))return false;
    if(entry.off===true)continue;
    if(entry.off!==undefined&&entry.off!==false||typeof entry.spec!=='string'||!entry.spec.trim())return false;
    const manifest=metadata[i];if(!object(manifest)||typeof manifest.name!=='string'||!manifest.name.trim())return false;
    const magpie=manifest.magpie===undefined?{}:manifest.magpie;if(!object(magpie))return false;
    // Declared async hooks can run after the observer, irrespective of list
    // order. Unknown hook forms also decline protocol proof conservatively.
    if(Object.hasOwn(magpie,'hooks'))return false;
    if(Object.hasOwn(magpie,'middleware')||Object.hasOwn(magpie,'gateway')){
      const middleware=magpie.middleware??magpie.gateway;
      if(typeof middleware!=='string'||!middleware.trim())return false;
      lastMiddleware=i;
    }
  }
  return plugins[ownIndex].off!==true&&lastMiddleware===ownIndex;
}
async function packageMetadata(spec,directory){
  // Read only an explicitly identified local package. Unresolved npm/Git or
  // standalone-module metadata remains unknown rather than guessing a cache.
  if(typeof spec!=='string'||!spec.startsWith('file:')&&!path.isAbsolute(spec)&&!/^\.\.?[\\/]/.test(spec))return undefined;
  try{
    const local=spec.startsWith('file:')?fileURLToPath(new URL(spec)):path.resolve(directory,spec);
    await directoryAncestors(local);
    const file=path.join(local,'package.json'),stat=await fs.lstat(file);
    if(!stat.isFile()||stat.isSymbolicLink()||stat.size>65536)return undefined;
    return JSON.parse(await fs.readFile(file,'utf8'));
  }catch{return undefined;}
}
async function initialConfiguration(directory,options){
  const file=path.join(directory,'plugins.json');
  let snapshot;
  try{snapshot=await configuration(file);}catch{throw Error('PLUGIN_CONFIGURATION_UNAVAILABLE');}
  let manifest,name;try{manifest=JSON.parse(await fs.readFile(path.join(root,'package.json'),'utf8'));({name}=manifest);if(typeof name!=='string'||!name)throw Error();}catch{throw Error('PLUGIN_CONFIGURATION_UNAVAILABLE');}
  const entries=snapshot.parsed.plugins.filter(entry=>entry&&ownSpec(entry.spec,name,directory,manifest.repository));
  if(entries.length!==1||entries[0].off===true)throw Error('PLUGIN_DISABLED');
  if(entries[0].off!==undefined&&entries[0].off!==false)throw Error('INVALID_PLUGIN_LIST');
  if(JSON.stringify(canonical(entries[0].options||{}))!==JSON.stringify(canonical(options)))throw Error('SERVICE_RELOADING');
  const plugins=snapshot.parsed.plugins,ownIndex=plugins.indexOf(entries[0]);
  const metadata=await Promise.all(plugins.map((entry,i)=>entry?.off===true?undefined:i===ownIndex?manifest:packageMetadata(entry?.spec,directory)));
  return {file,stamp:snapshot.stamp,protocolGuard:deriveProtocolGuard(plugins,ownIndex,metadata)};
}
export async function watchConfiguration(file,onChange,interval=200,initial){
  initial??=(await configuration(file)).stamp;
  let stopped=false,busy=false,invalidSince=0;
  const stop=()=>{stopped=true;clearInterval(timer);};
  const changed=()=>{if(stopped)return;stop();onChange();};
  const timer=setInterval(async()=>{
    if(stopped||busy)return;busy=true;
    try{const next=(await configuration(file)).stamp;invalidSince=0;if(next!==initial)changed();}
    catch(error){
      // Short partial JSON writes retain the last valid configuration; missing
      // or persistently unreadable configuration must not leave an orphan.
      if(error instanceof SyntaxError){invalidSince||=Date.now();if(Date.now()-invalidSince>=1000)changed();}
      else changed();
    }finally{busy=false;}
  },interval);timer.unref?.();
  return stop;
}
export async function startRuntime(input={},options={}){
  // Capture the owner's on-state and options before any slow startup work.
  const initial=input.directory?await initialConfiguration(input.directory,options):undefined;
  const lifetime=new AbortController();let closed=false,closeReason='',child,killTimer,stopWatch=()=>{};
  let finishExit;const exited=new Promise(resolve=>{finishExit=resolve;});
  const onParentExit=()=>child?.kill('SIGKILL');
  const onParentEnd=()=>stopFor('SERVICE_RELOADING');
  const detach=()=>{stopWatch();process.removeListener('exit',onParentExit);process.stdin.removeListener('end',onParentEnd);};
  const alive=()=>child&&child.exitCode===null&&child.signalCode===null;
  function stopFor(reason){
    if(closed)return;closed=true;closeReason=reason;detach();lifetime.abort();
    if(alive()){
      child.stdin.end();
      killTimer=setTimeout(()=>{if(alive())child.kill('SIGKILL');},1500);killTimer.unref?.();
    }else if(!child)finishExit();
  }
  const stop=()=>{stopFor('SERVICE_RELOADING');return exited;};
  async function currentConfiguration(){
    if(closed)throw Error(closeReason);
    if(initial){
      let next;try{next=(await configuration(initial.file)).stamp;}catch{stop();throw Error('SERVICE_RELOADING');}
      if(next!==initial.stamp){stop();throw Error('SERVICE_RELOADING');}
    }
    if(closed)throw Error(closeReason);
  }
  process.once('exit',onParentExit);process.stdin.once('end',onParentEnd);
  let ready;
  try{
  if(initial)stopWatch=await watchConfiguration(initial.file,stop,200,initial.stamp);
  const dataDir=path.resolve(options.dataDir||path.join(os.homedir(),'.config','magpie-model-check'));await privateDirectory(dataDir,lifetime.signal,path.join(dataDir,'runtime'));
  const platform={win32:'windows',darwin:'darwin',linux:'linux'}[process.platform],arch={x64:'amd64',arm64:'arm64'}[process.arch];
  const suffix=platform==='windows'?'.exe':'';
  let binary=options.binaryPath||path.join(root,'bin',`magpie-model-check-${platform}-${arch}${suffix}`);
  const port=options.port??3479;if(!Number.isInteger(port)||port<1||port>65535)throw Error('INVALID_PAGE_PORT');
  let env={...process.env};
  if(!env.MAGPIE_ADDR&&input.directory){try{const settings=JSON.parse(await fs.readFile(path.join(input.directory,'settings.json'),'utf8'));if(Number.isInteger(settings.port)&&settings.port>0&&settings.port<65536)env.MAGPIE_ADDR=`127.0.0.1:${settings.port}`;}catch{}}
  const config=resolveGateway(options,env);config.route_key=randomBytes(32).toString('hex');config.legacy_db=options.legacyDB||path.join(os.homedir(),'.config','magpie-quality-check/check.db');
  // onProvider is an observer, not a transport-final attestation. The tested
  // host still normalizes the packet after this hook; metadata order alone
  // cannot authorize an exact-wire identity experiment. A future explicit,
  // independently tested host attestation is needed before enabling this.
  config.protocol_guard=false;
  await currentConfiguration();
  if(process.platform!=='win32')binary=await prepareExecutable(binary,path.join(dataDir,'runtime'),lifetime.signal);
  lifetime.signal.throwIfAborted();
  child=spawn(binary,['-data',dataDir,'-listen',`127.0.0.1:${port}`,'-owned'],{env:{},windowsHide:true,stdio:['pipe','pipe','pipe']});
  child.once('error',()=>{stopFor('MODEL_CHECK_BINARY_UNAVAILABLE');finishExit();});
  child.once('exit',()=>{clearTimeout(killTimer);if(!closed)stopFor('MODEL_CHECK_START_FAILED');detach();finishExit();});
  child.once('close',()=>{clearTimeout(killTimer);detach();finishExit();});
  child.stderr.on('data',()=>{});child.stdin.on('error',()=>stopFor('MODEL_CHECK_START_FAILED'));
  ready=await new Promise((resolve,reject)=>{
    let text='',settled=false;
    const finish=(error,info)=>{if(settled)return;settled=true;clearTimeout(timeout);child.stdout.removeListener('data',read);lifetime.signal.removeEventListener('abort',abort);child.stdout.on('data',()=>{});error?reject(error):resolve(info);};
    const abort=()=>finish(Error(closeReason));
    const timeout=setTimeout(()=>stopFor('MODEL_CHECK_START_TIMEOUT'),15000);
    const read=chunk=>{
      text+=String(chunk);if(text.length>8192){stopFor('MODEL_CHECK_START_INVALID');return;}
      const end=text.indexOf('\n');if(end<0)return;
      try{const info=JSON.parse(text.slice(0,end));if(info.origin!==`http://127.0.0.1:${port}`||typeof info.control!=='string'||!/^[a-f0-9]{64}$/.test(info.control))throw Error();finish(undefined,info);}
      catch{stopFor('MODEL_CHECK_START_INVALID');}
    };
    child.stdout.on('data',read);lifetime.signal.addEventListener('abort',abort,{once:true});
    if(closed){abort();return;}
    child.stdin.write(JSON.stringify(config)+'\n');
  });
  await currentConfiguration();
  try{await publishRouteKey(root,config.route_key,lifetime.signal,currentConfiguration);}catch{if(!closed)stopFor('PRIVATE_ROUTE_PROVISION_FAILED');throw Error(closeReason);}
  }catch(error){
    if(!closed)stopFor(error.message||'MODEL_CHECK_START_FAILED');
    if(child){let deadline;try{await Promise.race([exited,new Promise(resolve=>{deadline=setTimeout(resolve,2000);})]);}finally{clearTimeout(deadline);}}
    detach();throw Error(closeReason);
  }
  async function request(api,method='GET',body,signal){
    if(closed)throw Error('SERVICE_RELOADING');
    const combined=AbortSignal.any([lifetime.signal,signal||AbortSignal.timeout(5000)]);combined.throwIfAborted();
    const response=await fetch(ready.origin+'/api/'+api,{method,headers:{Authorization:'Bearer '+ready.control,'Content-Type':'application/json'},...(body!==undefined?{body:JSON.stringify(body)}:{}),signal:combined,redirect:'error'});
    if(!response.ok){await response.body?.cancel();throw Error(`LOCAL_API_${response.status}`);}
    return response.json();
  }
  return {origin:ready.origin,request,stop,lifetime:lifetime.signal,get closed(){return closed;}};
}
