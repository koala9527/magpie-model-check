# P0 阶段报告：审计与最小路由能力实验

> 技术存档：保留 2026-11-17 文档重写前的详细内容。本页的“本轮/当前”指此前检查记录；现状请看[当前进度](../p0-report.md)，总体目标请看[新版方案](../../rebuild.md)。


日期：2026-11-17。规格：[rebuild.md](rebuild-v1.md)。分支 `main`，基线 `1ac059f`。

## 本轮结果

已完成仓库/官方源码审计、最小 middleware＋MCP 隔离实验、上游伪造路由证据负例及副本回滚。
这不是 V1 改造完成报告：P1–P6 尚未开始，P0 中的页面 bridge、样本持久化和完整资源取消能力也没有全部通过。

80 个原始文件逐一 SHA-256 复核未变；业务入口、Go 服务、既有 middleware、二进制和用户配置保持原状态。
新的实验只在 `artifacts/rebuild-20261117/` 的本机便携副本和合成 upstream 中运行，没有访问真实用户模型渠道或建立身份参考库。

## 可审查交付

| 文件 | 内容 |
| --- | --- |
| [architecture-audit.md](architecture-audit.md) | 旧质量门禁、Go 后台、浏览器指纹调用链及测试现状 |
| [magpie-api-audit.md](magpie-api-audit.md) | 官方接口、不同运行环境、版本分离及 PoC 逐项状态 |
| [reuse-and-remove.md](reuse-and-remove.md) | 旧门禁/别名/隧道退役依赖、历史迁移、保留许可证 |
| [risk-register.md](risk-register.md) | 路由证据、校准、生命周期和发行风险及验证门槛 |
| [implementation-plan.md](implementation-plan.md) | P0–P6 切片、依赖、验收及人工检查点 |
| [MODIFIED_FILE.mjs](../../artifacts/rebuild-20261117/poc/MODIFIED_FILE.mjs) | `onRequest` 只处理合法探针 marker；`onProvider` 记录白名单路由；响应/事件关联同次请求 |
| [DIFF_FILE.patch](../../artifacts/rebuild-20261117/DIFF_FILE.patch) | 相对于原始透传 middleware 副本的差异 |
| [VERIFICATION.txt](../../artifacts/rebuild-20261117/VERIFICATION.txt) | 四个绝对路径、hash、原始命令/输入/输出/退出码及真实集成结果 |
| [ROLLBACK.sh](../../artifacts/rebuild-20261117/ROLLBACK.sh) | 仅接受专用隔离副本路径，已实际执行并比对原始 hash |

`artifacts/` 遵循仓库现有 ignore 规则，不会随 npm 发行包或普通 Git 提交自动带走。它是本地可重新打开的证据目录；正式实现前应将最终采用的测试/模块迁入相应受版本管理目录。

## 实际版本和来源

| 对象 | 版本/结果 |
| --- | --- |
| 执行环境 | Windows amd64；Node 26.4.0；Go 1.27.1 |
| 本机实际 Magpie | 0.1.3960；`go version -m` 显示 revision `61be687ffae858e029f027ba076c8d45724b81b7` |
| 官方审计快照 | `255ccdd7caefca7ce305c9d056fb1449e501d454`；16 个独立源码文件对 tree 的 Git blob SHA-1 全匹配，并保存 SHA-256 |
| 本轮 main 查询 | `873b1f7d5e583fb59ff936929947a4316add34fd`；与本机版本分开记录 |
| 本轮 main 文件复核 | run.go、page.go、tools.go、host.js、host.go、gateway.go、pagebridge.js 与既有快照相同；另外两项源码请求失败，当前指南重抓 HTTP 错误，保持时效缺口 |
| ModelTrace | 固定 `6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2`，MIT；scorer/bank 两文件统一 LF 后与固定上游相同；没有声称 Python→JS 数值回归完成 |
| 源码压缩包 | 现有 `magpie-source.zip` 缺 ZIP 中央目录，不作为完整源码归档证据；可引用的是逐文件 blob 校验结果 |

