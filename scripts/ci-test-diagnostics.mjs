import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import os from 'node:os';
import { spawnSync } from 'node:child_process';

export function failureExcerpt(log) {
  const lines = String(log).split(/\r?\n/);
  const selected = new Set();
  let failures = 0;
  for (let index=0; index<lines.length; index++) {
    if (/^\s*not ok\b|^--- FAIL:|^FAIL(?:\s|$)/.test(lines[index]) && failures++ < 8) {
      for (let next=Math.max(0,index-1); next<Math.min(lines.length,index+35); next++) selected.add(next);
    }
  }
  const totals = lines.filter(line=>/^# (?:tests|pass|fail|skipped|cancelled)\b/.test(line));
  const errors = lines.filter(line=>/^\s*(?:not ok\b|error:)|^--- FAIL:/.test(line));
  const context = [...selected].toSorted((a,b)=>a-b).map(index=>lines[index]);
  return [...totals,...errors, ...(context.length?context:lines.slice(-40))].join('\n').slice(0,16000);
}

async function windowsOwnerDiagnostic() {
  const parent=await fs.realpath(os.tmpdir());
  const dir=await fs.mkdtemp(path.join(parent,'model-owner-diagnostic-'));
  try {
    const script="$ErrorActionPreference='Stop';$id=[System.Security.Principal.WindowsIdentity]::GetCurrent();$owner=(Get-Acl -LiteralPath $env:MODEL_CHECK_OWNER_PROBE_PATH).GetOwner([System.Security.Principal.SecurityIdentifier]).Value;@{token_default_is_user=($id.Owner.Value -eq $id.User.Value);created_owner_is_user=($owner -eq $id.User.Value);created_owner_is_token_default=($owner -eq $id.Owner.Value)}|ConvertTo-Json -Compress";
    const result=spawnSync(path.join(process.env.SystemRoot||'C:\\Windows','System32/WindowsPowerShell/v1.0/powershell.exe'),['-NoLogo','-NoProfile','-NonInteractive','-Command',script],{windowsHide:true,encoding:'utf8',timeout:20000,env:{...process.env,MODEL_CHECK_OWNER_PROBE_PATH:dir}});
    if(result.status!==0)throw Error('Windows owner diagnostic failed');
    const values=JSON.parse(result.stdout),fields=['token_default_is_user','created_owner_is_user','created_owner_is_token_default'];
    if(fields.some(field=>typeof values[field]!=='boolean'))throw Error('Windows owner diagnostic was invalid');
    // Report only booleans about the ephemeral runner, never account SIDs.
    return Object.fromEntries(fields.map(field=>[field,values[field]]));
  } finally {
    if(path.dirname(await fs.realpath(dir))!==parent)throw Error('Diagnostic path left its created fixture');
    await fs.rmdir(dir);
  }
}

async function windowsAclDiagnostic(){
  if(process.platform!=='win32')throw Error('Native Windows is required');
  const parent=await fs.realpath(os.tmpdir()),dir=await fs.mkdtemp(path.join(parent,'model-acl-diagnostic-'));
  try{
    const source=await fs.readFile(new URL('../plugin/runtime.mjs',import.meta.url),'utf8');
    const script=source.match(/const windowsAclScript=`([\s\S]*?)`;/)?.[1];
    if(!script)throw Error('Private ACL implementation was not found');
    const systemRoot=process.env.SystemRoot||'C:\\Windows',started=Date.now();
    const environment={SystemRoot:systemRoot};
    const result=spawnSync(path.join(systemRoot,'System32/WindowsPowerShell/v1.0/powershell.exe'),['-NoLogo','-NoProfile','-NonInteractive','-Command',script],{windowsHide:true,encoding:'utf8',timeout:10000,env:{...environment,MODEL_CHECK_PRIVATE_PATH:dir,MODEL_CHECK_PRIVATE_TREE:'1'}});
    const message=String(result.stderr||'').replaceAll(dir,'FIXTURE').replace(/S-1-(?:\d+-)*\d+/g,'SID').slice(0,2500);
    return {exit:result.status,signal:result.signal,error:result.error?.code||null,elapsed_ms:Date.now()-started,stderr:message};
  }finally{
    if(path.dirname(await fs.realpath(dir))!==parent)throw Error('Diagnostic path left its created fixture');
    await fs.rmdir(dir);
  }
}

export function workflowAnnotation(message) {
  const encoded = String(message).slice(0,16000).replaceAll('%','%25').replaceAll('\r','%0D').replaceAll('\n','%0A');
  return `::error title=Model-check test failure::${encoded}`;
}

export function diagnosticExit(value) {
  if (!/^\d{1,3}$/.test(String(value)) || Number(value)>255) throw Error('Expected the actual suite exit status');
  return Number(value);
}

if (process.argv[1] && path.resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
  try {
    if(process.argv.length===3&&process.argv[2]==='--windows-acl-probe'){
      const diagnostic=await windowsAclDiagnostic();
      const message='Windows private ACL diagnostic: '+JSON.stringify(diagnostic);
      if(diagnostic.exit===0)console.log(message);else console.error(workflowAnnotation(message));
      process.exitCode=diagnostic.exit===0?0:1;
    }else{
    if (process.argv.length!==4) throw Error('Expected a suite log file and its exit status');
    const status = diagnosticExit(process.argv[3]);
    process.exitCode=status;
    if (status!==0) {
      let log;
      try { log=await fs.readFile(process.argv[2],'utf8'); }
      catch { log='The mandatory suite failed and its captured log could not be read.'; }
      console.error(workflowAnnotation(failureExcerpt(log)));
      if(process.platform==='win32'&&process.env.GITHUB_ACTIONS==='true') {
        try { console.error(workflowAnnotation('Windows owner diagnostic: '+JSON.stringify(await windowsOwnerDiagnostic()))); }
        catch { console.error(workflowAnnotation('Windows owner diagnostic was not collected; original suite failure retained.')); }
      }
    }
    }
  } catch (error) { console.error(workflowAnnotation(error.message)); process.exitCode=1; }
}
