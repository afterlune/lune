# ROADMAP

> 按依赖推进：稳定与安全 → Core 公共底座 → 会话与 TUI → 上下文与输入 → 多客户端与扩展能力。
> 各里程碑可并行的工作不互相阻塞；进入有用户取舍的功能前，先完成对应决策门。
> 标签：`[I]` 分包、`[A]` 设置、`[B]` 历史、`[C]` 会话、`[E]` 上下文、`[G]` 结果瘦身、`[H]` 流式、`[D]` MCP、`[F]` Agent 团队、`[M]` 多模态、`[Q]` 提问。

## 当前基础与状态

- 工具分类、四档权限、不可见工具的可操作提示已存在；I-1 的请求快照日志和回归测试已加入，运行期根因仍待当前版本日志确认。
- 手动 /compact、用量统计、多模态输入结构和 ResponseStreamEvent 已有基础；自动压缩、流式请求和终端附件输入尚未完成。
- 根目录 core 是公开 Go API，TUI 通过它使用 Agent 能力；cmd/ 负责装配和启动。Core 不保存 UI transcript/activity 缓存，也不包含主题、角色图片或布局逻辑。
- 产品方向已确定：TUI 复刻 Codex 的高效交互体验；Desktop 是承载角色表现与复杂动效的独立客户端；两者共用 Core。

## M0 稳定性与安全

**目标：** 权限、配置和持久化规则明确，后续功能有可测试的基础。

- [ ] 用当前版本复现 I-1；结合请求快照日志定位类别或可见工具集合变化。未取得证据前保持问题开放，不猜测根因。
- [x] 注册工具时统一声明 category、权限类别和只读属性；移除 permissions.go、agentgraph 和默认 Registry 的重复硬编码。
- [x] 修复 workspace 模式下未知工具无需批准的安全缺口，并为四档权限与自定义工具补充覆盖测试。
- [x] 为 config.json 增加结构化加载与校验，覆盖模型目录、contextWindow、autoCompact、autoCompactThreshold、reserveOutputTokens 和附件限制；mcpServers 字段留到 M5 决策传输与鉴权后确定。
- [x] 为会话数据增加 SQLite user_version 迁移；明确 sessions.db、checkpoints.db 与团队成员 checkpoint 的删除级联规则。
- [x] 增加 internal/session/store_test.go，覆盖迁移、元数据并发更新、归档筛选和删除；token 计量仍由上下文策略决策门确定。
- [ ] 为 .luneagent/agent.log 增加日志轮转。

**完成条件：** 权限矩阵和未知工具路径有测试；配置错误可诊断；旧会话迁移与删除关系有测试；I-1 有根因证据或明确保留为待运行期取证。

## M1 Agent 公共底座

**目标：** 固定包边界、任务状态和设置传递方式，供会话、流式和新工具复用。

- [x] [I] 建立公开 core API，承接任务、状态、事件、会话、checkpoint、权限审批和用量；TUI 仅依赖 core API，cmd/ 仅装配 Core、TUI 或简单 CLI。
- [x] [I] 增加 provider/model 配置目录；provider 名称由配置决定，API 协议与 provider 解耦，支持 Responses 与 Anthropic Messages 兼容格式及自定义 endpoint。主 Agent 固定默认 profile，团队成员在子任务启动时选择 profile 并固定到该次运行。
- [ ] [M] 为 model profile 声明输入/输出模态范围；团队分配子任务时根据任务所需模态动态选择兼容 profile。当前先使用已配置 profile，不提前加入能力范围字段。
- [ ] [I] 继续拆分 internal/tui/tui.go 内的 model/update/view、pickers、usage、render 与 vim 逻辑，保持 Core 不依赖 TUI 表现。
- [ ] [I] 评估 internal/model 的 responses/client/network 边界，以及 internal/tool 的 read/write/exec/web/explore/ask 子包；保持 ToolExecutor 定义在 model 侧以避免循环依赖，迁移相关测试。
- [x] [A] Core Session 保存 model profile 与 reasoning effort；任务启动时固定设置并写入 checkpoint，恢复任务继续使用 checkpoint 中的设置。
- [ ] [A][E] 将 Goal、ContextWindow、AutoCompact 等设置加入 TaskOptions / State 并持久化，使 Recover 可恢复原设置。
- [ ] [A] 加入用户称呼与 persona 设置；用统一的 Instructions 合成器组合 Goal、称呼、persona 和 plan mode，保证 persona 不削弱系统权限约束。
- [x] [A] Core 根据会话 profile 构造主 Agent；requestModel() 将 reasoning effort 映射到 Reasoning；暂停恢复从 checkpoint 读取原 profile 和 effort。
- [ ] [A] 在 requestModel() 合成 Goal、称呼、persona 与 plan mode 的 Instructions；其设置需经 checkpoint/session 恢复。
- [ ] [A] 为非 TUI 的 model.Client 增加 SetModel。
- [ ] [H] 为流式扩展准备请求接口，保持现有 CreateResponse 调用兼容。
- [ ] [Q] 把权限批准的 TUI 消息与 channel 等待抽成通用 SetUserPromptHandler，供 ask_user 复用。

