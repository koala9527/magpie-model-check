# 当前架构：检测任务独立运行

更新：2026-11-18。[返回文档目录](README.md)

普通请求直接经过 Magpie 发给原渠道。模型检测使用独立挑战，不改业务请求，也不读日常对话。旧版的门禁、日志触发和远程控制台已退出新入口；旧源码保留作数据兼容和回归测试，不进入 npm 发行包。

```text
Magpie 页面 / MCP 工具
          ↓
本机插件运行实例 → Go 本机服务 → SQLite 目标、样本和历史
                              ↓
                        Magpie 网关
                              ↓
                   实际配置的供应商 / 路由组
                              ↓
                   route V3 观察与响应认证
```

| 部分 | 职责 | 入口 |
| --- | --- | --- |
| 插件工厂与运行时 | 隐藏启动本机服务、私有配置、关闭取消与重载 | `plugin/index.mjs`、`plugin/runtime.mjs` |
| 网关中间件 | 只记录独立探针的渠道和响应签章；普通请求原样通过 | `plugin/route.middleware.mjs` |
| Go 服务 | 受预算的检测、取消、中文 API/页面、实验审核与快照 | `cmd/model-check`、`internal/modelcheck` |
| 数据库 | schema v1、旧库只读迁移、用量预留、备份与恢复 | `internal/modelstore` |
| 指纹 | 固定版本算法、严格解析、原始数据 hash 与数值回归 | `internal/fingerprint` |
| 归因 | 六分区实验、来源回调、中心/规则重算、统计区间 | `internal/attribution` |

## 三种证明不混用

1. **渠道记录**：route V3 的 MAC 绑定本机观察的探针请求、供应商、完整响应及 SSE 链。
2. **采集协议**：当前宿主在观察钩子后仍整理请求，插件缺少最终上游请求证明；因此运行时固定 `protocol_guard=false`。
3. **模型身份**：还必须有真实来源、独立校准/测试及适用范围。当前真实参考样本为 0，legacy 和 synthetic 不满足身份准入。

所以“请求成功”“渠道 VERIFIED”“回答能解析”与“身份 MATCHED”是独立字段；当前实际宿主上的身份保持未验证，而非假装已经达成模型认证。

旧版架构和原始数据库细节保留在[旧架构调查](technical/architecture-audit.md)。当前接口以源码和[参考库指南](reference-library.md)为准。