原始来源记录：`official/refresh-results.json`、`tests/source-integrity.json`。ModelTrace 上游注明 GPT/Codex 与 Claude/OAIPro 的采集渠道和系统提示词偏差；来源可信性、可分性和独立校准仍需实验。

## 验证结果

下列相对路径均以仓库根目录为工作目录。

| 检查 | 实际命令 | 结果/退出码 |
| --- | --- | --- |
| 现有 Node 单测 | `node --test --test-isolation=none plugin/gateway-discovery.test.mjs plugin/console-readiness.test.mjs scripts/answer-analysis.test.mjs` | 9 pass / 0 fail；0 |
| 旧 middleware 对新要求 | 设置 `POC_MIDDLEWARE` 为 `poc/BASELINE_FILE.mjs`，再 `node --test artifacts/rebuild-20261117/poc/middleware.test.mjs` | 首轮 7 项中 2 pass / 5 fail；1，证明确实缺 marker/Provider 关联 |
| 新 PoC 回归 | `node --test --test-isolation=none artifacts/rebuild-20261117/poc/middleware.test.mjs artifacts/rebuild-20261117/poc/tool.test.mjs` | 9 pass / 0 fail；0 |
| 路由来源负例红测试 | 同上，新增 proof 测试后、修复前 | 7 pass / 2 fail；1；原公开字段校验确实能被伪造 |
| 真实 Gateway＋MCP | `node artifacts/rebuild-20261117/poc/integration.mjs` | 通过下面列出的本机 fixture 断言；0 |
| 现有 Go 全量 | `go test ./...` | 1；`TestLogDemandAndRotation` 在 Windows `os.Rename` 处失败，日志读取句柄未允许文件重命名；原有失败已复现 |
| 项目默认 race 检查 | `go test -race ./...` | 2；`go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`；没有运行成功 |
| 来源校验 | `python artifacts/rebuild-20261117/audit-sources.py` | `PINNED_SOURCE_BLOBS=16 MATCH; MODELTRACE_CANONICAL_FILES=2 MATCH; ARCHIVE=INVALID_ZIP_CENTRAL_DIRECTORY`；0 |
| 回滚与文件校验 | `node artifacts/rebuild-20261117/package-evidence.mjs` | 基线、修改、恢复行为均按预期；80 个原始文件未变；0 |

Node 子进程在受限环境中曾报 `spawn EPERM`；在已批准的隔离进程执行环境运行真实 Magpie/Bash 验证。Node 26 的 `--test-isolation=none` 允许单元测试在当前进程执行，未跳过测试。
首次未提供 Bun 的隔离 MCP 列表调用超时；使用已有 Bun 1.4.4 的项目内副本并显式设置 `MAGPIE_BUN` 后成功。只将这个方法记为已验证解决办法，不把下载问题猜成已定位根因。

真实集成已经验证：

1. 普通合成业务请求透传，原来的上游响应保持原样；中间件没有 403 门禁。
2. 探针请求使用原始目标 `fixture/solo`；正常上游模型改写为 `solo`；marker 在到达上游前移除。
3. `onProvider` 的 Provider/model 与 `onResponse`、SSE `onEvent` 关联到同一随机 probe ID。
4. 同一插件包通过 `/mcp/magpie/tools` 列出工具、执行一次主动挑战，一次 tool 对应一次合成 upstream 请求。
5. middleware 缺席时，合成上游可见 probe ID，并伪造全部公开路由字段；缺少私有本地 proof 后工具输出 `ROUTE_UNVERIFIED`。
6. 有可信本地关联时，仅返回 `route_binding_status=OBSERVED`、`identity_status=INCONCLUSIVE`，`calibrated_probability=null`。
7. `plugin off` 后工具列表移除，`plugin on` 后再次调用成功。
8. 请求执行中 off 后并未立即取消，约 10002ms 后依靠工具自身 10 秒超时返回 `UNAVAILABLE`。该断言通过只是确认现状，**不表示规格的立即停止要求已满足**。
9. 测试进程退出后 fixture Gateway 端口关闭；最终 7 次上游请求全部为本机合成输入。

