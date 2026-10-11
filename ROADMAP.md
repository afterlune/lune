# Lune 路线图（完整待办清单）

> 排序原则：**地基 → 公共底座 → 功能 → 新子系统**。
> 同一文件/同一接口的改动尽量集中在同一阶段，使前面的施工对后面的影响最小。
> 标签：`[A]` 会话设置 `[B]` 历史 `[C]` 会话管理 `[E]` 上下文预算/用量 `[G]` 工具结果瘦身 `[H]` 流式 `[D]` MCP `[F]` Subagent `[M]` 多模态输入 `[Q]` 提问 `[X]` 桌面端
>
> 原版 159 条 checkbox（157 条未完成、2 条已完成）全部保留；清单另含 3 条补充项，当前共 162 条（97 条已完成、65 条仍待跟踪）。每项按当前代码状态核对；部分实现、暂缓和待决策仍属于待办。
> [x] 表示已完成；[ ] 表示仍需跟踪。注释会区分部分实现、监控、暂缓和待决策。

---

## 技术选型（已定）

- [x] **TUI：Bubble Tea** —— `charm.land/bubbletea/v2`（v2 线），配 `charm.land/bubbles/v2` + `charm.land/lipgloss/v2`；**已在用**
- [x] **Desktop：Wails** —— **待引入**（当前 go.mod 无 Wails 依赖）；勾选表示技术选型已定，不代表实现完成；见 P5。
- 约定：两种前端**只依赖 `core` 包**，不重复实现 agent 逻辑。
  `core.Core` 已暴露稳定接口面（见 `internal/tui/interfaces.go` 的 `Client`：`RunTask`/`ResumeTask`/`Compact`/
  `CreateSession`/`ListSessions`/`SetPermissionMode`/`ResolveApproval`/`SetEventHandler` 等），Desktop 端应复用同一接口。
- 共享层设计：偏好/主题（`internal/tui/preferences.go`、`theme.go`）考虑下沉为可共用包，避免 TUI 与 Desktop 双份实现。

## 本轮核对结果

- [x] 交互式 CLI 逐次审批：TTY 可用 Y/N 批准或拒绝一次，Esc 中断；headless 失败关闭。CLI/Core 测试通过。
- I-1：当前版本一次只读 Plan 检查的 7 次模型请求均保持 files 类别与 read_file 可见性；该场景未复现，问题仍未确认，按 ISSUES.md 继续监控。


## P0 稳定与地基（逐项状态已按当前源码复核）

- [x] **【首位·门禁】TUI 设计先行：用 image gen 生成效果图，用户确认后再实施** — 用户指出旧版结构与 Codex 界面相似；新稿经确认后已落实会话工作区布局。
  - [x] 先用 image gen 出效果图（转写左右分栏、用户短标记、长输入折叠态、footer / 详情面板等） — 已生成三种新结构方向及状态稿，见 `docs/design/tui/redesign-review/`；选定稿为会话工作区。
  - [x] **交用户确认**；确认前**不得**改动 `internal/tui/` 的渲染实现 — 新稿已由用户选择；本轮实现以会话工作区为准。
  - [x] 覆盖场景：空态 / 对话态 / 长输入折叠 / 工具活动 / 详情面板 / 窄终端退化 / 模型提问 — 状态稿已覆盖；模型提问仅是未来功能示意，不代表功能已实现。
  - [x] 确认后再落实 P3.0（渲染改版） — 会话工作区渲染已完成并更新确定性截图；P3.0 其余视觉细节仍按对应子项跟踪。
