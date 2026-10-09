# ISSUES.md

运行期遇到的问题记录（由在 lune agent 内以 Plan 模式执行任务时观察到）。
Context: observed while running inside the lune agent in **plan mode**, with the
`files` category selected (read-only tool set).

> 说明：以下为**观察记录**，不都对已确证的产品缺陷；部分可能来自宿主/运行环境。已在“证据”里标注对应源码位置。

---

## I-1 工具在会话中途变为不可用，且提示信息误导

**状态**：已在 `719eb47` 增加可操作提示和脱敏诊断日志；类别选择丢失的根因仍未复现，继续跟踪。

**现象**
同一批任务中，`read_file` 连续两次返回：

```
Tool is not available in the current request.
```

而这些路径此前刚成功读取过（例如 `go.mod`、`cmd/main.go`）。重新调用 `select_tool_category` 选择 `files`（或 `command`）后即恢复。

**证据**
该字符串有两处来源，均为“调用名不在当前可见工具集合内”时的兜底输出：
- `internal/agentgraph/agent.go` → `executeTool`：
  `if !contains(state.VisibleTools, call.Name) { ... NewToolOutputText("Tool is not available in the current request.") }`
- `internal/model/client.go` → `RespondWithOptions`：
  `else if _, visible := visibleToolNames[call.Name]; !visible { ... }`

**影响**
- 分类按需发现（staged discovery）下，会出现“上一轮可用、这一轮不可用”的能力丢失；调用方（模型/用户）无法判断是“需重新选类目”还是“工具不存在”。
- 该失败以**普通工具输出**回传，而非错误（`Result.Err`），上层无法据此区分，容易导致反复重试或静默降级。
- 触发点不透明：`VisibleTools` 每轮 tool batch 后会置空（`state.VisibleTools = nil`），可见性由 `SelectedCategory` + `PlanMode` 共同决定，缺少可观测性。

**建议**
- 使该分支的文本**可操作**，例如：
  `Tool "read_file" is not visible; call select_tool_category to reveal category "files".`
- 或将其作为错误回传（`Result{Err: ...}`）以便上层区分“未可见”与“执行失败”。
- 排查 `State.SelectedCategory` / `VisibleTools` 在跨轮次（尤其新建会话 / 分支 / 恢复）时是否被意外清空，并在 UI/日志中暴露“当前可见工具集合”。

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
