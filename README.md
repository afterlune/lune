# Lune

`Lune` 是 Agent 引擎 [`luneCore`](https://github.com/afterlune/luneCore) 的宿主前端：全屏 TUI、行式 CLI 与 A2A 1.0 JSON-RPC 服务端。前端只负责展示、输入与本地设置；Agent 图编排、模型调用、工具执行、会话持久化与权限沙箱都在 `luneCore` 中完成。

## 前端与数据位置

| 前端 | 入口 | 会话数据 | 配置来源 |
| --- | --- | --- | --- |
| TUI（Bubble Tea v2） | `cmd/main.go`，默认在交互终端启动 | 项目内 `.lune/` | `%APPDATA%\lune\config.json` |
| 行式 CLI | `cmd/main.go`，`--cli` 或非 TTY 环境 | 项目内 `.lune/` | 同上 |
| A2A 服务端 | `cmd/main.go`，`--a2a-listen <addr>` | 项目内 `.lune/` | 同上 |

工作区外目录读取、命令执行与 Web 工具的授权规则见 [`AGENTS.md`](AGENTS.md)。本地外观与已授权宿主工具链设置保存在 `%APPDATA%\lune\settings.db`（Linux 为 `${XDG_CONFIG_HOME:-~/.config}/lune/settings.db`）。

## 快速开始

需要 Go 1.27.3。

```powershell
# 生成 bin/lune.exe 与 bin/lune-sandbox.exe
$env:GOWORK = 'off'
./scripts/build.ps1
./scripts/install.ps1      # 安装到 %LOCALAPPDATA%\Programs\lune\bin 并加入 PATH
```

```bash
# Linux：本机构建 + amd64/arm64 交叉构建，产物在 bin/ 与 bin/linux-<arch>/
./scripts/build-linux.sh
./scripts/install-linux.sh  # 安装到 ~/.local/bin；配置写入 XDG_CONFIG_HOME 或 ~/.config/lune
```

无桌面服务器可在 provider 中设置 `"apiKeyEnv": "LUNE_DEEPSEEK_API_KEY"`，由启动环境提供密钥；配置文件只保存变量名，无需 Secret Service。配置与启动示例见[用户配置与凭证](docs/maintenance.md#用户配置与凭证)。

所有会产出仓库二进制文件的 Go 构建都必须传入 `-trimpath`，脚本、文档与测试辅助代码保持一致。

## 验证

```powershell
$env:GOWORK = 'off'
go vet -unsafeptr=false ./...
go test -trimpath ./...
```

仓库不配置 CI，提交前请在本机运行上述验证，完整命令见 [`docs/maintenance.md`](docs/maintenance.md)。

## 按需 Lune A2A 容器

`deploy/docker-compose.a2a.yml` 提供一个常驻轻代理。它在专用内网发布 A2A 1.0 Bearer 端点；收到请求后才启动原生 `lune --a2a-listen` worker。活动或等待交互的任务会保持 worker 运行，所有任务终态后一小时才卸载。A2A task 表是进程内状态，异常重启时代理不会自动重放结果不明的请求。

先复制 `deploy/lune-runtime.config.example.json` 为被 Git 忽略的 `deploy/lune-runtime.config.json`，再复制 `.env.example` 为 `.env`，设置随机 `LUNE_A2A_TOKEN` 和 `LUNE_DEEPSEEK_API_KEY`。Windows DPAPI 配置不能由 Linux 容器解密，因此此服务使用自己的 Linux provider 配置。默认工作目录绑定为 `D:\WorkSpace:/workspace`，只使用 Lune `workspace` 权限；会话状态写在 `D:\WorkSpace\.lune`，全局设置保存在 Compose named volume。

```powershell
docker compose --env-file .env -f deploy/docker-compose.a2a.yml up -d --build
docker compose -f deploy/docker-compose.a2a.yml ps
```

Compose 创建 `moonfei-lune-a2a` 内部网络，不发布宿主机端口。MoonFei 通过该网络连接 `http://lune-a2a:18091`；需在 MoonFei `.env` 设置 `MOONFEI_A2A_INTERNAL_HTTP_ORIGINS=http://lune-a2a:18091`，再在网页「设置 → 外部协议扩展」添加 Lune 节点并保存同一 Bearer 令牌。容器带 Git、Bash、Go 1.27.3、Python 3 和 Java 17/Maven；Flutter/Android SDK 未包含。

## 文档

- [`TUI.md`](TUI.md)：TUI 界面、快捷键与交互规范
- [`docs/tui-core.md`](docs/tui-core.md)：TUI 与 Core 的边界、状态所有权
- [`docs/maintenance.md`](docs/maintenance.md)：依赖更新、验证命令、生成文件与发布
- [`docs/a2a.md`](docs/a2a.md)：A2A 权限、认证、任务生命周期与交互合同
- [`docs/mcp.md`](docs/mcp.md)：外部 MCP 服务器配置
- [`AGENTS.md`](AGENTS.md)：设计准则、权限档位与仓库约定

## 非目标

不实现 skill（技能）机制：没有 `/skill` 命令、没有 skill 加载器、没有 `skills/*.md` 目录约定。能力扩展走既有的工具注册表与工具分类（`select_tool_category`），Plan 模式通过工具元数据把可见工具集裁剪为只读工具。

旧执行存储升级前请停止所有实例，运行 `lune migrate --state-dir <状态目录>`。升级保留历史、设置和已记录用量，备份后作废旧未完成任务；操作说明见[工程维护指南](docs/maintenance.md#执行存储升级)。

## 开源协议

本项目采用 [MIT 许可证](LICENSE)。