- [ ] 修 I-1：类别选择/工具可见性跨轮丢失 + 提示可操作化（已复现 3 次，含写入反复失败） — 监控中：当前只读 Plan 运行 7 次请求未复现；没有证据时不做猜测性修复。
- [x] 统一工具元数据：注册时声明 `category + permission class + read-only`，替换硬编码（原 `tool/permissions.go`、`agentgraph`、`registry.NewDefaultRegistry`）
- [x] 修 `workspace` 模式对未知工具免批准的安全缺口（同一改造）
- [x] 命令取消会结束 shell 子进程：Unix 通过独立 process group 终止整组，Windows 通过 `taskkill /T` 结束进程树，并设置 `WaitDelay` 作为管道关闭兜底；Linux/Windows 回归测试验证后台子进程不会在取消后继续完成。
- [x] 内置文件工具在 Registry 生命周期内持有 workspace-scoped `os.Root` 执行读取、写入、编辑、列举、glob 与 grep；路径替换后仍锁定原目录，路径检查后的 symlink 越界会被拒绝，递归 grep 在打开前跳过特殊文件；Core 正常关闭及启动失败均释放句柄。
- [x] 结构化配置加载+校验（`models` / `mcpServers` / `contextWindow` / `autoCompact` / 附件大小上限） — providers/models、context/autoCompact、Responses `serverCompaction`、附件限制及 MCP stdio/Streamable HTTP/OAuth CIMD URL schema 均有配置校验。
- [x] 会话 `data` 加版本号 + 迁移策略（`migrateLegacySession` 已有雏形，需正式版本化） — Core 会话 payload 使用独立 `data_version=3`；版本缺失的旧数据经 v0→v1→v2→v3 迁移并以比较后原子替换写回，更新会话数据不改变更新时间；拒绝负数或高于当前版本的数据。
- [x] 定 `sessions.db` 与 `checkpoints.db` 的级联删除关系
- [x] 定 token 计数口径（服务器 `usage.InputTokens` vs 本地启发式）
- [x] 新增 `internal/session/store_test.go`
- [x] 新增 `/help`（行式 CLI 与 TUI 均列出各自支持的命令）
- [x] 日志轮转 `.luneagent/agent.log`

## P1 公共底座（一次改好，后续都直接复用）

- [x] `[A][E]` `TaskOptions` / `State` 增加 `Model` / `ReasoningEffort` / `Goal` / `ContextWindow` / `AutoCompact` 等字段，并在 `Start` 写入 `State`（`Recover` 不传 `TaskOptions`，必须持久化） — Core TaskRequest/agentgraph TaskOptions 支持当前 profile 同 provider 下的 API model 覆盖；覆盖值保存在 checkpoint，恢复后继续使用。ModelProfile/provider/model、reasoning、Goal 与上下文设置均写入任务状态。
- [ ] `[A]` **「模型对用户的称呼」设置**：与 `Goal` / plan mode 一起组合进 `Instructions`
- [ ] `[A]` **「模型人物背景设定（persona）」**：同一管线注入；**合成顺序**上只塑造风格，**不得弱化** plan mode / 权限等系统约束
- [ ] `[A]` **`Instructions` 合成器**：把 `Goal` + `称呼` + `persona` + plan mode 常量统一拼装
- [ ] `[A][E]` `requestModel()` 应用上述设置（model 回退默认、effort → `Reasoning`、instructions 走合成器） — 部分实现：model fallback 与任务级 model override、reasoning effort、Goal 与 Plan 指令已接入；上下文窗口现在驱动自动压缩；称呼/persona 顺序待决策。
- [ ] `[A][E]` 设置持久化并恢复（含称呼、persona）——现在由 `core.Session` 承载 — 部分实现：现有会话/检查点保存 model、effort、Goal 和上下文字段；称呼与 persona 不存在。
- [ ] `[Q]` **交互式回调通道抽象**：把审批通道（`Core.ResolveApproval` + `core.ApprovalRequest`）泛化成通用用户提问通道，供 `[Q]` 复用 — Core 已有 request/resolve 事件通道和 headless/subagent fail-fast；TUI 表单/URL 交互待设计确认。

## P2 会话与历史（相关能力已落地）

- [x] `[C]` `session.Store.Delete(ctx, id, workspace)`
- [x] `[C]` `session.Store.SetTitle(ctx, id, workspace, title)`
- [x] `[C]` `session.Store.SetArchived(ctx, id, workspace, bool)`
- [x] `[C]` `session.Store.List` 增加 `includeArchived`（JSON1 过滤，免 schema 迁移）
- [x] `[C]` `core.Session` / 持久化加 `Title` / `Archived`（upsert 不覆盖）
- [x] `[C]` 会话面板键位：`d` 删除（Y/N 确认）、`r` 重命名、`a` 归档、`Tab` 显示已归档；渲染优先 `title`
- [x] `[C]` 删除当前会话时自动切到新会话
- [x] `[B]` `/fork`：复制历史到新 session 并切换
- [x] `[B]` `/rewind`：按 user turn 回退历史（禁止跨 `function_call`/`output` 切分）；paused task 时禁用并提示

