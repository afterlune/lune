# 工程维护

本文是 luneAgent 的维护入口。Go 版本以根目录 `go.mod` 的 `go` 指令为准；桌面前端使用 Node.js 22.12+、npm 和仓库中的 `package-lock.json`。

## Go 模块与工作区

`luneAgent` 通过 `go.mod` 固定 luneCore 版本。开发者可以在本机使用未跟踪的 `go.work` 联调兄弟目录中的 luneCore；提交前用 `GOWORK=off` 验证，确保验证的是 `go.mod` 固定的版本。根目录的 `go test ./...` 只测试 luneAgent，不会运行 luneCore 的测试。

在 Windows 上运行完整的 luneAgent 验证：

```powershell
$env:GOWORK = 'off'
go vet -unsafeptr=false ./...
go test -trimpath ./...
./scripts/build.ps1
```

Windows 上的 vet 仅关闭 `unsafeptr` 分析器：ConPTY 测试按 Win32 API 契约将不透明 `HPCON` 句柄作为 `lpValue` 传入；其余分析器仍会检查全部 Go 包。

提交前运行 `gofmt -l $(git ls-files '*.go')` 确认输出为空，需要格式化时运行 `gofmt -w`。若本机同时检出 luneCore，可单独运行其测试：

```powershell
go -C ..\luneCore test -trimpath -count=1 -p 1 ./...
```

桌面 GUI 目前只在 Windows 构建。在 Linux 上验证除桌面外的全部包：

```bash
pkgs=$(GOWORK=off go list ./... | grep -v -E '/cmd/desktop|/internal/desktop')
GOWORK=off go vet $pkgs
GOWORK=off go test -trimpath $pkgs
```

Linux 本机运行 `./scripts/build-linux.sh` 会默认使用 `go.mod` 固定的 luneCore 版本，在 `bin/` 生成本机程序，并在 `bin/linux-amd64`、`bin/linux-arm64` 生成对应交叉构建产物；需要联调本地 luneCore 时，可显式设置 `GOWORK`。如果 Go 不在 `PATH`，可通过 `GO=/path/to/go` 或 `GOROOT=/path/to/go-root` 指定安装位置。Linux 上验证 CLI/TUI 改动时无需构建或测试 `internal/desktop`；该包需要 GTK4、WebKitGTK 6、GLib 和 libsoup 开发依赖，只有涉及 Desktop 的改动才按下方桌面端流程验证。

## 依赖更新

先查看可更新模块，再在单模块模式更新根模块依赖。不要把本地目录写入 `go.mod`；需要联调时用本机 `go.work`：

```powershell
$env:GOWORK = 'off'
go list -m -u all
go get -u ./...
go get github.com/afterlune/luneCore@main
go mod tidy
```

如果所选依赖要求更高的 Go 版本，应同步提高 `go.mod` 的 `go` 指令和 `deploy/Dockerfile.a2a` 的构建镜像版本。Wails Go 模块、`@wailsio/runtime` 与安装说明保持同一版本。前端安装固定使用 `npm ci`；更新 `package.json` 后提交对应的 `package-lock.json`，锁文件使用 npm 官方 registry。

