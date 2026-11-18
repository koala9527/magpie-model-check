# V1 本地页面与服务契约

状态：实现中；用户于 2026-11-17 批准开始正式改造并推送新仓库。

普通页面只调用官方 bridge 的 `ready`、`options.get()` 和 `service`。普通文件页面受到 `frame-src 'none'` 和 `connect-src 'none'` 限制；它使用导航进入所声明的本地服务页面，而不是嵌套 iframe 或增加任意 fetch。Go 服务留在 loopback；数据、预算、检测及取消属于服务，网关中间件仅关联受控请求。Magpie web 使用官方 token 代理地址，桌面窗口使用本机服务地址。

页面使用每次启动生成的独立页面会话，不获得 Magpie Key、路由签名密钥或后台控制令牌。服务内页面读 `<meta name="model-check-session">` 的会话并以 `Authorization: Bearer ...` 请求下列相对接口。Go 页面的 API 错误均为 `{error:{code,message}}`。业务请求不经过本地控制 API。

| 接口 | 输入 / 输出 |
| --- | --- |
| `GET api/state` | `{targets,batches,banks,settings,usage,active_jobs,capabilities,legacy_records}` |
| `GET api/models` | `{models:[{id}],gateway_configured}`；不返回供应商密钥 |
| `POST api/targets` / `PUT api/targets/{id}` | 完整 Target；服务负责校验，身份/检测时间由服务维护 |
| `DELETE api/targets/{id}` | 检测进行中返回 409；删除不退款、不删除旧版备份 |
| `POST api/targets/{id}/checks` | `{}`，202 返回 `{batch_id}`；按目标 request_budget 执行 |
| `GET api/batches/{id}` | `{batch,samples,result}`；result 在执行中为 null |
| `POST api/batches/{id}/cancel` | `{}`；取消仅对应批次 |
| `POST api/settings` | daily_requests、daily_tokens、concurrency、timeout_seconds、retention_days |
| `GET api/backup` | JSON 便携备份；页面提供复制/文本保存操作 |
| `POST api/restore` | JSON 备份原文，活动任务时 409；恢复前备份，额度取 max |
| `POST api/banks` | Bank 元数据和 data；来源待审核，上传的 trusted 不直接生效 |
| `GET api/banks/{id}/export` | 单个 Bank JSON |
| `DELETE api/banks/{id}` | 内置历史 bank 保留，可删用户导入 bank |

字段遵循 `internal/modelstore/types.go` 的 snake_case JSON；目标必要字段是 model_id 和 claimed_model，provider_id 为空时返回 ROUTE_UNVERIFIED；enabled 默认 true，auto_check 默认 false；interval_minutes 至少 15，request_budget 至少 1。状态保留七种定义，候选分数不是概率。

页面必须提供模型、详情/历史、参考库三个功能视图；显示有效/无效原因、真实渠道状态、原始受控回答和版本，不显示伪造百分比。API Key 不进入 options、页面、导出、数据库配置或错误信息。
