import {statusText,reasonText,numberText,scoreText,timeText} from './ui-format.mjs';
import {bindFormAction} from './ui-forms.mjs';
import {createRenderGuard} from './ui-refresh.mjs';
import {policyFields,referenceReviewPayload,normaliseBankImport,metricText} from './ui-reference.mjs';

const $=id=>document.getElementById(id), session=document.querySelector('meta[name="model-check-session"]').content;
let state=null,selectedBatch='',editingTarget=null,dataMode='backup',refreshing=false,visible=true,reviewingReference=null;
const stateChanged=createRenderGuard(),reportChanged=createRenderGuard();
function node(tag,text='',className=''){const el=document.createElement(tag);if(text!==undefined)el.textContent=text;if(className)el.className=className;return el;}
function notice(text,error=false){$('notice').textContent=text;$('notice').className=error?'error':'';const dialog=document.querySelector('dialog[open]');if(dialog){let feedback=dialog.querySelector('.dialog-notice');if(!feedback){feedback=node('p','','dialog-notice');feedback.setAttribute('role','status');feedback.setAttribute('aria-live','polite');dialog.querySelector('.dialog-heading').after(feedback);}feedback.textContent=text;feedback.className=`dialog-notice ${error?'error':''}`;}}
function clearDialogNotice(dialog){dialog.querySelector('.dialog-notice')?.remove();}
async function api(path,method='GET',body){
  const options={method,headers:{Authorization:`Bearer ${session}`}};
  if(body!==undefined){options.headers['Content-Type']='application/json';options.body=typeof body==='string'?body:JSON.stringify(body);}
  const response=await fetch(new URL(`api/${path}`,location.href),options);
  let result;try{result=await response.json();}catch{throw Error('本地服务返回了异常内容，请刷新页面。');}
  if(!response.ok)throw Error(result?.error?.message||`请求未完成（HTTP ${response.status}）。`);
  return result;
}
function action(label,fn,className=''){const button=node('button',label,className);button.type='button';button.addEventListener('click',async()=>{button.disabled=true;try{await fn();}catch(error){notice(error.message,true);}finally{button.disabled=false;}});return button;}
function pill(status){return node('span',statusText(status),`status ${Object.keys({MATCHED:1,MISMATCHED:1,UNKNOWN:1,INCONCLUSIVE:1,ROUTE_UNVERIFIED:1,UNAVAILABLE:1,PENDING:1}).includes(status)?status:'PENDING'}`);}
function secondary(parent,text){parent.append(node('div',text,'secondary'));}
function confirmAction(title,description){
  const dialog=$('confirm-dialog');clearDialogNotice(dialog);$('confirm-title').textContent=title;$('confirm-description').textContent=description;dialog.showModal();
  return new Promise(resolve=>{const finish=value=>{dialog.close();$('confirm-yes').onclick=null;$('confirm-no').onclick=null;dialog.oncancel=null;resolve(value);};$('confirm-yes').onclick=()=>finish(true);$('confirm-no').onclick=()=>finish(false);dialog.oncancel=event=>{event.preventDefault();finish(false);};});
}
function currentView(){return location.hash.startsWith('#banks')?'banks':location.hash.startsWith('#history')?'history':'targets';}
function setView(){const view=currentView();for(const key of ['targets','history','banks']){$(`${key}-view`).hidden=key!==view;document.querySelector(`[data-view="${key}"]`).setAttribute('aria-current',key===view?'page':'false');}if(view==='history'){const id=location.hash.split('/')[1];if(id&&id!==selectedBatch){selectedBatch=id;loadReport(id).catch(error=>notice(error.message,true));}}}
async function refresh(){
  if(refreshing)return;refreshing=true;
  try{state=await api('state');if(stateChanged(state))render();if(selectedBatch&&currentView()==='history')await loadReport(selectedBatch);}
  catch(error){notice(error.message,true);}finally{refreshing=false;}
}
function render(){
  if(!state)return;
  const usage=state.usage||{},settings=state.settings||{},jobs=state.active_jobs||[];
  $('budget-line').replaceChildren(node('span',`今日请求 ${numberText(usage.requests)} / ${numberText(settings.daily_requests)}`),node('span',`Token 已预留 ${numberText(usage.tokens)} / ${numberText(settings.daily_tokens)}`),node('span',`检测中 ${jobs.length} / ${settings.concurrency}`),node('span','按 UTC 日期结算'));
  const tbody=$('target-rows');tbody.replaceChildren();
  const targets=state.targets||[];
  if(!targets.length){const row=node('tr'),cell=node('td','还没有检测目标。添加一条 Magpie 模型连接后，就可以独立发题并保存报告。','empty');cell.colSpan=5;row.append(cell);tbody.append(row);}
  for(const target of targets){
    const row=node('tr'),model=node('td'),claim=node('td'),result=node('td'),schedule=node('td'),operations=node('td');
    model.append(node('strong',target.model_id,'mono'));secondary(model,target.provider_id?`渠道：${target.provider_id}`:'渠道 ID 尚未指定');
    claim.append(node('div',target.claimed_model));secondary(claim,[target.expected_family,target.expected_version].filter(Boolean).join(' / ')||'家族与版本尚未填写');
    const running=jobs.find(job=>job.target_id===target.id);result.append(pill(running?'PENDING':target.last_status));secondary(result,running?'独立检测进行中…':timeText(target.last_checked_at));
    const label=node('label','','check-label'),check=document.createElement('input');check.type='checkbox';check.checked=target.auto_check;check.setAttribute('aria-label',`为 ${target.model_id} 开启定时检测`);label.append(check,node('span',target.auto_check?`每 ${target.interval_minutes} 分钟`:'关闭'));
    check.addEventListener('change',async()=>{const next=check.checked;check.disabled=true;try{if(next&&!target.provider_id)throw Error('请先编辑目标，填写要确认的渠道 ID。');if(next&&!await confirmAction('开启定时检测？',`每 ${target.interval_minutes} 分钟最多发送 ${target.request_budget} 个独立请求，并消耗每日预算。普通请求不受影响。`)){check.checked=false;return;}await api(`targets/${encodeURIComponent(target.id)}`,'PUT',{...target,auto_check:next});notice(next?'定时检测已开启。':'定时检测已关闭。');await refresh();}catch(error){check.checked=!next;notice(error.message,true);}finally{check.disabled=false;}});
    schedule.append(label);secondary(schedule,target.enabled?'目标已启用':'目标已暂停');
    const actions=node('div','','row-actions');
    if(running){actions.append(action('查看进行中报告',()=>{selectedBatch=running.batch_id;location.hash=`history/${selectedBatch}`;return loadReport(selectedBatch);}),action('取消',async()=>{await api(`batches/${running.batch_id}/cancel`,'POST',{});notice('已发送取消指令。');await refresh();}));}
    else{const run=action('手动检测',()=>startCheck(target),'primary');run.disabled=!target.enabled;actions.append(run);}
    actions.append(action('编辑',()=>openTarget(target)),action('删除',async()=>{if(!await confirmAction('删除检测目标？',`将删除 ${target.model_id} 的目标配置。普通业务请求与旧版原始备份保留。`))return;await api(`targets/${encodeURIComponent(target.id)}`,'DELETE');notice('目标已删除。');await refresh();},'danger'));
    operations.append(actions);row.append(model,claim,result,schedule,operations);tbody.append(row);
  }
  renderHistory();renderBanks();
  $('legacy-count').textContent=String((state.legacy_records||[]).length);const legacy=$('legacy-list');legacy.replaceChildren();
  for(const record of state.legacy_records||[]){const details=node('details'),summary=node('summary',`${record.model||'旧模型'} · ${timeText(record.time)} · 质量结果：${record.verdict||'未记录'}`);details.append(summary,node('pre',JSON.stringify(record.data,null,2),'raw'));legacy.append(details);}
}
function renderHistory(){
  const filter=$('history-filter'),previous=filter.value;filter.replaceChildren(new Option('全部目标',''));
  for(const target of state.targets||[])filter.append(new Option(target.model_id,target.id));filter.value=previous;
  const list=$('history-list');list.replaceChildren();
  const batches=(state.batches||[]).filter(batch=>!filter.value||batch.target_id===filter.value);
  if(!batches.length)list.append(node('p','还没有检测记录。','empty'));
  for(const batch of batches){const button=action('',async()=>{selectedBatch=batch.id;location.hash=`history/${batch.id}`;renderHistory();await loadReport(batch.id);},`history-item ${selectedBatch===batch.id?'selected':''}`);button.append(node('div',batch.claimed_model||'未填写声明'),pill(batch.status),node('div',timeText(batch.started_at),'secondary'));list.append(button);}
}
async function startCheck(target){
  if(!await confirmAction('运行独立检测？',`将通过 ${target.model_id} 最多发出 ${target.request_budget} 个请求。费用计入本机预算，候选分数不是身份保证。`))return;
  const {batch_id}=await api(`targets/${encodeURIComponent(target.id)}/checks`,'POST',{});selectedBatch=batch_id;location.hash=`history/${batch_id}`;notice('检测已开始，原始回答将保存在本机。');await refresh();
}
function rowPair(dl,label,value){dl.append(node('dt',label),node('dd',value??'—'));}
async function loadReport(id){
  const data=await api(`batches/${encodeURIComponent(id)}`);if(selectedBatch!==id)return;
  if(!reportChanged({id,data}))return;
  const report=$('report');report.replaceChildren();const {batch,result}=data;
  const heading=node('div','','report-heading');heading.append(node('h3',batch.claimed_model||'检测报告'),pill(result?.identity_status||batch.status));
  if(!batch.completed_at)heading.append(action('取消检测',async()=>{await api(`batches/${id}/cancel`,'POST',{});notice('已取消这次检测。');await refresh();}));report.append(heading);
  report.append(node('p',result?reasonText(result.rejection_reason):'请求正在执行；完成后显示渠道证据与回答。','subtle'));
  const summary=node('dl','','report-summary');rowPair(summary,'声明模型',batch.claimed_model);rowPair(summary,'认证身份',result?.predicted_model||'尚未认证');rowPair(summary,'认证家族',result?.predicted_family||'尚未认证');rowPair(summary,'渠道验证',batch.route_binding_status==='VERIFIED'?'本机签名已确认目标渠道':'尚未确认目标渠道');rowPair(summary,'有效样本',`${batch.valid_samples} / ${(data.samples||[]).length}`);rowPair(summary,'校准概率',result?.calibrated_probability==null?'— 尚无独立校准':String(result.calibrated_probability));rowPair(summary,'算法版本',batch.algorithm_version);rowPair(summary,'题目 / 参考库',`${batch.challenge_version} / ${batch.bank_version}`);rowPair(summary,'开始 / 完成',`${timeText(batch.started_at)} / ${batch.completed_at?timeText(batch.completed_at):'执行中'}`);report.append(summary);
  if(result?.candidates?.length){const section=node('section','','report-section');section.append(node('h4','候选比较（不是已认证身份）'));const wrap=node('div','','table-wrap'),table=node('table'),head=node('thead'),headrow=node('tr');for(const value of ['候选模型','家族','相似分数','距离'])headrow.append(node('th',value));head.append(headrow);table.append(head);const body=node('tbody');for(const candidate of result.candidates.slice(0,6)){const row=node('tr');row.append(node('td',candidate.model,'mono'),node('td',candidate.family||'—'),node('td',scoreText(candidate.score)),node('td',scoreText(candidate.distance)));body.append(row);}table.append(body);wrap.append(table);section.append(wrap,node('p','相似分数越大越接近；距离越小越接近。这里不提供未经验证的准确率。','footnote'));report.append(section);}
  const samples=node('section','','report-section');samples.append(node('h4','原始受控测试回答'));
  for(const [index,sample]of(data.samples||[]).entries()){
    const detail=node('details','','sample'),title=node('summary',`测试 ${index+1} · ${sample.valid?'格式有效':`无效：${sample.invalid_reason}`} · ${sample.latency_ms} ms`);detail.append(title);
    const route=sample.route_evidence||{};detail.append(node('p',route.authenticated?`已确认渠道：${route.provider_id} / ${route.provider_model}，尝试 ${route.attempt_count} 次。`:'本次缺少可信渠道记录。','subtle'));
    detail.append(node('p',`接口自报 model：${sample.response_model_field||'未返回'}（不作为身份依据）`,'secondary'));detail.append(node('pre',sample.raw_response||'未获得回答正文','raw'));
    const params=node('details');params.append(node('summary','查看测试参数、解析和渠道记录'),node('pre',JSON.stringify({challenge_id:sample.challenge_id,parser_version:sample.parser_version,request_params:sample.request_params,parsed_numbers:sample.parsed_numbers,route_evidence:sample.route_evidence,input_tokens:sample.input_tokens,output_tokens:sample.output_tokens},null,2),'raw'));detail.append(params);samples.append(detail);
  }
  if(!data.samples?.length)samples.append(node('p','还没有保存的回答。','subtle'));report.append(samples);
  if(result){const metrics=node('details','','report-section');metrics.append(node('summary','完整计算记录'),node('pre',JSON.stringify(result.metrics,null,2),'raw'));report.append(metrics);}
}
function renderBanks(){
  const tbody=$('bank-rows');tbody.replaceChildren();
  $('reference-current').textContent=state.reference?.active_bank_id?`检测使用：${state.reference.active_bank_id}。结论仅适用于已验证的协议、渠道和日期。`:'当前仅作内置历史候选比较；尚未启用经过独立验证的参考实验。';
  for(const bank of state.banks||[]){
    const row=node('tr'),name=node('td'),count=node('td'),source=node('td'),validation=node('td'),operations=node('td'),metrics=bank.validation_metrics||{};
    name.append(node('strong',bank.id));secondary(name,bank.version);secondary(name,bank.algorithm_version);
    count.append(node('div',`${bank.model_count} 个模型`));secondary(count,bank.sample_count?`${bank.sample_count} 条原始样本`:'样本数尚未建立');
    source.append(node('div',bank.source_type));secondary(source,bank.trusted?'本机来源审核已记录':'来源待审核 / 历史资料');
    validation.append(node('div',metrics.independent_validation?'独立实验达到本机审核目标':'尚未通过独立验证'));
    if(metrics.active)secondary(validation,'正在用于检测');
    secondary(validation,bank.provenance);
    const actions=node('div','','row-actions');
    actions.append(action('指标 / 来源审核',()=>openReference(bank.id)),action('查看 / 导出',async()=>openData('export',await api(`banks/${encodeURIComponent(bank.id)}/export`))));
    if(metrics.independent_validation&&metrics.protocol_compatible&&!metrics.active)actions.append(action('用于后续检测',async()=>{if(!await confirmAction('使用这份参考实验？','后续检测会固定此版本；仅在来源、独立指标、实际协议及渠道日期均有效时下身份结论。'))return;await api(`banks/${encodeURIComponent(bank.id)}/activate`,'POST',{});notice('后续检测将使用这份经过本机审核的参考实验。');await refresh();},'primary'));
    if(bank.id!=='builtin-modeltrace')actions.append(action('删除',async()=>{if(!await confirmAction('删除参考库？',`删除 ${bank.id} 的本地参考资料。已有检测记录保留原版本信息。`))return;await api(`banks/${encodeURIComponent(bank.id)}`,'DELETE');await refresh();},'danger'));
    operations.append(actions);row.append(name,count,source,validation,operations);tbody.append(row);
  }
}
const splitLabels={reference:'建立参考',calibration:'拟合判断标准',test:'独立已知模型测试',unknown_test:'独立未知模型测试',channel_holdout:'保留渠道测试',time_holdout:'后续日期测试'};
const metricLabels={false_match_rate:'误认其他模型',false_replacement_rate:'误报模型替换',unknown_recall:'未知模型识别',known_class_rejection_rate:'已知模型暂不判断',top1_accuracy:'第一候选正确率',family_accuracy:'家族正确率'};
async function openReference(id){
  const detail=await api(`banks/${encodeURIComponent(id)}`);reviewingReference={id,...detail};
  const {validation}=detail,form=$('reference-form');form.reset();clearDialogNotice($('reference-dialog'));
  $('reference-summary').textContent=`${detail.bank.version} · ${validation.dataset_kind==='synthetic'?'合成机制实验':validation.dataset_kind==='real'?'待核对来源的实际采集实验':'历史描述性资料'} · ${validation.identity_validated?'已达到指定独立验证目标':'身份验证尚未完成'} · ${detail.protocol_compatible?'与当前采集协议一致':'当前采集协议不适用'}。校准概率尚未建立。`;
  $('reference-validation').textContent=JSON.stringify(validation,null,2);$('reference-provenance').value=JSON.stringify(detail.provenance,null,2);
  const wrap=node('div','','table-wrap'),table=node('table'),head=node('thead'),headrow=node('tr');
  for(const label of ['分区 / 独立组',...Object.values(metricLabels)])headrow.append(node('th',label));head.append(headrow);table.append(head);
  const body=node('tbody');
  for(const [split,metrics]of Object.entries(validation.by_split||{})){
    const row=node('tr');row.append(node('td',`${splitLabels[split]||split} · ${metrics.independent_groups} 组`));
    for(const key of Object.keys(metricLabels))row.append(node('td',metricText(metrics[key])));body.append(row);
  }
  table.append(body);wrap.append(table);$('reference-metrics').replaceChildren(wrap);
  const canReview=validation.dataset_kind==='real'&&detail.provenance?.length>0;
  $('reference-policy').hidden=!canReview;$('reference-policy').disabled=!canReview;$('reference-review-submit').hidden=!canReview;
  for(const [name,labelText]of Object.entries(policyFields)){
    const field=form.elements.namedItem(name);field.value=detail.local_review?.policy?.[name]??'';
  }
  for(const name of ['reviewer','review_note'])form.elements.namedItem(name).value=detail.local_review?.[name]||'';
  $('reference-dialog').showModal();
}
async function openTarget(target=null){
  editingTarget=target;$('target-form').reset();clearDialogNotice($('target-dialog'));$('target-dialog-title').textContent=target?'编辑模型目标':'添加模型目标';
  if(target)for(const key of ['model_id','provider_id','claimed_model','expected_family','expected_version','request_budget','interval_minutes','enabled','auto_check']){const field=$('target-form').elements.namedItem(key);if(field.type==='checkbox')field.checked=target[key];else field.value=target[key]||'';}
  $('target-dialog').showModal();
  try{const {models}=await api('models');$('model-ids').replaceChildren(...models.map(model=>new Option(model.id,model.id)));}catch{notice('模型列表暂未读取；请填写你在 Magpie 中使用的完整模型 ID。');}
}
function formValue(form,name){const field=form.elements.namedItem(name);return field.type==='checkbox'?field.checked:field.type==='number'?Number(field.value):field.value.trim();}
function openData(mode='backup',value=null){
  dataMode=mode;$('data-form').reset();clearDialogNotice($('data-dialog'));$('data-content').value=value?JSON.stringify(value,null,2):'';
  $('data-title').textContent=mode==='bank'?'导入参考库':mode==='export'?'参考库内容':'备份 / 恢复';$('data-help').textContent=mode==='bank'?'导入同算法、同题目协议的参考文件，并完整填写样本来源。上传的可信标记不会直接生效。':mode==='export'?'只包含参考资料与来源，不包含 API Key。可选择文本并复制到本地 JSON 文件。':'先生成完整备份并保存为本地 JSON 文件。恢复前会自动保留恢复点；当日已用额度不回退。';$('file-label').hidden=mode==='export';$('data-submit').hidden=mode==='export';$('data-submit').textContent=mode==='bank'?'导入资料':'恢复备份';$('data-export').hidden=mode!=='backup';$('data-dialog').showModal();
}
bindFormAction($('target-form'),async form=>{const body={};for(const key of ['model_id','provider_id','claimed_model','expected_family','expected_version','request_budget','interval_minutes','enabled','auto_check'])body[key]=formValue(form,key);try{if(body.auto_check&&!editingTarget?.auto_check&&!await confirmAction('保存并开启定时检测？',`每 ${body.interval_minutes} 分钟最多发送 ${body.request_budget} 个独立请求，额度以每日预算为上限。`))return;await api(editingTarget?`targets/${encodeURIComponent(editingTarget.id)}`:'targets',editingTarget?'PUT':'POST',body);$('target-dialog').close();notice('目标已保存。');await refresh();}catch(error){notice(error.message,true);}});
bindFormAction($('settings-form'),async form=>{const body={};for(const key of ['daily_requests','daily_tokens','concurrency','timeout_seconds','retention_days'])body[key]=formValue(form,key);try{await api('settings','POST',body);$('settings-dialog').close();notice('预算已保存，已用额度保留。');await refresh();}catch(error){notice(error.message,true);}});
bindFormAction($('data-form'),async()=>{try{const content=$('data-content').value;if(dataMode==='bank'){await api('banks','POST',normaliseBankImport(JSON.parse(content)));notice('参考资料已导入，已重算原始实验，来源等待本机审核。');}else{if(!await confirmAction('恢复这份备份？','当前数据会先保存恢复点；检测任务须先停止，当日额度取较大值。恢复会清除本机来源审核，需要重新核对参考实验。'))return;await api('restore','POST',content);notice('备份已恢复；参考实验需要重新做本机来源审核。');}$('data-dialog').close();await refresh();}catch(error){notice(error.message,true);}});
for(const [name,text]of Object.entries(policyFields)){const label=node('label',text),input=document.createElement('input');input.type='number';input.name=name;input.min='0';input.max='1';input.step='any';input.required=true;label.append(input);$('reference-policy-fields').append(label);}
bindFormAction($('reference-form'),async form=>{try{if(!reviewingReference)return;const values={};for(const name of [...Object.keys(policyFields),'reviewer','review_note','confirm_independent_source_review'])values[name]=formValue(form,name);const payload=referenceReviewPayload(reviewingReference,values);await api(`banks/${encodeURIComponent(reviewingReference.id)}/review`,'POST',payload);const id=reviewingReference.id;$('reference-dialog').close();await refresh();await openReference(id);notice('本机来源审核已记录，判断标准与独立指标已重算；达到目标后可显式用于后续检测。');}catch(error){notice(error.message,true);}});
$('reference-reset').addEventListener('click',async()=>{try{await api('reference/reset','POST',{});notice('后续检测仅作历史候选比较。');await refresh();}catch(error){notice(error.message,true);}});
$('data-file').addEventListener('change',async()=>{const file=$('data-file').files[0];if(!file)return;if(file.size>32*1024*1024){notice('请选择 32 MB 以内的 JSON 文件。',true);return;}$('data-content').value=await file.text();});
$('data-copy').addEventListener('click',async()=>{const field=$('data-content');field.focus();field.select();try{await navigator.clipboard.writeText(field.value);notice('内容已复制。');}catch{notice('内容已选中，请使用 Ctrl+C 或系统复制，再保存为 JSON 文件。');}});
$('data-export').addEventListener('click',async()=>{try{$('data-content').value=JSON.stringify(await api('backup'),null,2);notice('备份已生成，不含 API Key。');}catch(error){notice(error.message,true);}});
$('target-add').addEventListener('click',()=>openTarget());$('refresh').addEventListener('click',refresh);$('history-filter').addEventListener('change',renderHistory);$('bank-import-open').addEventListener('click',()=>openData('bank'));$('backup-open').addEventListener('click',()=>openData());
$('settings-open').addEventListener('click',()=>{if(!state)return;clearDialogNotice($('settings-dialog'));for(const [name,value]of Object.entries(state.settings)){$('settings-form').elements.namedItem(name).value=value;}$('settings-dialog').showModal();});
for(const button of document.querySelectorAll('[data-close]'))button.addEventListener('click',()=>$(button.dataset.close).close());
addEventListener('hashchange',setView);addEventListener('message',event=>{if(event.source===parent&&typeof event.data?.modelCheckVisibility==='boolean')visible=event.data.modelCheckVisibility;});
setView();refresh();setInterval(()=>{if(visible&&document.visibilityState!=='hidden'&&!document.querySelector('dialog[open]'))refresh();},2000);
