# P4 包契约（独立集成边界）

本包不依赖 `modelstore`、HTTP 或插件 runtime，不修改 P2 的 ModelTrace scorer。

```go
func Load(bankJSON, corpusJSON []byte, options Options) (*Engine, ValidationReport, error)
func MarshalBank(corpusJSON []byte, version string, models []Model) ([]byte, error)
func BuiltinLegacy() (*Engine, ValidationReport)
func Wilson(events, total int, confidence float64) (Metric, error)
func (e *Engine) Decide(request Request) Decision
func (e *Engine) Report() ValidationReport
func (e *Engine) RawBankJSON() []byte
func (e *Engine) RawCorpusJSON() []byte
func ProtocolSHA256(challenges []fingerprint.Challenge, systemPromptSHA256 string) (string, error)
func MarshalBundle(bankJSON, corpusJSON []byte) ([]byte, error)
func ParseBundle(raw []byte) (ReferenceBundle, error)
func LoadBundle(raw []byte, options Options) (*Engine, ValidationReport, error)
func (e *Engine) ProvenanceJSON() ([]byte, error)
func CredentialFindingsJSON(raw []byte) ([]CredentialFinding, error)
```

- `MarshalBank` 只生成绑定原始 corpus SHA-256 的版本化 manifest，不授予验证状态。
- `Load` 重新严格解析 corpus 中的原始回答，计算 ModelTrace 355 维特征，按参考组重建每模型/挑战的单位参考中心，再仅从 calibration 分区推导距离、间隔和一致性阈值。此归因距离方法另行版本化，不冒充 legacy robust score。
- 已导入的 `validated`、`thresholds`、`validation_metrics` 仅为不采信的历史字段；不覆盖重算结果。原始 corpus、回答和来源证据均绑定 SHA-256，哈希证明完整性而不是标签真实性。
- `Options.Policy` 来自实验负责人明确指定的目标，不设通用身份门槛、固定充分样本数或默认概率。Wilson 区间按独立完整组计数，不将同批挑战或多种声明当成独立试验。
- `Options.VerifyProvenance` 是集成层提供的**独立审核函数**，逐条核验绑定的采集记录、标签、渠道、日期和证据。导入文件中的布尔值不是审核函数。未提供审核或真实数据缺失时，身份验证保持未完成。
- 所有六分区均须独立：reference、calibration、test、unknown_test、channel_holdout、time_holdout；group/batch/session 不跨组复用。渠道 holdout 的渠道、时间 holdout 的 UTC 日期不得出现在其他分区；unknown_test 的未知模型不得用于 calibration。
- 合成 corpus 必须显式标记 `synthetic`，来源类型也须为 `synthetic`。可在单测中检验 MATCHED/MISMATCHED/UNKNOWN/INCONCLUSIVE 的内部机制，但公开 `Decide` 不把合成实验升级为真实身份验证。
- `BuiltinLegacy` 保持 ModelTrace 原始 bank 字节和 digest，报告来源未验证、缺少独立 corpus/校准。主代理可以继续用 P2 `Rank` 展示描述性候选，不将它们转成归因结论。
- `Decision.CalibratedProbability` 始终为 nil（JSON `null`）；Brier/ECE 没有拟合概率模型时同样为 null。
- 请求的 route 标记必须由主服务的真实签名检查产生，不从导入 corpus 或上游 `model` 字段推断。任何缺失/不一致证据返回 INCONCLUSIVE 和明确 reasons。

## 主服务 JSON envelope

`modelstore.Bank.Data` 可按已协调的 envelope 保存：

```json
{
  "format": "magpie-model-check.reference-bundle",
  "bank_json": "原始 manifest JSON 字符串",
  "corpus_json": "原始 corpus JSON 字符串"
}
```

外层可以正常 JSON 编解码，两个内层字符串必须保持原字节；不要重新序列化内层 JSON 再沿用旧 hash。`LoadBundle` 等价于 `ParseBundle` 后把两个原字符串转成 UTF-8 bytes 调用 `Load`。

内层 bank manifest 由 `MarshalBank` 生成，格式为 `magpie-attribution-bank`，字段为 `schema_version/version/algorithm_version/method_version/corpus_sha256/challenge_set_sha256/kind/models/source_type/validated`。`models` 项为 `{id,family}`；`source_type` 为 `raw_corpus`；`validated` 初始为 false，导入 true 不改变重算结果。builtin legacy 原始 JSON 可以单独装入 bundle，`corpus_json` 为空，仍只作未验证描述性资源。

内层 corpus 字段为 `schema_version/algorithm_version/kind/system_prompt_sha256/challenges/records`。`challenges` 使用 P2 `fingerprint.Challenge` 的完整 JSON 字段。`Record` 的完整字段在 `types.go`，包括独立 group/batch/session、分区、模型/家族/渠道/时间、原始回答/hash、请求 ID/参数、标签和路由证据及其原始 JSON 值的 hash。所有时间按 UTC 日期进行分组校验。`ProtocolSHA256` 绑定按 ID 排序的完整挑战及 system-prompt hash；prompt、数量、parser、版本和 parameters 都参与绑定。nil parameters 按空对象规范化；JSON 数字保留精确 token，种子等大整数不会先舍入成 float64。参数对象忽略成员顺序，但不同数字记法不进行未经验证的等价转换。

