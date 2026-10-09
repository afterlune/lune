# ROADMAP

> 按依赖推进：稳定与安全 → Agent 公共底座 → 会话能力 → TUI 交互 → 上下文与输入 → 扩展能力。
> 各里程碑可并行的工作不互相阻塞；进入有用户取舍的功能前，先完成对应决策门。
> 标签：`[I]` 分包、`[A]` 设置、`[B]` 历史、`[C]` 会话、`[E]` 上下文、`[G]` 结果瘦身、`[H]` 流式、`[D]` MCP、`[F]` Subagent、`[M]` 多模态、`[Q]` 提问。

## 当前基础与状态

- 工具分类、四档权限、不可见工具的可操作提示已存在；I-1 的请求快照日志和回归测试已加入，运行期根因仍待当前版本日志确认。
- 手动 /compact、用量统计、多模态输入结构和 ResponseStreamEvent 已有基础；自动压缩、流式请求和终端附件输入尚未完成。
- TUI 主实现位于 internal/tui；cmd/tui.go 和 internal/loop/ 当前不存在。后续任务按当前结构规划，是否新建专用包列为架构决策。

## M0 稳定性与安全

**目标：** 权限、配置和持久化规则明确，后续功能有可测试的基础。

- [ ] 用当前版本复现 I-1；结合请求快照日志定位类别或可见工具集合变化。未取得证据前保持问题开放，不猜测根因。
- [x] 注册工具时统一声明 category、权限类别和只读属性；移除 permissions.go、agentgraph 和默认 Registry 的重复硬编码。
- [x] 修复 workspace 模式下未知工具无需批准的安全缺口，并为四档权限与自定义工具补充覆盖测试。
- [ ] 为 config.json 增加结构化加载与校验，覆盖 models、mcpServers、contextWindow、autoCompact、autoCompactThreshold、reserveOutputTokens 和附件限制。
- [ ] 为会话数据增加版本与迁移策略；明确 sessions.db 和 checkpoints.db 的删除级联规则。
- [ ] 增加 internal/session/store_test.go；按上下文策略决策门确定 token 计数口径并补测试。
- [ ] 为 .luneagent/agent.log 增加日志轮转。

**完成条件：** 权限矩阵和未知工具路径有测试；配置错误可诊断；旧会话迁移与删除关系有测试；I-1 有根因证据或明确保留为待运行期取证。

## M1 Agent 公共底座

**目标：** 固定包边界、任务状态和设置传递方式，供会话、流式和新工具复用。

- [ ] [I] 按决策门确定包边界后拆分：internal/tui/ui.go 的 model/update/view、pickers、usage、render、vim；cmd/graph_runtime.go 的 responder、compaction、usage observer、history；让 cmd/ 只保留装配与入口。
- [ ] [I] 评估 internal/model 的 responses/client/network 边界，以及 internal/tool 的 read/write/exec/web/explore/ask 子包；保持 ToolExecutor 定义在 model 侧以避免循环依赖，迁移相关测试。
- [ ] [A][E] 扩展 TaskOptions / State，持久化 Model、ReasoningEffort、Goal、ContextWindow、AutoCompact 等任务设置，使 Recover 可恢复原设置。
- [ ] [A] 加入用户称呼与 persona 设置；用统一的 Instructions 合成器组合 Goal、称呼、persona 和 plan mode，保证 persona 不削弱系统权限约束。
- [ ] [A][E] 在 requestModel() 应用模型默认值、effort 到 Reasoning 的映射及 Instructions 合成；为 graphResponder 增加并发安全的设置接口，保存并恢复到 savedTUISession。
- [ ] [A] 为非 TUI 的 model.Client 增加 SetModel。
- [ ] [H] 为流式扩展准备请求接口，保持现有 CreateResponse 调用兼容。
- [ ] [Q] 把权限批准的 TUI 消息与 channel 等待抽成通用 SetUserPromptHandler，供 ask_user 复用。

**完成条件：** 设置经过 Start、checkpoint/Recover 和 session reopen 后保持一致；请求行为及系统约束有测试；包拆分后 go test ./... 通过。

## M2 会话与历史

**目标：** 完成会话生命周期和可恢复的历史操作。

