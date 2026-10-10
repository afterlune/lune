# 待做 / TODO（按优先级排序）

> 排序原则：**地基 → 公共底座 → 功能 → 新子系统**。
> 同一文件/同一接口的改动尽量集中在同一阶段，使前面的施工对后面的影响最小。
> 标签：`[A]` 会话设置 `[B]` 历史 `[C]` 会话管理 `[E]` 上下文预算/用量 `[G]` 工具结果瘦身 `[H]` 流式 `[D]` MCP `[F]` Subagent `[M]` 多模态输入 `[Q]` 提问 `[X]` 桌面端
>
> **⚠️ 本文件状态：已过期，待重新基线化。** 项目已大幅重构：顶层已新增 `core/`；
> TUI 已迁至 `internal/tui/`（含 `theme.go`/`avatar.go`/`preferences.go`/`assets/`）；`cmd/` 仅剩 `main.go`+`core_cli.go`；
> `internal/loop/` 已删除；流式（`core.StreamResponse`/`ModelStreamEvent`）、team/subagent（`core/team.go`/`team_store.go`）、
> 多 provider（`model.Provider`/`provider_http.go`/`responses_provider.go`/`anthropic_messages.go`/`ModelProfile`）、
> 会话迁移（`Core.migrateLegacySession`）**均已落地**。因此下文多条已完成或引用了已不存在的文件。

---

## 技术选型（已定）

- [x] **TUI：Bubble Tea** —— `charm.land/bubbletea/v2`（v2 线），配 `charm.land/bubbles/v2` + `charm.land/lipgloss/v2`；**已在用**
- [x] **Desktop：Wails** —— **待引入**（当前 `go.mod` 无 Wails 依赖）
- 约定：两种前端**只依赖 `core` 包**，不重复实现 agent 逻辑。
  `core.Core` 已暴露稳定接口面（见 `internal/tui/interfaces.go` 的 `Client`：`RunTask`/`ResumeTask`/`Compact`/
  `CreateSession`/`ListSessions`/`SetPermissionMode`/`ResolveApproval`/`SetEventHandler` 等），Desktop 端应复用同一接口。
- 共享层设计：偏好/主题（`internal/tui/preferences.go`、`theme.go`）考虑下沉为可共用包，避免 TUI 与 Desktop 双份实现。

## P5 桌面端（Wails，`[X]`）

- [ ] 引入 Wails 并评估构建链影响（`go.mod`、`wails.json`、CI、交叉编译与打包）
- [ ] 基于 `core.Core` 的 `Client` 接口面接线，**不**重写 agent 逻辑
- [ ] 能力对齐 TUI：会话管理（含 `delete` / `rename` / `archive`）、工具审批、流式增量输出、用量与上下文占用展示
- [ ] 模型提问（`[Q]`）在桌面端的呈现（模态对话框 + 文本/选项输入）
- [ ] 打包与分发（macOS / Windows / Linux）与自动更新策略
- [ ] 密钥与配置：当前 `config.json` 明文 API key → 桌面端应改用系统钥匙串（Keychain / Credential Manager / Secret Service）
- [ ] 偏好与主题复用：与 TUI 共享同一份偏好/主题定义
- [ ] 与 TUI 的互操作：同一工作区、同一 `sessions.db` / `checkpoints.db` 并发访问的锁与一致性
- [ ] 测试：核心逻辑复用已有 `core` 测试；桌面端补接口层与关键流程测试

---

## P0 稳定与地基（先行；不解决则后续每步都踩坑或返工）

- [ ] **【首位·门禁】TUI 设计先行：用 image gen 生成效果图，用户确认后再实施**
  - [ ] 先用 image gen 出效果图（转写左右分栏、用户短标记、长输入折叠态、footer / 详情面板等）
  - [ ] **交用户确认**；确认前**不得**改动 `internal/tui/` 的渲染实现
  - [ ] 覆盖场景：空态 / 对话态 / 长输入折叠 / 工具活动 / 详情面板 / 窄终端退化 / 模型提问
  - [ ] 确认后再落实 P3.0（渲染改版）
