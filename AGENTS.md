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

- Register each tool with metadata for its category, permission class, and read-only behavior; an empty category means the tool is not discovered by category. Keep permission decisions and Plan mode filtering based on this metadata rather than tool names.
  注册工具时统一声明类别、权限类别和只读属性；类别为空表示该工具不参与按类别发现。权限判断和 Plan 模式筛选均读取元数据，不按工具名称判断。
- Unclassified tools are blocked in `read-only` and `workspace` modes; `workspace` does not prompt for missing metadata. `ask` prompts for unclassified tools, while `full-access` allows every registered tool.
  未分类工具在 `read-only` 和 `workspace` 模式下会被阻止；`workspace` 不会为缺失元数据弹出授权。`ask` 会请求授权，`full-access` 允许所有已注册工具。
- Keep the Chinese and English text in this file in sync.
  本文件的中英文需保持同步。
