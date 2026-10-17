# luneAgent 桌面端

桌面端在 Windows 10/11 上运行，使用 Wails v3、Svelte 5 和 TypeScript。它与现有终端程序并行安装：`bin/lune.exe` 仍启动 TUI/CLI，`bin/lune-desktop.exe` 启动桌面窗口。TUI 与 Desktop 可以同时打开同一项目；两者的会话、队列和检查点独立保存。

## 构建和运行

安装 Go 1.26.9、Node.js 和仓库固定的 Wails CLI：

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.30
./scripts/build.ps1
./scripts/build-desktop.ps1
```

桌面二进制输出到 `bin/lune-desktop.exe`。运行桌面程序需要 WebView2 Runtime；如果缺少运行时，启动错误窗口会提供安装说明。

### Windows Beta 安装包

安装 NSIS 3.x 并确保 `makensis` 在 `PATH` 中，然后运行：

```powershell
./scripts/package-desktop.ps1
```

脚本生成两个 Windows x64 安装包：

- `bin/luneAgent-0.1.0-windows-amd64-user-setup.exe`：当前用户安装，无需管理员权限。
- `bin/luneAgent-0.1.0-windows-amd64-machine-setup.exe`：所有用户安装，需要管理员权限。

如果目标电脑缺少 WebView2 Runtime，安装器会运行 Microsoft Evergreen Bootstrapper，因此需要联网。运行时安装失败会中止桌面端安装并显示错误提示。

卸载默认保留 `%APPDATA%\luneAgent\settings.db` 中的模型设置和加密 API 密钥。卸载器提供可选复选框，勾选后清除当前运行卸载器账户的设置；该选项不会删除工作区文件或其他 Windows 用户的数据。卸载前先退出 luneAgent。

Beta 安装包尚未签名，Windows 可能显示 SmartScreen 提示。安装器和桌面端只包含 GUI 程序；现有 `bin/lune.exe` TUI/CLI 仍单独构建。

## 首次设置

在模型设置中添加提供方、模型 ID 和默认模型。API 密钥保存在 `%APPDATA%\luneAgent\settings.db`，并由当前 Windows 用户的 DPAPI 加密。前端只接收密钥是否已保存的标记，留空可保留已保存的密钥。

Desktop 的提供方、模型档案、默认模型、主题和最近工作区保存在当前用户的 `settings.db`。TUI/CLI 继续使用自己的工作区 `config.json`，不读取 Desktop 数据库。Desktop 的项目运行设置（上下文窗口、自动压缩、输出预留、图片限制和 MCP 服务）按规范化项目路径保存在 `settings.db` 中；同一项目的 Local 与 Worktree 会话共用这些设置。

Desktop 完全忽略项目中的 `config.json`，不会导入、迁移或改写它。需要配置 Desktop 时，在工作区侧栏打开“项目设置”；需要配置 TUI/CLI 时，编辑工作区的 `config.json`。两套配置各自独立。

Desktop 会话数据保存在用户配置目录下 `luneAgent/desktop/<project-hash>`，Worktree 的代码目录保存在用户缓存目录下 `luneAgent/worktrees/<project-hash>/<session-id>`。TUI/CLI 继续使用项目内 `.luneagent`。升级后已有 `.luneagent` 数据保持原样，Desktop 从独立会话列表开始，不自动复制旧会话。

每个新会话可选 `Local` 或 `Worktree`，默认 `Local`。Local 直接操作所选目录；Worktree 从当前已提交 `HEAD` 创建 Git detached 工作树，不复制未提交改动。Worktree 只在 Git 仓库中可用，嵌套目录会映射到工作树里的相同相对路径。变更视图可选择 Worktree 文件并应用到原项目；Desktop 会先检查整批差异，存在冲突时不应用任何文件。应用后的改动保持未提交状态，不会自动暂存或提交。会话完成、结果应用或会话删除后，工作树都会保留，由用户自行检查和清理。Local 会话仍共享同一份项目文件；需要隔离并行修改时请选择 Worktree。

## 运行中发送与编辑

任务运行时仍可编辑输入框并发送后续消息。新消息会显示在对话中，按顺序排队，当前任务结束后执行；审批、提问、恢复卡片或 Plan 确认待处理时，需先处理当前请求。

已进入会话历史的用户消息可以编辑。编辑会从该消息开始回退并重新运行，移除后续对话记录；任务运行期间提交编辑时，当前任务和已接受的队列会先完成。已经执行的文件操作不会自动撤销。

排队中的消息可以单独撤回或编辑。撤回只取消选中的待执行消息，不会中断当前任务或改变其余消息顺序；编辑排队消息会替换该位置，并取消后续排队消息。

## 工作区浏览与改动审查

工作区侧栏提供会话、文件、变更和待处理视图。文件视图支持目录浏览、按文件名查找、文本预览和用系统默认应用打开文件；浏览与预览只允许访问当前工作区，跳过 `.git`、`.luneagent` 和常见依赖/构建目录。变更视图按当前会话工作目录列出相对 Git `HEAD` 的暂存、未暂存、删除、重命名和未跟踪改动，并显示 unified diff；忽略文件不显示，二进制文件只显示说明。

待处理视图汇总当前工作区所有会话需要用户处理的请求。选择会话后，桌面端切换到对应对话完成授权、回答、恢复或 MCP 交互；中心只显示请求类型和工具/服务器名，不保存请求正文，也不发送系统通知。

每轮执行会将工具返回的文件改动按路径汇总，并保留各步骤返回的差异片段。工具详情和改动摘要会随桌面执行记录保存；桌面端不提供代码编辑器或集成终端，代码编辑继续交给外部 IDE。

## 会话与 MCP 管理

侧栏可搜索、重命名和删除会话，并显示 Local/Worktree 模式及运行状态。不同会话可以并行运行，切换会话不会中断后台任务。删除当前会话时先切换到其他会话；如果没有其他会话，会先创建一个 Local 会话。删除会同时清除 Desktop 执行记录与会话数据，但保留关联的 Git 工作树。

项目设置面板可编辑上下文、自动压缩、输出预留、图片限制和 MCP 服务（本地进程参数、远程 HTTP 地址及环境变量），也可查看连接状态、启用或停用服务，并为远程 HTTP 服务启动授权。环境变量值使用当前 Windows 用户的 DPAPI 加密保存在数据库中，前端只会收到变量名称和是否已配置的标记。更改项目运行设置会在所有会话空闲后重建该项目的 Local 与 Worktree Core，并恢复当前选中的会话。

## 前端预览与截图

`cmd/desktop/frontend` 包含确定性的浏览器预览页面，不连接真实 Core：

```powershell
cd cmd/desktop/frontend
npm install
npm run dev
```

截图基线测试覆盖工作区、变更审查、待处理中心、Question 和 Plan 面板：

```powershell
npx playwright install chromium
npm run check
npm run test:screenshots
```
