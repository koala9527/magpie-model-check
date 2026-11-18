# modelstore 契约

独立的 SQLite 数据包：只依赖标准库及仓库已有的 `modernc.org/sqlite`，不 import 检测服务、旧 `check` 包或 Magpie。类型和 JSON 字段定义以同目录 `types.go` 为准。

## API

```go
Open(dir string, legacyDB string) (*Store, error)
DefaultSettings() Settings

(*Store).Close() error
(*Store).PutTarget(Target) (Target, error)
(*Store).Targets() ([]Target, error)
(*Store).Target(id string) (Target, error)
(*Store).DeleteTarget(id string) error
(*Store).Settings() (Settings, error)
(*Store).PutSettings(Settings) error
(*Store).Usage(day string) (Usage, error)
(*Store).Reserve(day string, requests, tokens int) error
(*Store).ChargeExtra(day string, tokens int) error
(*Store).PutBatch(Batch) error
(*Store).Batches(targetID string, limit int) ([]Batch, error)
(*Store).Batch(id string) (Batch, error)
(*Store).PutSample(Sample) error
(*Store).Samples(batchID string) ([]Sample, error)
(*Store).PutResult(Result) error
(*Store).Result(batchID string) (Result, error)
(*Store).PutTemplate(Template) error
(*Store).Templates() ([]Template, error)
(*Store).PutBank(Bank) error
(*Store).Banks() ([]Bank, error)
(*Store).Bank(id string) (Bank, error)
(*Store).DeleteBank(id string) error
(*Store).Legacy() ([]LegacyRecord, error)
(*Store).Export() ([]byte, error)
(*Store).Restore([]byte) error
(*Store).Prune(days int) error
```

- 类型：`Target`、`Settings`、`Usage`、`Sample`、`Batch`、`Result`、`Template`、`Bank`、`LegacyRecord`。`Sample.ResponseSHA256`、`SystemPromptHash`、`RequestID` 是可选 JSON 字段；响应 SHA 在缺省时由存储层补全。
- `ErrNotFound`、`ErrBudgetExceeded`、`ErrInvalidData` 支持 `errors.Is`。
- `PutTarget` 为新目标生成 ID 和创建/更新时间，默认身份状态为 `PENDING`，默认自动检测关闭。更新保留原 `CreatedAt`。其它 `Put*` 由调用方提供 ID；`Batch.TargetID` 创建后不可改指向其它目标。
- 删除目标仅解除历史的目标关联，不删除历史或预算。`Batches("", 0)` 返回全部历史；`limit > 0` 限制数量，按真实时间倒序。每个批次仅有一条归因结果。
- 时间字符串须为 RFC3339；预算日期为 `YYYY-MM-DD`，由调用方固定日期口径并在请求跨日时继续使用原预留日。JSON 数字保留大整数精度。

## 存储和预算

新库文件为 `dir/model-check.db`；`user_version=1`。六个业务表为 `model_targets`、`challenge_templates`、`fingerprint_samples`、`fingerprint_batches`、`attribution_results`、`reference_banks`；另有独立 settings、每日账本、旧质量历史和迁移标记表。

写操作使用 `BEGIN IMMEDIATE`、外键和 WAL/FULL，跨多个 Store/进程原子读取当前配置并写账本。默认预算/并发/超时/保留天数为 `60 / 300000 / 2 / 30 / 30`。设置限制：

| 字段 | 范围 |
| --- | --- |
| daily_requests | 0–1000000 |
| daily_tokens | 0–1000000000 |
| concurrency | 1–8 |
| timeout_seconds | 1–120 |
| retention_days | 1–3650 |

每日预算 0 用于暂停。`Reserve` 的预留永久记账；失败、取消、删除历史、重启、Prune、恢复均不退款。实际 Tokens 大于预留时，调用 `ChargeExtra` 计入差额；允许超额度形成债务，后续 `Reserve` 拒绝。实际用量较少不退差额。

`Prune` 只清理过期且非活动的新检测历史；永远保留 `legacy_history`、每日账本和恢复快照，也不删除目标、挑战或参考库。活动批次 `PENDING/RUNNING/CHECKING` 保留。

## 迁移、导出和恢复

旧库的 `state` JSON 通过只读连接迁移。先用 `VACUUM INTO` 保存含已提交 WAL 页的独立 SQLite 快照，再从该快照读取 state；不会改写或删除旧库。快照位于 `dir/backups/legacy-<state SHA>-<snapshot SHA>.sqlite`，配有 `.sha256` 和 `.json` 清单（原库主文件 SHA、快照 SHA、state SHA、数量）。原库 SHA 是主文件字节 SHA；带 WAL 时，以快照/state SHA 描述实际迁移内容。

目标与全部旧历史、历史预算和迁移标记在同一事务中写入。旧质量 `passed` 只作为旧历史保留，不转为 `MATCHED`；迁移目标为 `PENDING` 且自动检测关闭。重复启动或事务中断后重试不重复导入；导出/恢复同时保留迁移完成标记。旧 `config` 和 `alias` 不进入新表、页面或导出；原始快照私有保留，含旧配置，不作为页面内容或导出包。

旧 `cleared_usage` 仍计入请求账本；已删旧记录缺少 Token 总量时，保守计入至少当日配置的 Token 额度，不按 0 处理。

导出是确定性的 JSON：`format / schema / tables / hash`。`format=magpie-model-check.modelstore`；hash 是去掉 hash 字段后全对象按键排序的规范 JSON SHA256。Bank/Legacy 的 `data` 在备份中用 **JSON 字符串**保存原始 JSON 字节，避免 RawMessage 被压缩而改变原始 SHA。日常 API 中仍为 `json.RawMessage`。

恢复严格验证大小、格式、schema、完整表/字段、重复键/ID、外键、日期、资源上限、非空类型、有限数值和内容 SHA；拒绝认证头、API Key、Cookie 等凭据字段。全部验证通过后，事务内先生成并同步 `dir/backups/restore-*.json` 及 `.sha256`，再单事务替换业务记录。每日账本不删除，逐日 Requests/Tokens **分别取 max**，备份未包含的日期也保留。调用方必须在恢复前拒绝活动 job。

## 测试

```powershell
go test -mod=readonly -count=1 -v ./internal/modelstore
go vet -mod=readonly ./internal/modelstore
```

测试库全部使用 `t.TempDir()`，包括真实 WAL、触发器故障、子进程退出、迁移和恢复副本；不会将测试 SQLite 或私有快照放进源码目录。测试参考库均显式标为不可信的固定单元测试 fixture，不作为真实身份参考。
