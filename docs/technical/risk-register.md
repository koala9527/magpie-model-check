# P0：风险登记与验证门槛

> 技术存档：保留 2026-11-17 文档重写前的详细内容。本页的“本轮/当前”指此前检查记录；现状请看[当前进度](../p0-report.md)，总体目标请看[新版方案](../../rebuild.md)。


核验日期：2026-11-17。状态含义：`已观察`＝本仓库/固定官方源码中的事实；`待验证`＝需要真实集成/实验；`已验证（fixture）`不代表真实用户渠道已验收。

| ID | 风险与证据 | 影响 | 验证方法/退出条件 |
| --- | --- | --- | --- |
| R01 | 旧 `saveLocked` 返回 403，规格 V1 只检测 | 高；影响正常请求 | P1 建立所有协议的透传测试；不通过则保持迁移未完成并恢复原文件，不发布新插件 |
| R02 | 探针借模型名前缀，当前没有实际 Provider 记录 | 高；测错渠道且错误归因 | 真实 Gateway 的 `onProvider`＋唯一探针 marker＋最终 response/event 关联；缺证据一律 ROUTE_UNVERIFIED |
| R03 | Middleware 是 moejs，无 fetch/计时器/Node API | 高；虚构后台/IPC | 在官方引擎运行 PoC；middleware 只同步观察，计算/网络移到 tool/已验证宿主路径 |
| R04 | file-page 是 opaque-origin sandbox，bridge 没有任意网络/任意工具执行能力 | 高；页面看似完整但按钮不工作 | 安装真实页面，验证 bridge/options/permissions；若需要 ServiceSpec，单独验证服务代理、鉴权、启停，不外推 file-page 能 fetch |
| R05 | 共享 Bun 宿主与旧 Go child 的生命周期；PoC off 后在途请求约 10002ms 才结束 | 高；关闭插件后任务继续，未满足立即取消 | 已验证 off 移除工具、on 恢复、退出网关端口关闭；生产实现须另建任务取消机制并测试。`Restart/retire` 允许旧调用继续，不能依赖它立即中断 |
| R06 | 旧 browser scorer 无开放集拒识，所有有效输入强制选 18 类之一 | 高；未知模型被误认证 | 独立 unknown_test，类条件校准、margin 和跨挑战一致性；无校准时仅 INCONCLUSIVE，不生成身份概率 |
| R07 | 旧参考 bank 标签/渠道证明、数据集切分不完整 | 高；渠道/日期泄漏 | 按批次、渠道、日期/会话分组，严格分 train/calibration/test/unknown/holdout；未审核来源不进正式 bank |
| R08 | `/\d+/g` 能把负号/小数/夹杂文字当数字段 | 高；污染样本 | 新严格 parser 拒绝/记录完整原因；legacy parser 单独 golden，不悄悄改旧算法协议 |
| R09 | scorer/bank 原始 hash 和来源 README 不同 | 中；错误供应链结论 | 已确认差异为 CRLF/LF；统一 LF 后与固定上游匹配。发行包同时记原始 hash 与 canonical hash |
| R10 | Windows `0600` 无已验证的 ACL/加密保护；密钥和状态同 JSON | 高；密钥暴露 | Windows ACL/密钥库或明确受控环境变量路径；备份/日志/导出/页面 options 做反向泄漏测试 |
| R11 | daily_limit 是单模型请求量，不是全局 token 上限 | 中；意外费用和并发 | 调度前原子预留请求/token；每目标锁＋全局 semaphore；取消/失败不误恢复已花预算；重启保持账本 |
| R12 | 自动触发依赖 rate-limited console.log 和外部 log_path | 中；漏检测/错误递归 | V1 手动优先；不靠日志当可靠任务队列；普通业务样本不自动入库 |
| R13 | 数据只一张 state BLOB，无 schema 迁移/一致性备份 | 高；改名丢历史 | SQLite migration 事务、old JSON 保留、幂等测试、在线备份/恢复验证后再切换 |
| R14 | 没有跟踪的 CI；现有脚本含 Unix 命令/无后缀 binary/手拼 file:// URL | 中；Windows 误报通过 | 区分现有基线失败与新回归；用 pathToFileURL/本平台 binary；真实 Windows 安装，Linux/macOS 单独 runner |
| R15 | 旧远程控制台将本机管理服务通过隧道暴露 | 高；偏离本地 V1 产品 | 先列 Console/auth/download 依赖并关启动链；本轮不启用任何隧道、不发送密钥/样本到第三方 |
| R16 | 固定官方版本与安装用户的 Magpie 版本可能不同 | 高；hook/page/API 漂移 | 报告固定 commit 和执行版本；增加兼容能力检测；缺能力不伪装验收通过 |
| R17 | 仅 nonce/source/provider 字段可被上游伪造，middleware fail-open 时尤其危险 | 高；虚假路由归因 | PoC 已加每次测试的私有本地 proof，并实测移除 middleware、泄漏 nonce、伪造公开字段仍 ROUTE_UNVERIFIED。proof 不进请求/options/page目录/tool结果。产品化安全共享/存储仍待验证；路由观察不是签名模型身份 |
| R19 | 现有 source.zip 缺 ZIP 中央目录；当前文档重抓及部分源码请求失败 | 中；把损坏/过期文件当可复现来源 | 固定源码逐项对 tree blob hash，当前 main 分文件复核成功与失败分别记录；损坏压缩包不作为完整源码证据 |
| R18 | 固定样本数、阈值或 softmax 百分比没有本项目独立盲测依据 | 高；统计承诺不成立 | 报样本量、假匹配/替换率、unknown recall、置信区间；由校准产生版本化阈值，实验决定发布门槛 |

## P0 的必需人工决策

1. 检查旧门禁/远程控制台的退役与历史保留路径是否符合实际使用方式。
2. 检查最小 PoC 的证据足以选择 tool/file-page/service-page 哪条集成路线；尚未验证的路由、IPC、生命周期保持待验证。
3. 检查采用的官方 Magpie 固定版本；正式兼容版本应由真实安装测试确定，而不是猜一个最低版本。
4. 审核后按 [implementation-plan.md](implementation-plan.md) 开始 P1；未审核不跨阶段整体替换运行代码。
