import { startRuntime } from './runtime.mjs';

const objectArgs=properties=>({type:'object',properties,required:Object.keys(properties),additionalProperties:false});
function pause(ms,signal){return new Promise((resolve,reject)=>{if(signal.aborted){reject(Error('CANCELLED'));return;}const abort=()=>{clearTimeout(timer);reject(Error('CANCELLED'));};const timer=setTimeout(()=>{signal.removeEventListener('abort',abort);resolve();},ms);signal.addEventListener('abort',abort,{once:true});});}
const unavailable=reason=>({identity_status:'UNAVAILABLE',reason,calibrated_probability:null});

// Official OpenCode-compatible factory. No control-only inference provider,
// auth impersonation, automatic quality trigger or remote tunnel is registered.
export default async function ModelCheck(input,options={}){
  const runtime=await startRuntime(input,options);
  return { tool:{
    targets:{description:'List configured Magpie model-check targets. Ordinary traffic is never blocked.',args:objectArgs({}),async execute(_args,ctx){try{return (await runtime.request('state','GET',undefined,ctx.abort)).targets;}catch{return unavailable(runtime.closed?'SERVICE_RELOADING':'LOCAL_DATA_UNAVAILABLE');}}},
    check:{description:'Run budget-limited independent behavioral challenges for a configured target through its local Magpie gateway. This can incur API costs; scores are not identity probabilities.',args:objectArgs({target_id:{type:'string',description:'ID from the targets tool'}}),async execute(args,ctx){
      let batchID;const signal=AbortSignal.any([ctx.abort,runtime.lifetime,AbortSignal.timeout(45000)]);
      try{signal.throwIfAborted();const started=await runtime.request(`targets/${encodeURIComponent(args.target_id)}/checks`,'POST',{},signal);batchID=started.batch_id;
        for(;;){signal.throwIfAborted();const report=await runtime.request(`batches/${batchID}`,'GET',undefined,signal);if(report.batch.completed_at)return {batch_id:batchID,...report.result};await pause(250,signal);}
      }catch{if(batchID)await runtime.request(`batches/${batchID}/cancel`,'POST',{},AbortSignal.timeout(2000)).catch(()=>{});return unavailable(ctx.abort.aborted?'CANCELLED':runtime.closed?'SERVICE_RELOADING':'CHECK_NOT_COMPLETED');}
    }},
    report:{description:'Read a persisted model-check report. Response model fields are self-reported, not identity evidence.',args:objectArgs({batch_id:{type:'string'}}),async execute(args,ctx){try{const report=await runtime.request(`batches/${encodeURIComponent(args.batch_id)}`,'GET',undefined,ctx.abort);for(const sample of report.samples||[]){if(sample.raw_response.length>16000){sample.raw_response=sample.raw_response.slice(0,16000);sample.tool_display_truncated=true;}}return report;}catch{return unavailable('REPORT_UNAVAILABLE');}}},
    cancel:{description:'Cancel one active model-check batch and its in-flight local gateway request.',args:objectArgs({batch_id:{type:'string'}}),async execute(args,ctx){try{return await runtime.request(`batches/${encodeURIComponent(args.batch_id)}/cancel`,'POST',{},ctx.abort);}catch{return unavailable('CANCEL_NOT_COMPLETED');}}},
  }};
}
