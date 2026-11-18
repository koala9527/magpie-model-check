# 参考库怎么准备、审核和启用

更新：2026-11-17。[当前进度](p0-report.md) · [后续安排与 Goal](implementation-plan.md) · [技术合约](../internal/attribution/README.md)

**当前真实模型参考样本为 0。** 内置 ModelTrace legacy bank 是历史描述性资料；标成 `synthetic` 的数据只用于检查程序机制。二者都不升级为真实身份参考，概率保持 `null`。

参考库不是一张模型排名表，而是一份能回到原始回答和真实来源记录的实验。下面的步骤适用于之后实际采集的资料，格式示意不是已采集样本。

## 1. 导入不等于审核，审核不等于激活

| 顺序 | 你需要做什么 | 程序检查什么 |
| --- | --- | --- |
| 导入 | 保存原始 bundle，导入参考库或原样导入已有导出文件 | 原始字符串、版本、凭据、hash、回答解析和独立分组；导入可信标记忽略 |
| 检查 | 打开参考库详情，核对 raw 资料、分区、每个模型/渠道样本量与不足原因 | 重算报告和完整 provenance 记录列表 |
| 来源审核 | 在本机逐条查证标签、渠道、日期、请求和原始回答；填写审核人、说明及全部 Policy | 当前 bundle/corpus/provenance hash 与全部记录 ID 绑定，不能漏项或重复 |
| 重新评估 | 阅读提交审核后的新报告，不只看“审核已保存” | 用原文重建中心、只从 calibration 推导规则，并检查独立测试和 Wilson 区间 |
| 显式激活 | 满足条件后明确选择启用这份库 | 真实来源、身份验证和当前采集协议全部通过；有检测运行时先完成或取消 |

改文件、同 ID 重新导入、改 Policy 或重新审核会取消相应激活；需要再看新报告并显式激活。`reviewed=true` 表示本机审核记录已保存，不等于 `identity_validated=true`，也不表示已经启用。

没有完整来源或数据不足，就保留资料并补证据，不将 `validated` 改成 true，不抄入一组阈值来绕过重算。

## 2. 原始文件和 hash 怎样对应

bundle 外层格式如下；两个内层字段是**完整原始 JSON 的字符串，不是嵌套对象**：

```json
{
  "format": "magpie-model-check.reference-bundle",
  "bank_json": "这里放完整原始 manifest JSON 字符串",
  "corpus_json": "这里放完整原始 corpus JSON 字符串"
}
```

`bank_json` manifest 使用 `magpie-attribution-bank` 格式，包含版本、算法、方法、corpus hash、挑战协议 hash、数据种类和模型/家族名单。用现有 `attribution.MarshalBank` 绑定原始 corpus，再用 `MarshalBundle` 组装，不手写一个“已验证”的 manifest。模型名单至少包含两个不同已知类别，具体版本要明确。

| 详情字段 | hash 对应的内容 |
| --- | --- |
| `bank.sha256` | 导入资料的 `data` 原始字节；通常是整个 bundle |
| `validation.bank_sha256` | bundle 中 `bank_json` 的原始 manifest 字节 |
| `validation.corpus_sha256` | `corpus_json` 的原始字节 |
| `provenance_sha256` | 服务当前返回的完整来源绑定列表 |
| `validation.challenge_set_sha256` | 完整挑战、系统提示词 hash 和参数形成的版本化协议 |

**审核请求的 `bank_sha256` 要取 `bank.sha256`，不是 `validation.bank_sha256`。** 导入后从当前详情取这三个审核 hash；不要重排原始 JSON、改换行或修改一个回答后沿用旧 hash。hash 证明内容一致，不证明模型标签真实。

### 准备页面/API 接受的导入文件

参考库 HTTP 接口保存的是 `bankTransfer`：`data` 仍是原始 bundle 字符串。以下标准库 Python 示例可把已有 bundle 包成 `bank-import.json`，供页面“导入参考库”或本机认证接口使用；它不修改内层 bank/corpus，也不授予审核资格。

```python
from pathlib import Path
import hashlib
import json

raw = Path("reference-bundle.json").read_bytes()
text = raw.decode("utf-8")
bundle = json.loads(text)
assert bundle["format"] == "magpie-model-check.reference-bundle"
assert isinstance(bundle["bank_json"], str)
assert isinstance(bundle["corpus_json"], str)
manifest = json.loads(bundle["bank_json"])
transfer = {
    "version": manifest["version"],
    "algorithm_version": manifest["algorithm_version"],
    "challenge_version": "modeltrace-challenge-v1",
    "source_type": "manual_import",
    "provenance": "人工导入的原始实验；实际来源见 corpus，仍需逐条独立审核",
    "trusted": False,
    "sha256": hashlib.sha256(raw).hexdigest(),
    "data": text,
}
Path("bank-import.json").write_text(
    json.dumps(transfer, ensure_ascii=False, indent=2) + "\n",
    encoding="utf-8",
)
```

