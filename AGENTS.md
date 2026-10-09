# AGENTS.md

Guidance for contributors and AI agents working in this repository.
本文件写给本仓库的贡献者与 AI agent。

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
  — see `internal/tool` and `internal/agentgraph`.
  工具注册表 + 工具分类按需发现（`select_tool_category`）—— 见 `internal/tool`、`internal/agentgraph`。
- Plan mode restricts the visible tool set to read-only tools — see `internal/agentgraph`.
  Plan 模式将可见工具集裁剪为只读工具 —— 见 `internal/agentgraph`。

## Permission modes / 权限档位

The tool permission setting has four modes:
工具权限设置有四档：

- `read-only`: workspace reads are allowed; web tools require approval; writes and commands are blocked.
  `read-only`：允许读取工作区文件；web 工具需授权；写入与命令会被阻止。
- `workspace`: workspace file operations are allowed; commands and web tools require approval.
  `workspace`：允许工作区文件操作；命令和 web 工具需授权。
- `ask`: workspace reads are allowed; every other tool requires approval.
  `ask`：允许读取工作区文件；其他工具均需授权。
- `full-access`: all registered tools run without approval; commands can access paths outside the workspace.
  `full-access`：所有已注册工具均无需授权；命令可以访问工作区以外的路径。

Custom registries default to `read-only`; the built-in registry explicitly uses `workspace`. In the TUI, `Shift+Tab` cycles through the four modes and `/permissions <mode>` selects one directly.
自定义 Registry 默认使用 `read-only`；内置 Registry 显式使用 `workspace`。TUI 中按 `Shift+Tab` 循环切换四档，也可用 `/permissions <mode>` 直接选择。

Plan mode is independent of the permission setting: it limits the visible tools to read-only tools, while the active permission mode can still require approval for a tool such as web access.
Plan 模式与权限档位相互独立：Plan 模式只向模型提供只读工具；当前权限档位仍可能要求对 web 访问等工具进行授权。

## Conventions / 约定

- Register new built-in tools in `internal/tool`, assign a tool category, and explicitly review how the tool is classified in `internal/tool/permissions.go` and `internal/agentgraph`.
  新增内置工具请在 `internal/tool` 注册并归入工具分类，同时检查它在 `internal/tool/permissions.go` 和 `internal/agentgraph` 中的权限与只读分类。
- Until tool permission metadata is unified, do not assume an unknown tool will require approval: `workspace` currently explicitly gates commands and web tools, while other tool names can pass through without approval. Track this gap in `ROADMAP.md`.
  工具权限元数据统一前，不要假设未知工具一定会要求授权：当前 `workspace` 档明确拦截命令和 web 工具，其他工具名可能无需授权即可通过。此缺口由 `ROADMAP.md` 跟踪。
- Keep the Chinese and English text in this file in sync.
  本文件的中英文需保持同步。
