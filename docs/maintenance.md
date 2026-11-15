# 工程维护

本文是 Lune（前端宿主仓）的维护入口。Go 版本以根目录 `go.mod` 的 `go` 指令为准。

## Go 模块与工作区

`lune` 通过 `go.mod` 固定 `luneCore` 版本。开发者可以在本机使用未跟踪的 `go.work` 联调兄弟目录中的 `luneCore`；提交前用 `GOWORK=off` 验证，确保验证的是 `go.mod` 固定的版本。根目录的 `go test ./...` 只测试当前前端仓库，不会运行 `luneCore` 的测试。

在 Windows 上运行完整的 Lune 验证：

```powershell
$env:GOWORK = 'off'
go vet -unsafeptr=false ./...
go test -trimpath ./...
./scripts/build.ps1
```

Windows 上的 vet 仅关闭 `unsafeptr` 分析器：ConPTY 测试按 Win32 API 契约将不透明 `HPCON` 句柄作为 `lpValue` 传入；其余分析器仍会检查全部 Go 包。

提交前运行 `gofmt -l .` 确认输出为空，需要格式化时运行 `gofmt -w .`。若本机同时检出 `luneCore`，可单独运行其测试：

```powershell
go -C ..\luneCore test -trimpath -count=1 -p 1 ./...
```

在 Linux 上验证全部包：

```bash
GOWORK=off go vet ./...
GOWORK=off go test -trimpath ./...
```

Linux 本机运行 `./scripts/build-linux.sh` 会默认使用 `go.mod` 固定的 `luneCore` 版本，在 `bin/` 生成本机程序，并在 `bin/linux-amd64`、`bin/linux-arm64` 生成对应交叉构建产物；需要联调本地 `luneCore` 时，可显式设置 `GOWORK`。如果 Go 不在 `PATH`，可通过 `GO=/path/to/go` 或 `GOROOT=/path/to/go-root` 指定安装位置。

## 依赖更新

先查看可更新模块，再在单模块模式更新根模块依赖。不要把本地目录写入 `go.mod`；需要联调时用本机 `go.work`：

```powershell
$env:GOWORK = 'off'
go list -m -u all
go get -u ./...
go get github.com/afterlune/luneCore@main
go mod tidy
```

如果所选依赖要求更高的 Go 版本，应同步提高 `go.mod` 的 `go` 指令和 `deploy/Dockerfile.a2a` 的构建镜像版本。

## 用户配置与凭证

TUI/CLI 的用户配置在 Windows 为 `%APPDATA%\lune\config.json`，Linux 通常为 `~/.config/lune/config.json`（遵循 `XDG_CONFIG_HOME`）。读取时会收紧该文件权限（0600），并按平台保护 provider 的 `apiKey`：

- Windows：明文 `apiKey` 会被 DPAPI 加密为 `apiKeyEncrypted` 后写回，启动时在内存中解密再交给 Core，文件本身不再保存明文密钥。
- Linux：Secret Service 可用时，密钥保存在系统凭证库中，配置文件只保存随机引用；启动时在内存中恢复密钥再交给 Core。无桌面凭证服务或写入失败时，保留明文配置并在 stderr 提示风险，文件权限仍限制为当前用户。
- 其它平台：`apiKey` 保持明文；启动时会在 stderr 提示风险，该平台需要自行限制配置文件权限。

无桌面服务器可使用 `apiKeyEnv`，将 provider 的 `apiKey` 替换为环境变量引用，例如：

```json
{
  "protocol": "responses",
  "baseURL": "https://api.deepseek.com",
  "apiKeyEnv": "LUNE_DEEPSEEK_API_KEY",
  "serverCompaction": true
}
```

所有平台均支持该字段。启动时变量值只会注入传给 Core 的内存配置；配置文件始终保存变量名。仅使用该来源时会跳过系统凭证库探测，并且不会产生明文配置警告。配置中的每个显式环境变量引用都必须可读取且非空；引用缺失、值为空或同时配置其它非空密钥来源时，启动会报告 provider 和字段/变量名。允许保留空的 `apiKey` 字段。

在 Bash 中，可通过隐藏输入提供当前会话的密钥，避免把值写入命令历史：

```bash
IFS= read -r -s -p 'Provider API key: ' LUNE_DEEPSEEK_API_KEY
printf '\n'
export LUNE_DEEPSEEK_API_KEY
./bin/lune
```

无人值守部署由 CI、容器或服务管理器的凭证机制在启动前注入同名环境变量。启动脚本继承该环境即可，例如：

```bash
#!/usr/bin/env bash
exec /opt/lune/bin/lune "$@"
```

每次启动重新读取变量，因此轮换密钥只需更新启动环境。环境变量本身不提供磁盘加密，部署系统负责管理其来源。

