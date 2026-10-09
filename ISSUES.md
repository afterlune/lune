# ISSUES.md

运行期遇到的问题记录（由在 lune agent 内以 Plan 模式执行任务时观察到）。
Context: observed while running inside the lune agent in **plan mode**, with the
`files` category selected (read-only tool set).

> 说明：以下为**观察记录**，不都对已确证的产品缺陷；部分可能来自宿主/运行环境。已在“证据”里标注对应源码位置。

---

## I-1 工具在会话中途变为不可用，根因尚未定位

**状态**：当前 `agentgraph` 生产链路已记录每轮请求的类别与可见工具快照；类别状态的任务内保留、SQLite 恢复和新任务重置均有回归测试。运行期根因仍未确认，继续跟踪。

**现象**
同一批任务中，`read_file` 连续两次返回：

```
Tool is not available in the current request.
```

而这些路径此前刚成功读取过（例如 `go.mod`、`cmd/main.go`）。重新调用 `select_tool_category` 选择 `files`（或 `command`）后即恢复。

**证据**
- `internal/agentgraph/agent.go` 和 `internal/model/model.go` 都会检查模型调用是否属于本轮可见工具集合；不属于时返回普通工具输出，而不是执行工具。
- `unavailableToolMessage` 会在已知工具分类时提示调用 `select_tool_category`，并在无法归类时提示检查当前可用工具。
- CLI/TUI 的生产请求经公开 `core` 包中的运行时进入 `agentgraph`；每轮请求日志记录请求序号、计划模式、所选分类和可见工具名，类别选择及不可见工具调用也有日志。
- 图状态在类别选择后保留 `SelectedCategory`，下一轮据此重算可见工具；新增测试覆盖 SQLite 重开后恢复类别，以及新任务只看到类别选择器。
- 旧提示 `Tool is not available in the current request.` 不在当前源码中；现有 `.luneagent/agent.log` 也没有该文本或类别快照，因此不能用它确认当前版本的运行期根因。

**影响**
- 分类按需发现（staged discovery）下曾观察到“上一轮可用、这一轮不可用”；新增的请求级快照可用于判断后续复现时，类别选择或可见工具集合在哪一轮发生变化。
- 该分支以**普通工具输出**回传，而非 `Result.Err`；模型可据提示恢复，但上层仍不能把“工具不可见”与“执行失败”直接区分。
- `VisibleTools` 每轮 tool batch 后会按设计清空，可见性由 `SelectedCategory` + `PlanMode` 重新计算。诊断信息写入日志，TUI 不直接展示。

**已处理**
- 不可见工具的提示说明了应如何选择工具分类；请求日志记录类别和可见工具名，不记录提示词、工具参数或结果。
- 回归测试验证：类别选择后暂停并重开 SQLite 仍能看到该类别工具；下一个独立任务从类别选择器开始；同一响应中调用隐藏类别工具仍会被拦截并提示恢复方式。

**待查**
- 收集一次使用当前版本复现问题时，`agent model request started` 与 `tool call is not in the visible tool set` 附近的日志，核对请求序号、`selected_category` 和 `visible_tools`；不要附带提示词或工具参数。
- 再决定是否需要把“工具不可见”与普通工具执行失败区分为不同错误类型；目前该提示仍以工具输出返回。

---

## I-2（已解决）缺少目录列举与内容检索工具，仓库探索只能靠猜路径

**状态**：已在 `14800f2` 增加只读 `list_dir`、`glob`、`grep` 工具，并纳入 `files` 分类。

**现象**
本会话中无法列出目录内容，也无法按文件名/内容检索；`read_file` 对目录直接报错，只能按**精确路径**逐个尝试。为摸清结构，本次累计猜了 40+ 个路径。

**证据**
- `internal/tool/registry.go` → `NewDefaultRegistry` 仅注册：`read_file`、`write_file`、`edit_file`、`command`、`web_search`、`web_fetch`。无 `list_dir` / `glob` / `grep`。
- `internal/tool/workspace.go` → `resolveWorkspacePath` 明确要求路径“**name a file**”。
- `command` 理论上可 `ls`/`grep`，但在 `workspace` 模式需逐次批准，`read-only` 模式直接禁止（`internal/tool/permissions.go`）。

**影响**
“理解项目结构 / 定位代码”类任务效率极低且不可靠；对只读探索场景（Plan 模式）尤其明显。

**建议**
- 增设只读工具：`list_dir`、`glob`（文件名匹配）、`grep`（内容检索），归入 `files` 分类，并在 `read-only` 模式下放行。
- 或允许 `read_file` 对目录路径返回条目列表而非报错。

---

## I-3（已解决）目录场景的错误信息歧义

**状态**：已在 `14800f2` 区分目录路径、越界路径和越界符号链接的错误信息。

**现象**
对目录调用 `read_file` 时返回：

```
path must stay inside the workspace and name a file
```

把“目标是目录（不支持）”与“路径越界”混为一谈，容易让人误以为发生了越界。

**证据**
- `internal/tool/workspace.go`：
  `if !pathWithin(root, candidate) || candidate == root { return "", errors.New("path must stay inside the workspace and name a file") }`

**建议**
区分两种情形并给出不同错误：目录 → `path refers to a directory; listing is not supported`；越界 → `path escapes the workspace`。

---

## I-4（已解决）自定义注册表的默认权限档位是 full-access

**状态**：自定义 Registry 默认改为 `read-only`；内置 Registry 继续显式使用 `workspace`。权限系统现有 `read-only`、`workspace`、`ask`、`full-access` 四档。

**现象**
`NewRegistry` / `NewCategorizedRegistry` 内部默认 `PermissionModeFullAccess`，仅 `NewDefaultRegistry` 末尾显式改为 `PermissionModeWorkspace`。

**影响**
若有人构造自定义 registry 却忘记设置模式，会静默地以 full-access 运行（无批准）。属于易踩的“安全默认”问题。

**证据**
- `internal/tool/registry.go`：`NewRegistry`、`NewCategorizedRegistry` 初始化 `newPermissionSettings(PermissionModeFullAccess)`；`NewDefaultRegistry` 末尾 `SetPermissionMode(PermissionModeWorkspace)`。

**处理**
通用构造器默认使用 `PermissionModeReadOnly`；需要更高权限的调用方应显式调用 `SetPermissionMode`。内置 `NewDefaultRegistry` 仍使用 `PermissionModeWorkspace`。

---

## 附：未能确认的项
- I-1 的根因（是产品逻辑清空了类别选择，还是宿主/运行环境在轮次间重置了 `select_tool_category` 的选择）本次无法在只读模式下确证，需要日志或可复现步骤进一步定位。
