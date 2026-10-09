# 待做 / TODO（按优先级排序）

> 排序原则：**地基 → 公共底座 → 功能 → 新子系统**。
> 同一文件/同一接口的改动尽量集中在同一阶段，使前面的施工对后面的影响最小。
> 标签：`[I]` 分包 `[A]` 会话设置 `[B]` 历史 `[C]` 会话管理 `[E]` 上下文预算/用量 `[G]` 工具结果瘦身 `[H]` 流式 `[D]` MCP `[F]` Subagent `[M]` 多模态输入

---

## P0 稳定与地基（先行；不解决则后续每步都踩坑或返工）

- [ ] **【首位·门禁】TUI 设计先行：用 image gen 生成效果图，用户确认后再实施**
  - [ ] 先用 image gen 出效果图（转写左右分栏、用户短标记、长输入折叠态、footer / 详情面板等）
  - [ ] **交用户确认**；确认前**不得**改动 `cmd/tui.go` 的渲染实现
  - [ ] 覆盖场景：空态 / 对话态 / 长输入折叠 / 工具活动 / 详情面板（Ctrl+B）/ 窄终端退化
  - [ ] 确认后再落实 P3.0（渲染改版）
- [ ] 修 I-1：类别选择/工具可见性跨轮丢失 + 提示可操作化（已复现 3 次，含写入反复失败）
- [ ] `[I]` 重新规划分包（**越早做，后面新增代码越省**）
  - [ ] 明确 `internal/loop/` 用途或删除（当前为空目录）
  - [ ] 拆 `cmd/tui.go`（≈2.1k 行）：model/update/view、pickers、usage、render、vim
  - [ ] 拆 `cmd/graph_runtime.go`：responder / compaction / usage-observer / session-history
  - [ ] 非入口逻辑迁出 `package main`，`cmd/` 只留装配与入口
  - [ ] `internal/model` 细分：`responses` / `client`（含流式）/ `net`
  - [ ] `internal/tool` 细分：read / write / exec / web / explore
  - [ ] 保持防环约定（`model` 内定义 `ToolExecutor`）
  - [ ] `cmd/*_test.go` 随之迁移，`go test ./...` 全绿
- [ ] 统一工具元数据：注册时声明 `category + permission class + read-only`，替换三处硬编码（`permissions.go` / `agentgraph.isReadOnlyTool` / `registry.NewDefaultRegistry`）
- [ ] 修 `workspace` 模式对未知工具免批准的安全缺口（同一改造）
- [ ] 结构化配置加载+校验（`models` / `mcpServers` / `contextWindow` / `autoCompact` / 附件大小上限）
- [ ] 会话 `data` 加版本号 + 迁移策略
- [ ] 定 `sessions.db` 与 `checkpoints.db` 的级联删除关系
- [ ] 定 token 计数口径（服务器 `usage.InputTokens` vs 本地启发式）
- [ ] 新增 `internal/session/store_test.go`
- [ ] 新增 `/help`
- [ ] 日志轮转 `.luneagent/agent.log`

## P1 公共底座（一次改好，P2–P4 都直接复用）

- [ ] `[A][E]` `TaskOptions` / `State` 增加 `Model` / `ReasoningEffort` / `Goal` / `ContextWindow` / `AutoCompact` 等字段，并在 `Start` 写入 `State`（`Recover` 不传 `TaskOptions`，必须持久化）
- [ ] `[A]` **「模型对用户的称呼」设置**：写入 `State` / `TaskOptions`，与 `Goal` / plan mode 一起组合进 `Instructions`
- [ ] `[A]` **「模型人物背景设定（persona）」**：同上管线注入；**合成顺序**上 persona 只塑造风格，**不得弱化** plan mode / 权限等系统约束
- [ ] `[A]` **`Instructions` 合成器**：把 `Goal` + `称呼` + `persona` + plan mode 常量统一拼装（避免各处 if 散落）
- [ ] `[A][E]` `requestModel()` 应用：model 回退默认、effort → `Reasoning`、instructions 走上述合成器
- [ ] `[A][E]` `graphResponder` 加字段与 setter（`historyMu`/atomic，避免与 `Respond` 竞争）
- [ ] `[A][E]` `savedTUISession` 持久化并恢复上述设置（含称呼、persona）
- [ ] `[A]` `model.Client` 加 `SetModel`（非 TUI 路径）
- [ ] `[H]` 请求接口分层预留（`Requester` 扩展或新增流式接口，不破坏既有 `CreateResponse`）

## P2 会话与历史（集中在 `session.Store` + TUI 会话面板/选择器）