## P3 渲染基调 → 命令 → 上下文效率 → 输入 → 提问

> 本区完整保留转写、上下文、流式、附件与提问任务。会话工作区方向已获确认并落地；剩余设计细节按已选方向继续核验。

### P3.0 输出渲染改版：**会话工作区时间线，正文保持常规字重**（先行；当前“杂乱无章”的根因）
> 前置：须先完成 P0 首位的「效果图 → 用户确认」门禁。
> 基调：**少样式、多结构**。清晰来自对齐/缩进/间距，而非整段上色或整段加粗。
> User / Lune / Tool 等事件以短角色标签进入同一条时间线；颜色只用于角色和状态标签，不铺满正文。整体不刺眼。

- [x] **左右分栏**：旧布局已实现用户右对齐；会话工作区已改为单列时间线，User / Lune / Tool 以角色栏左对齐
- [x] **正文常规字重**：旧版短标记方案已被角色栏替代；现代时间线的角色标签用主题色、正文不加粗
- [x] 区分事件优先靠**角色栏 + 留白**；主题色只用于短角色/状态标签，不铺满正文 — User/Lune/Tool/Note/Approval/Error/Queued 标签保留主题状态色；所有对应正文为默认前景色并有逐角色回归测试。
- [x] **不辣眼睛**：避免高饱和/高亮/纯色块；不叠加边框与背景色 — 深浅/Anime 三主题语义色对比度均 ≥4.5:1、HSL 饱和度 ≤0.68、单通道亮度 ≤242/255；消息正文不铺色块，选区使用清晰对比色；确定性截图覆盖会话栏与抽屉分隔线。
- [x] 其余加粗一律移除：角色标签与正文不加粗 — TUI 运行视图统一使用常规字重；选择状态用箭头和主题色表示，提示用语义色表示。
- [x] 层级靠结构：缩进 + 间距 + 单一前缀标记区分工具/通知/错误
- [x] Markdown 降级：标题→纯文本或极简前缀；代码块保持原样缩进；**不**引入行内加粗/斜体/彩色标题/表格重排
- [x] 一致性：assistant 与 user 走同一处理路径
- [x] 工具条目去重：内联状态与活动面板重复 → 择一或明确分工
- [x] 宽度感知：会话工作区角色栏与正文宽度随终端宽度重算，不再右对齐用户块
- [x] 间距规范化：块间空行统一
- [x] 窄终端退化：会话栏收起为顶部会话切换器，事件角色栏与正文继续左对齐
- [x] 渲染性能：避免每帧全量重建 + `SetContent`
- [x] 测试：确定性截图覆盖角色时间线、长文本、抽屉滚动和窄屏布局；交互测试覆盖会话切换与布局稳定性 — 快照源文件路径通过 runtime source 定位，交叉编译时从工作目录查找仓库根；Anime fixture 固定 `NO_COLOR=1`，不受宿主环境影响。

- [x] **长条目可点击折叠/展开**
  - [x] 条目增加 `collapsed`，并持久化
  - [x] 长用户输入**默认折叠**为 N 行；点击首行/标记切换展开 — 超过 6 行或 600 字符时自动折叠，显示 3 行预览；点击首行/标记可展开。
  - [x] 折叠状态提示**只对超阈值长条目显示**（现代时间线使用 `Show more` / `Show less`）
  - [x] **点击命中测试**：建立「条目 → 渲染行区间」索引 + viewport 偏移换算
- [x] 点击落在活动面板/选择器时不误触；补键盘等价操作 — 点击命中已隔离活动面板与选择器；`/fold` 列出长用户消息，`/fold <n>` 可切换任意条目，Alt+M 仍切换最近一条。

- [x] **会话可回到底部** — 回到底部按键、滚动期间不跟随及恢复跟随均有回归测试。
  - [x] 新增按键跳到末尾（候选：`End` / `G` / `Ctrl+End`）
  - [x] 滚离底部时**不**自动跟随；回到底部后恢复自动跟随
  - [x] 可选「↓ 回到底部」提示；与折叠、流式增量共存不得强制拉回底部