- [ ] [C] session.Store 支持按工作区删除、设置标题、归档/取消归档；列表支持包含已归档会话，优先用 JSON1 过滤避免 schema 迁移。
- [ ] [C] Snapshot 与 savedTUISession 保存 Title / Archived，upsert 不覆盖既有值。
- [ ] [B] /fork 复制历史并切换到新会话；/rewind 按 user turn 回退，不切开 function call/output 配对，paused task 时禁用并提示。
- [ ] 为删除、归档、标题、过滤、fork、rewind 和中断恢复补充存储与行为测试。

**完成条件：** 会话状态重开后保持；归档过滤和删除范围正确；历史操作不会破坏工具调用配对或 paused task。

## M3 TUI 与交互

**目标：** 在确认的视觉方案上完成日常工作流、会话操作和模型提问。

- [ ] **设计门禁：** 生成空态、对话、长输入折叠、工具活动、详情面板、窄终端及模型提问效果图；用户确认后再改渲染实现。
- [ ] 重整转写布局：用户消息右对齐，仅用户短标记可加粗并可用低饱和色；正文常规字重。assistant 与 user 走一致的渲染路径，其他条目靠缩进、间距、单一轻量前缀分层；移除其余加粗，避免高亮铺底、重复工具条目和 badge 独占行。标题用纯文本/极简前缀，代码块保持缩进，不重排表格或引入行内样式。
- [ ] 做宽度感知换行与块宽（用户块目标约 70–80%），窄终端退化为左对齐；统一块间距，控制刷新成本并增加用户对齐、长输入和窄终端快照测试。
- [ ] 长用户输入默认折叠为 N 行，collapsed 状态保存到 savedTUIEntry；只为超阈值条目显示折叠指示。点击首行/标记和键盘均可切换；通过渲染行索引命中条目，活动面板和选择器点击不误触。
- [ ] 支持回到底部并在底部时自动跟随；滚离底部时保留阅读位置，新内容不强制拉回，可显示回到底部提示；与折叠及流式增量共存。
- [ ] 增加 /help；完善 /goal（查看/设置/清除）、/effort（选择）、/model（菜单或名称）、/user 或 /callme（查看/设置/清除称呼）、/persona（多行查看/设置/清除）、/context（查看/设置窗口、预留、reset）、/autocompact（查看/on/off/threshold）；更新 tuiCommands、命令预测和 footer，并记录 model/effort。
- [ ] [C] 在会话面板用 d 删除（Y/N 确认）、r 重命名、a 归档、Tab 显示已归档；优先显示 title，删除当前会话后切到新会话。
- [ ] [Q] 在 internal/tool 注册 ask_user 工具与 ask 类别，通过区别于权限 Y/N 的文本提问界面等待回答；把回答持久化为 function_call_output 并继续任务。该工具不触发批准，且不得绕过 workspace 权限规则。
- [ ] [Q] plan mode 允许提问；Esc/Ctrl+C 可中断并走 checkpoint/continue。按决策门确定 headless 行为、选项/默认值、超时、预算计数及 subagent 内提问的失败/上抛方式。
- [ ] 决定 TUI 与非 TUI 的命令集合是同步还是有意不同。

**完成条件：** 设计经用户确认；布局、窄终端、折叠、点击、底部跟随和会话面板均有测试；ask_user 的回答、取消、恢复及 headless 行为有覆盖。

## M4 上下文、流式与多模态输入

**目标：** 在请求、历史和 TUI 间统一预算与增量输入体验。

- [ ] [E] 增加 context 窗口及占用显示：footer 保持单行简要（如 ctx 已用/窗口/比例）；详情面板与用量表显示窗口、占比、缓存命中率，并按模型汇总。
- [ ] [E] 按决策门确定自动压缩边界；在所选边界触发瘦身和摘要，并保留现有手动 /compact。
- [ ] [G] 接近预算时保留最近 N 轮原文和失败态，其余结果替换为状态或头尾摘录；只瘦身 function_call_output.output，保留 call_id 配对。
- [ ] [G] 支持 pin(call_id)、drop_result(call_id) 及 scratchpad；明确默认保留策略、保留窗口、客户端兜底与模型决策的分工、笔记存储位置；覆盖 agentgraph 与 model 两条历史路径。
- [ ] [M] 多模态内容不参与文本瘦身或文本 token 估算，单独确定计量与保留策略。
- [ ] [H] 解析 SSE 并复用 ResponseStreamEvent；图节点累积完整 Response，TUI 增量显示 text/reasoning/args，处理未闭合内容。
- [ ] [H] 流式支持 Esc 取消、中断恢复和 completed usage；保留非流式回退并按模型能力降级。
- [ ] [M] 按决策门选定支持的模态与平台剪贴板方案；输入支持 Ctrl+V 图像、文件路径/拖拽（按扩展名选择图像、内联文本或 read_file）及文本与附件混合组装，通过 MessageContent.Parts 统一到现有 ResponseInput JSON 路径。
- [ ] [M] 显示附件占位与文件名/尺寸等元信息，支持移除附件；对类型、大小、压缩和 sessions.db 体积做限制，plan mode 与折叠、流式共存。
- [ ] [M] 终端优先使用占位与元信息；图形协议作为决策门确定的可选能力。