- [ ] `[C]` `session.Store.Delete(ctx, id, workspace)`
- [ ] `[C]` `session.Store.SetTitle(ctx, id, workspace, title)`
- [ ] `[C]` `session.Store.SetArchived(ctx, id, workspace, bool)`
- [ ] `[C]` `session.Store.List` 增加 `includeArchived`（JSON1 过滤，免 schema 迁移）
- [ ] `[C]` `Snapshot` / `savedTUISession` 加 `Title` / `Archived`（upsert 不覆盖）
- [ ] `[C]` 会话面板键位：`d` 删除（Y/N 确认）、`r` 重命名、`a` 归档、`Tab` 显示已归档；渲染优先 `title`
- [ ] `[C]` 删除当前会话时自动切到新会话
- [ ] `[B]` `/fork`：复制历史到新 session 并切换（复用面板/`SetSession`）
- [ ] `[B]` `/rewind`：按 user turn 回退历史（禁止跨 `function_call`/`output` 切分）；paused task 时禁用并提示

## P3 渲染基调 → 命令 → 上下文效率 → 输入（先定渲染，后续展示都基于它）

### P3.0 输出渲染改版：**只标记用户，正文一律常规字重**（先行；当前“杂乱无章”的根因）
> 前置：须先完成 P0 首位的「效果图 → 用户确认」门禁。
> 基调：**少样式、多结构**。清晰来自对齐/缩进/间距，而非整段上色或整段加粗。
> **只有用户消息做区分**；agent / 工具 / 通知保持默认样式。整体不刺眼。

- [ ] **左右分栏**：**用户消息右对齐**；agent / 工具 / 通知左对齐（默认样式，不加色）
- [ ] **加粗只用于短标记，不用于正文**：用户块用一个加粗的短标记（如 `▸` / `you`）标识，**用户输入正文保持常规字重**——长输入尤其不能整段加粗
- [ ] 区分用户优先靠**对齐 + 短标记**；颜色为可选、且仅用于该短标记（低饱和），不铺满正文
- [ ] **不辣眼睛**：避免高饱和/高亮/纯色块；不叠加边框与背景色
- [ ] 其余加粗一律移除：只保留“用户标记”这一处加粗（`entryBadge` 等文案中的 `Bold(true)` 去掉）
- [ ] 层级靠结构：缩进 + 间距 + 单一前缀标记（如 `·`、`↳`）区分工具/通知/错误，替代“badge 独占一行 + 原文直出”
- [ ] Markdown 降级：标题→纯文本或极简前缀；代码块保持原样缩进；**不**引入行内加粗/斜体/彩色标题/表格重排
- [ ] 一致性：assistant 与 user 走同一处理路径（现 assistant 走 markdown、user 直出原文）
- [ ] 工具条目去重：内联 `↳ name · status` 与活动面板重复 → 择一或明确分工
- [ ] 宽度感知：缩进/换行/块宽随宽度计算，不单靠 viewport `SoftWrap`；用户块右对齐、块宽上限约 70–80%
- [ ] 间距规范化：块间空行统一，避免密集/稀疏混杂
- [ ] 窄终端退化：宽度不足时用户块退回左对齐（仍保留短标记）
- [ ] 渲染性能：避免每次 `refreshPanels()` 全量重建 + `SetContent`（条目 500 上限仍 O(n)/帧）
- [ ] 测试：转写渲染快照（含用户右对齐/短标记、长输入不加粗、窄终端退化）

- [ ] **长条目可点击折叠/展开**
  - [ ] `tuiEntry` 增加 `collapsed`，并持久化到 `savedTUIEntry`
  - [ ] 长用户输入**默认折叠**为 N 行（与“长输入不好看”同一目标）；点击首行/标记切换展开
  - [ ] 折叠指示（`▸`/`▾`）**只对超过阈值的长条目显示**，避免满屏符号
  - [ ] **点击命中测试**：`Update()` 的 `case tea.MouseClickMsg: return m, nil` 目前是空实现；需在渲染时记录「条目 → 渲染行区间」索引，再用 viewport `YOffset()` 把点击 Y 换算到条目
  - [ ] 点击落在活动面板/选择器（`detailsOpen` / pickers）时不误触
  - [ ] 提供键盘等价操作（如 `Tab` / `Space` 折叠当前条目）

- [ ] **会话可回到底部**
  - [ ] 新增按键跳到末尾（候选：`End` / `G` / `Ctrl+End`），当前无此绑定
  - [ ] 滚离底部时**不**自动跟随；回到底部后恢复自动跟随（复用现有 `AtBottom()` / `GotoBottom()`）
  - [ ] 状态提示：滚离底部且有新内容时显示「↓ 回到底部」（可选）
  - [ ] 与折叠、流式增量渲染共存：增量追加时不得强制拉回底部