### P3.1 命令
- [x] `[A]` `/goal`（查看/设置/清除）、`/effort`（选择菜单）、`/model`（菜单 + `<name>`） — TUI 已实现并持久化会话级 Goal；模型请求会带入 Goal，Plan mode 指令仍限制为只读工具。非 TUI 命令集同步仍由 P4 决策项跟踪。
- [ ] `[A]` **`/user`（或 `/callme`）**：查看 / 设置 / 清除「模型对用户的称呼」
- [ ] `[A]` **`/persona`**：查看 / 设置 / 清除「模型人物背景设定」（支持多行输入）
- [x] `[E]` `/context`（查看/设置窗口/预留/reset）、`/autocompact`（查看/on/off/threshold） — TUI 已实现查看、设置、重置、校验和会话持久化；窗口缺失时会提示无法自动触发。
- [x] `[E]` 配置项 `contextWindow` / `autoCompact` / `autoCompactThreshold` / `reserveOutputTokens`

### P3.2 上下文效率

> 配置与检查点保存上下文设置；Responses 端点按能力走服务端压缩，Anthropic Messages 和无该能力的 Responses 端点在本地先清理工具结果、再摘要；可移植对话转写与 provider 专用上下文分开保存，reasoning、opaque 和未知 provider 输出不会跨协议重放。
- [x] `[E]` 自动压缩触发（任务内生效；先瘦身、再摘要） — 新任务默认开启，阈值 80%；需配置 `contextWindow`。Responses provider 可显式启用 `serverCompaction`；Claude 本地路径先清旧工具输出再摘要。provider 切换后从完整转写重建上下文。
- [x] `[E]` footer 增加上下文占用比例 `ctx 12.3k/64k (19%)`，保持一行简要（按宽度裁剪） — 使用当前任务解析出的 contextWindow 与服务器 `usage.InputTokens`；窗口未知时显示 `unknown`，不推断 provider 默认值。
- [x] `[E]` 详情面板增加窗口/占比/缓存命中率、按 agent 分组统计（`core` 已有 per-agent usage 记录） — 请求行和会话累计按 agent 分组；窗口/占比、cache rate 均可见；缺失 server input 时单独显示文本估算；旧会话无法回溯的累计值明确标注。
- [x] `[E]` 口径统一：以服务器 `usage.InputTokens` 为准，缺失时估算 — 请求前从 instructions、文本历史和工具 schema 做粗略估算；服务器值优先，估算单独保存/展示、不计入 measured totals；图片内容和未知多模态项不参与。
- [ ] `[G]` 客户端兜底瘦身：接近预算时保留最近 N 轮原文 + 失败态，其余替换为状态占位/头尾摘录 — 自动压缩已按最旧优先清理 `function_call_output` 并摘要较早历史；固定头尾摘录、失败态保留和选择性 pin/drop/scratchpad 仍待实现。
- [x] `[G]` 只替换 `function_call_output` 的 `output` 内容，保持 `call_id` 配对 — 清理保留调用 ID 与状态，图片及未知多模态部件不改写。
- [ ] `[G]` 模型选择性表达：`pin`（关键 `call_id` 不瘦身）/ `drop_result` / scratchpad 笔记
- [x] `[G]` 双落点：历史组装处（`core/runtime.go` 与 `agentgraph`） — session 保留完整转写；checkpoint 与请求使用可按 provider/model 重建的独立上下文。
- [x] `[M]` 图片/附件不参与文本瘦身与 token 估算 — 文本估算排除图片和未知多模态项，本地自动压缩只清理文本工具输出并保留非文本部件。

### P3.3 流式（provider/Core 已落地；TUI 增量渲染部分落地）
> `core.StreamResponse` / `model.ModelStreamEvent` / `Core.onModelStream` 已实现。
- [ ] 增量渲染的**流式态**细节（首 token 延迟、reasoning delta 展示开关） — 部分实现：provider/Core 与 TUI 已接入 lead 文本增量，按 32ms 批量刷新，并在 Activity/Usage 中记录任务开始到首段可见文本的延迟；reasoning delta 显示策略仍待决策。
- [x] 未闭合 markdown / 半行处理 — 增量渲染保留未换行尾部，未闭合强调/代码定界符和开放代码块可见；CRLF/CR 归一化，测试覆盖跨增量分隔符。
- [x] 取消传播与中断恢复衔接的边界用例 — Core 端到端测试覆盖 delta 后取消/checkpoint 恢复和增量后 provider failure；TUI 测试验证 partial 保留并与恢复后的新回复分开显示。
- [x] 从 `response.completed` 取 usage 与现有 `usageTrackingRequester` 观测点对齐

