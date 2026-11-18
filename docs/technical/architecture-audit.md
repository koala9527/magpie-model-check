# P0：当前架构与调用链审计

> 技术存档：保留 2026-11-17 文档重写前的详细内容。本页的“本轮/当前”指此前检查记录；现状请看[当前进度](../p0-report.md)，总体目标请看[新版方案](../../rebuild.md)。


核验日期：2026-11-17（Asia/Shanghai）。规格：[rebuild.md](rebuild-v1.md)。
审计基线：`1ac059f`，分支 `main`，产品版本 `0.3.0`。本轮复核时 `docs/` 和 `rebuild.md` 已存在且未跟踪；继续补齐其引用的实验与报告。原始 hash 清单中的 80 个文件逐一复核未变。

## 结论

当前项目是质量检测/人工复核工具，尚不是规格定义的开放集身份检测插件。
它把 Bun 中的 Provider 插件、独立 Go HTTP 服务和会动态改写的 Gateway middleware 组合起来；ModelTrace 是一个浏览器侧的手动功能。
最重要的迁移点是**移除业务门禁、取消模型名探针别名、建立真实路由证据和独立校准流程**，而不是只换标题。

P0 不更改现有运行代码、二进制、数据库或用户配置。独立能力实验放在被 `.gitignore` 排除的 `artifacts/rebuild-20261117/` 中。
P1 的开始条件是人工检查本审计及 [实施计划](implementation-plan.md)。

## 目录、语言和入口

| 部分 | 实际位置及符号 | 现状 |
| --- | --- | --- |
| npm 元数据 | [`package.json`](../../package.json) | `magpie-intelligence-check@0.3.0`，ES module；`main=./plugin/index.mjs`；没有 description、license、repository 和依赖声明 |
| Provider 插件 | [`plugin/index.mjs`](../../plugin/index.mjs)，default `{id, server}` | id/auth.provider 都是 `quality-check`；`config` 注册无推理模型的控制 Provider |
| Go 命令 | [`cmd/quality-check/main.go`](../../cmd/quality-check/main.go)，`main/run` | Go module 仍是 `github.com/mu-zi-lee/magpie-intelligence-check`；`go.mod` 要求 Go 1.26.0 |
| 服务/状态机 | [`internal/check/service.go`](../../internal/check/service.go)，`Service/New/Trigger/run` | 检測状态、请求预算、单模型锁、持久化和门禁发布绑在同一个 Service |
| 管理 HTTP | [`internal/check/http.go`](../../internal/check/http.go)，`ManagedHandler` | 本机独立管理 API；不是 Magpie plugin page bridge |
| 前端 | [`internal/check/web/index.html`](../../internal/check/web/index.html)、`web/assets/app.js` | Go embed 的原生 HTML/CSS/JS；独立管理页面通过 fetch 调用 Go API |
| 指纹 | [`internal/check/modeltrace.go`](../../internal/check/modeltrace.go)、`web/modeltrace/fingerprint-core.js` | Go 生成三条挑战；浏览器评分；没有后端归因结果表 |
| 发布 | [`scripts/build-release.mjs`](../../scripts/build-release.mjs) | 五个平台的 Go 二进制＋插件 tgz＋SHA-256 manifest；会写回 `bin/` |

没有锁定社区插件的 Magpie API/最低版本。不得把官方 **内置 Go 插件**的 `pluginrt.API="1.0"` 当成这个 npm 包的 API 版本。

## 现有调用链

```text
Magpie 共享 Bun 插件宿主
  -> plugin/index.mjs:server -> config / auth.loader / authorize
  -> start(): spawn quality-check-<os>-<arch> -data ... -gate ... -owned
  -> Go run(): 数据目录锁 + loopback HTTP + control token + stdin 存活关系
  -> check.New(): 读取 check.db -> saveLocked()
  -> saveLocked(): 保存 state JSON -> 原子覆盖 plugin/gate.middleware.js

业务请求 -> middleware.onRequest
  -> 未启用监测：透传
  -> 私有 quality-probe/<alias>/<model>：删掉模型名前缀后透传
  -> 已通过/限时放行：透传
  -> 其他已监测模型：console.log MAGPIE_QUALITY_TRIGGER + ctx.reject(403,...)
  -> WatchLog/LogLine -> Trigger(model,"milk",false)
  -> Go call(): Magpie /v1/chat/completions -> Grade -> 状态/门禁更新

手动 ModelTrace -> POST /api/action {action:"modeltrace"}
  -> TriggerModelTrace -> 三条 quality-probe/... 请求 -> Results
  -> panel.js -> analyzeGlobalOutputs -> 18 类闭集候选/softmax
```

`saveLocked` 的门禁分支在 `service.go:215–242`；探针发送在 `service.go:419` 和 `modeltrace.go:90`。
当前没有 `onProvider`、`onResponse` 或 `onEvent` 的实际路由记录。别名被剥离、`/v1/models` 可访问和响应 `model` 字段都不是命中目标 Provider 的证据。

## 配置、持久化和生命周期