## 生成文件和仓库卫生

`bin/`、`agent.log`、`sessions.db` 与项目工作区下的 `.lune/` 属于构建产物或运行数据，应由 `.gitignore` 排除，不要提交到仓库。

## 大文件维护

按职责、状态所有权和变化原因拆文件，不设机械行数上限。拆分时保持外部命令、Go API 与运行行为不变；测试跟随清晰的功能边界移动，职责集中的测试无需仅为缩短文件而拆分。

`internal/tui` 的状态所有权边界：`usage.go` 管会话用量与上下文窗口，`view_cache.go` 管转录渲染缓存与脏标记，`modal_state.go` 管互斥的模态浮层与优先级，`composer_state.go` 管输入框、补全、历史、选区与复制反馈，`run_state.go` 管任务运行态（运行标志、Core 任务快照、队列、流式缓冲与载入冻结）；`tuiModel` 只保留跨域状态（会话身份、任务交互卡片、注意力提醒与主题设置）。新增字段先决定归属，再决定放哪个文件。斜杠命令的名称、帮助文案、目标前端、是否需要会话与运行中可用性统一放在无 UI 依赖的 `internal/slashcmd`，CLI 与 TUI 两个前端只保留命令派发与子命令特例。

当前边界如下：`internal/app/app.go` 负责初始化 Core 并按运行模式分发至行式 CLI（`internal/cli`）、全屏 TUI（`internal/tui`）或 A2A 服务端（`internal/a2a`）；`internal/cli/core_cli.go` 保留行式 CLI 的主循环与交互协调，CLI 执行、授权、状态查询、进程操作、会话操作和项目集成各有独立文件。`internal/settings/store.go` 负责 SQLite 初始化，保存 TUI 主题与 `/toolchains` 宿主工具链设置。`internal/tui` 保留共享 `tuiModel` 的交互协调；工作区文件提及和 Markdown／计划标签处理分别位于独立子包，渲染主题通过调色板参数传入。新增职责优先归入对应模块；若需要跨模块传递大量可变状态，应先收敛状态所有权，而不是只移动函数。

## 安全检查与命令测试

Core CI 固定使用 `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`，升级 Go 时同步验证分析器能识别新语法。Linux 上单独运行 Core 全量验证时使用 `GOWORK=off`，并运行 `go test -race -trimpath -p 1 ./internal/runtime ./internal/tool ./internal/command ./internal/mcp`。沙箱集成测试自动构建带 `-trimpath` 的 helper，并为 Go 工具链授权完整安装根目录。

A2A 的权限、远程部署和交互合同见 [A2A 服务](a2a.md)。

## 执行存储升级

新执行引擎使用 `execution-format` 标记和 `executions-v2.db`。旧状态目录不会在正常启动时隐式升级。
停止所有使用该目录的 Lune 实例后执行：

```bash
lune migrate --state-dir /absolute/path/to/.lune
```

命令不需要模型配置或凭证。升级先取得独占锁，用 SQLite `VACUUM INTO` 在目录内创建私有的
`upgrade-backup-*` 备份，再转换会话与清理执行状态，最后写入版本标记。
历史、设置、标题和已记录用量保留；旧未完成任务被标记为 discarded，不再自动重放。
未执行的排队输入仍在备份中，可以人工查看后重新提交。旧 checkpoint 文件保留不动，provider
上下文由历史重建；活动目标变为暂停，需要明确恢复。失败时保留备份并允许重试。
不要让旧二进制重新打开升级后的目录，也不要在运行实例存在时替换数据库文件。

Core 的 `Close(ctx)`、`SessionRuntime.Close(ctx)`、`Interrupt(ctx, id)` 和
`ResolveApprovalDecision(ctx, id, decision)` 均接收调用上下文。`Interrupt` 返回 `(accepted, error)`；
布尔授权接口已删除，调用方明确选择本次允许、会话允许或拒绝。进入资源关闭阶段后，超时只结束调用方等待，单一清理过程继续运行；MCP 操作和 transport 退出前保持存储打开，完成后自动关闭存储。可再次调用 Close 等待同一清理结果。关闭期间禁止接受新工作。自定义 provider/tool 仍须配合 context 取消。

发布此跨仓库改动时，先发布 luneCore，再更新 Lune 的固定依赖版本并运行 `GOWORK=off` 检查。
本地联调允许使用未跟踪的 go.work；不要提交指向本地目录的 replace，也不要伪造未发布的 pseudo-version。

队列协调或结果提交失败时会停止 drain，并通过 WaitTurn 返回临时错误；队列和 checkpoint 保留，
没有自动重试计时器。修复存储问题后，重新打开会话或重新调用 WaitTurn 触发协调；已完成的
checkpoint 会补提交结果，不重新执行模型或已完成的工具。