### P3.4 输入栏支持任意模态粘贴（`[M]`）
> 状态：图片附件端到端流程已实现；任意文件、音频/PDF 与终端图形协议仍不在当前范围。
- [x] 输入栏可**直接粘贴图片**：剪贴板文件列表优先，其次剪贴板位图；转换为内联 `data:image/...;base64` 和 `InputImage`。
- [x] 消息组装：文本 + 图片/附件 → `MessageContent.Parts`，而非纯字符串 — TUI 标签与图片部件按草稿顺序组装。
- [x] 输入栏显示**占位标记**，发送时替换为真实部件 — 可编辑元信息标签；删除标签会移除关联图片，撤销/重做可恢复。
- [ ] 粘贴路径/拖拽：图片路径与终端文件拖放按扩展名分流到 `InputImage`；其它文件路径仍按普通文本处理，未自动读取。
- [x] 类型与大小校验（上限进配置），超限给出可操作提示 — 支持 PNG/JPEG/GIF/WebP；默认每条消息最多 5 张、单张 10 MiB、合计 20 MiB，项目正数配置可覆盖。远程 URL/FileID 保留现有校验语义。
- [x] 与 `ParseTaskInput` 的 JSON 通路统一，避免两套语义
- [x] 多模态条目渲染（占位 + 元信息；可选 kitty/iTerm2 图形协议）与持久化体积评估 — 当前采用可编辑标签，不用终端图形协议；图片字节随会话 SQLite 历史持久化，不保存临时路径。图片不进入本地文本 token 估算。
- [x] plan mode 下允许图片输入；支持 `Ctrl+V` 入口与移除单个附件 — Plan 的工具只读限制保持不变。
- [x] 测试：图片粘贴组装、占位渲染、超限提示、持久化往返 — 覆盖 TUI 草稿/队列/撤销与双主题 14 张 composer 状态截图，Core 测试覆盖 history 持久化。

### P3.5 模型可以提问（`[Q]`）
> 状态：原需求完整保留；通用 Core 交互通道已实现，`ask_user` 工具和 TUI 表单渲染尚未实现。
> 现状：agent 只会自主跑到结束；审批仍使用 `core.ApprovalRequest`，MCP elicitation 复用新增的事件 + 等待应答通道。
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

> 团队代理主流程与 MCP 后端已实现；本区保留团队展示/测试余项、MCP TUI 设计门槛及命令决策。

- [x] `[F]` Subagent / Team **剩余项**（主体已落地于 `core/team.go`；隔离测试、用量归属和成员活动展示均已补齐）
  - [x] 独立上下文与父子隔离的**回归测试**补齐 — Core 端到端测试验证 worker 只收到独立任务文本，不继承 parent history；parent 仍保留自身历史并接收 child 结果。
  - [x] 深度上限、禁止自我递归 — 当前 worker Agent 未注入 TeamExecutor，因此不能递归委派；没有额外深度配置。
  - [x] 用量归属的展示口径（team 成员已带 `agentID/profile/provider/modelID`） — Core 会话记录保留 agent/profile/provider/model；TUI Usage 明细行已展示归属并在恢复会话后保留。
  - [x] 前端嵌套展示成员活动 — `EventAgentStarted/Finished` 携带 `TeamRunID`；Activity/Usage 抽屉按 Team 分组显示成员状态与模型。
- [x] `[D]` MCP：`config.json` 增 `mcpServers` — stdio、Streamable HTTP 与 OAuth CIMD 配置校验已完成。
- [x] `[D]` 当前版 JSON-RPC 客户端（`2026-07-28`：tools/list、tools/call、resources/read；stdio + Streamable HTTP JSON/SSE）— 不发送已移除的 initialize 握手；MRTR、`x-mcp-header`、分页和当前版本请求元数据均已实现。
- [x] `[D]` MCP 工具注册到 `tool.Registry`（`mcp` 分类、Network 权限、readOnlyHint 仅影响 Plan 可见性）— 动态注册/移除及结果多模态内容、transcript-only audience 均复用现有工具与会话数据流。
- [ ] `[D]` 连接/健康检查/进程回收；`/mcp` 列出、启停、重连 — Core manager 已有工作区启用状态、启动恢复、OAuth/keyring、stdio 进程回收及未来调用前重启；TUI 命令、OAuth 状态及 elicitation 表单/URL 屏待确认 [MCP 设计稿](docs/design/tui/mcp-review.svg) 后实施并补多状态截图。
- [ ] `/review`（语义待定）
- [x] 更新命令清单 / footer 文案；日志加 `model` / `effort` — TUI 命令清单已列出 `/model`、`/effort`；旧式 footer 显示设置摘要，现代深浅主题 footer 按已确认设计保持紧凑；模型请求日志记录 model 与 reasoning_effort。CLI 命令集是否同步仍由下一项决策跟踪。
- [ ] 决定是否同步非 TUI 命令集（`cmd/core_cli.go`）

