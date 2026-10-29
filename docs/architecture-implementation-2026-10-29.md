# 执行与持久化架构优化实施报告

日期：2026-10-29。起点：luneAgent `31b0f8b`、luneCore `423444b`。本文记录本轮实现及联动提交验证；Core 提交为 `b26354f`，Agent 固定到该远端版本。Desktop 仅机械适配共享 API，没有进行功能设计、前端修改或 GUI 验收。前置审查见 [审计报告](architecture-audit-2026-10-29.md)。

## 1. 项目总体评价

项目定位是可持久恢复的 coding agent：Core 提供模型接入、工具与权限、Graph 执行、队列、问答、MCP、目标及会话持久化；Agent 提供 CLI/TUI/A2A 宿主。核心路径为 SubmitTurn → 持久队列 → worker → Responder/Graph → conversation/result/event 提交 → WaitTurn/事件投影。

值得保留的是工具元数据驱动权限与 Plan 过滤、canonical transcript 与 provider context 分离、工具调用账本、新版本 checkpoint 恢复、宿主与 Core 分工。这些能力有实际用户语义，不能通过换成简单模型调用循环等价替代。没有引入 skill 机制、新框架、兼容适配层或新的依赖。

主要结构问题是结束状态判定分散、全量快照更新的字段所有权不清、目标与普通输入使用不同 admission 边界，以及升级修复混入正常打开路径。本轮围绕这些关联问题实施，保留图执行和多宿主能力边界。

## 2. 问题清单与依据

| 等级 | 已确认问题、位置 | 影响与验证 |
| --- | --- | --- |
| P1 | Core 目标创建/恢复的状态检查与普通 SubmitTurn 不在同一 admission 边界；`internal/runtime/goals.go`、`session_queue.go` | 并发状态检查可能过时。新测试持有 admission gate 后插入普通任务，目标必须返回 busy；持锁期限测试确保取消不能仍提交目标。 |
| P1 | Core worker 与 run 重复重建终态；`core_execution_finish.go`、`session_queue_worker.go` | 内存、checkpoint、队列结果存在不同提交时点，增加错误终态和修复路径。现在 run 返回 typed outcome，worker 提交并通知；原子提交故障注入、重启恢复和重复提交回滚测试通过。 |
| P1 | 元数据更新读取/回写整份 Session；`internal/session/store.go`、runtime 的 usage/settings/grants 更新 | 依赖不断扩大的保护字段清单，已有上下文回滚缺陷的结构性根因。改为字段组事务；两份独立 Store 并发修改用量/设置保留全部计数与历史，删除后更新不能复活会话。 |
| P1 | A2A Close 等待 task.opMu 不可取消；`internal/a2a/lifecycle.go` | 一个占用任务操作锁的请求可阻塞关停。新增 held-operation 测试验证 deadline 返回；恢复输入绑定 request/admission cancellation，Core 控制接口接收 ctx。 |
| P2 | 目标完成后排队用户输入因旧 GoalID 被暂停；清除目标后原已暂停用户输入也可能滞留 | `goal_execution.go`、`goals.go`；分别新增真实排队执行测试。普通消息可脱离完成/清除的目标，自动续跑仍受目标身份和状态约束。 |
| P2 | 迟到的旧 WaitTurn 结果可能清空新任务的 live stream/推理状态；`internal/tui/runtime_transcript.go` | 回归模拟新任务已经开始，旧结果只能插入旧任务位置，不能借用新任务时长或清理其缓冲。 |
| P2 | MCP 关停交互收到普通关闭错误或生命周期 sentinel，取决于时序；Core `core_close.go` | race 运行暴露原有测试失败；统一生命周期错误，相关测试在 race 下重复 10 次通过。 |
| P2 | CLI 授权等待无法响应取消，部分 Interrupt 错误未处理；`core_cli_approval.go`、`core_cli_execution.go` | 授权键盘通道没有输入时也能取消；中断失败传播给调用方，取消后的清理使用有限 context。 |
| P2 | 旧执行升级修复位于打开/压缩/回退等热路径 | 删除旧事件扫描与执行修复，改成显式离线升级；升级回滚/备份/重复运行/未来版本拒绝/活动目录锁/取消测试覆盖。 |

上述严重性是对相关错误行为的工程判断，不表示发现了新的远程代码执行漏洞。权限元数据、沙箱、MCP 不自动重放断连副作用等既有约束未降低。

## 3. 同类方案对比

对照于当天读取的官方仓库，范围限于 agent loop、steering/follow-up 与 admission/cancellation 源码，没有进行跨产品性能排名。

