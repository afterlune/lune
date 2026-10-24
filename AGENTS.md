# AGENTS.md

Guidance for contributors and AI agents working in this repository.
本文件写给本仓库的贡献者与 AI agent。

## 设计准则

以下五条是用户定下的长期准则，是判断"该不该这么改"的依据；本文件其余规则都由它们派生。
与其它规则冲突时，以准则为准。

1. **实用性高于象征性**
   不为了"看起来像框架""概念完整""和某项目对齐"而增加能力。任何抽象、接口和功能，都必须解决真实问题。
2. **简洁优雅高于复杂完备**
   在满足需求的前提下，优先选择更少的概念、更少的状态、更少的配置、更短的调用链。复杂度必须有明确收益，否则删除。
3. **正确设计高于向后兼容**
   当出现明显更好的实现时，允许并鼓励破坏性更新。不得为了兼容旧 API、旧数据结构或旧行为而永久保留次优设计。
4. **兼容成本不得污染核心**
   如果兼容旧版本会提高核心复杂度，则放弃兼容。必要的迁移成本应由版本升级、迁移工具或调用方承担，而不是由核心长期背负。
5. **不为假想需求设计**
   不因为"未来可能需要"提前增加抽象。只有真实用例或已经出现的结构性问题，才有资格推动核心设计变化。

## Non-goals / 非目标

### Skill / 技能机制

- **English:** The skill mechanism is explicitly **out of scope**. Do not implement a skill layer:
  no `/skill` (or `/skills`) command, no skill loader, no `skills/*.md` (or similar) directory
  convention, and no skill-based capability extension.
- **中文：** 明确**不实现** skill（技能）机制。不新增 skill 层：不提供 `/skill`（或 `/skills`）
  命令、不实现 skill 加载器、不引入 `skills/*.md`（或类似）目录约定，也不做任何基于 skill 的能力扩展。

Capability extension must go through the existing mechanisms instead:
能力扩展请改用既有机制：

- Tool registry plus tool categories with on-demand discovery (`select_tool_category`)
  — see `luneCore/internal/tool` and `luneCore/internal/agentgraph` in the Core module.
  工具注册表 + 工具分类按需发现（`select_tool_category`）—— 见 Core 模块中的 `luneCore/internal/tool`、`luneCore/internal/agentgraph`。
- Plan mode restricts the visible tool set to read-only tools — see `luneCore/internal/agentgraph` in the Core module.
  Plan 模式将可见工具集裁剪为只读工具 —— 见 Core 模块中的 `luneCore/internal/agentgraph`。

## Permission modes / 权限档位

The tool permission setting has four modes:
工具权限设置有四档：

- `read-only`: workspace reads are allowed; reads outside the workspace, web tools, and unclassified tools require approval; writes and commands are blocked.
  `read-only`：默认允许读取工作区；读取工作区外目录、web 工具及未分类工具需授权；写入与命令会被阻止。
- `workspace`: workspace reads and writes are allowed; external filesystem access, commands, web tools, and unclassified tools require approval.
  `workspace`：默认允许读取和写入工作区；工作区外文件访问、命令、web 工具及未分类工具需授权。
- `auto`: based on `workspace` permissions with automatic model review; safe operations and commands run without prompts, while risky actions or review failures prompt for approval.
  `auto`：基于 `workspace` 档位并增加模型自动审查；安全操作与命令自动放行无需弹窗，有风险或审查失败的操作弹出交互式授权。
- `full-access`: registered tools run without approval and may use current-user filesystem access; extremely dangerous commands are always blocked.
  `full-access`：已注册工具均无需授权，可访问当前用户有权访问的文件；极度危险命令始终被阻止。

Custom registries default to `read-only`; the built-in registry explicitly uses `workspace`. In the TUI, `/permissions` opens the permission selector and `/permissions <mode>` selects a mode directly; `Shift+Tab` toggles Plan mode instead of cycling permissions.
自定义 Registry 默认使用 `read-only`；内置 Registry 显式使用 `workspace`。TUI 中 `/permissions` 打开权限选择器，`/permissions <mode>` 可直接选择权限档位；`Shift+Tab` 用于切换 Plan 模式，不再循环权限档位。

