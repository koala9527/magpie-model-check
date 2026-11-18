# P0：保留、迁移与退役清单

> 技术存档：保留 2026-11-17 文档重写前的详细内容。本页的“本轮/当前”指此前检查记录；现状请看[当前进度](../p0-report.md)，总体目标请看[新版方案](../../rebuild.md)。


日期：2026-11-17。决定依据：[架构审计](architecture-audit.md)、[官方接口审计](magpie-api-audit.md)、[rebuild.md](rebuild-v1.md)。
状态：供人工检查的迁移方案；P0 未执行重命名、删功能或数据库迁移。

## 保留并先建立回归

| 对象 | 决定 | 必须先做的验证 |
| --- | --- | --- |
| `third_party/modeltrace/LICENSE` 和固定版本 JS/bank | 保留来源、hash 和 MIT；旧 bank 作为 legacy/imported，不自动成为正式身份参考库 | LF 归一化 hash、固定输入的特征/评分/排序逐项 golden test |
| Go 数字挑战生成 | 保留原协议为 `legacy-modeltrace-v1` | 固定随机选择，逐项对照固定上游 Python；独立于新严格 parser |
| 超时、取消、SSE 完整结束检查、响应大小限制 | 抽取复用，不沿用“回答通过＝身份通过”含义 | 截断/超时/429 都只导致 UNAVAILABLE/PENDING |
| 单目标锁、历史审核、删除历史仍保留预算计数 | 迁移数据与通用资源控制 | 并发取消、重启、删除目标不删样本、预算不复活 |
| SQLite 驱动/原子写入经验 | 是否保留取决于真实持久化和 IPC PoC | 在线一致性备份、迁移事务、失败恢复；不能复制正在写入的 db 文件冒充备份 |
| 本项目及字体/图标/第三方许可证 | 全部保留；产品去标识不删版权来源 | 每个发行包包含必要声明，不把合法来源算作残留产品标识 |

## 分阶段迁移

| 对象/旧标识 | 新行为 | 依赖与验收 |
| --- | --- | --- |
| npm 名 `magpie-intelligence-check` | `magpie-model-check`；显示名 `Magpie Model Check`；指定英文描述 | 先核对插件 short name、tools 名和已安装 spec；不要悄悄重写用户 plugins.json |
| Go module、`cmd/quality-check`、`bin/quality-check-*` | 若引擎继续保留，再改 module/import/命令/发行脚本为新名称 | `go list`/build/安装测试；保留旧命令说明及迁移路径，避免只改入口未改脚本 |
| `~/.config/magpie-quality-check/check.db` | 新命名私有目录、版本化身份表 | 识别明确旧路径→一致性备份→事务迁移→校验条目/历史→切换；重试幂等；旧目录留作回滚，不能直接丢弃 |
| `State.Targets` | `model_targets`，增加 `claimed_model` 等显式字段 | 旧 `model` 只迁移为目标 ID，不猜测真实身份/Provider；`route_binding_status=unverified`、`auto_check=false` |
| `State.Results` | legacy 历史与新 `fingerprint_samples/batches/results` 分开 | 旧质量题/人工复核不是指纹训练样本；旧 passed/review 不映射成 MATCHED/MISMATCHED |
| Gateway key | 独立密钥管理或受控环境变量引用 | 普通 plugin page 的 options 对沙盒可读；不能把新密钥塞进页面可读 options 或导出包 |
| 现有独立控制台 | 官方 plugin page 的三个简洁视图 | 先验证 bridge；file-page 没有任意 fetch 或工具调用 bridge；服务页另走官方 ServiceSpec 与生命周期 PoC |

## 待退役的功能及调用依赖

| 功能 | 当前依赖关系 | 退役顺序 |
| --- | --- | --- |
| 业务请求 403 门禁、hold/allow、15 分钟放行 | `saveLocked`→动态 middleware；`Action`→UI；gate_time/restart/integration 等测试以阻断为预期 | 先备份运行态 middleware，改为只观察/透传；新非干扰测试通过；确认遗留门禁未被残留安装继续加载；再移除旧动作及相应 UI/API |
| `quality-probe/<secret>/<model>` 别名 | 普通质量检测和 ModelTrace 发请求；动态 middleware 剥前缀；日志触发递归规避 | 先验证独立 metadata 标记及实际 Provider 证据；保持 model 原样；新集成验收通过后删别名及旧别名说明 |
| 日志触发自动牛奶题 | `console.log`→`WatchLog/LogLine`→`Trigger`→`Grade`→门禁 | 关闭触发链，再关闭 tailer，再迁移 UI；不能留下业务失败但无人消费的门禁 |
| 通用质量/页面生成题、HTML/SVG answer preview | `prompts.go`→Results→HTTP download/preview→前端脚本、测试、字体 | 旧历史保留或只读导出；身份检测页不再展示通用跑分；先清依赖再精简资源 |
| Cloudflare 临时远程控制台及自动下载器 | `auth.methods[1]`→cloudflared-runtime→Go Console→HTTP tunnel | 移除远程入口与启动链；确认进程退出；不上传/转发样本；相关合法第三方声明按发行物是否仍包含决定保留 |
| 控制用伪 Provider `quality-check` | `config` 注册、`auth` 打开管理链接、安装测试预期 Intelligence Check | page/tool 完成真实启用流程后移除；不能假定删除 Provider 等于逐插件后台已销毁 |

## 回滚与数据恢复

1. P1/P3/P5 每次改变前保存源文件 hash、插件 spec/options 的备份和一致性数据备份。
2. 迁移使用 schema version 和事务，保存旧 JSON 的原始版本；来源/身份缺失用 NULL 或 unverified，不补造。
3. 代码回滚与数据回滚分开：新代码停写、恢复旧目录/配置指向，再校验旧应用读取；不得直接反向 DROP 用户新样本。
4. 原插件被移除时默认保留本地历史；删除数据是明确的独立用户动作，不能作为安装/卸载脚本的默认行为。
5. 本轮 `artifacts/rebuild-20261117/ROLLBACK.sh` 仅接受该目录的 `rollback-check/MODIFIED_FILE.mjs`，已在副本上运行并比对原始 SHA-256；恢复后为透传、无路由观测。`poc/MODIFIED_FILE.mjs` 保持修改状态，80 个原始文件未变。真实迁移回滚将在相应阶段另外验证。
