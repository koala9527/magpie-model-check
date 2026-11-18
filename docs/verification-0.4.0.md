# 0.4.0 最终验收：哪些已经跑通

更新：2026-11-18。本文记录软件验收，不把测试夹具的输出当成真实模型实验。

## 一句话结果

新入口、独立检测、中文页面、SQLite 历史、预算、参考实验审核和备份恢复已经实现。最终源码通过 Go/Node/vet、完整 Go race、六目标构建和 npm 包检查；Windows amd64 的真实 Magpie 安装、启停、重启及页面流程通过。**真实模型参考样本为 0，概率为 null，当前宿主不授予模型身份结论。**

## 最终命令与结果

命令从项目根目录执行。原始证据位于本地 `artifacts/rebuild-work-20261117/`；该目录不提交，也不进入 npm 包。

| 命令 | 实际结果 | 原始记录 |
| --- | --- | --- |
| `node scripts/test.mjs` | exit 0；保留与新 Go 包、go vet 通过；Node/前端 119 项、0 失败、0 跳过；实际 npm pack 22 文件、6 二进制 | `release-final-tests.txt` |
| `node scripts/test.mjs --race` | exit 0；完整 Go race 通过，modelcheck 78.703s；使用真实 GCC，不忽略编译失败 | `final-race.txt` |
| `node scripts/build-release.mjs --output-dir dist` | exit 0；Windows/macOS/Linux 各 amd64/arm64，6 个 CGO0 二进制及 SHA-256 通过 | `final-build.txt`、`dist/manifest.json`、`dist/checksums.txt` |
| `node scripts/package-check.mjs --release-dir dist --require-all-targets --output-dir dist/npm` | exit 0；实际打包、文件允许名单和六二进制 hash 通过；私有运行文件、数据库、密钥和旧辅助工具不入包 | `final-package.txt`、`dist/npm/package-check.json` |
| `node scripts/magpie-integration.mjs --tarball dist/npm/magpie-model-check-0.4.0.tgz --evidence-dir artifacts/rebuild-work-20261117/final-native` | exit 0；Windows amd64 的实际 npm 安装、MCP 注册/检测、历史/预算、关闭/启用、完整进程重启和恢复通过 | `final-native.txt`、`final-native/*/install-check.json` |
| `python scripts/fingerprint-golden.py --upstream-python` | exit 0；28 单例、4 批量输入、排序一致；Go↔JS distance 最大误差 1.1102230246251565e-16，容差 1e-12；上游 NumPy 实现实际执行 | `final-golden.txt`、`fingerprint/regression.json` |

本机测试工具为 Node.js 22.23.2、Go 1.27.1、Windows amd64。race 使用 `CGO_ENABLED=1` 和 w64devkit 2.11.0 中的 GCC 16.2；发行构建使用 CGO0。CI 按 `go.mod` 配置 Go，并强制运行 race。

原生安装使用固定 Magpie CLI v0.1.4361 和 Bun 1.4.4；宿主下载 hash 校验通过。HTTP 验收实际发送 OpenAI chat JSON/SSE、Anthropic messages、OpenAI Responses 请求，均得到 HTTP 200，普通回答保持不变。**这些是 HTTP 协议客户端，不是外部 CLI 应用已经实测的声明。**

## 实际页面与失败路径

- 在真正的 Magpie service-page iframe 中操作，sandbox 为 `allow-scripts`；表单按钮和 Enter 不依赖宿主提供 `allow-forms`。
- 打开报告与原始回答后刷新，展开状态保持；目标、历史和预算操作已实际执行。
- 导入 42 条生成的准入夹具，空 Policy 提交被阻止，填写明确的测试目标后本机审核保存成功。
- 独立完整组不足时，重算验证未通过，页面没有激活按钮；夹具不因此成为真实参考数据。
- 最终页面控制台错误/警告为 0。证据为 `ui-final-workflow.json`、`ui-final-report.jpg` 和 `ui-final-review.jpg`。
- 最终 smoke 记录 PASS、6 次合成请求；调度/失败验收记录 PASS、7 次合成请求，覆盖取消、超时、退避、预算和并发。
- 实际渠道组中首渠道失败，次渠道成功，记录 provider 与 `attempt_count=2`；错误请求为 `UNAVAILABLE`，不影响普通请求。
- 备份恢复保持已预留用量、不退款；导入的历史身份/协议声明降为待重验，来源审核和激活作废，定时检测关闭。