**完成条件：** 设置经过 Start、checkpoint/Recover 和 session reopen 后保持一致；请求行为及系统约束有测试；包拆分后 go test ./... 通过。

## M2 会话与历史

**目标：** 完成会话生命周期和可恢复的历史操作。

- [x] [C] session.Store 支持按 workspace 删除、设置标题、归档/取消归档；列表在 limit 前用 JSON1 筛选已归档会话。
- [x] [C] Core Session 持久化 Title / Archived；history、设置和用量更新不会覆盖元数据。
- [x] [B] `/fork` 复制历史与模型设置并切换到新会话；`/rewind <n>` 按 user turn 回退，保留工具调用配对并拒绝 paused task。
- [x] 为删除级联、归档、标题、过滤、fork、rewind 和中断任务清理补充存储与行为测试。

**完成条件：** 会话状态重开后保持；归档过滤和删除范围正确；历史操作不会破坏工具调用配对或 paused task。

## M3 TUI 与交互

**目标：** 在现有 Go TUI 技术栈中复刻 Codex 的高效交互，完成日常工作流、会话操作和模型提问。

- [x] 完成 Codex 与当前 TUI 的交互差距盘点；以[官方键位映射](https://github.com/openai/codex/blob/main/codex-rs/tui/src/keymap.rs)和[输入框](https://github.com/openai/codex/blob/main/codex-rs/tui/src/public_widgets/composer_input.rs)为交互参考，不移植源码。当前已有多行输入、命令箭头选择与 Enter 执行、排队输入、任务中断/恢复、四档权限、授权确认、脱敏工具活动、用量详情和启动时恢复最近会话。
- [ ] **P0 · 统一 TUI 产品方向：** 移除 TUI 的 anime 主题、角色 sprite、Studio 布局及其设置；保留文本优先的终端体验。角色表现与复杂动画需求归入 M6 Desktop。
- [ ] **P1 · 输入与命令发现：** 保留现有 textarea、多行输入及命令预测交互；补 `/help`、命令说明/用法提示和按当前界面变化的快捷键提示。键位按简单的界面上下文处理，暂不新增可配置 keymap 系统。
- [ ] **P1 · 对话阅读：** 增加长用户消息折叠、滚离底部时的新消息提示和明确的回到底部操作；继续保留当前滚动位置与底部自动跟随行为，并覆盖窄终端及折叠状态。
- [ ] **依赖边界：** 会话删除、重命名、归档及历史操作跟随 M2；流式文本和工具参数展示跟随 M4。当前脱敏工具摘要、权限确认、任务中断/恢复作为已有基线保留，不在本轮重复造一套交互。
- [x] 增加 /model 与 /effort 选择器及直接参数设置；支持命令预测、会话保存、footer 展示和恢复。
- [ ] 完善 /goal（查看/设置/清除）、/user 或 /callme（查看/设置/清除称呼）、/persona（多行查看/设置/清除）、/context（查看/设置窗口、预留、reset）、/autocompact（查看/on/off/threshold）；更新 footer。
- [ ] [C] 在会话面板用 d 删除（Y/N 确认）、r 重命名、a 归档、Tab 显示已归档；优先显示 title，删除当前会话后切到新会话。
- [ ] [Q] 在 internal/tool 注册 ask_user 工具与 ask 类别，通过区别于权限 Y/N 的文本提问界面等待回答；把回答持久化为 function_call_output 并继续任务。该工具不触发批准，且不得绕过 workspace 权限规则。
- [ ] [Q] plan mode 允许提问；Esc/Ctrl+C 可中断并走 checkpoint/continue。按决策门确定 headless 行为、选项/默认值、超时、预算计数及 subagent 内提问的失败/上抛方式。
- [ ] 决定 TUI 与非 TUI 的命令集合是同步还是有意不同。

**完成条件：** 上述差距清单按优先级落地；输入编辑、命令发现、长消息折叠、滚动跟随、工具活动、窄终端和会话恢复有测试；TUI 不承担角色图片或复杂动画；ask_user 的回答、取消、恢复及 headless 行为有覆盖。

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

**目标：** 在配置、权限、任务状态和取消机制稳定后接入外部工具与并发 Agent 团队。

- [ ] [D] 在配置中支持 mcpServers；实现 JSON-RPC 2.0 initialize、tools/list、tools/call，按决策门选定 stdio / streamable HTTP 与鉴权。
- [ ] [D] 将 MCP 工具注册到 Registry 的 mcp 分类并默认要求批准；处理连接健康、进程回收和 /mcp 列表/启停/重连。
- [x] [F] 增加团队委派工具；主 Agent 拆分独立任务并汇总结果，成员并发执行且拥有隔离的 State/history/runID/Agent；成员使用独立配置的 model profile，不共享父历史或写入父 checkpoint。
- [x] [F] 支持并发上限配置、父任务取消传播、权限审批共享、按成员归属的事件与用量统计，以及成员 checkpoint 恢复。
- [ ] [F] 明确递归委派、团队嵌套与团队运行时的细粒度轮数预算；当前成员不再启动子团队。
- [ ] 按决策门明确 /review 的语义和主命令入口。
- [ ] 不新增 skill 层；工具扩展继续走 Registry 与工具分类。

**完成条件：** MCP 有连接失败、授权和进程清理测试；团队有成员隔离、并发、取消、结果汇总、用量和恢复测试，并明确递归委派边界。

## M6 Desktop 客户端

**目标：** 复用 Core 能力，为角色沟通与复杂动效提供独立桌面体验。

- [ ] 在实现前确定 Desktop 的 UI 框架、打包方式和运行边界；默认复用同进程 Go Core，只有选择非 Go 前端时才评估桥接协议。
- [ ] Desktop 通过 Core 使用任务、会话、状态、事件、工具审批和用量；不得复制 Agent 执行、权限判断或会话存储逻辑。
- [ ] 在 Desktop 展示角色图片、背景、窗口布局和复杂动画；动画状态由 Core 事件驱动，表现细节留在 Desktop。
- [ ] 覆盖任务启动/取消/恢复、工具活动、权限确认及会话重开，确认 Desktop 与 TUI 遵循相同 Core 行为。

**完成条件：** Desktop 可独立消费 Core API；UI 表现与执行逻辑分层；任务、权限、恢复和会话行为与 TUI 一致。

## 前置决策门

每项只阻塞依赖它的工作，不阻塞无关里程碑。

- **架构与依赖（已决）：** Core 是根目录的公开 Go `core` 包；TUI 与 Desktop 都复用 Core；默认同进程调用，不新增 IPC 或 app/loop 层。Desktop 框架与打包方式在 M6 开始前决定；若选非 Go 前端，再评估桥接协议。
- **模型与 persona（M1/M3 前）：** 模型清单来源、DeepSeek 模型名、effort 取值和窗口默认值；称呼命令名与粒度；persona 是会话级还是全局、多套配置、是否作用于 subagent/摘要及其 TUI 身份影响；Instructions 合成顺序。
- **上下文策略（M0/M4 前）：** 服务器 usage.InputTokens 与本地启发式的优先级/回退；压缩在图内任务中还是任务间；/context 窗口按模型还是全局；结果瘦身保留范围、决策权分层、scratchpad 存储和窄 footer 取舍。
- **提问（M1/M3 前）：** ask_user 工具还是结束回合等用户下一条消息；选项/默认值/多问；headless 行为、超时与取消；是否计入 token 和工具轮数；subagent 内失败还是上抛。plan mode 允许提问是既定要求。
- **TUI 与附件（M3/M4 前）：** Codex 交互差距清单和当前版本参考方式；长输入折叠行数/保留行数/适用角色/键位、回到底部键及提示；图片还是扩展到音频/PDF；剪贴板方案（如 atotto/clipboard 或各平台命令）、大小与压缩、session DB 限额、占位或 kitty/iTerm2 图形协议及 plan mode 下附件输入。
- **流式与扩展（M4/M5 前）：** 流式默认值和 reasoning 是否显示；MCP 配置结构、传输与鉴权；团队成员的 Goal/plan/model/effort 继承、递归委派边界；/review 语义和 CLI/TUI 命令差异。

## 依赖顺序

M0 → M1 → M2/M3 → M4 → M5。M3 以 Codex 交互差距清单替代独立视觉方案门禁；ask_user 依赖 M1 提示通道；流式依赖 M1 请求接口与 M3 渲染结构；MCP 依赖 M0 配置和权限；Agent 团队依赖 M1 任务状态及 M0 权限基础。M6 在 M1 Core API、M2 会话能力和 M3 TUI 基线稳定后启动，不依赖 M5 的 MCP/团队扩展完成。