真实 Gateway 的 Provider 重试/组路由、真实页面 bridge、任意样本数据库/IPC、完整进程树无残留、真实用户渠道和独立身份盲测仍待验证。Provider 重试只有独立 middleware 单测，不能写成真实 Gateway 重试验收。

## 基线、修改、回滚的逐字结果

共同测试输入：`model=fixture/solo`；`marker={version:1,id:11111111111111111111111111111111}`；`ctx.provider={id:fixture,model:solo}`；另有 ordinary business request。

```text
node artifacts/rebuild-20261117/poc/check.mjs BASELINE
BASELINE route=ROUTE_UNVERIFIED model=fixture/solo business=UNCHANGED marker=FORWARDED
exit=0

node artifacts/rebuild-20261117/poc/check.mjs MODIFIED
MODIFIED route=OBSERVED model=fixture/solo business=UNCHANGED marker=REMOVED
exit=0

bash artifacts/rebuild-20261117/ROLLBACK.sh artifacts/rebuild-20261117/rollback-check/MODIFIED_FILE.mjs
ROLLBACK restored=BASELINE_FILE.mjs hash=MATCH behavior=PASS_THROUGH
exit=0

node artifacts/rebuild-20261117/poc/check.mjs ROLLBACK
ROLLBACK route=ROUTE_UNVERIFIED model=fixture/solo business=UNCHANGED marker=FORWARDED
exit=0
```

实际调用的 node/Bash 绝对可执行路径及 stderr 见 VERIFICATION.txt。上述 check.mjs 为直接钩子实验，只证明有无本地观察，不验证 tool 的 proof；tool proof 的通过依据为单测及真实 MCP 集成。

原始 middleware SHA-256：`4140462e2a9850efac1958999a052dc7ce9d23a5758cbdd9dc1bef09bf3cde57`。
修改副本 SHA-256：`fa5c887d1abecebd764e2bf2b9b9f0db1375429843f79ab478941ad725d755fb`。
回滚只操作 `rollback-check` 副本，恢复后的 hash 与原始文件相同；`poc/MODIFIED_FILE.mjs` 仍保留改动。

## 人工检查点与后续

[原规格第 2.3 节](rebuild-v1.md#23-必须输出的考古文件) 明确要求：“完成考古并通过人工检查后，再进入重构。”
因此本轮交付可检查的审计和最小实验，没有跨入 P1 重命名/退役运行功能，也没有把不完整的 P0 能力写成已验收。

建议人工检查确认迁移方向后，先补齐 page bridge、宿主内持久化及可取消任务的验证，再依照实施计划推进 P1 的命名/数据迁移与旧门禁退役。当前证据不足以决定正式保留 Go sidecar；不要仅为沿用旧实现作该决定。

后续可直接使用的执行提示词：

```text
先读 rebuild.md、docs/p0-report.md、docs/magpie-api-audit.md 和 docs/implementation-plan.md。
以用户对 P0 的人工检查结论为入口，先补齐页面 bridge、持久化、可取消任务的隔离验证。
沿用已验证的本地路由证据负例，不用响应 model、自报 source 或 nonce 单独证明路由。
采用已验证的架构逐片实现 P1；保留历史和许可证，建立失败测试后再改行为。
没有可信参考/独立校准保持 INCONCLUSIVE，路由未证实保持 ROUTE_UNVERIFIED，错误只记 UNAVAILABLE。
继续记录真实执行命令/结果，验证副本回滚，更新各阶段状态，不把 fixture 当真实渠道或身份验收。
```