已有 `GET /api/banks/{id}/export` 导出文件可以原样重新导入，保留其 `data` 字符串。裸 bundle 与导入文件不是同一个 HTTP 请求结构，API 调用使用上面的包装。

## 3. 怎样做本机来源审核

先完成或取消检测，再逐条查证：

- 模型标签和版本依据是什么，能否由独立采集资料核验，而不只是供应商自称或响应 `model`。
- 渠道、UTC 时间、请求 ID、采样参数和系统提示词，是否与实际请求记录相符。
- 原始回答和证据 hash 是否相符，是否有截断、改写、重试或复制回答。
- group/batch/session 和数据分区是否按真实采集事实划分。
- 来源资料是否去除了密钥、认证头和无关业务对话；扫描器通过不是任意不透明文本都没有秘密的证明。

审核人填写姓名/标识和具体核验说明，确认独立来源审核，并提交**全部且无重复**的 `approved_record_ids`。这个列表记录审核人的实际工作，不是把所有 ID 勾上就自动完成来源鉴定。

Policy 是事先明确的实验目标，不是导入的分类阈值，也不是身份概率。所有目标都填写 0–1 数值；置信水平不含 0 和 1：

| 字段 | 用人话解释 |
| --- | --- |
| `confidence` | 希望统计区间按多高的置信水平计算 |
| `max_false_match_rate` | 把其他模型认成某个已知身份，最多允许多少 |
| `max_false_replacement_rate` | 把正常模型误报为替换，最多允许多少 |
| `min_unknown_recall` | 库外模型至少有多少能识别为未知 |
| `max_known_rejection_rate` | 已知模型被拒绝判断的比例，最多允许多少 |
| `min_top1_accuracy` | 已知模型第一候选的正确率至少多少 |
| `min_family_accuracy` | 家族正确率至少多少 |

程序不自动填一套可通过的目标，也不把合成夹具里的目标用作真实实验标准。缺少总体时指标和区间为空；样本不足以支持目标时继续补采。Wilson 按独立完整组计数，不把同组的三条挑战或多个声明当独立试验。

### 本机认证 API 对照

由插件页面或已认证的本机客户端调用。页面/控制会话凭据只用于本机请求，不写进 bundle、审核 JSON 或实验报告；**它不是来源审核密钥**。

| 请求 | 用途 |
| --- | --- |
| `POST /api/banks` | 提交上面的导入包装 |
| `GET /api/banks/{id}` | 获取 validation、全部 provenance、审核 hash、协议兼容和当前 active 状态 |
| `POST /api/banks/{id}/review` | 提交逐条来源审核和 Policy；服务同时重新计算并返回新 validation |
| `POST /api/banks/{id}/activate`，请求体 `{}` | 明确启用已通过真实验证且兼容当前协议的库 |
| `POST /api/reference/reset`，请求体 `{}` | 取消当前激活选择，回到未激活真实库的状态；不等于作废所有来源审核 |
| `GET /api/banks/{id}/export` | 导出原始 data 资料，不导出审核密钥或本机签章 |

审核请求包含 `bank_sha256/corpus_sha256/provenance_sha256/reviewer/review_note/approved_record_ids/confirm_independent_source_review/policy`。hash 取当前详情，Policy 按上表完整填写，确认字段只在实际审核完成后设为 true。详情里的 `local_review` 可以用于本机复查，不把它当可携带的外部认证。

## 4. 激活之后仍有哪些限制

- 当前收集器固定使用 ModelTrace 三条挑战及严格 parser；参数包含 `stream=false`、`max_tokens=2048`，系统提示词为空。这些是当前请求协议，不是最优采样设置或统计保证。按服务的完整挑战及 `ProtocolSHA256` 生成协议，不手改提示词或另加参数后沿用旧 hash。
- 当前渠道、UTC 日期和这个渠道/日期组合要出现在独立验证范围中。新日期、未见渠道或拼出来的组合保持 `INCONCLUSIVE`，不会自动延长有效期。
- 每轮挑战完整；有多轮时分别计算，状态、预测模型和家族必须一致。部分轮次、同轮渠道变化或轮间分歧均拒绝身份判断。
- route V3 认证本机看到的请求并绑定完整响应；这还不是 Magpie 最终上游请求线上证明。当前宿主在中间件后会整理请求，插件因此固定 `protocol_verified=false` 并拒绝身份结论。只有将来宿主级最终请求证明存在且独立通过测试，才可启用协议准入。