## 桌面端验证与发布

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.34
./scripts/build-desktop.ps1
cd cmd/desktop/frontend
npm ci
npm run check
npm run build
npx playwright install chromium
npm run test:screenshots
```

Playwright 截图基线在 Windows 本机上验证。发布版本以 `cmd/desktop/build/config.yml` 为源；修改版本或 Wails 构建信息后，运行 `wails3 task common:update:build-assets` 并检查生成资产，再用 `scripts/package-desktop.ps1` 构建 Windows 安装包。

## 用户配置与凭证

TUI/CLI 的用户配置在 Windows 为 `%APPDATA%\luneAgent\config.json`，Linux 通常为 `~/.config/luneAgent/config.json`（遵循 `XDG_CONFIG_HOME`）。读取时会收紧该文件权限（0600），并按平台保护 provider 的 `apiKey`：

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

桌面端不使用该文件，它的 provider 密钥以加密形式保存在 `settings.db`。

## 生成文件和仓库卫生

提交桌面前端实际使用的 Wails bindings 与构建资产；构建后若这些已跟踪文件变化，应检查并提交对应生成结果。根目录 `frontend/`、`.desktop-pre-extract.svelte`、`agent.log` 和 `sessions.db` 是本地生成或运行数据，应留在工作区并由 `.gitignore` 排除。

## 大文件维护

按职责、状态所有权和变化原因拆文件，不设机械行数上限。拆分时保持外部命令、Go API、DesktopState 接口与运行行为不变；测试跟随清晰的功能边界移动，职责集中的测试无需仅为缩短文件而拆分。

`internal/tui` 的状态所有权边界：`usage.go` 管会话用量与上下文窗口，`view_cache.go` 管转录渲染缓存与脏标记，`modal_state.go` 管互斥的模态浮层与优先级，`composer_state.go` 管输入框、补全、历史、选区与复制反馈，`run_state.go` 管任务运行态（运行标志、Core 任务快照、队列、流式缓冲与载入冻结）；`tuiModel` 只保留跨域状态（会话身份、任务交互卡片、注意力提醒与主题设置）。新增字段先决定归属，再决定放哪个文件。斜杠命令的名称、帮助文案、目标前端、是否需要会话与运行中可用性统一放在无 UI 依赖的 `internal/slashcmd`，两个前端只保留命令派发与子命令特例。

当前边界如下：`internal/cli/core_cli.go` 保留 TUI 命令入口与调度；CLI 执行、授权、状态查询、进程操作、会话操作和项目集成各有独立文件。`internal/settings/store.go` 负责 SQLite 初始化与通用设置访问，模型、外观、工作区配置和恢复状态各自持久化。`internal/desktop/workspace_lifecycle.go` 管理工作区生命周期，会话搜索位于 `workspace_search.go`。`internal/tui` 保留共享 `tuiModel` 的交互协调；工作区文件提及和 Markdown／计划标签处理分别位于独立子包，渲染主题通过调色板参数传入。桌面前端的 `desktopState.svelte.ts` 协调对话与会话；执行活动、桌面进程、设置和共享类型分别由相邻模块持有。新增职责优先归入对应模块；若需要跨模块传递大量可变状态，应先收敛状态所有权，而不是只移动函数。

## 安全检查与命令测试

Core CI 固定使用 `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`，升级 Go 时同步验证分析器能识别新语法。Linux 上单独运行 Core 全量验证时使用 `GOWORK=off`，并运行 `go test -race -trimpath -p 1 ./internal/runtime ./internal/tool ./internal/command ./internal/mcp`。沙箱集成测试自动构建带 `-trimpath` 的 helper，并为 Go 工具链授权完整安装根目录。

A2A 的权限、远程部署和交互合同见 [A2A 服务](a2a.md)。

## 执行存储升级

新执行引擎使用 `execution-format` 标记和 `executions-v2.db`。旧状态目录不会在正常启动时隐式升级。
停止所有使用该目录的 Lune 实例后执行：

```bash
lune migrate --state-dir /absolute/path/to/.luneagent
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

发布此跨仓库改动时，先发布 luneCore，再更新 Agent 的固定依赖版本并运行 `GOWORK=off` 检查。
本地联调允许使用未跟踪的 go.work；不要提交指向本地目录的 replace，也不要伪造未发布的 pseudo-version。

队列协调或结果提交失败时会停止 drain，并通过 WaitTurn 返回临时错误；队列和 checkpoint 保留，
没有自动重试计时器。修复存储问题后，重新打开会话或重新调用 WaitTurn 触发协调；已完成的
checkpoint 会补提交结果，不重新执行模型或已完成的工具。