- 插件通过第二参数 `options` 读取 `binaryPath/dataDir/gatewayURL/gatewayKey/logPath/cloudflaredPath`；默认数据目录是 `~/.config/magpie-quality-check`。
- `gateway-discovery.mjs:discoverGateway` 读取 Magpie settings 的端口和 `MAGPIE_ADDR`，以 `/v1/models` 做可连接性测试；默认网关 key 是 `magpie`。这不是 Provider 凭据读取，也不验证路由。
- `OpenStore` 只建一张 `state(id CHECK(id=1),body BLOB)` 表；`check.db` 的 body 是完整 `State` JSON。不存在 schema version/migration 框架，也没有规格建议的六张身份检测表。
- 该 JSON 包括 Gateway key 和探针别名；`Snapshot` 对 HTTP 输出抹掉二者，文件使用 `0600`。Windows 的 `os.Chmod` 不等于已验证的 ACL/加密密钥保护。
- Go `run` 用 `flock` 排他数据目录，并用 `-owned` 的 stdin EOF 或信号触发 shutdown；`Service.Close` 取消 context、等待 goroutine、关 SQLite。
- Bun `start` 缓存 `ready/child`，监听 exit/error；宿主进程 exit 时杀子进程。没有已声明的、逐插件 disable/dispose 钩子。必须在真实 Magpie 关闭和重载实验中检查子进程、监听器和门禁快照残留。
- 远程管理入口通过 `cloudflared-runtime.mjs` 下载/验证 cloudflared，再把管理 HTTP 暴露为临时远程控制台。这是 V1 明确不做的功能，须先列迁移依赖再退役。
- 历史删除会保留每日已用请求计数，但没有原始样本保留期、全局并发/每日 token 上限和在线数据库备份/恢复接口。

## ModelTrace 与基准质量

上游：`https://github.com/xqy2006/ModelTrace`，固定 commit `6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2`，MIT。
直接读取了固定版本的 `fingerprint.py`、JS scorer 和 bank，而不只依据 README。

- 值域 `1..355`；Hellinger 特征 355 维，平滑 `ALPHA=0.5`；有序分块 4×16 加个位分布 10，合计 74 维；`ORDERED_BLOCK_WEIGHT=0.25`。
- 主评分是标准化/去 nuisance basis 的特征与 centroid 的点积，加有序块评分；`profile_similarity` 另用 Jensen–Shannon 分布相似度。没有已实现的类条件开放集距离门槛。
- 解析用 `/\d+/g` 寻找最长数字段，滤掉越界值；负号、浮点和多余文字没有严格拒绝。有效量门槛来自原算法（80 或 expected_count 的 55%），不能解释成经本项目验证的身份准确率。
- bank 有 18 个候选、`built_at=2026-10-09T06:24:27.469312+00:00`、`recommended_queries=3`、`robust` 和闭集 beta/cv_accuracy。
- 当前数据没有每条原始参考样本的来源证明、独立 train/calibration/test/unknown/channel/time 划分；不得称为官方可信身份库或把 softmax 百分比解释为身份概率。
- 固定上游 README 明确写明 GPT 样本来自 Codex、Claude 样本来自 OAIPro，并提示系统提示词可明显影响结果。这些采集环境/渠道是实验标签，不能合并解释为同一“官方 API 基准”。
- 本机文件是 CRLF；**统一为 LF 后** scorer 和 bank 与固定上游逐字节一致。原始文件 hash 不同不能直接认定供应链内容被改写。

## 测试、构建和第三方声明

- 有 33 个 Go `Test...` 函数、9 个 Node 单元测试，以及脚本式浏览器/包/安装/客户端/真实 Magpie 集成测试。
- `scripts/magpie-integration.mjs` 的成功条件是旧版“403 门禁→别名探针→通过后放行”，不是新 V1 验收；`MAGPIE_BINARY` 必須显式配置。
- `.github/` 仅有 question-bank issue template，没有已跟踪的 CI workflow；不能把本机通过等同于 CI/跨平台发行通过。
- 已保留的声明：本项目 MIT、ModelTrace MIT、cloudflared Apache-2.0、Lucide ISC、Space Grotesk/Space Mono OFL-1.1。Go 依赖及实际发布二进制的 notices 仍需 P6 完整核查。
- 本轮命令、逐项结果、失败原因、源文件 hash 和 PoC 状态见 [P0 阶段报告](p0-report.md) 与 `artifacts/rebuild-20261117/VERIFICATION.txt`。

## 本轮运行证据

- Windows/amd64，Node 26.4.0、Go 1.27.1；实际运行的 Magpie 为 `0.1.3960`，编译 revision `61be687ffae858e029f027ba076c8d45724b81b7`。
- 独立便携副本使用自身 data、Bun 副本、随机端口和合成上游；没有调用真实用户渠道。已通过普通业务透传、非流式/SSE 路由关联、MCP 主动探针、伪造证据负例、off/on 工具移除/恢复。
- off 时已经执行的请求继续到自身 10 秒超时，实测约 10002ms；不能把工具移除写成任务立即取消。宿主 `Restart/retire` 的源码也允许旧调用完成，`retireWait` 为 10 分钟。
- 现有 Node 9 项与新 PoC 9 项通过。`go test ./...` 的原有 Windows 日志 rename 失败已复现；`go test -race ./...` 因 CGO 未启用退出 2。两项均没有通过，未删除或跳过相关测试。
- 页面只声明了 manifest 和 bridge 示例，未完成真实页面加载验证；任意样本持久化、私有 IPC 和生产级取消仍待验证。最小网络路径通过不等于 P0 所有能力或 V1 产品已验收。

## 审计验收边界

“P0 审计完成”“固定官方源码支持”“隔离 fixture 验证通过”“用户真实渠道验收通过”是四种不同状态。
PoC 只验证插件能力和请求关联，不采集付费模型的身份基准、不证明用户渠道模型身份、不修改现有业务配置。