来源审核、独立指标通过、激活、协议与本次采集认证是分开的关口。任何不足都保留理由；当前 `calibrated_probability=null`，分数不改写为百分比。

## 5. 后续真实参考实验怎么做

1. **定范围和预算。** 列出已知模型/准确版本/家族、相邻版本、未知模型、渠道和日期，填写费用上限、停止条件、审核方法及全部 Policy。未确定实际来源或预算时先准备记录，不发付费请求。
2. **固定当前采集协议。** 使用完全相同的挑战、系统提示词、请求参数和 parser；保存协议 hash。不同参数或提示词另开实验版本。
3. **按独立用途采集。** `reference` 建库，`calibration` 定规则，`test` 测已知，`unknown_test` 测未见模型，`channel_holdout` 留渠道，`time_holdout` 留未来 UTC 日期。已知类在各相应分区覆盖；校准、留渠道、留时间还需要未知样本，最终未知模型不复用未知校准模型。
4. **保留真实分组。** 一个完整组包含每条挑战各一次。组、批次、会话不跨组复用，UTC 日期不跨分区，留渠道不出现在其他分区，留时间晚于其他数据。一次批次里的多轮可用于一致性检查，但不能拆成多个独立统计组；改 ID 不会创造独立性。
5. **保存原文和依据。** 留下有效及无效请求；误格式/截断记录单独保留，不静默删除。当前用于校准的 corpus 要有可严格解析的完整组；补采替代样本时也保留失败台账和真实采集关联。
6. **生成并检查 bundle。** 按真实资料填写 corpus，用现有 MarshalBank/MarshalBundle 生成版本化文件；核对原始回答、证据和整个 corpus 的 hash。
7. **审核、重评，再决定是否激活。** 按前文准入链执行。查看各分区、模型/版本、渠道和日期的样本数、误认/误报、未知识别、拒绝判断及 Wilson 区间，不以总体高分掩盖薄弱版本。
8. **新数据或方法变更另记版本。** 测试未达目标就保留失败和补采需求；不回写最终 holdout 来修规则，也不降 Policy 达标。改变方法后准备新的未见测试资料。

### Corpus 格式速查

顶层字段为 `schema_version/algorithm_version/kind/system_prompt_sha256/challenges/records`；当前 schema 为 1，算法为 `modeltrace-6cd4a8ff-v1`。`challenges` 使用完整 `fingerprint.Challenge`；`records` 每条包含：

```text
id, group_id, batch_id, session_id, split,
model, family, channel, collected_at, source_type, challenge_id,
raw_response, response_sha256, finish_reason, request_id,
request_parameters, system_prompt_sha256,
label_evidence, label_evidence_sha256,
route_evidence, route_evidence_sha256
```

时间使用 RFC3339，分区按 UTC 日期检查。`request_parameters` 是该挑战的实际参数 JSON 对象；两个 evidence 字段是可逐条独立核验的原始 JSON 值，各有对应 raw hash，不携带密钥。来源类型可为 `official_api_reference`、`magpie_provider_reference`、`third_party_channel`、`unknown_test`、`manual_import`；这些名字不自动授予可信性。

实际采集数据经核验后才登记 `kind=real`。模拟数据必须为 `kind=synthetic` 且记录来源为 `synthetic`；不要改标签把模拟输出升级为真实参考。完整类型见[attribution/types.go](../internal/attribution/types.go)，可复用的后续任务提示见[执行计划的 Goal 说明](implementation-plan.md#6-可复用的后续-goal-说明)。

## 恢复与导出

来源审核密钥 `reference-review.key` 和带本机签章的审核状态留在本机私有数据目录，不随参考库或数据库备份导出。导入他人文件的“可信”字段不会复制他人的审核权；本机完整性签章也不是官方模型身份凭证。

**任何通过请求读取和凭据检查、进入数据替换阶段的有效恢复尝试，都会先持久化清空本机来源审核及激活选择。即使随后深层校验拒绝恢复，这些旧审核也已经作废。** 正在检测、超限或含凭据的输入在此前拒绝；若审核状态清空写入失败，恢复不继续。

恢复后重新打开原始资料，逐条审核、重评、显式激活。导出的文件保留可复查数据而不是审核授权；当日已记用量不因恢复而回退。完整基础恢复已有页面记录，最终发布 smoke 仍由主任务收尾。