- [ ] 修 I-1：类别选择/工具可见性跨轮丢失 + 提示可操作化（已复现 3 次，含写入反复失败）
- [ ] 统一工具元数据：注册时声明 `category + permission class + read-only`，替换硬编码（原 `tool/permissions.go`、`agentgraph`、`registry.NewDefaultRegistry`）
- [ ] 修 `workspace` 模式对未知工具免批准的安全缺口（同一改造）
- [ ] 结构化配置加载+校验（`models` / `mcpServers` / `contextWindow` / `autoCompact` / 附件大小上限）——provider catalog 已部分落地，待补齐
- [ ] 会话 `data` 加版本号 + 迁移策略（`migrateLegacySession` 已有雏形，需正式版本化）
- [ ] 定 `sessions.db` 与 `checkpoints.db` 的级联删除关系
- [ ] 定 token 计数口径（服务器 `usage.InputTokens` vs 本地启发式）
- [ ] 新增 `internal/session/store_test.go`
- [x] 新增 `/help`（行式 CLI 与 TUI 均列出各自支持的命令）
- [ ] 日志轮转 `.luneagent/agent.log`

## P1 公共底座（一次改好，后续都直接复用）

- [ ] `[A][E]` `TaskOptions` / `State` 增加 `Model` / `ReasoningEffort` / `Goal` / `ContextWindow` / `AutoCompact` 等字段，并在 `Start` 写入 `State`（`Recover` 不传 `TaskOptions`，必须持久化）
- [ ] `[A]` **「模型对用户的称呼」设置**：与 `Goal` / plan mode 一起组合进 `Instructions`
- [ ] `[A]` **「模型人物背景设定（persona）」**：同一管线注入；**合成顺序**上只塑造风格，**不得弱化** plan mode / 权限等系统约束
- [ ] `[A]` **`Instructions` 合成器**：把 `Goal` + `称呼` + `persona` + plan mode 常量统一拼装
- [ ] `[A][E]` `requestModel()` 应用上述设置（model 回退默认、effort → `Reasoning`、instructions 走合成器）
- [ ] `[A][E]` 设置持久化并恢复（含称呼、persona）——现在由 `core.Session` 承载
- [ ] `[Q]` **交互式回调通道抽象**：把审批通道（`Core.ResolveApproval` + `core.ApprovalRequest`）泛化成通用用户提问通道，供 `[Q]` 复用

## P2 会话与历史

- [ ] `[C]` `session.Store.Delete(ctx, id, workspace)`
- [ ] `[C]` `session.Store.SetTitle(ctx, id, workspace, title)`
- [ ] `[C]` `session.Store.SetArchived(ctx, id, workspace, bool)`
- [ ] `[C]` `session.Store.List` 增加 `includeArchived`（JSON1 过滤，免 schema 迁移）
- [ ] `[C]` `core.Session` / 持久化加 `Title` / `Archived`（upsert 不覆盖）
- [ ] `[C]` 会话面板键位：`d` 删除（Y/N 确认）、`r` 重命名、`a` 归档、`Tab` 显示已归档；渲染优先 `title`
- [ ] `[C]` 删除当前会话时自动切到新会话
- [ ] `[B]` `/fork`：复制历史到新 session 并切换
- [ ] `[B]` `/rewind`：按 user turn 回退历史（禁止跨 `function_call`/`output` 切分）；paused task 时禁用并提示

## P3 渲染基调 → 命令 → 上下文效率 → 输入 → 提问

### P3.0 输出渲染改版：**只标记用户，正文一律常规字重**（先行；当前“杂乱无章”的根因）
> 前置：须先完成 P0 首位的「效果图 → 用户确认」门禁。
> 基调：**少样式、多结构**。清晰来自对齐/缩进/间距，而非整段上色或整段加粗。
> **只有用户消息做区分**；agent / 工具 / 通知保持默认样式。整体不刺眼。

