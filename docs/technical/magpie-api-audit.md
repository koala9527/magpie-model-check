# P0：官方 Magpie 插件接口与能力 PoC 核验

> 技术存档：保留 2026-11-17 文档重写前的详细内容。本页的“本轮/当前”指此前检查记录；现状请看[当前进度](../p0-report.md)，总体目标请看[新版方案](../../rebuild.md)。


核验日期：2026-11-17（Asia/Shanghai）。
官方仓库：<https://github.com/yetone/magpie>。
本轮固定版本：[`255ccdd7caefca7ce305c9d056fb1449e501d454`](https://github.com/yetone/magpie/tree/255ccdd7caefca7ce305c9d056fb1449e501d454)。
这个 SHA 是已有审计快照的 `commits/main` 结果，**不是本机用户安装的 Magpie 版本**；本轮重新查询 main 的结果见下表，不能混称为同一次查询。
官方指南：[插件开发](https://usemagpie.ai/docs/zh/plugins)。原始 HTML、固定源码及 hash 保存于 `artifacts/rebuild-20261117/official/`。

| 版本记录 | SHA/版本 | 用途 |
| --- | --- | --- |
| 既有官方源码快照 | `255ccdd7caefca7ce305c9d056fb1449e501d454` | 下方 S1–S11 的接口源码依据，逐文件对 tree blob hash |
| 本轮 main 查询 | `873b1f7d5e583fb59ff936929947a4316add34fd`，2026-11-17T05:31:08Z | `current-commit.json`；选定源码重新下载对照，成功/失败记录在 `refresh-results.json` |
| 本机执行二进制 | Magpie `0.1.3960`，revision `61be687ffae858e029f027ba076c8d45724b81b7` | 本轮隔离 Gateway/MCP 实验实际执行版本 |

本轮成功重抓的 run.go、page.go、tools.go、host.js、host.go、gateway.go、pagebridge.js 与既有快照逐字节相同。middleware.go、pluginpage.go 的当前版本请求未成功；当前指南重抓返回 HTTP 错误，仍以已有指南快照核对并保留这个时效缺口。源码证据不能代替本机实测，未实测能力不能从更新版本外推。

## 证据分级

- **已核对（官方文档＋源码）**：接口存在/具体限制有依据；不等于本项目运行验证。
- **已验证（隔离 fixture）**：本轮在测试输入/进程中实际运行；不代表用户真实渠道。
- **待验证**：未执行或证据尚不充分；不猜测实现。
- 内置 `internal/pluginrt` 的 `Manifest.API/^1` 是 Go 内置插件规范，不能直接套到 npm/OpenCode 社区插件。

## 核验表

| 核验项 | 当前真实接口与限制 | 依据/具体符号 | 状态 |
| --- | --- | --- | --- |
| 社区包格式/入口 | npm/git/本地目录；`main/exports`；async `(input, options)=>hooks`、具名插件函数或 default `{id,server}`；同包可声明其他能力 | 指南“插件函数/加载”；[S1] `host.js` 的插件加载、`toolsOf/argsSchema`；[S2] `Middleware`；[S3] `Window` | 已核对 |
| 注册 middleware | `magpie.middleware` 或 `*.middleware.js/.mjs`；不能因为在网关进程就假定 Node 能力 | 指南“网关中间件”；[S2] `Middleware`；[S4] `mw/current` | 已核对 |
| `onRequest(body,ctx)` | 路由前；能改模型/拒绝请求；返回/undefined 约定；V1 不使用 reject、模型名重写 | [S5] `Run.Request/reject`；指南钩子表 | 已核对 |
| `onProvider(body,ctx)` | 不是 `(provider,ctx)`；`ctx.provider={id,name,account?,model}`；每次 Provider 尝试重跑，不能在此拒绝/改路由 | [S5] `Run.Tried` 设置 `ctx.provider`，重试从原 onRequest 输出开始；指南钩子表 | 已核对 |
| `onResponse(body,ctx)` | 非流式完整 JSON；`ctx.status`；可改回复，不保证压缩/非 JSON 可观察 | [S5] `Run.Wrap`；指南钩子表 | 已核对 |
| `onEvent(event,ctx)` | 每个 SSE JSON；null 丢事件；Chat 无事件名，不设 events 白名单；DONE/注释直接透传 | [S5] 流式 writer/event 处理；指南 events 说明 | 已核对 |
| 请求关联 | 每请求独有 `ctx.state`；该请求各钩子共享；没有文档承诺任意 HTTP header/request ID 能被 middleware 读取 | [S5] `Run/acquire`；指南 ctx 列表 | 同请求 state 已核对；跨 tool 关联待 PoC |
| middleware 权限/时间 | moejs，同步轻量；无 fetch/定时器/Node API；只相对目录导入；request/provider/response 250ms、event 50ms、load 1s；异常/超时 fail-open | [S4] limits；指南“运行方式/钩子出错” | 已核对 |
| `onUsage(row,options)` | 异步尽力交付的用量统计，无请求体/prompt/完整响应；忙时可丢弃，不是指纹样本队列 | 指南“路由与用量记录”；官方 `internal/middleware/points.go` | 已核对文档；不用于样本采集 |
| Provider 插件 | 调用 config、auth methods/loader/usage/refresh/checkin、provider.models、chat.headers；Provider 自己的 fetch 在 Bun | 指南“PluginInput/钩子”；[S1] host | 已核对 |
| 插件选项 | `plugins.json` 的 options 为插件第二参数；middleware 从 `ctx.options` 读；官方 CLI `magpie plugin options`；默认示例不等于已生效配置 | 指南“选项”；[S2] `OptionsExample`；[S3] `SetPageOptions` | 已核对 |
| 配置变更/关闭 | 社区插件 on/off/options 触发宿主重载；旧宿主可完成已有调用，`retireWait=10min`；无已证实的逐插件任意 dispose API | [S6] `Restart/retire/host.stop/host.kill`；Unix session/Windows job 管理进程树 | 本机 off/on 工具移除/恢复已验证；在途请求不立即取消，见下方 |
| MCP 工具 | OpenCode `hooks.tool`；args 可 JSON Schema/zod；`execute(args,ctx)` 可 fetch、`ctx.abort`；通过 `/mcp/magpie/tools` 提供 | [S1] `toolsOf/argsSchema`；[S7] `Tools/RunTool/ToolTimeout=55s`；指南“给每个智能体的工具” | 已核对 |
| 发起内部探针 | MCP execute 可调用显式配置的本机 Gateway HTTP 地址；没有文档保证从现有 Provider 自动取得凭据或指定隐藏内部 route | [S7] 工具执行；指南示例只证明 execute 可以 fetch | 接口能力已核对；最终命中路由待 PoC/真实渠道 |
| 模型/Provider 列表 | file-page 的 `providers:list` 仅 id/name；`/v1/models` 不是实际路由证据；目标/声明身份须显式配置 | [S8] 读取授权/返回投影；指南 page 读取表 | 已核对；不会伪造 Provider/身份来源 |
| file-page UI | `magpie.page.file/title/icon/titles`；HTML 文件＋`_magpie/bridge.js`；iframe/CSP sandbox allow-scripts、connect-src none、opaque origin | [S3] `Window/FileIn`；[S8] `servePluginPage/pagePolicy/apiFramed`；指南“窗口里的页面” | 已核对 |
| page bridge | `magpie.ready/theme/onTheme/options.get/options.set/read`；读取须 manifest permissions；没有通用执行 MCP tool/fetch bridge | [S9] bridge api/ready；[S8] read/options；指南 page 读取表 | 已核对 |
| 通用持久化 | 官方保存插件 options/auth，不等于任意 samples/db 存储接口；middleware 无文件能力，file-page 无窗口存储 | 指南插件文件/page 限制；[S3] SetPageOptions | arbitrary storage/样本保留策略待验证 |
| 本地服务页面 | 当前源码额外支持 `magpie.page.service={port,option?}`，Bun host 服务在 loopback；web 下有代理与 `magpie.service`，不是 file-page 取消 CSP | [S3] `ServiceSpec/service/ServicePort`；[S8] `servePageService`；[S9] `api.service`；[S10] 官方 pageservice fixture | 已核对源码；本项目服务/IPC/鉴权/跨平台生命周期待验证 |
| Bun 网关请求钩子 | 当前指南已有 `magpie.hooks`/`*.hooks.js`，异步 onRequest/onResponse，可使用 ctx.signal 的 fetch；与 moejs middleware 不同，不能把请求前的上下文当实际 Provider | [S11] `Gateway/GatewayContext/GatewayBegin/GatewayRun`；指南“网关请求钩子” | 已核对；不是本轮默认集成路线 |

## 相对于 rebuild.md 的必要补充

1. 规格关于 **moejs middleware** 和 **file-page** 的限制仍正确，不能互相混用运行环境。
2. 当前官方新增/完善了 **Bun gateway hooks** 和 **page.service** 路径。应保留为候选路线并独立验证，不把它们归入 middleware 的 fetch 能力。
3. `page.service` 只给本地 HTTP 服务位置/代理约定；数据库接口、IPC 鉴权、任务取消和数据迁移仍由插件实现并测试。
4. 从 file-page 直接点按钮执行工具没有当前通用 bridge 依据。最小方案由用户/agent 调用 MCP；页面先展示目标 options 和 Provider 名称。完整交互若选服务页须另做 PoC。
5. 本轮不给现有 `plugin/index.mjs` 随意添加一个假设的生命周期钩子，也不假定自动读取 Magpie/Provider key。

## 能力 PoC 与判定规则

隔离包位于 `artifacts/rebuild-20261117/poc/`；它不是已启用到用户 Magpie 的正式插件。
仅向测试本机 Gateway/合成 upstream 发一次受控短挑战，不执行真实身份实验，不使用业务对话。

标记方案：专门探针请求在**请求体**携带版本化 opaque marker；middleware `onRequest` 识别后移除 marker，保留原 model；`ctx.state` 关联 `onProvider` 的真实观察；仅对该探针回复附加本地观测元数据。
不使用 `quality-probe/` 模型名前缀；不从上游 `model` 自报字段推断 Provider；缺 marker/缺 Provider/不匹配时结果为 ROUTE_UNVERIFIED。
即使观察到了 route，PoC 的 identity_status 也只为 INCONCLUSIVE，没有指纹库/校准就不输出 MATCHED/MISMATCHED。

仅 nonce、source 字符串和 Provider 字段匹配不足以证明本地观察来源：middleware 缺席/fail-open 时，nonce 可能到达上游。最终 PoC 在每个独立测试副本中生成本地随机 proof，写入该副本的 middleware 和 Bun 环境；不写入请求、page 目录、插件 options、tool 返回值。真实负例关闭 middleware，让合成上游伪造所有公开字段，tool 仍输出 ROUTE_UNVERIFIED。源码模板的 `localProof=null` 默认不具备路由证明能力。该实验只证明私有测试通信可行，正式存储/密钥分发和部署还需产品化验证。

| 必验项 | 验证输入/方法 | 本轮状态 |
| --- | --- | --- |
| 同包声明 middleware＋tool＋page | 同一个 fixture 包中 middleware 和 MCP 成功；page manifest 有声明 | middleware＋tool 已验证（本机 fixture）；页面 bridge 运行待验证 |
| `onProvider` 最终 Provider/model | 本机 Gateway 返回 fixture/solo 的真实观察；重试切换有独立单测 | 本机单 Provider 已验证；真实 Gateway 重试/组路由待验证 |
| `onResponse/onEvent` 同请求关联 | marker→ctx.state→非流式回复/SSE 事件；请求间隔离单测 | 已验证（本机 Gateway fixture＋单测） |
| MCP execute 到本机 Gateway | `/mcp/magpie/tools` 列表与调用，key 来自测试环境变量 | 已验证（Magpie 0.1.3960 fixture）；未调用真实用户渠道 |
| 用户配置 URL/key 的官方入口 | plugins.json options 配 URL 和环境变量名，MCP 实际读取；key 不在 page options | options→tool 路径已验证；GUI/options CLI 完整设置流程待验证 |
| 不改目标路由/不递归 | tool 请求 fixture/solo，upstream 收到正常改写后的 solo，无 marker；一次 tool 对应一次 upstream | 已验证（单 Provider fixture）；复杂路由与其他 middleware 组合待验证 |
| 官方持久化/sidecar 通信 | options/auth 已存在；任意样本存储、服务/IPC、SQLite lifecycle 另验 | 待验证 |
| 禁用/重载释放 | off 移除工具，on 恢复；在途请求 off 后约 10002ms 才由自身超时结束；退出后网关端口关闭 | 部分验证；立即取消未满足，不能标全通过 |
| 普通业务不受影响/隐私 | 普通请求透传；中间件不保存正文、provider key/account 被白名单排除 | 已验证（单测＋本机合成业务）；未运行真实业务/完整页面隐私验收 |

表内状态将依据 [P0 阶段报告](p0-report.md) 和原始命令输出更新。未运行项保持待验证，源码或 Node 单测不能冒充真实 Magpie 集成验收。

首次未指定 Bun 副本的 MCP 列表调用超时；显式 `MAGPIE_BUN` 指向已有 Bun 的隔离副本后成功。它是已验证的环境解决办法，原超时根因没有单独证明。既有 `magpie-source.zip` 缺中央目录，不能作为完整归档证据；使用已独立下载、逐 blob 校验的源码文件。

## 固定官方源码索引

- [S1] [`internal/plugin/host.js`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/internal/plugin/host.js)：插件加载、`toolsOf`（L1802）、`argsSchema`（L1873）。
- [S2] [`internal/plugin/middleware.go`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/internal/plugin/middleware.go)：`Middleware`、`OptionsExample`。
- [S3] [`internal/plugin/page.go`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/internal/plugin/page.go)：`Window`（L145）、`ServiceSpec`、`PageReads`、`SetPageOptions`。
- [S4] [`internal/middleware/middleware.go`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/internal/middleware/middleware.go)：`requestLimit/eventLimit/loadLimit`、middleware 加载。
- [S5] [`internal/middleware/run.go`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/internal/middleware/run.go)：`Run.Request`（L143）、`Run.Tried`（L173）、`Run.Wrap`（L253）、state/事件关联。
- [S6] [`internal/plugin/host.go`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/internal/plugin/host.go)：`Restart`（L845）、`host.stop`（L943）、`host.kill`（L970）。
- [S7] [`internal/plugin/tools.go`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/internal/plugin/tools.go)：`Tools`、`RunTool`、`ToolTimeout`。
- [S8] [`internal/gui/pluginpage.go`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/internal/gui/pluginpage.go)：page read/options、`pagePolicy`、`servePluginPage`、`servePageService`。
- [S9] [`internal/gui/pagebridge.js`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/internal/gui/pagebridge.js)：`options.get/set`、`read`、`ready`、`service`。
- [S10] [`internal/plugin/testdata/pageservice/index.js`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/internal/plugin/testdata/pageservice/index.js) 与 `package.json`：官方 loopback service fixture。
- [S11] [`internal/plugin/gateway.go`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/internal/plugin/gateway.go)：Bun 异步钩子、允许上下文、预算与取消。
- 设计说明：[`docs/subsystems/plugin-runtime.md`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/docs/subsystems/plugin-runtime.md)、[`docs/subsystems/gateway-middleware.md`](https://github.com/yetone/magpie/blob/255ccdd7caefca7ce305c9d056fb1449e501d454/docs/subsystems/gateway-middleware.md)。