## P5 桌面端（Wails；暂缓，待桌面产品需求确认）

> 暂缓条件：全部桌面端工作等明确产品需求后再排期；下列任务逐项保留。
> 状态：全部暂缓；启动前先确认桌面端使用场景。Wails 选型已定，但实现尚未开始。

- [ ] 引入 Wails 并评估构建链影响（`go.mod`、`wails.json`、CI、交叉编译与打包）
- [ ] 基于 `core.Core` 的 `Client` 接口面接线，**不**重写 agent 逻辑
- [ ] 能力对齐 TUI：会话管理（含 `delete` / `rename` / `archive`）、工具审批、流式增量输出、用量与上下文占用展示
- [ ] 模型提问（`[Q]`）在桌面端的呈现（模态对话框 + 文本/选项输入）
- [ ] 打包与分发（macOS / Windows / Linux）与自动更新策略
- [ ] 密钥与配置：当前 `config.json` 明文 API key → 桌面端应改用系统钥匙串（Keychain / Credential Manager / Secret Service）
- [ ] 偏好与主题复用：与 TUI 共享同一份偏好/主题定义
- [ ] 与 TUI 的互操作：同一工作区、同一 `sessions.db` / `checkpoints.db` 并发访问的锁与一致性
- [ ] 测试：核心逻辑复用已有 `core` 测试；桌面端补接口层与关键流程测试

## P6 A2A（agent ↔ agent 跨网络；暂缓，待明确消费方）
> 状态：暂缓；跨网络代理互操作尚无已确认消费方。

> 与 MCP / team 不同轴：MCP 是「接能力」，team 是「进程内多代理」，A2A 是「跨网络代理互操作」
> （Agent Card 发现、JSON-RPC `message/send`/`message/stream`、task 生命周期、artifacts、SSE、push、鉴权）。
> 现状：仓库**没有任何 HTTP server**（仅测试用 `httptest`），服务端侧等于从零开始。

- [ ] **先做客户端侧**：把远程 A2A agent 接入 `core/team.go` 的成员抽象作为一种 transport（复用并发、取消传播、用量归属、可恢复语义）
- [ ] 前置：MCP 完成（共用 JSON-RPC + SSE 基础设施）
- [ ] `OPEN` **服务端侧**（把 lune 暴露为 A2A agent）：需 HTTP server + 鉴权 + 任务存储 + **workspace 隔离** + 权限档位映射 + 限流；否则等于把 `command`（可越出工作区）与文件工具开到网络
- [ ] 明确消费方后再启动（谁调用 lune？lune 调用谁？）

---

## 待定与暂缓决策（原问题全部保留）