- [ ] **左右分栏**：**用户消息右对齐**；agent / 工具 / 通知左对齐（默认样式，不加色）
- [ ] **加粗只用于短标记，不用于正文**：用户块用一个加粗的短标记（如 `▸` / `you`）标识，**正文保持常规字重**——长输入尤其不能整段加粗
- [ ] 区分用户优先靠**对齐 + 短标记**；颜色为可选、且仅用于该短标记（低饱和），不铺满正文
- [ ] **不辣眼睛**：避免高饱和/高亮/纯色块；不叠加边框与背景色
- [ ] 其余加粗一律移除：只保留“用户标记”这一处加粗
- [ ] 层级靠结构：缩进 + 间距 + 单一前缀标记区分工具/通知/错误
- [ ] Markdown 降级：标题→纯文本或极简前缀；代码块保持原样缩进；**不**引入行内加粗/斜体/彩色标题/表格重排
- [ ] 一致性：assistant 与 user 走同一处理路径
- [ ] 工具条目去重：内联状态与活动面板重复 → 择一或明确分工
- [ ] 宽度感知：缩进/换行/块宽随宽度计算；用户块右对齐、块宽上限约 70–80%
- [ ] 间距规范化：块间空行统一
- [ ] 窄终端退化：宽度不足时用户块退回左对齐（仍保留短标记）
- [ ] 渲染性能：避免每帧全量重建 + `SetContent`
- [ ] 测试：转写渲染快照（含用户右对齐/短标记、长输入不加粗、窄终端退化）

- [ ] **长条目可点击折叠/展开**
  - [ ] 条目增加 `collapsed`，并持久化
  - [ ] 长用户输入**默认折叠**为 N 行；点击首行/标记切换展开
  - [ ] 折叠指示（`▸`/`▾`）**只对超阈值长条目显示**
  - [ ] **点击命中测试**：建立「条目 → 渲染行区间」索引 + viewport 偏移换算
  - [ ] 点击落在活动面板/选择器时不误触；补键盘等价操作

- [ ] **会话可回到底部**
  - [ ] 新增按键跳到末尾（候选：`End` / `G` / `Ctrl+End`）
  - [ ] 滚离底部时**不**自动跟随；回到底部后恢复自动跟随
  - [ ] 可选「↓ 回到底部」提示；与折叠、流式增量共存不得强制拉回底部

### P3.1 命令
- [ ] `[A]` `/goal`（查看/设置/清除）、`/effort`（选择菜单）、`/model`（菜单 + `<name>`）
- [ ] `[A]` **`/user`（或 `/callme`）**：查看 / 设置 / 清除「模型对用户的称呼」
- [ ] `[A]` **`/persona`**：查看 / 设置 / 清除「模型人物背景设定」（支持多行输入）
- [ ] `[E]` `/context`（查看/设置窗口/预留/reset）、`/autocompact`（查看/on/off/threshold）
- [ ] `[E]` 配置项 `contextWindow` / `autoCompact` / `autoCompactThreshold` / `reserveOutputTokens`

### P3.2 上下文效率
- [ ] `[E]` 自动压缩触发（任务内生效；先瘦身、再摘要）
- [ ] `[E]` footer 增加上下文占用比例 `ctx 12.3k/64k (19%)`，保持一行简要（按宽度裁剪）
- [ ] `[E]` 详情面板增加窗口/占比/缓存命中率、按 agent 分组统计（`core` 已有 per-agent usage 记录）
- [ ] `[E]` 口径统一：以服务器 `usage.InputTokens` 为准，缺失时估算
- [ ] `[G]` 客户端兜底瘦身：接近预算时保留最近 N 轮原文 + 失败态，其余替换为状态占位/头尾摘录
- [ ] `[G]` 只替换 `function_call_output` 的 `output` 内容，保持 `call_id` 配对
- [ ] `[G]` 模型选择性表达：`pin`（关键 `call_id` 不瘦身）/ `drop_result` / scratchpad 笔记
- [ ] `[G]` 双落点：历史组装处（`core/runtime.go` 与 `agentgraph`）
- [ ] `[M]` 图片/附件不参与文本瘦身与 token 估算

### P3.3 流式（已落地，剩余为体验补齐）
> `core.StreamResponse` / `model.ModelStreamEvent` / `Core.onModelStream` 已实现。
- [ ] 增量渲染的**流式态**细节（首 token 延迟、reasoning delta 展示开关）
- [ ] 未闭合 markdown / 半行处理
- [ ] 取消传播与中断恢复衔接的边界用例
- [ ] 从 `response.completed` 取 usage 与现有 `usageTrackingRequester` 观测点对齐

