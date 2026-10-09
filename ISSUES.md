# ISSUES.md

运行期遇到的问题记录（由在 lune agent 内以 Plan 模式执行任务时观察到）。
Context: observed while running inside the lune agent in **plan mode**, with the
`files` category selected (read-only tool set).

> 说明：以下为**观察记录**，不都对已确证的产品缺陷；部分可能来自宿主/运行环境。已在“证据”里标注对应源码位置。

---

## I-1 工具在会话中途变为不可用，根因尚未定位

**状态**：`719eb47` 已将提示改为可操作说明，并增加诊断日志；工具可见性丢失的根因仍未确认，继续跟踪。

**现象**
同一批任务中，`read_file` 连续两次返回：

```
Tool is not available in the current request.
```

而这些路径此前刚成功读取过（例如 `go.mod`、`cmd/main.go`）。重新调用 `select_tool_category` 选择 `files`（或 `command`）后即恢复。

**证据**
- `internal/agentgraph/agent.go` 和 `internal/model/model.go` 都会检查模型调用是否属于本轮可见工具集合；不属于时返回普通工具输出，而不是执行工具。
- `unavailableToolMessage` 会在已知工具分类时提示调用 `select_tool_category`，并在无法归类时提示检查当前可用工具。
- `internal/agentgraph/agent.go` 和 `internal/model/model.go` 会记录所选分类、可见工具及预期分类，便于排查；当前源码不再使用旧提示 `Tool is not available in the current request.`。

**影响**
- 分类按需发现（staged discovery）下，仍观察到“上一轮可用、这一轮不可用”；当前提示能引导重新选择分类，但还不能解释可见性状态为何变化。
- 该分支以**普通工具输出**回传，而非 `Result.Err`；模型可据提示恢复，但上层仍不能把“工具不可见”与“执行失败”直接区分。
- `VisibleTools` 每轮 tool batch 后会按设计清空，可见性由 `SelectedCategory` + `PlanMode` 重新计算。新增日志包含本轮可见性快照，但用户界面不直接展示这些诊断信息。

**已处理**
- 不可见工具的提示现在会说明应如何选择工具分类；诊断日志记录分类和可见工具名，不记录工具参数或结果。

**待查**
- 复现并定位 `State.SelectedCategory` / `VisibleTools` 在跨轮次、分支或恢复时与模型预期不一致的原因。
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