## 独立审查问题怎样关闭

| 发现 | 修复与对应回归 |
| --- | --- |
| finish_reason 等嵌套字段可能反射本机凭据 | 持久化前检查完整样本；先验证原始签章和解析，再脱敏；命中后去除当前采集权威性，保留原始状态；`privacy_review_test.go` |
| 失败发布回滚可能覆盖较晚的渠道密钥 | 跨进程独占锁覆盖旧值读取、发布、验证和回滚整个事务；竞争方明确失败；`runtime-hardening.test.mjs` 的 concurrent publishers 用例 |
| 一轮只检查首样本日期 | 每条样本校验有效时间、同一 UTC 日期和实际渠道/模型；跨日、非法时间拒绝整轮；`date_review_test.go` |
| GitHub 安装标识与本地配置识别不一致 | 按 package repository 识别精确 GitHub/Git/SSH 注册，拒绝同名仿冒仓库；repository registrations 用例 |
| Windows 打出的 npm 包在 POSIX 上缺少可执行位 | 启动前检查正规、当前用户拥有的二进制；单链接使用文件描述符设 0700，Bun 共享链接使用独立私有副本；native binaries unpacked / Bun hardlinked 用例 |
| 恢复文件中的声明可被重新当成当前身份 | 原始字节保留，但导入结果、route 与 protocol 均回到未验证；审核、激活及调度降权；`restore_authority_test.go` |

审查后全量测试和 race 均重跑通过。原始审查与 RED/GREEN 日志保留，不把发现记录改写成原本没有问题。

## 三种证据分别记账

1. **构建：** 六个目标均编译、hash 与打包通过。
2. **原生运行：** 本机实际观测 Windows amd64。其他五个目标由 [GitHub Actions](https://github.com/koala9527/magpie-model-check/actions) 的对应原生安装任务分别报告；编译成功不是原生运行成功。
3. **模型身份：** 真实参考样本仍为 0；legacy 只供描述、synthetic 只验证程序。route V3 绑定本地观察到的请求、渠道、完整 envelope/SSE 链，但不是最终上游请求证明。运行时固定 `protocol_guard=false`，概率 null，身份保持未验证或暂不判断。

真实实验的下一轮任务模板和准入流程见[执行计划](implementation-plan.md)及[参考库指南](reference-library.md)。原始技术规格 `docs/technical/rebuild-v1.md` 未改动，SHA-256 为 `2b92cae092af5cca3052e1b923b8957b502a97098588d101d27554c2e81e3516`。

## 发布后发现的问题与复测

- macOS 的系统临时目录可能通过 `/var` 别名访问。测试先解析真实目录；生产仍拒绝符号链接与重解析路径，没有放宽路径检查。
- Bun 的 GitHub 安装会把缓存和安装目录中的二进制做成硬链接。POSIX 使用内容 hash 绑定的私有副本，源文件按 no-follow 打开；不修改共享缓存的权限。
- 取消必须在关闭源文件后、启动子进程前再次检查。`stop()` 返回子进程退出的 Promise；测试清理先等待退出，再删除临时可执行文件。
- Windows 原生 CI 复现了精简环境下 PowerShell cmdlet 自动加载超时：同一 ACL 脚本在完整环境中成功，仅补 TEMP/TMP 仍超时；所有者诊断正确。ACL 实现改用 .NET 文件系统/访问控制 API，不依赖自动加载的模块，不继承应用凭据，原有所有者、重解析点和私有 DACL 检查保持不变。
- 修复后本机完整 suite 为 **119 项通过、0 失败、0 跳过**，Go/vet 和实际 npm pack 通过，记录 `release-final-tests.txt`。完整 race 再次通过，记录 `platform-fix-race.txt`（缓存命中如实保留）。
- 六个原生 CI 任务和总准入任务仍然全部必需；失败、取消、跳过或缺失都拒绝总准入。对应提交的实际结果查看 [GitHub Actions](https://github.com/koala9527/magpie-model-check/actions)，不把编译通过替代原生安装。

Windows CI 的打包检查还复现了 Git Bash GNU tar 把 `C:\\...` 归档地址解释为远程地址的问题。包检查和 Bun ZIP 校验/解包统一调用 Windows 自带 tar；保留允许名单、归档路径和 hash 检查，新增 Windows tar 选择回归。原错误 exit 2 和修复后的实际 npm pack exit 0 均保留。