### P3.4 输入栏支持任意模态粘贴（`[M]`）
- [ ] 输入栏可**直接粘贴图片**：剪贴板图像 → `data:image/...;base64` → `InputImage`
- [ ] 消息组装：文本 + 图片/附件 → `MessageContent.Parts`，而非纯字符串
- [ ] 输入栏显示**占位标记**，发送时替换为真实部件
- [ ] 粘贴路径/拖拽：按扩展名分流（图片→`InputImage`，文本→内联或 `read_file`）
- [ ] 类型与大小校验（上限进配置），超限给出可操作提示
- [ ] 与 `ParseTaskInput` 的 JSON 通路统一，避免两套语义
- [ ] 多模态条目渲染（占位 + 元信息；可选 kitty/iTerm2 图形协议）与持久化体积评估
- [ ] plan mode 下是否允许图片输入；`Ctrl+V` 入口与移除单个附件
- [ ] 测试：图片粘贴组装、占位渲染、超限提示、持久化往返

### P3.5 模型可以提问（`[Q]`）
> 现状：agent 只会自主跑到结束；**可复用审批通道**（`core.ApprovalRequest` + `Core.ResolveApproval` 已是「事件 + 等待应答」）。
- [ ] 新增 `ask_user` 工具并注册进 `internal/tool`（新分类 `ask`）
- [ ] 阻塞式问答：经通用用户提问通道发事件并等待应答（不另造一套）
- [ ] TUI / Desktop：提问条目渲染（需要**文本输入**，区别于审批的 Y/N）；支持多行回答
- [ ] 回答写入 history 作为 `function_call_output`，**任务继续**（不结束回合）
- [ ] 取消：Esc / Ctrl+C 中断提问 → 走既有 checkpoint 暂停与继续
- [ ] 超时与默认答案（可选）；无前端（管道/headless）时降级
- [ ] plan mode 下**允许**提问
- [ ] 权限归类：`ask_user` 自身不触发审批，也不能落进免批准缺口
- [ ] subagent/team 成员内不允许提问（无用户通道）→ 明确失败并回传，或上抛父代理
- [ ] 与流式、折叠、回到底部、会话持久化共存
- [ ] 测试：问答回合、取消、暂停/恢复、history 往返、headless 降级

## P4 新子系统与收尾

- [ ] `[F]` Subagent / Team **剩余项**（主体已落地于 `core/team.go`）
  - [ ] 独立上下文与父子隔离的**回归测试**补齐
  - [ ] 深度上限、禁止自我递归
  - [ ] 用量归属的展示口径（team 成员已带 `agentID/profile/provider/modelID`）
  - [ ] 前端嵌套展示成员活动
- [ ] `[D]` MCP：`config.json` 增 `mcpServers`
- [ ] `[D]` JSON-RPC 2.0 客户端（initialize / tools/list / tools/call），stdio + streamable HTTP
- [ ] `[D]` MCP 工具注册到 `tool.Registry`（`mcp` 分类，默认需批准）
- [ ] `[D]` 连接/健康检查/进程回收；`/mcp` 列出、启停、重连
- [ ] `/review`（语义待定）
- [ ] 更新命令清单 / footer 文案；日志加 `model` / `effort`
- [ ] 决定是否同步非 TUI 命令集（`cmd/core_cli.go`）

## P6 A2A（agent ↔ agent 跨网络）—— 建议延后

> 与 MCP / team 不同轴：MCP 是「接能力」，team 是「进程内多代理」，A2A 是「跨网络代理互操作」
> （Agent Card 发现、JSON-RPC `message/send`/`message/stream`、task 生命周期、artifacts、SSE、push、鉴权）。
> 现状：仓库**没有任何 HTTP server**（仅测试用 `httptest`），服务端侧等于从零开始。

- [ ] **先做客户端侧**：把远程 A2A agent 接入 `core/team.go` 的成员抽象作为一种 transport（复用并发、取消传播、用量归属、可恢复语义）
- [ ] 前置：MCP 完成（共用 JSON-RPC + SSE 基础设施）
- [ ] `OPEN` **服务端侧**（把 lune 暴露为 A2A agent）：需 HTTP server + 鉴权 + 任务存储 + **workspace 隔离** + 权限档位映射 + 限流；否则等于把 `command`（可越出工作区）与文件工具开到网络
- [ ] 明确消费方后再启动（谁调用 lune？lune 调用谁？）

