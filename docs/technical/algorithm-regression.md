# ModelTrace 数值移植与回归记录

本轮实现 `internal/fingerprint`，不接管网关、存储、身份判定或旧服务。固定算法版本为 `modeltrace-6cd4a8ff-v1`，严格解析版本为 `strict-integers-v1`。

## 1. 固定基线与声明

上游为 [ModelTrace 固定提交](https://github.com/xqy2006/ModelTrace/tree/6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2)，完整 commit：`6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2`。

MIT License、`Copyright (c) 2026 xqy2006` 的完整声明保留在 `internal/fingerprint/reference/LICENSE`；移植文件也标注了出处。原 `internal/check/modeltrace.go` 和旧 JS/bank 保持不变。参考副本及来源表见 `internal/fingerprint/reference/README.md`。

| 资源 | 本轮原始字节 SHA-256 | 统一 LF 后 SHA-256 |
| --- | --- | --- |
| JS | `adff55637f6c0534c2c10703e028d4d1970d325e8daecb55efaa7f95a1938d5c` | `83fa5bd611e18f8339122582335123c8ea168ed242298bb31f4e363abeeb6e4a` |
| bank | `eeab6ff54d6ffb2b294ebfd6cce25bce387f7a268b66759144f64c16f6c66b9f` | `f267d0513aa06981d024ec5394746fceddf44e4e8a75fa6bf8304255f4b32d6d` |
| Python | `387fe5c44cc5f859995432267b0d3b462fc26e82419fc22e3ee7f36cc2502d98` | 同左 |

JS/bank 的本机副本使用 CRLF。加载评分库时核对 canonical LF hash；导出时不改字节和换行。`LegacyBankSHA256()` 对实际嵌入的原始字节计算，不能用 canonical hash 冒充原始导出文件的 hash。

## 2. Go API

```go
const AlgorithmVersion = "modeltrace-6cd4a8ff-v1"
const ParserVersion = "strict-integers-v1"

type Challenge struct {
    ID string
    Version int
    Prompt string
    SHA256 string
    Min, Max, Count int
    ParserVersion string
    Parameters map[string]any
}

func DefaultChallenges() []Challenge
func Parse(raw string, c Challenge, finishReason string) ([]int, string)
func Features(numbers []int) ([]float64, error)
func OrderedFeatures(numbers []int) ([]float64, error)
func Rank(numbers []int) ([]Candidate, error)
func LegacyBankJSON() []byte
func LegacyBankSHA256() string

type Candidate struct {
    Model string    // JSON model
    Family string   // JSON family
    Score float64   // JSON score
    Distance float64 // JSON distance
}
```

- `DefaultChallenges` 固定选择原生成器中已有的第一个文案变体，数量分别为 292、312、332；ID、version、UTF-8 prompt SHA-256 可重算。原 Python 的固定选择生成器已逐字核对这三条 prompt。
- 每次调用返回新的挑战切片及各自独立的 `Parameters` map。map 为空表示维持旧实现未显式指定的请求参数，不补造 `temperature`、`top_p` 或 Token 设置。这也不代表采样参数已被实验固定和验证。
- **固定三条挑战和固定数量只保证可复现，不承诺统计充足性或身份识别能力。**
- `LegacyBankJSON` 返回原始 bank 字节的独立副本，调用方修改切片不改变嵌入库和评分；`LegacyBankSHA256` 返回原始字节的小写 hex digest。正式元数据/导出可直接使用这两个接口，无需再次复制文件。
- 特征和排名支持任何非空的 `1..355` 整数序列；空值或越界值返回 `nil, error`，不改变调用方切片。`Rank` 返回全部 18 个候选，分数降序，完全相同时保持 bank 原始顺序；JSON 没有概率和身份判定字段。

## 3. 严格解析契约

成功返回完整 `[]int` 和空字符串；失败始终返回 `nil` 和原因。不抽取最长片段，不忽略负号，不滤掉越界值，不截取前 N 项凑数量。

仅接受 ASCII 无符号十进制整数、ASCII 逗号和 Unicode 空白。前导零和重复值有效。逗号不能产生空项；代码围栏、JSON 数组、正号、其他标点不作为格式容错处理。

| 原因 | 触发条件/例子 |
| --- | --- |
| `invalid_challenge` | 不支持的 parser version；非正 count；范围不在 `1..355` 内或 min > max |
| `truncated` | finish reason 为 `length`、`max_tokens`、`max_output_tokens`、`token_limit`、`incomplete`，忽略大小写/外围空白；即使数量碰巧正确或回答为空也拒绝 |
| `empty` | 除外围空白外没有内容 |
| `negative` | 合法数字记法以 ASCII `-` 开始，包括 `-0`、负小数 |
| `decimal` | 小数记法或科学计数法，例如 `.2`、`2.`、`2.5`、`2e2` |
| `text` | 非数字 token 含字母/非 ASCII 数字，包括前置说明、括号里的文字、尾部中文 |
| `invalid_format` | 其余非整数格式，或空逗号项，例如 `[1,2,3]`、`+2`、`1,,2` |
| `out_of_range` | 数字低于 c.Min、高于 c.Max，或整数转换溢出 |
| `wrong_count` | 格式和值均有效，但数量不等于 c.Count |

优先顺序：挑战契约 → 明确截断 → 空值 → 按输入 token/分隔符顺序出现的内容错误 → 最终数量。一个 token 内先识别完整的负数/小数记法，再检查全部字符；文字前有标点也仍报告 `text`。未列出的 finish reason 不参与截断判断，完成状态由采集层另行记录。

该 parser 与 legacy `/\d+/g` 最长数字段解析分开测试。新 parser 没有 legacy 的 80 项 / 55% 容忍规则；不把宽松解析通过解释为身份样本有效。

## 4. 数学定义

### 355 维边际特征

`H[i] = sqrt((count[i] + 0.5) / (N + 0.5 * 355))`，每个取值对应一维，平方范数为 1。输入顺序不影响此特征。

### 74 维有序特征

按原始顺序均分四块，余数分配给较早的块；每块 16 个等宽 bin，index 为 `min(15, floor(((x-1)/355)*16))`，各 bin 平滑 0.5 后取概率平方根。追加 `x % 10` 的 10 维个位分布，同样平滑 0.5。

四个分块各 16 维，加个位 10 维，总计 74 维。短序列的空块仍有平滑分布；每个分布的平方范数为 1，完整 74 维向量的平方范数为 5。

### 稳健分数与距离

- 边际特征按 bank mean/scale 标准化，按 **JS 顺序逐个减去当前残差的 nuisance basis 投影**，L2 归一化后与各 centroid 点积，再在模型维度标准化。
- 有序特征的 template 部分对每个模型取 12 个 environment centroid 点积的最大值并标准化；nuisance 部分投影后归一化、点积、标准化。两部分各占 0.5，再标准化。
- 最终分数 `(1-weight)*marginal + weight*ordered`；固定 bank 的 weight 是原有 `0.25`，没有增加最后一次标准化。原 JS 的缺失/零权重有序块分支也保留。
- 范数/标准差下限 `1e-12` 来自原实现，是数值稳定项，不是身份判断门槛。
- `Candidate.Distance = sqrt(JS(p,q)/ln(2))`，即旧 `1-profile_similarity` 的 Jensen–Shannon 归一化距离。p 是观测直方图（不平滑），q 是加 0.5 平滑的参考直方图。距离越小越接近，但排名只使用 robust score。
- Go 直接计算 distance，旧 JS 先计算 similarity 再相减，产生最多约 `1.11e-16` 的舍入差异。

原 Python 同时执行矩阵投影，并对边际分数多标准化一次。Go 明确复现 JS 运算顺序；独立 Python/NumPy 回归保留原 Python 的这些不同操作，而不是把它们改成 Go 来取得一致。

## 5. Golden 与独立回归

固定 28 组输入：1/2/3/4/5 项短序列、上下端点、重复值、所有 bin 边界、正反均匀序列、LCG 产生的 79/80/81 等多种长度及 292/312/332 附近非四整除长度、个位 0、相同直方图但不同排列。

另有 1/2/3/4 个有效回答的 4 组批次。JS 使用原公开 `analyzeGlobalOutputs`；Go 单测逐条评分求均值并合并计数核对该行为，不增加新的公开批次身份判定接口。共有 32 组完整排名、576 个候选位置逐项核对。

- `testdata/golden.mjs` 只在内存移除 ES export 标记来调用原私有特征/评分函数，不改原源码数学表达式。
- `testdata/golden.json` 是旧 JS/bank 的数值期望值，Go 测试不需要 Node/Python。
- `scripts/fingerprint-golden.py` 默认重跑 JS、执行 Go golden 测试、运行独立 stdlib Python 实现；加 `--upstream-python` 还执行固定未修改的原 Python/NumPy 源码及固定选择 prompt 生成器。
- 只有显式 `--write` 才重建 golden。正常检查不以 Go 输出覆盖 JS 期望值。
- 回归驱动器覆盖数值漂移、非有限数、完整排序漂移及旧 Go observation 文件误复用；执行前移除旧 observation，成功 exit 但没有新报告也失败。
- 执行前同时移除旧 `regression.json`；失败不会留下上一轮的 PASS 报告。Go golden 单测在任何数值断言失败后不会打印成功摘要或写入观测文件。

绝对误差预算为 `1e-12`：这是明确的 float64 工程回归预算，不是统计校准门槛，也不是上游宣称的精度保证。实际最大误差低于该预算；绝对误差和非零期望值的相对误差逐项保存在 `regression.json`。

本轮 Windows/amd64，Go 1.27.1、Node 22.23.2、Python 3.14.6、NumPy 2.5.4 的实测：

| 对照 | 355 / 74 维最大绝对误差 | 最终 score 最大绝对误差 | distance 最大绝对误差 | 完整排序 |
| --- | --- | --- | --- | --- |
| Go ↔ JS | `0 / 0` | `0` | `1.1102230246251565e-16` | 32/32 一致 |
| 独立 stdlib Python ↔ JS | `0 / 0` | `1.7763568394002505e-15` | `8.8817841970012523e-16` | 32/32 一致 |
| 固定原 Python/NumPy ↔ JS | `0 / 0` | `2.2204460492503131e-15` | `8.8817841970012523e-16` | 32/32 一致 |

## 6. 可重复执行与证据

在仓库根目录执行：

```powershell
go test ./internal/fingerprint -count=1 -v
go test ./internal/fingerprint -count=1 '-coverprofile=artifacts/rebuild-work-20261117/fingerprint/coverage.out'
python internal/fingerprint/testdata/regression_test.py
python scripts/fingerprint-golden.py --upstream-python
# 只在需要显式更新固定 JS 期望值时：
python scripts/fingerprint-golden.py --write
```

先写失败测试、确认 RED，再实现：初始缺失 API 的 RED 日志，挑战/parser/features/rank/bank 的 GREEN 日志，以及标点包围文字分类的真实 RED→GREEN 和回滚副本，均放在 `artifacts/rebuild-work-20261117/fingerprint`。`VERIFICATION.txt` 记录逐条命令、输入、字面 stdout/stderr、exit status、hash、独立审查及最终验证结果。

Go 测试同时覆盖库维度/model_order/scale/环境 centroid 检查、返回值独立副本、稳定 tie 排序、缺失/零权重分支和并发评分。最新 `go test -coverprofile` 的字面输出为 `98.4% of statements`；同一 profile 的 `go tool cover -func` 汇总为 `98.7%`。两个实际输出分别保存，不用其中一个覆盖另一个。

P2 完成时的普通 `go test ./... -count=1` 和 `go vet ./...` 都以 exit 0 完成，历史输出保留；独立 Python 驱动测试及包含原 NumPy 实现的数值回归也通过。独立静态审查发现的旧 observation 复用缺陷已按 RED→GREEN 修复；真实负向运行 `GOFLAGS=-skip=TestGoldenParity` 以 exit 1 报告 `Go golden command produced no fresh observation report`，且没有 PASS 报告。恢复正常环境后回归重新通过。额外的 isolated alpha `.5→.6` 变异和 parser 条件反转均被测试捕获，恢复副本后通过。

**P2/P4 最终 race 已通过。** 固定 official w64devkit GCC 16.2.0，在最终独立审查修复后的源码上执行：

```powershell
$env:CGO_ENABLED='1'
$env:CC='C:\Code\GithubProject\magpie-model-check\artifacts\rebuild-work-20261117\compiler\w64devkit\bin\gcc.exe'
$env:CGO_LDFLAGS=''
$env:PATH=(Split-Path -Parent $env:CC)+';'+$env:PATH
go test -race ./internal/fingerprint ./internal/attribution -count=1
```

字面输出为 `ok .../internal/fingerprint 12.919s`、`ok .../internal/attribution 35.697s`，exit 0。编译器 hash、版本、环境和完整输出保留在 `go-race-fingerprint-attribution-gcc-final.txt`。此前 portable Zig 的 TSan 启动错误 code 87、exit 1 保留在 `go-race-all.txt`，属于历史失败，不再表示当前 P2/P4 race 待验；该历史失败也不被覆盖为成功。

最终 `go test ./internal/fingerprint ./internal/attribution -count=1` 输出 2.324s / 3.088s、exit 0；`go vet ./internal/fingerprint ./internal/attribution` exit 0。P4 的最终 coverage 为 91.5%。同一时间的仓库级 `go test ./... -count=1` 和 `go vet ./...` 均 exit 1：其他审查的 `artifacts/**` 临时 Go 文件存在外部依赖、混合包名和缺失 embed 资源；正式 `cmd/internal` 范围还出现主服务新审核端点三项测试的 404 和数据库清理占用。`go vet ./cmd/... ./internal/...` exit 0。这些失败完整保留在 P4 的 `go-all-final.txt`、`go-vet-all-final.txt`、`go-source-final.txt`，不改集成目录、不降低测试范围来宣称全仓库通过。

## 7. 参考库与诊断含义

旧 bank 含 18 个闭集候选，但缺少本项目要求的逐条原始样本来源证明、独立 train/calibration/test/unknown/channel/time 划分及开放集验证。bank 的 beta/cv_accuracy 等原字段只作为原始资源随导出保留，不被 Go `Rank` 用来生成概率或身份结论。

本轮只证明数学移植、数据完整性和解析行为。合成 golden 不是实际模型的身份实验数据；SHA-256 证明资源字节一致，不证明 bank 标签、渠道或底层模型身份。排序第一也不代表 MATCHED/MISMATCHED，短序列能计算更不代表样本充足。正式参考库元数据应继续标记来源未验证，身份概率保持空值，判断门槛等待独立实验校准和验证。

## 8. P4 独立归因包与收尾

归因实验机制位于独立 `internal/attribution`，不改 P2 legacy scorer。完整类型/函数/JSON envelope 合约见该包 `README.md`：原始 bank/corpus 字节绑定、来源审核回调、协议 hash、bundle 导入导出与凭据扫描均有独立测试。

方法另行版本化为 `hellinger-reference-centroid-v1`。只用 reference 完整组构建单位中心、calibration 完整组形成经验包络；test/unknown/channel/time holdout 只评估，不回写阈值。Wilson CI 按独立完整组计数，独立性仍依赖真实采集和来源审核；group/batch/session/date 校验不是物理独立性的证明。显式实验 Policy 没有默认误报目标或固定充分组数。

112 个完整合成组、224 条回答只证明阈值和决策机制。公开合成决策为 INCONCLUSIVE，`identity_validated=false`、`calibrated_probability=null`。实际模型参考样本为 0；真实来源、可分性、误报能力、相邻版本和新渠道/日期适用性仍需独立采集实验。来源审核通过也不单独授予身份验证。

P2/P4 的四件隔离事务证据分别在 `artifacts/rebuild-work-20261117/fingerprint` 及其 `p4` 子目录：`MODIFIED_FILE`、`DIFF_FILE`、`VERIFICATION.txt`、可执行 `ROLLBACK.sh`。生产修复保留，回滚只操作隔离副本；P4 身份验证条件反转变异被测试捕获，恢复副本后通过。全证据路径索引及最终源码 SHA manifest 放在同一证据根目录。包源码已完成，可冻结供主任务进行 module 迁移和集成；没有 commit/push 或集成目录修改。