**完成条件：** token 口径、预算和压缩不破坏 call_id 或多模态内容；流式可取消并恢复；附件组装、限制提示和持久化往返有测试。

## M5 扩展能力

**目标：** 在配置、权限、任务状态和取消机制稳定后接入外部工具与子代理。

- [ ] [D] 在配置中支持 mcpServers；实现 JSON-RPC 2.0 initialize、tools/list、tools/call，按决策门选定 stdio / streamable HTTP 与鉴权。
- [ ] [D] 将 MCP 工具注册到 Registry 的 mcp 分类并默认要求批准；处理连接健康、进程回收和 /mcp 列表/启停/重连。
- [ ] [F] 增加 Subagent 工具及隔离的 State/history/runID/Agent；每个子代理使用独立 history，不继承或共享父历史，子历史不写入父 session/checkpoint；父传任务，子回传截断摘要，限制工具集与递归深度。
- [ ] [F] 支持深度上限、禁止自我递归、复用 MaxToolRounds 的轮数预算、父 Esc 取消传播、权限和用量隔离，以及 TUI 嵌套活动展示。
- [ ] 按决策门明确 /review 的语义和主命令入口。
- [ ] 不新增 skill 层；工具扩展继续走 Registry 与工具分类。

**完成条件：** MCP 有连接失败、授权和进程清理测试；Subagent 有隔离、深度、取消、回传和用量测试。

## 前置决策门

每项只阻塞依赖它的工作，不阻塞无关里程碑。

- **架构与依赖（M1 前）：** 包路径与职责（internal/tui 或 internal/cli/tui；internal/app 或 internal/runtime）、是否创建 internal/loop、是否新增 cmd/<binary> 入口、是否引入依赖。
- **模型与 persona（M1/M3 前）：** 模型清单来源、DeepSeek 模型名、effort 取值和窗口默认值；称呼命令名与粒度；persona 是会话级还是全局、多套配置、是否作用于 subagent/摘要及其 TUI 身份影响；Instructions 合成顺序。
- **上下文策略（M0/M4 前）：** 服务器 usage.InputTokens 与本地启发式的优先级/回退；压缩在图内任务中还是任务间；/context 窗口按模型还是全局；结果瘦身保留范围、决策权分层、scratchpad 存储和窄 footer 取舍。
- **提问（M1/M3 前）：** ask_user 工具还是结束回合等用户下一条消息；选项/默认值/多问；headless 行为、超时与取消；是否计入 token 和工具轮数；subagent 内失败还是上抛。plan mode 允许提问是既定要求。
- **TUI 与附件（M3/M4 前）：** 效果图工具/模型、存放位置与逐图或整体确认；用户标记样式/颜色、用户块宽度和窄屏阈值、折叠行数/保留行数/适用角色/键位、回到底部键及提示；图片还是扩展到音频/PDF；剪贴板方案（如 atotto/clipboard 或各平台命令）、大小与压缩、session DB 限额、占位或 kitty/iTerm2 图形协议及 plan mode 下附件输入。
- **流式与扩展（M4/M5 前）：** 流式默认值和 reasoning 是否显示；MCP 传输与鉴权；Subagent 是工具还是显式命令、嵌套/并发、Goal/plan/model/effort 继承；/review 语义和 CLI/TUI 命令差异。

## 依赖顺序

M0 → M1 → M2/M3 → M4 → M5。M3 的视觉改动必须先过设计门；ask_user 依赖 M1 提示通道；流式依赖 M1 请求接口与 M3 渲染结构；MCP 依赖 M0 配置和权限；Subagent 依赖 M1 任务状态及 M0 权限基础。