- [ ] **Wails 版本**：v2 稳定线 vs v3（alpha）；前端框架/语言（React / Vue / Svelte / 原生）
- [ ] Desktop 是否复用 `internal/tui` 的渲染与主题逻辑，还是完全另写；偏好/主题是否下沉为共享包
- [ ] Desktop 的密钥存储方案（系统钥匙串）与 `config.json` 兼容策略 — 暂缓：仅在桌面端或多人使用需求明确后启动。
- [ ] TUI 与 Desktop 同时打开同一工作区时的 `sessions.db` / `checkpoints.db` 并发与锁策略
- [ ] 提问形式：工具 `ask_user`（任务内暂停等答）vs 结束回合由用户下一条消息回答
- [ ] 是否支持选项（单选/多选）与默认值；是否允许多问合并
- [ ] headless / 管道模式下提问的行为（直接失败 / 自动取默认 / 跳过）
- [ ] 提问是否计入 token 预算与工具轮数上限
- [ ] A2A：规范版本、是否需要 push notifications、Agent Card 鉴权方案、远程 agent 权限是否继承本地档位
- [ ] 多模态范围：只做图片，还是含音频/PDF/任意文件
- [x] 剪贴板图像获取方式：采用 `golang.design/x/clipboard` 的跨平台文件列表/位图读取；`atotto/clipboard` 继续提供文本粘贴回退。
- [x] 终端内是否做图形显示（kitty / iTerm2 图形协议）还是仅占位 + 元信息 — 采用占位标签与元信息，不传输终端图形协议。
- [x] 附件大小上限与是否压缩后上传；是否计入 `sessions.db` 体积与上下文预算 — 默认 5 张/条、10 MiB/张、20 MiB/条；不压缩，内联数据随 SQLite 会话历史持久化，图片不计入本地文本 token 估算。
- [ ] `Instructions` 合成顺序与优先级：Goal / 称呼 / persona / plan mode 谁先谁后；**persona 不得覆盖或弱化**系统约束 — 待决策：Goal/persona 管线未接通，须保持系统权限与 Plan 约束优先。
- [ ] persona 存哪一层：会话还是全局配置；是否支持多套方案切换
- [ ] persona 是否也传给 team 成员与压缩摘要请求（默认只作用于主 agent）
- [ ] persona 对前端的显示名/头像影响（已有 `internal/tui/avatar.go`）
- [ ] 称呼粒度：仅称呼还是同时影响语气；是否只作用于主 agent
- [ ] 称呼命令名与形态：`/user` vs `/callme`；空值是否等于恢复默认
- [ ] TUI 效果图用哪个 image gen 工具/模型；效果图存放位置（如 `docs/design/`）；确认流程
- [x] 自动压缩方案（任务内 vs 仅任务间） — 任务内生效；Responses 可使用服务端 `context_management`，Anthropic Messages 使用客户端清理旧工具结果并摘要。
- [x] `/context` 窗口按模型还是全局 — 现状已定：项目配置为默认值，会话可覆盖；不是按模型 profile 设置。
- [ ] `/model` 清单来源（provider catalog 已有，待定是否用户可编辑） — 部分实现：目前使用 ProviderCatalog；是否允许用户编辑尚未决定。
- [ ] `/mcp` 传输范围与鉴权
- [x] 是否允许新增依赖（`go.mod`） — 图片剪贴板读取与 WebP 解码采用 `golang.design/x/clipboard`、`golang.org/x/image`。
- [ ] 各家模型名 / effort 取值 / 窗口默认值（已接多 provider，需逐家确认） — 部分实现：model profile 与 effort 配置已接入；各 provider context window 默认值未定义。
- [ ] team 成员形态与上限、是否允许嵌套/并发
- [x] 成员是否继承父的 `Goal` / plan mode（默认不继承） — 现状已定：PlanMode 与 reasoning effort 继承；Goal/context 设置不继承，已有测试验证。
- [x] 工具结果瘦身默认策略（纯状态 / 头尾摘录 / 仅保留最近一轮） — 达到预算后从最旧工具结果起替换为状态占位，再摘要历史。
- [x] 工具结果瘦身保留窗口（仅当前 user turn / 最近 N 轮） — 不设固定轮数；保留当前完整 user turn，旧工具结果按需清理。
- [ ] 瘦身决策权分层（客户端兜底 + 模型 pin/笔记）
- [ ] scratchpad / 笔记的存储位置（会话内 vs 工作区文件）
- [ ] footer 窄终端取舍（上下文占比 vs in/out）
- [ ] 流式默认开关；reasoning delta 是否展示
- [x] 用户标记形态：`▸` / `you` / `@you`（择一）；是否给它上淡色 — 当前实现为 ▸ YOU。
- [x] 用户块宽上限与窄终端退化阈值 — 当前实现为宽终端约 75% 宽度，96 列以下不右对齐。
- [x] 折叠：默认折叠阈值与折叠后保留行数；哪些角色可折叠 — 当前实现只折叠长用户消息；阈值 6 行或 600 字符，预览 3 行。
- [x] 回到底部的按键绑定与是否显示底部提示 — 当前快捷键为 alt+end，滚离底部时保留位置并显示回到底部提示。
- [x] 是否需要保留/拆分本文件（概览 + `docs/` 分轨） — 当前决定：保留单一完整 ROADMAP，不拆分；所有原条目继续留在此文件。
