# 技术资料索引

更新：2026-11-18。本索引把本轮接口与最终验收、原始规格、历史调查分开，避免把旧状态当成今天的进度。

用户先看[改造方案](../../rebuild.md)、[当前进度](../p0-report.md)和[参考库指南](../reference-library.md)。后续工作以[执行计划](../implementation-plan.md)为准。

## 本轮实现和使用合约

| 资料 | 查什么 |
| --- | --- |
| [0.4.0 最终验收](../verification-0.4.0.md) | 最终源码的测试、race、六目标发行包、Windows 原生安装、实际页面和审查修复；编译、运行与身份实验分别记账 |
| [参考库指南](../reference-library.md) | 原始 bundle、raw/hash/分组检查、本机逐条审核、Policy 重评、显式激活、恢复和真实实验步骤 |
| [算法回归记录](algorithm-regression.md) | 固定 ModelTrace 版本、严格解析、特征/排序误差及数值回归 |
| [归因包合约](../../internal/attribution/README.md) | 类型和 JSON 字段、ProtocolSHA256、bundle/raw/provenance 接口、Wilson 和归因方法 |
| [参考准入代码](../../internal/modelcheck/reference.go) | 固定采集协议、原始实验检查、参考库元数据与检查接口 |
| [本机审核/激活](../../internal/modelcheck/reference_audit.go) | 全记录审核绑定、本机签章、Policy 重算、激活和恢复失效 |
| [实际请求与多轮适配](../../internal/modelcheck/service.go) / [决策适配](../../internal/modelcheck/reference_decision.go) | 启动时参考快照、采集协议、预算、完整轮次与多轮一致 |
| [HTTP 合约](../../internal/modelcheck/http.go) / [渠道和协议认证](../../internal/modelcheck/route.go) | data 字符串传输、认证接口、恢复边界、route v3 完整响应证明及宿主最终上游协议限制 |
| [完整指纹/归因证据索引](../../artifacts/rebuild-work-20261117/fingerprint/EVIDENCE_INDEX.md) | 实际命令、退出码、数值结果、独立审查、变异和隔离回滚；其中集成失败属于当时快照，修复现状看当前进度 |

源码 module 迁移、最终测试/race、六目标 pack 和 Windows 原生安装已完成。链接中的源码是本轮合约依据，不是六个平台均已实际运行的证明；平台运行结果以各自原生 CI 记录为准。

## 原始规格：保持原文

[rebuild-v1.md](rebuild-v1.md) 保留原始技术规格，未因本轮文档收尾改动。需要完整的数据字段、来源分类、实验划分、误报指标、空值和发布验收要求时查该文档。规格中的历史阶段状态不代替[当前进度](../p0-report.md)。

当前实现会保存目标、题目、原始样本、批次、结果、参考资料和旧历史；实际字段以 [modelstore 类型](../../internal/modelstore/types.go)及[attribution 类型](../../internal/attribution/types.go)为准。来源审核和本机审核密钥另行保存，不随数据库备份或参考库导出传递。

## 历史调查与旧版用法

| 存档 | 用途 |
| --- | --- |
| [旧版详细用法](current-version-usage.md) | 旧质量控制台、门禁、选项和旧开发命令；不是新入口操作指南 |
| [旧架构调查](architecture-audit.md) | 当时的入口、调用链、数据库、后台、算法和依赖 |
| [接口核查](magpie-api-audit.md) | 官方源码、精确版本、插件能力和早期实验 |
| [原迁移清单](reuse-and-remove.md) / [技术问题记录](risk-register.md) | 旧功能依赖和原检查要求 |
| [原执行计划](implementation-plan.md) | 旧阶段编号、切片和详细检查点 |
| [早期实验记录](p0-report.md) | 当时的命令、结果、环境、文件校验及恢复输出 |

这些资料保留排错和决策背景；其中早期批准、页面、存储、取消和 race 状态不作本轮进度声明。

## 证据目录怎样看

- `artifacts/rebuild-work-20261117/`：本轮服务、参考库、页面、协议、构建与打包记录。
- 其 `fingerprint/` 和 `fingerprint/p4/`：数学回归、独立审查、阈值/分组机制、原始数据绑定和四件隔离验证证据。
- 其 `release-tooling-r1/`：已有构建、pack 和工具检查快照；六平台编译与本机运行分别标记。
- `artifacts/rebuild-20261117/`：更早的中间件准备实验。
- `artifacts/docs-rewrite-20261117/`：前一次文档整理的原文副本与检查。

这些路径被 Git 忽略；本地能打开日志不意味着日志进入提交或发行包。可随源码阅读的最终摘要在[验收报告](../verification-0.4.0.md)，原始日志保留实际命令与结果。