### P3.1 命令
- [ ] `[A]` `/goal`（查看/设置/清除）、`/effort`（选择菜单）、`/model`（菜单 + `<name>`）
- [ ] `[A]` **`/user`（或 `/callme`）**：查看 / 设置 / 清除「模型对用户的称呼」
- [ ] `[A]` **`/persona`**：查看 / 设置 / 清除「模型人物背景设定」（支持多行输入）
- [ ] `[E]` `/context`（查看/设置窗口/预留/reset）、`/autocompact`（查看/on/off/threshold）
- [ ] `[E]` 配置项 `contextWindow` / `autoCompact` / `autoCompactThreshold` / `reserveOutputTokens`

### P3.2 上下文效率
- [ ] `[E]` 自动压缩触发（方案 A：图内，任务内生效；先瘦身、再摘要）
- [ ] `[E]` footer 增加上下文占用比例 `ctx 12.3k/64k (19%)`，保持一行简要（按宽度裁剪）
- [ ] `[E]` 详情面板（Ctrl+B）增加窗口/占比/缓存命中率、按模型分组统计；用量表补窗口列
- [ ] `[E]` 口径统一：以服务器 `usage.InputTokens` 为准，缺失时估算
- [ ] `[G]` 客户端兜底瘦身：接近预算时保留最近 N 轮原文 + 失败态，其余替换为状态占位/头尾摘录
- [ ] `[G]` 只替换 `function_call_output` 的 `output` 内容，不删除该项，保持 `call_id` 配对
- [ ] `[G]` 模型选择性表达：`pin`（关键 `call_id` 不瘦身）/ `drop_result(call_id)` / scratchpad 笔记
- [ ] `[G]` 双落点：`internal/agentgraph` 与 `internal/model` 历史路径
- [ ] `[M]` 图片/附件不参与文本瘦身与 token 估算（需单独计量与保留策略）

### P3.3 流式（在 P3.0 渲染结构之上做增量渲染）
- [ ] `[H]` 流式客户端（SSE 解析，复用既有 `ResponseStreamEvent`）
- [ ] `[H]` 图请求节点流式消费（delta 累积为完整 `Response`）
- [ ] `[H]` TUI 增量渲染（text/reasoning/args delta）+ 未闭合内容处理（**保持 P3.0 基调**）
- [ ] `[H]` 取消传播（Esc 断开）、中断恢复衔接、从 `response.completed` 取 usage
- [ ] `[H]` 保留非流式回退 + 按模型能力降级

### P3.4 输入栏支持任意模态粘贴（`[M]`）
> 现状：`parseInputLine` 已能接收 `ResponseInput` JSON（数组形式）并交给 `model.NewResponseInput`；模型层已支持
> `InputImage{ImageURL/FileID/Detail}` 与 `MessageContent.Parts`。缺的是把它做成一等公民的输入体验。
- [ ] 输入栏可**直接粘贴图片**：剪贴板图像 → `data:image/...;base64,...` → `InputImage`
- [ ] 消息组装：文本 + 图片/附件 → `MessageContent.Parts`（`NewMessageContentParts`），而非纯字符串
- [ ] 输入栏显示**占位标记**（如 `[图片 1]`、`[文件 a.png]`），发送时替换为真实部件
- [ ] 粘贴**文件路径 / 拖拽**：识别路径 → 按扩展名决定（图片→`InputImage`，文本→内联或走 `read_file`）
- [ ] 类型与大小校验（上限进配置），超限给出可操作提示而非静默失败
- [ ] 与 `parseInputLine` 的 JSON 通路统一：一条路径，避免「JSON 粘贴」与「附件粘贴」两套语义
- [ ] 多模态条目**渲染**：终端无法显示位图 → 占位 + 元信息（文件名/尺寸/大小）；可选 kitty/iTerm2 图形协议
- [ ] 多模态条目**持久化**：`history` 已可承载（`ResponseInput` 支持 parts），但 `sessions.db` 体积需评估与上限
- [ ] plan mode 下是否允许图片输入（只读探索场景可用图）
- [ ] 快捷键/命令：粘贴图片的显式入口（如 `Ctrl+V`）与移除单个附件
- [ ] 与折叠 / 回到底部 / 流式共存；测试：图片粘贴组装、占位渲染、超限提示、持久化往返

## P4 新子系统与收尾（依赖 P0–P3 全部底座）