- [pi agent README](https://github.com/earendil-works/pi/blob/main/packages/agent/README.md)：区分执行中的 steering 与停止边界后的 follow-up，在工具/消息边界消费。借鉴清晰的继续条件；没有照搬扩展机制，也没有据此删除 Lune 的持久队列和恢复语义。
- [Crush agent 源码](https://github.com/charmbracelet/crush/blob/main/internal/agent/agent.go)：使用每会话 dispatch 交接锁、活动取消和 accepted 计数协调并发，并明确缩短交接锁范围。借鉴操作生命周期和状态交接的所有权；没有照搬其所有计数/取消序号。Lune 本轮复用已有 admission gate，仍有跨会话串行 admission 的取舍，未新增一套调度框架。

外部方案支持的是边界设计判断，不能证明 Lune 所有模块比它们复杂。文件数量、代码行数和功能列表均未作为优劣证据。

## 4. 设计决策

### 执行结束由 worker 提交

`runOutcome` 明确携带状态、响应、conversation、预算原因、问题和错误。worker 完成持久事务后更新内存投影、释放执行所有权、通知结果。正常结束不再调用 checkpoint 恢复路径猜测本轮结果；进程重启仍允许从 checkpoint 补齐未提交结果。问答暂停仍保留图等待点与已回答后的恢复交接。

没有改为全量事件溯源或通用 workflow。原有 transaction 已能原子提交 conversation/result/event/队列移除，新增框架不会解决额外真实需求。

### 字段组更新替代整份快照更新

Session 创建为 insert-only；settings、usage、approvals、path_grants 各自在事务内只读取和写回拥有的 JSON 字段。删除 `usageMu` 和 Save 的“不可覆盖字段”列表。SQLite 仍存整份会话 JSON，内部 JSON 更新仍与文档大小相关；本轮没有引入表规范化迁移或宣称所有写入变为 O(1)。小会话的事务/JSON 包装开销是需要接受的代价。

### 生命周期与配置分离

`goalRuns` 只保存执行关联；`goalSessions` 保存 armed/交互选项，不再用 `"session:"+id` 伪执行键。创建、恢复、暂停、普通提交与自动续跑复用 admission 边界，等待执行停止时释放 gate，避免等待工作反过来需要同一锁。

### 显式离线升级

新执行格式为 2，checkpoint 文件为 `executions-v2.db`。旧状态目录正常启动返回 ErrUpgradeRequired。独立命令/API 取得独占生命周期锁和已有会话锁，先用 SQLite VACUUM INTO 备份 sessions.db，再转换会话、将旧未完成任务标记 discarded、清理旧执行记录、暂停活动目标，最后写版本标记。

保留历史/设置/元数据/已记录用量及旧 checkpoint 文件；provider context 从 canonical history 重建。排队输入不自动重放，原始输入留在备份。用户已接受作废旧未完成执行，这比长期维护双引擎/旧 replay 分支更清晰。没有对真实用户 StateDir 执行升级。

## 5. 已实施改动与破坏性接口

Core：

```go
client.Close(ctx) error
runtime.Close(ctx) error
runtime.Interrupt(ctx, executionID) (bool, error)
runtime.ResolveApprovalDecision(ctx, requestID, decision) error
core.UpgradeState(ctx, stateDir) (core.UpgradeReport, error)
```

删除布尔授权 API；调用方必须明确 once/session/deny。目录会话授权不再同时记录冗余的精确工具授权。Core 关闭超时保持存储打开，允许等待工作结束后重试。

Agent：CLI、TUI、oneshot、A2A 及测试适配以上 API；增加 `lune migrate --state-dir <directory>`，在加载模型配置和读取 stdin 前分派。TUI 删除 terminalEvents 终态表及事件对 durable result 的覆盖，保留异步身份、wait generation 和中断后旧 delta 隔离等必要状态。Desktop 仅适配调用签名。

文档：更新 Core README、Agent README、命令帮助及工程维护指南。Core 已先提交并推送为 `b26354f`；Agent 的 go.mod/go.sum 固定到 `v0.1.1-0.20261029065600-b26354f5a04b`，没有本地 replace。

## 6. 验证结果

已执行：

- Core：`GOWORK=off go test -trimpath ./...`；`GOWORK=off go vet ./...`。
- Core race：runtime/session/tool/command/mcp；MCP 错误语义修正后重跑 runtime/session/stateformat。目标 admission、完成/清除后的用户队列在 race 下重复 5 次；MCP 生命周期回归重复 10 次；提交事务重复回滚与存储测试通过。
- Agent：A2A/app/CLI/TUI/markdown 的 race；上述包及 settings/slashcmd/workspacereview/filemention/goalcmd/terminaltext/toolchains 的测试和 vet。A2A 清理失败、CLI 中断错误处理最后调整后单独重跑对应 race。
- 独立模块验证：开发阶段使用临时 modfile 联调；Core 推送后，Agent 固定远端 `b26354f` 对应 pseudo-version，运行 `go mod tidy`，在 `GOWORK=off` 下重跑全部 Linux 支持包测试、vet、Linux amd64/arm64 和 Windows amd64 构建及 Windows Desktop 测试编译。`go list -m -json` 确认模块来自下载缓存且没有 replace。
- 构建：Linux amd64/arm64 和 Windows amd64 的 CLI、lune-sandbox，全部 `-trimpath`。Windows Desktop `go test -c -trimpath` 编译通过，没有执行 GUI。
- 临时二进制 smoke：`--help`，无模型配置/凭证的 `migrate` 分派。真实转换与备份验证在临时 SQLite fixture 中完成。
- 两仓库 `git diff --check`、修改文件 gofmt；禁用真实模型 smoke 的环境入口。没有对真实用户 StateDir 执行转换、清理或任务重放。

性能采样使用同一机器、各 5 次、`-benchtime=200ms -benchmem -trimpath`，比较 HEAD 的临时归档与修改后工作树，取中位数。Core 实际编译器 Go1.27.1（模块最低 1.26.10）；TUI Go1.27.3。用量基准先填入 200 条记录，包含 SQLite WAL/FULL 的本地文件系统开销。原始结果见 [基准记录](benchmarks/execution-2026-10-29.txt)。

| 基准 | 修改前 | 修改后 | 解释 |
| --- | --- | --- | --- |
| 用量写入，1 KiB 历史 | 3.509 ms；350,401 B；76 allocs | 4.508 ms；399,248 B；138 allocs | 耗时约增加 28.5%，Go 分配字节约增加 13.9%；事务读取字段和包装有实际开销。 |
| 用量写入，1 MiB 历史 | 17.871 ms；7,381,713 B；99 allocs | 10.648 ms；404,940 B；139 allocs | 耗时约减少 40.4%，Go 分配字节减少 94.5%；没有声称 allocation 次数减少。 |
| TUI 2000 项 cached refresh | 49.421 µs；607 B；28 allocs | 50.031 µs；607 B；28 allocs | 约 1.2% 差异，不认定有速度收益。 |
| TUI 2000 项完整 frame | 499.056 µs；101,776 B；4515 allocs | 462.984 µs；101,604 B；4515 allocs | 约 7.2% 的样本耗时差异；本轮主要改结束处理，没有足够依据归因为渲染优化。 |

这不是跨硬件或生产负载的统计结论。大历史的分配下降支持字段写入的实际收益；小会话回退明确保留，选择依据首先是消除快照覆盖与跨实例计数丢失。

## 7. 剩余问题与限制

其中持续写失败与 MCP 关闭边界的后续实施见[后续修复报告](runtime-followup-2026-10-29.md)；下列内容保留本报告提交时的状态。

- 跨仓库依赖联动已完成：先推送 Core，再固定 Agent 依赖并验证。未创建版本 tag 或发布安装包；后续二进制发布仍需按维护指南执行。
- context 是协作取消，不会强杀任意自定义 provider/tool；普通互斥区、文件系统调用及 MCP transport 自身清理仍有其等待边界。不能把已验证的 held-operation deadline 推广为任意第三方实现的硬实时关停保证。
- A2A runtime Close 失败时保留任务所有权，由既有观察循环重试，不发布终态或提前允许同会话新任务。长期故障最终仍依赖关停 deadline 返回错误与宿主处理，没有增加持久化清理队列。
- 持续存储写失败时，队列恢复/调度仍可能重复尝试；已验证结果事务不部分提交和重启不重复模型调用，未模拟长期磁盘故障、断电、SQLite 文件损坏或多机共享盘。
- 全局 admission gate 可串行化不同会话的短暂 DB admission；没有证据支持本轮再引入每会话 gate 生命周期。SQLite 单写者本身也仍是限制。
- Windows 仅交叉构建，未在 Windows 实机运行文件锁、ConPTY 或原生 PowerShell。未执行 GUI/IME 实机验证、Desktop 功能测试、真实模型 API、真实用户数据升级。

## 8. 最终评估

改进集中在可验证的状态/字段所有权：减少正常结束对恢复逻辑的依赖，消除全快照回写这一类覆盖风险，减少前端终态解释状态，明确升级与运行的边界。恢复、排队、steering、目标、权限与 MCP 等真实功能仍保留。收益不是代码行数下降；代价包括一次显式存储升级、公开 API 破坏性变化、小会话写入的额外事务包装，以及发布时必须联动两个仓库。