运行时 `Request` 字段为 `claimed_model/bank_version/algorithm_version/challenge_set_sha256/channel/collected_at/route_verified/system_prompt_sha256/request_parameters/observations`。每个 `Observation` 有 `challenge_id/raw_response/finish_reason`，可带自己的 `request_parameters`；缺省时使用 request 的共同参数。route 标记由主服务验签后的可信收集器构造，外部请求体没有授予它的权限。新渠道、新日期或从不同实验拼成的未观察渠道/日期组合都保持 INCONCLUSIVE。

`ValidationReport` 分别报告 `integrity_validated/provenance_validated/mechanics_validated/identity_validated`。来源审核不是身份验证：只有真实 corpus、全部独立 holdout 目标通过且来源审核通过，身份验证字段才成立。合成数据的 `identity_validated` 始终为 false，即使机制测试通过。

`ProvenanceJSON` 返回包含完整 Record、Challenge 和 corpus hash 的绑定列表，供独立来源审核和凭据扫描使用。`CredentialFindingsJSON` 会扫描整个 envelope、所有未知字段、嵌套 JSON 字符串、来源证据和常见凭据文本，只报告路径与原因，不回显值。扫描是常见模式检查，不是任意不透明字符串的无凭据证明；主服务仍须执行本地数据字段审核、禁止密钥导出以及认证的审核/激活流程。签名密钥留在主服务本地，不属于本包或导出格式。

## 最小数学方法与验证含义

方法版本为 `hellinger-reference-centroid-v1`。每个模型/挑战以 reference 完整组的 355 维平方根概率特征均值重新 L2 归一化为参考中心。类条件距离为 `sqrt(sum((feature-center)^2)/2)`；完整组取挑战距离平均。`score=1-distance` 仅是描述性显示值，不是 P2 legacy robust score，也不是概率。

阈值采用最小的经验 genuine-calibration 包络：类距离上限及每挑战距离上限取真实标签 calibration 组观测最大值，间隔与一致性下限取观测最小值。间隔非正、版本不可区分或校准缺失时不形成可用身份判定。最终 test/unknown/channel/time 数据只评估规则，不回写阈值。多个类包络重叠、部分挑战落在已知类而其他挑战不一致时，结果为 INCONCLUSIVE；只有全部挑战都在所有类包络之外时才为 UNKNOWN。

没有预设误报门槛、校准概率或充分组数。`Policy` 是显式实验目标；每个完整组只计一次，Wilson 上/下界与这些目标比较。FMR 定义为每独立组“任一错误已知身份被接受”的保守事件率，不把 K 个声明重复算成 K 个独立试验。False Replacement 以已知组为分母；Unknown Recall 以未知组为分母；Known Rejection 包含 UNKNOWN 和 INCONCLUSIVE，防止弃权伪装成高准确率。缺少总体时，值和 CI 为 null。

报告包含按 split/model/model+split/channel/UTC date 分层的指标和混淆矩阵；reference 指标是建库内重用，calibration 指标是拟合内诊断，都不替代独立测试。审核目标逐个检查最终 split、各已知版本及渠道，不用总体准确率掩盖薄弱版本。逐日期指标保留自己的样本数与 CI，不声称新日期泛化。Wilson 的独立 Bernoulli 假设仍需要来源审核和实验设计支持；group/session/batch/date 校验不等于物理采集独立性的证明。

全部夹具显式标记 synthetic，只证明机制。当前实际模型参考样本为 0；正式部署的经验阈值、可分性、误报能力和适用日期/渠道仍需真实独立采集验证。

此契约先于集成固定。主代理负责持久化映射和调用适配；本包不写集成目录，不签署来源审核，不改 module 名称。

## 本轮验证状态（2026-11-17）

- P2/P4 最终普通测试、`go vet` 和 Windows GCC 16.2.0 `go test -race` 均通过；P4 的最终 statement coverage 为 91.5%。
- 独立静态审查发现的扫描错误回显、大小写字段别名和非法 UTF-8 三项问题均有真实 RED→GREEN 回归。来源审核回调的参数 map 隔离、精确大整数参数、分区泄漏和未观察渠道/日期组合也有对抗测试。
- 独立副本验证了共享参数 map 的旧行为回滚；身份验证条件反转变异以失败测试检出，恢复副本后测试通过。生产源码始终保留修复。
- 证据在 `artifacts/rebuild-work-20261117/fingerprint/p4/VERIFICATION.txt`，全证据索引在其上级 `EVIDENCE_INDEX.md`。所有数据夹具仅为合成机制测试，真实模型参考样本仍为 0。
- 本包源码可以冻结供主任务迁移 module。仓库级最终 `go test ./...` / `go vet ./...` 另记录了其他 `artifacts` 临时 Go 文件的构建错误；主服务新增审核端点测试的 404 与清理错误由集成任务处理，不计为本包测试通过。