- [ ] `[F]` Subagent：新增工具注册进 `internal/tool`（新分类，不引入 skill 层）
- [ ] `[F]` **独立上下文**：每个子代理自有 `State`/history，不继承、不共享父会话历史
- [ ] `[F]` 父→子只传任务描述，子→父只回传截断摘要；子历史不写入 `savedTUISession` / 父 checkpoint
- [ ] `[F]` 独立 runID 命名空间 + 独立 `Agent` 实例 + 受限工具集
- [ ] `[F]` 深度上限、禁止自我递归、轮数预算（复用 `MaxToolRounds`）
- [ ] `[F]` 取消传播（父 Esc → 子）、权限归类、用量分开统计
- [ ] `[F]` TUI 嵌套展示子代理活动；测试上下文隔离/深度/取消/回传/用量
- [ ] `[D]` MCP：`config.json` 增 `mcpServers`
- [ ] `[D]` JSON-RPC 2.0 客户端（initialize / tools/list / tools/call），stdio + streamable HTTP
- [ ] `[D]` MCP 工具注册到 `tool.Registry`（`mcp` 分类，默认需批准）
- [ ] `[D]` 连接/健康检查/进程回收；`/mcp` 列出、启停、重连
- [ ] `/review`（语义待定）
- [ ] 更新 `tuiCommands` / footer 文案；日志加 `model` / `effort`
- [ ] 决定是否同步 `cmd/main.go` 非 TUI 命令集

---

## 待定（需先拍板，否则施工中会返工）

- [ ] 多模态范围：只做图片，还是含音频/PDF/任意文件（决定 `ContentPart` 扩展与校验）
- [ ] 剪贴板图像获取方式：`atotto/clipboard` 仅文本 vs 各平台命令（`pngpaste` / `xclip` / `wl-paste` / PowerShell）
- [ ] 终端内是否做图形显示（kitty / iTerm2 图形协议）还是仅占位 + 元信息
- [ ] 附件大小上限与是否压缩（PNG→JPEG、缩放）后上传
- [ ] 附件是否计入 `sessions.db` 体积与上下文预算（与 `[E]` 口径协同）
- [ ] `Instructions` 合成顺序与优先级：Goal / 称呼 / persona / plan mode 谁先谁后；**persona 不得覆盖或弱化** plan mode、权限、只读等系统约束
- [ ] persona 存哪一层：会话（`savedTUISession`）还是全局（`config.json`）；是否支持多套方案切换
- [ ] persona 是否也传给 subagent 与压缩摘要请求（默认只作用于主 agent）
- [ ] persona 对 TUI 的影响：是否改变「LUNE」显示名或用户短标记
- [ ] 称呼粒度：仅称呼（如「你」/名字）还是同时影响语气；是否只作用于主 agent
- [ ] 称呼命令名与形态：`/user` vs `/callme`；空值是否等于恢复默认
- [ ] TUI 效果图用哪个 image gen 工具/模型；效果图存放位置（如 `docs/design/`）；确认流程（逐图确认 vs 整体确认）
- [ ] 自动压缩方案 A（图内）/ B（仅任务间）
- [ ] `/context` 窗口按模型还是全局
- [ ] `/model` 清单来源（内置 / config）
- [ ] `/mcp` 传输范围与鉴权
- [ ] 是否允许新增依赖（`go.mod`）
- [ ] DeepSeek 模型名 / effort 取值 / 窗口默认值
- [ ] subagent 形态（工具 vs 显式命令）、是否允许嵌套/并发
- [ ] 子代理是否继承父的 `Goal` / plan mode（默认不继承）
- [ ] 子代理与父的 model / effort 是否一致
- [ ] 工具结果瘦身默认策略（纯状态 / 头尾摘录 / 仅保留最近一轮）
- [ ] 工具结果瘦身保留窗口（仅当前 user turn / 最近 N 轮）
- [ ] 瘦身决策权分层（客户端兜底 + 模型 pin/笔记）
- [ ] scratchpad / 笔记的存储位置（会话内 vs 工作区文件）
- [ ] footer 窄终端取舍（上下文占比 vs in/out）
- [ ] 流式默认开关；reasoning delta 是否展示
- [ ] 用户标记形态：`▸` / `you` / `@you`（择一）；是否给它上淡色
- [ ] 用户块宽上限（比例/列数）与窄终端退化阈值
- [ ] 折叠：默认折叠阈值（行数/字符数）与折叠后保留行数；哪些角色可折叠
- [ ] 回到底部的按键绑定（`End` / `G` / `Ctrl+End`）与是否显示底部提示
- [ ] 目标包名与层级（`internal/tui` vs `internal/cli/tui`；`internal/app` vs `internal/runtime`）
- [ ] 是否引入 `cmd/<binary>/main.go` 子目录
- [ ] `internal/loop/` 保留还是删除
