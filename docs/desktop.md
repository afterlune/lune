# luneAgent 桌面端

桌面端在 Windows 10/11 上运行，使用 Wails v3、Svelte 5 和 TypeScript。它与现有终端程序并行安装：`bin/lune.exe` 仍启动 TUI/CLI，`bin/lune-desktop.exe` 启动桌面窗口。两个入口在同一时间只能占用同一个工作区。

## 构建和运行

安装 Go 1.26.9、Node.js 和 WebView2 Runtime，然后安装仓库固定的 Wails CLI：

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.30
./scripts/build.ps1
./scripts/build-desktop.ps1
```

桌面二进制输出到 `bin/lune-desktop.exe`。如果 Windows 没有 WebView2 Runtime，启动错误窗口会提供安装说明。

## 首次设置

在模型设置中添加提供方、模型 ID 和默认模型。API 密钥保存在 `%APPDATA%\luneAgent\settings.db`，并由当前 Windows 用户的 DPAPI 加密。前端只接收密钥是否已保存的标记，留空可保留已保存的密钥。

提供方、模型档案、默认模型、主题和最近工作区是用户级设置，会被桌面端、TUI 和 CLI 共用。MCP、上下文和图片附件限制仍属于工作区 `config.json`。

打开包含旧模型配置的工作区时，桌面端会先显示迁移预览。对于同名提供方、模型档案或默认模型，可以分别保留全局设置或使用项目设置。迁移取消或失败时，项目配置保持原样；也可以直接用原有项目配置打开工作区。

当前 Core 会在合并全局模型档案之前验证工作区 `config.json`。因此，配置里还含有 MCP、上下文或附件设置的项目暂时无法完成模型字段迁移：桌面端会回滚设置数据库和原文件，并允许继续使用项目内原有模型配置。要让这类工作区完成无损迁移，需要先调整 Core 的配置加载顺序。

## 前端预览与截图

`cmd/desktop/frontend` 包含确定性的浏览器预览页面，不连接真实 Core：

```powershell
cd cmd/desktop/frontend
npm install
npm run dev
```

截图基线测试覆盖工作区、Question 和 Plan 面板：

```powershell
npx playwright install chromium
npm run check
npm run test:screenshots
```