---

## 待定（需先拍板，否则施工中会返工）

- [ ] **Wails 版本**：v2 稳定线 vs v3（alpha）；前端框架/语言（React / Vue / Svelte / 原生）
- [ ] Desktop 是否复用 `internal/tui` 的渲染与主题逻辑，还是完全另写；偏好/主题是否下沉为共享包
- [ ] Desktop 的密钥存储方案（系统钥匙串）与 `config.json` 兼容策略
- [ ] TUI 与 Desktop 同时打开同一工作区时的 `sessions.db` / `checkpoints.db` 并发与锁策略
- [ ] 提问形式：工具 `ask_user`（任务内暂停等答）vs 结束回合由用户下一条消息回答
- [ ] 是否支持选项（单选/多选）与默认值；是否允许多问合并
- [ ] headless / 管道模式下提问的行为（直接失败 / 自动取默认 / 跳过）
- [ ] 提问是否计入 token 预算与工具轮数上限
- [ ] A2A：规范版本、是否需要 push notifications、Agent Card 鉴权方案、远程 agent 权限是否继承本地档位
- [ ] 多模态范围：只做图片，还是含音频/PDF/任意文件
- [ ] 剪贴板图像获取方式：`atotto/clipboard` 仅文本 vs 各平台命令（`pngpaste` / `xclip` / `wl-paste` / PowerShell）
- [ ] 终端内是否做图形显示（kitty / iTerm2 图形协议）还是仅占位 + 元信息
- [ ] 附件大小上限与是否压缩后上传；是否计入 `sessions.db` 体积与上下文预算
- [ ] `Instructions` 合成顺序与优先级：Goal / 称呼 / persona / plan mode 谁先谁后；**persona 不得覆盖或弱化**系统约束
- [ ] persona 存哪一层：会话还是全局配置；是否支持多套方案切换
- [ ] persona 是否也传给 team 成员与压缩摘要请求（默认只作用于主 agent）
- [ ] persona 对前端的显示名/头像影响（已有 `internal/tui/avatar.go`）
- [ ] 称呼粒度：仅称呼还是同时影响语气；是否只作用于主 agent
- [ ] 称呼命令名与形态：`/user` vs `/callme`；空值是否等于恢复默认
- [ ] TUI 效果图用哪个 image gen 工具/模型；效果图存放位置（如 `docs/design/`）；确认流程
- [ ] 自动压缩方案（任务内 vs 仅任务间）
- [ ] `/context` 窗口按模型还是全局
- [ ] `/model` 清单来源（provider catalog 已有，待定是否用户可编辑）
- [ ] `/mcp` 传输范围与鉴权
- [ ] 是否允许新增依赖（`go.mod`）
- [ ] 各家模型名 / effort 取值 / 窗口默认值（已接多 provider，需逐家确认）
- [ ] team 成员形态与上限、是否允许嵌套/并发
- [ ] 成员是否继承父的 `Goal` / plan mode（默认不继承）
- [ ] 工具结果瘦身默认策略（纯状态 / 头尾摘录 / 仅保留最近一轮）
- [ ] 工具结果瘦身保留窗口（仅当前 user turn / 最近 N 轮）
- [ ] 瘦身决策权分层（客户端兜底 + 模型 pin/笔记）
- [ ] scratchpad / 笔记的存储位置（会话内 vs 工作区文件）
- [ ] footer 窄终端取舍（上下文占比 vs in/out）
- [ ] 流式默认开关；reasoning delta 是否展示
- [ ] 用户标记形态：`▸` / `you` / `@you`（择一）；是否给它上淡色
- [ ] 用户块宽上限与窄终端退化阈值
- [ ] 折叠：默认折叠阈值与折叠后保留行数；哪些角色可折叠
- [ ] 回到底部的按键绑定与是否显示底部提示
- [ ] 是否需要保留/拆分本文件（概览 + `docs/` 分轨）
