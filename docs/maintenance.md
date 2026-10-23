# 工程维护

本文是 luneAgent 的维护入口。Go 版本以根目录 `go.mod` 的 `go` 指令为准；桌面前端使用 Node.js 22.12+、npm 和仓库中的 `package-lock.json`。

## Go 模块与工作区

`luneAgent` 通过 `go.mod` 固定 luneCore 版本。开发者可以在本机使用未跟踪的 `go.work` 联调兄弟目录中的 luneCore；CI 不使用该工作区，因此验证的是 `go.mod` 固定的版本。根目录的 `go test ./...` 只测试 luneAgent，不会运行 luneCore 的测试。

在 Windows 上运行完整的 luneAgent 验证：

```powershell
$env:GOWORK = 'off'
go vet -unsafeptr=false ./...
go test -trimpath ./...
./scripts/build.ps1
```

Windows 上的 vet 仅关闭 `unsafeptr` 分析器：ConPTY 测试按 Win32 API 契约将不透明 `HPCON` 句柄作为 `lpValue` 传入；其余分析器仍会检查全部 Go 包。

`gofmt -w` 只用于主动格式化；CI 通过 `gofmt -l` 检查格式，不会改写文件。若本机同时检出 luneCore，可单独运行其测试：

```powershell
go -C ..\luneCore test -trimpath ./...
```

Linux CI 运行 CLI/TUI 支持包测试，并交叉构建 Linux amd64、arm64 的 lune 与 sandbox。桌面 GUI 目前只在 Windows 构建。Linux 本机运行 `./scripts/build-linux.sh` 会默认使用 `go.mod` 固定的 luneCore 版本，在 `bin/` 生成本机程序，并在 `bin/linux-amd64`、`bin/linux-arm64` 生成对应交叉构建产物；需要联调本地 luneCore 时，可显式设置 `GOWORK`。如果 Go 不在 `PATH`，可通过 `GO=/path/to/go` 或 `GOROOT=/path/to/go-root` 指定安装位置。Linux 上验证 CLI/TUI 改动时无需构建或测试 `internal/desktop`；该包需要 GTK4、WebKitGTK 6、GLib 和 libsoup 开发依赖，只有涉及 Desktop 的改动才按下方桌面端流程验证。

## 依赖更新

先查看可更新模块，再在单模块模式更新根模块依赖。不要把本地目录写入 `go.mod`；需要联调时用本机 `go.work`：

```powershell
$env:GOWORK = 'off'
go list -m -u all
go get -u ./...
go get github.com/afterlune/luneCore@main
go mod tidy
```

如果所选依赖要求更高的 Go 版本，应同步提高 `go.mod` 的 `go` 指令和 CI 工具链。Wails Go 模块、`@wailsio/runtime` 与安装说明保持同一版本。前端安装固定使用 `npm ci`；更新 `package.json` 后提交对应的 `package-lock.json`，锁文件使用 npm 官方 registry。

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

Playwright 截图基线在 Windows runner 上验证。发布版本以 `cmd/desktop/build/config.yml` 为源；修改版本或 Wails 构建信息后，运行 `wails3 task common:update:build-assets` 并检查生成资产，再用 `scripts/package-desktop.ps1` 构建 Windows 安装包。CI 不发布安装包，也不调用真实模型 API。

## 生成文件和仓库卫生

提交桌面前端实际使用的 Wails bindings 与构建资产；构建后若这些已跟踪文件变化，应检查并提交对应生成结果。根目录 `frontend/`、`.desktop-pre-extract.svelte`、`agent.log` 和 `sessions.db` 是本地生成或运行数据，应留在工作区并由 `.gitignore` 排除。

## 大文件维护

按职责、状态所有权和变化原因拆文件，不设机械行数上限。拆分时保持外部命令、Go API、DesktopState 接口与运行行为不变；测试跟随清晰的功能边界移动，职责集中的测试无需仅为缩短文件而拆分。

`internal/tui` 的状态所有权边界：`usage.go` 管会话用量与上下文窗口，`view_cache.go` 管转录渲染缓存与脏标记，`modal_state.go` 管互斥的模态浮层与优先级，`composer_state.go` 管输入框、补全、历史、选区与复制反馈；`tuiModel` 只保留跨域状态（会话身份、任务运行态与任务交互卡片）。新增字段先决定归属，再决定放哪个文件。斜杠命令的名称、帮助文案、目标前端、是否需要会话与运行中可用性统一放在无 UI 依赖的 `internal/slashcmd`，两个前端只保留命令派发与子命令特例。

当前边界如下：`internal/cli/core_cli.go` 保留 TUI 命令入口与调度；CLI 执行、授权、状态查询、进程操作、会话操作和项目集成各有独立文件。`internal/settings/store.go` 负责 SQLite 初始化与通用设置访问，模型、外观、工作区配置和恢复状态各自持久化。`internal/desktop/workspace_lifecycle.go` 管理工作区生命周期，会话搜索位于 `workspace_search.go`。`internal/tui` 保留共享 `tuiModel` 的交互协调；工作区文件提及和 Markdown／计划标签处理分别位于独立子包，渲染主题通过调色板参数传入。桌面前端的 `desktopState.svelte.ts` 协调对话与会话；执行活动、桌面进程、设置和共享类型分别由相邻模块持有。新增职责优先归入对应模块；若需要跨模块传递大量可变状态，应先收敛状态所有权，而不是只移动函数。
