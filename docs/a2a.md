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

单进程最多保留 16 个非终态任务和 128 个终态任务；终态最多保留一小时，清理在任务访问、接收和完成时执行。到期查询返回 task not found。任务索引保存在工作区 `.lune/a2a`，服务重启时恢复，并通过 Core 持久会话重新打开已确认的执行。提交状态不明的任务会标记失败，要求先检查会话。context 对应 Core 的持久会话，清理 task 不删除会话。

## 按需 Docker 代理

`deploy/docker-compose.a2a.yml` 使用常驻 Go 代理转发到按需启动的原生 A2A worker。代理自己提供健康检查和 Agent Card；首个已认证 JSON-RPC 请求到达时启动 loopback-only 的 Lune 子进程。对外 Bearer 在代理终止，不转发给 worker。代理仅在所有已观察任务终态且空闲一小时后卸载 worker；等待交互的任务会保持运行直到恢复或取消。

若要从 Lune Web 查看 MoonFei 委派创建的同一会话，在同一工作区启动 `lune web --workspace <工作区> --shared-state`。A2A task 的 `contextId` 就是 Core session ID。Lune Web 使用 `?sessionId=<contextId>` 打开对应会话；两个 Core 共享状态，Core 的会话锁仍会阻止同一会话并发执行。

工作目录固定为 `/workspace`，默认绑定 Windows 宿主的 `D:\WorkSpace`，权限为 `workspace`。容器使用独立 Linux 配置，模型密钥通过 `apiKeyEnv` 提供；不得提交 `.env` 或 `deploy/lune-runtime.config.json`。重启后，已确认交给 Core 的活动或等待交互任务会恢复原 task ID 与 context。Core 尚未确认接收的提交会标记失败，代理不会自动重放，MoonFei 将结果不明任务交给用户判断。
