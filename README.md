# Lune

`luneAgent` 是 Agent 引擎 `luneCore` 的三个前端宿主：全屏 TUI、行式 CLI 与 Windows 桌面端。前端只负责展示、输入与本地设置；Agent 图编排、模型调用、工具执行、会话持久化与权限沙箱都在 `luneCore` 中完成。

## 前端与数据位置

| 前端 | 入口 | 会话数据 | 配置来源 |
| --- | --- | --- | --- |
| TUI（Bubble Tea v2） | `cmd/main.go`，默认在交互终端启动 | 项目内 `.luneagent/` | `%APPDATA%\luneAgent\config.json` |
| 行式 CLI | `cmd/main.go`，`--cli` 或非 TTY 环境 | 项目内 `.luneagent/` | 同上 |
| 桌面端（Wails v3 + Svelte 5） | `cmd/desktop/main.go` | `%APPDATA%\luneAgent\desktop\<project-hash>` | `%APPDATA%\luneAgent\settings.db` |

TUI/CLI 与桌面端的会话和配置彼此独立，互不迁移。工作区外目录读取、命令执行与 Web 工具的授权规则见 [`AGENTS.md`](AGENTS.md)。

## 快速开始

需要 Go 1.27.3；桌面端另需 Node.js 22.12+、npm 与固定版本的 Wails CLI（`wails3@v3.0.0-beta.34`）。

```powershell
# TUI / CLI：生成 bin/lune.exe 与 bin/lune-sandbox.exe
$env:GOWORK = 'off'
./scripts/build.ps1
./scripts/install.ps1      # 安装到 %LOCALAPPDATA%\Programs\luneAgent\bin 并加入 PATH

# 桌面端：生成 bin/lune-desktop.exe
./scripts/build-desktop.ps1
```

```bash
# Linux：本机构建 + amd64/arm64 交叉构建，产物在 bin/ 与 bin/linux-<arch>/
./scripts/build-linux.sh
```

无桌面服务器可在 provider 中设置 `"apiKeyEnv": "LUNE_DEEPSEEK_API_KEY"`，由启动环境提供密钥；配置文件只保存变量名，无需 Secret Service。配置与启动示例见[用户配置与凭证](docs/maintenance.md#用户配置与凭证)。

所有会产出仓库二进制文件的 Go 构建都必须传入 `-trimpath`，脚本、文档与测试辅助代码保持一致。

## 验证

```powershell
$env:GOWORK = 'off'
go vet -unsafeptr=false ./...
go test -trimpath ./...
```

Linux CI 验证 CLI/TUI/A2A 相关包（`internal/a2a`、`internal/assistanttext`、`internal/app`、`internal/cli`、`internal/tui/...`、`internal/settings`、`internal/slashcmd`、`internal/workspacereview`、`internal/filemention`、`internal/goalcmd`、`internal/terminaltext`、`internal/toolchains`）并交叉构建二进制；桌面端需要 GTK4/WebKitGTK 开发依赖，仅在 Windows 构建与验证。完整命令见 [`docs/maintenance.md`](docs/maintenance.md)。

## 文档

- [`TUI.md`](TUI.md)：TUI 界面、快捷键与交互规范
- [`docs/tui-core.md`](docs/tui-core.md)：TUI 与 Core 的边界、状态所有权
- [`docs/desktop.md`](docs/desktop.md)：桌面端构建、设置与布局
- [`docs/maintenance.md`](docs/maintenance.md)：依赖更新、验证命令、生成文件与发布
- [`docs/a2a.md`](docs/a2a.md)：A2A 权限、认证、任务生命周期与交互合同
- [`docs/mcp.md`](docs/mcp.md)：外部 MCP 服务器配置
- [`AGENTS.md`](AGENTS.md)：设计准则、权限档位与仓库约定

## 非目标

不实现 skill（技能）机制：没有 `/skill` 命令、没有 skill 加载器、没有 `skills/*.md` 目录约定。能力扩展走既有的工具注册表与工具分类（`select_tool_category`），Plan 模式通过工具元数据把可见工具集裁剪为只读工具。

旧执行存储升级前请停止所有实例，运行 `lune migrate --state-dir <状态目录>`。升级保留历史、设置和已记录用量，备份后作废旧未完成任务；操作说明见[工程维护指南](docs/maintenance.md#执行存储升级)。
