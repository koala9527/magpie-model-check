export const policyFields=Object.freeze({
  confidence:'统计置信水平（0–1，不含端点）',
  max_false_match_rate:'把其他模型认成声明模型：允许的最高误认率',
  max_false_replacement_rate:'把正常模型误报为替换：允许的最高误报率',
  min_unknown_recall:'未知模型识别率：至少达到',
  max_known_rejection_rate:'已知模型暂不判断的比例：最多允许',
  min_top1_accuracy:'已知模型第一候选正确率：至少达到',
  min_family_accuracy:'模型家族正确率：至少达到'
});

export function referenceReviewPayload(detail,values){
  const reviewer=String(values.reviewer||'').trim(),review_note=String(values.review_note||'').trim();
  if(!reviewer||!review_note||values.confirm_independent_source_review!==true)throw Error('请填写审核人和审核说明，并确认已独立核对全部来源记录。');
  const policy={};
  for(const field of Object.keys(policyFields)){
    if(values[field]===undefined||String(values[field]).trim()==='')throw Error('请明确填写每项实验目标；页面没有自动接受的默认门槛。');
    const value=Number(values[field]);
    if(!Number.isFinite(value)||value<0||value>1||(field==='confidence'&&(value===0||value===1)))throw Error('实验目标请填 0–1 的数值；置信水平不接受 0 或 1。');
    policy[field]=value;
  }
  const approved_record_ids=(detail.provenance||[]).map(binding=>binding.record.id);
  if(!approved_record_ids.length||new Set(approved_record_ids).size!==approved_record_ids.length)throw Error('来源记录不完整，请重新读取参考库。');
  return {bank_sha256:detail.bank.sha256,corpus_sha256:detail.validation.corpus_sha256,provenance_sha256:detail.provenance_sha256,
    reviewer,review_note,confirm_independent_source_review:true,approved_record_ids,policy};
}

export function normaliseBankImport(input){
  if(input?.format==='magpie-model-check.reference-bundle'){
    const manifest=JSON.parse(input.bank_json);
    return {version:manifest.version,algorithm_version:manifest.algorithm_version,challenge_version:'modeltrace-challenge-v1',
      source_type:'manual_import',provenance:'人工导入的绑定原始实验；仍需独立来源审核与指标验证',trusted:false,data:JSON.stringify(input)};
  }
  if(!input||typeof input.data!=='string')throw Error('请选择参考 bundle，或带有原始 data 字符串的参考库导出文件。');
  JSON.parse(input.data);
  return input;
}

export function metricText(metric){
  if(metric?.value==null||!metric.total)return '未测量（0 组）';
  const text=`${metric.events} / ${metric.total} 组 · ${(metric.value*100).toFixed(1)}%`;
  const ci=metric.wilson_ci;
  return ci?`${text}；${(metric.confidence*100).toFixed(1)}% 区间 ${(ci.lower*100).toFixed(1)}–${(ci.upper*100).toFixed(1)}%`:`${text}；置信水平未指定`;
}
