# A2A 服务

`lune --a2a-listen 127.0.0.1:18091` 默认使用 workspace 权限。`--permission read-only|workspace|auto|full-access` 在监听前生效。只有数字 loopback 地址可不设置 token；通配地址、普通主机名和非 loopback 地址必须设置 `LUNE_A2A_TOKEN`。客户端使用 Bearer token。远程服务通过可信反向代理提供 HTTPS。

SendMessage 接受按顺序排列的 text 和 JSON data parts。初始 data 作为标记为 JSON 的用户内容传给模型，不作为控制命令解析。新任务返回 Core session ID 作为 contextId；使用有效 contextId 的后续任务继续同一工作区会话，未知 ID 报错。同一上下文同时只运行一个 A2A task。

请求最多等待 15 秒后返回任务快照；断连不取消已接受任务，客户端通过 GetTask 继续查询。CancelTask 请求 Core 中断，只有 Core 确认终态后才显示 canceled。正常退出关闭服务接收、停止本服务任务，再关闭 Core。

待交互状态包含 data：`kind`、`request_id`、`payload`。审批 payload 使用 `tool`、`arguments`、`path_grant`（`path`、`access`）、`auto_review_reason` 和 `fingerprint`；问答的 `questions` 包含全部问题和选项。模型恢复提供 `failure`，工具恢复提供 `tool`、`arguments`，MCP 交互提供 `server` 和 `request`。续传消息携带 taskId，data 必须包含对应 request_id：

```json
{"request_id":"approval-id","decision":"allow_once"}
```

| kind | 回复字段 |
|---|---|
| approval | decision: allow_once、allow_session 或 deny |
| user_question | answers: 按问题 ID 映射的回答对象 |
| model_recovery | decision: retry 或 cancel |
| tool_recovery | decision: retry、skip 或 cancel |
| user_interaction | response: MCP 结构化 JSON 响应 |

文本 yes/allow/approve 只批准一次，文本 allow_session 才创建会话授权。结构化回复需匹配 request_id；单个待交互问题也可用文本回答。输入验证或恢复失败时保留待处理状态，允许重试。

需要重新规划的 execution blocker、预算暂停和无可映射交互的暂停会结束当前 A2A task，返回结构化原因；Core 会话和检查点保留。A2A 不自动增加预算或扩大授权，可通过支持该操作的宿主恢复会话。

单进程最多保留 16 个非终态任务和 128 个终态任务；终态最多保留一小时，清理在任务访问、接收和完成时执行。到期查询返回 task not found。task 查询记录不跨服务重启保存；context 对应 Core 的持久会话，清理 task 不删除会话。