Plan mode locks the active permission mode to `read-only` for its duration. The TUI remembers the previous mode and restores it when Plan mode ends; `/permissions` cannot raise access while Plan mode is active. `Shift+Tab` or `/plan` exits Plan mode and restores the previous mode. Choosing an implementation option in plan review also exits Plan mode and restores the previous mode before implementation starts. External-directory reads and read-only web tools still require approval.
Plan 模式期间会将当前权限档位锁定为 `read-only`。TUI 会记住进入前的档位，并在退出 Plan 模式时恢复；Plan 模式期间，`/permissions` 不能提高权限。按 `Shift+Tab` 或执行 `/plan` 会退出 Plan 模式并恢复之前的档位。在计划审查中选择实施时，也会先退出 Plan 模式并恢复之前的档位，再启动实施。工作区外目录读取和只读 web 工具仍需授权。

Approved commands and commands run under `full-access` have network access enabled by default. `full-access` is also supported when running as root in container environments (e.g. Docker, Podman, WSL). When sandboxing causes container conflicts or native execution is preferred, the OS sandbox can be bypassed using the `--no-sandbox` flag or `LUNE_SANDBOX=off` environment variable.
已批准的命令与 `full-access` 模式下的命令默认允许访问网络。在容器环境（如 Docker、Podman、WSL）中以 root 运行时亦支持 `full-access` 模式。当沙箱产生容器环境冲突或需要原生执行时，可通过 `--no-sandbox` 参数或 `LUNE_SANDBOX=off` 环境变量跳过 OS 沙箱。

## Conventions / 约定

- Every Go build that produces a repository binary must pass `-trimpath`, including development builds and test helpers that compile executables. Keep scripts and documented build commands consistent.
  所有会生成仓库二进制文件的 Go 构建均需传入 `-trimpath`，包括开发构建和会编译可执行文件的测试辅助代码。脚本与文档中的构建命令需保持一致。
- Register each tool with metadata for its category, permission class, and read-only behavior; an empty category means the tool is not discovered by category. Keep permission decisions and Plan mode filtering based on this metadata rather than tool names.
  注册工具时统一声明类别、权限类别和只读属性；类别为空表示该工具不参与按类别发现。权限判断和 Plan 模式筛选均读取元数据，不按工具名称判断。
- Unclassified tools require approval in `read-only`, `workspace`, and `auto` modes; `full-access` allows every registered tool.
  未分类工具在 `read-only`、`workspace` 和 `auto` 模式下需交互授权；`full-access` 允许所有已注册工具。
- Approved tool operations (both interactively approved and auto-reviewed) are remembered for the session; subsequent identical tool calls within the same session do not require re-approval.
  已授权的工具操作（包括交互式授权与自动审查放行）在会话中予以记录；同会话中后续相同操作无需重复授权。
- Built-in file tools can request a session-scoped directory grant for paths outside the workspace. A grant covers that directory and descendants with either `read` or `read_write` access; a write grant also covers reads. Denials are not remembered, grants persist when reopening the same session, and new or forked sessions do not inherit them. Deleting a session deletes its grants. There is no separate grant-management UI. These grants apply only to built-in file tools, never to shell commands.
  内置文件工具可为工作区外路径请求会话级目录授权。授权范围覆盖指定目录及其子目录，权限为 `read` 或 `read_write`；读写授权也包含读取。拒绝不会被记住；重新打开同一会话时授权继续有效；新会话和分叉会话不继承授权。删除会话即删除授权，不另设授权管理界面。目录授权只作用于内置文件工具，不作用于 Shell 命令。
- Commands that can be approved interactively are not permanently denied just because they match a high-risk pattern. A narrow extreme-danger denylist remains blocked in every mode, including `full-access`; non-interactive plan execution still requires exact authorization for high-risk commands.
  可交互授权的命令不会仅因匹配一般高风险模式而永久禁止。少数极度危险命令在所有档位（包括 `full-access`）下始终被阻止；非交互计划执行仍要求对高风险命令逐条精确授权。
- Follow [the engineering maintenance guide](docs/maintenance.md) for dependency updates, validation commands, generated files, and release builds.
  依赖更新、验证命令、生成文件和发布构建按[工程维护指南](docs/maintenance.md)执行。
- Keep the Chinese and English text in this file in sync.
  本文件的中英文需保持同步。
