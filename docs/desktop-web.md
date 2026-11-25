# Web 与 Electron 前端

`desktop/` 使用一套 React/TypeScript 聊天界面，连接独立运行的 `lune-core` Runtime Protocol v1。Web 页面只连接远程 WSS Core；Electron 可以启动本机 Core 的 stdio 子进程，也可以连接远程 WSS Core。Core 拥有工作区、配置、权限、会话和执行状态；前端为每个 RPC 明确传入 `sessionId`。

一个 Core 可供多个前端和设备连接。新建会话会得到独立的历史与任务队列；在另一设备选择同一会话 ID 时，两端会共享历史、待处理请求和运行事件。

## 会话、任务与附件

- 会话侧栏支持分页、归档筛选、标题和正文搜索。窄屏和 Electron 紧凑窗口通过聊天顶部的会话按钮打开同一列表，按 Esc 可关闭。会话菜单可改名、归档、删除、分叉、导出 Markdown 和调整推理强度。分叉复制当前会话历史，使用新的会话 ID 和独立队列。
- 输入区的 **Plan** 使用 Core 的只读工具筛选。运行中默认排队，使用 **Add to running task** 可向当前任务补充输入。队列中的每条消息可单独取消。手动压缩和回退须等待当前会话空闲，回退先预览待移除的轮次。
- 附件支持选择和拖放。支持 UTF-8 文本、Markdown、CSV、PNG 和 JPEG，每条消息最多 5 个文件、合计 4 MiB。图片每个最多 2 MiB，文本每个最多 128 KiB。图片入口要求所选模型 profile 配置 `"supportsInputImages": true`。附件随会话保存，其他客户端可以从同一个 Core 读回。
- Electron 的 **Screenshot** 允许选择显示器，将截图放入草稿预览。**Quick window** 使用当前窗口的紧凑模式，默认全局快捷键是 `Ctrl+Shift+Space`，可在连接设置中更改；返回主窗口时保留当前会话和草稿。

新 Core 在握手时公布支持的扩展。旧 Core 仍支持基本聊天，其扩展入口会按握手能力隐藏。TUI 继续直接使用进程内 Core。

## 外观

在连接页或聊天顶部的 **Appearance** 菜单选择 **System**、**Light** 或 **Dark**。默认跟随系统外观，系统切换时页面一起更新。Web 在当前站点保存选择，Electron 在应用用户配置中保存。外观设置与 Core 和会话无关。

## Web 工作台

左侧显示工作区和会话。右上角的「文件」打开右侧目录树，选中文件后可在同一侧只读预览。预览标题栏可以重新展开目录树。远程文件浏览由 Core 握手中的 `workspace.files` 能力启用，范围为服务端指定的工作目录。

顶栏可以切换简体中文和 English。本机 Web 在 Lune 用户设置中记住语言，远程 Web 在当前站点记住语言。会话标题、聊天内容和文件名保留原文。

本机 `lune web` 可在左侧打开其他工作目录。每个目录有独立的 Core 运行时和 `.lune/` 状态。切换时其他目录中的任务继续执行，左侧显示当前订阅会话的运行或待交互状态。退出 `lune web` 会按 Core 的关停规则保留排队任务与可恢复执行状态。

## 开发

需要 Node.js 22.12 或更新版本与 npm。安装依赖并启动 Electron：

```sh
cd desktop
npm ci
npm run dev
```

Electron 的本机模式需要配好 Go Core 二进制。当前平台构建：

```sh
npm run build:core
npm run dev
```

Electron 通过 **Configure Core** 配置供应商和模型。默认配置、可选的工作区覆盖、外观、快捷键及最近连接保存在应用用户目录的 `settings.sqlite`。本机 Core 通过私有 stdin 接收配置，会话状态仍保存在工作区的 `.lune/`。远程模式由服务端管理模型配置。

渲染层新增或改动 Runtime Protocol 调用后运行 `npm run methods:check`：它校验 `src/renderer` 请求的每个方法都在 `src/electron/main.ts` 的 `allowedRuntimeMethods` 白名单内。缺少时会话只在桌面端运行时失败，打包与类型检查都发现不了。

可通过 **Import config.json…** 预览并导入旧配置。用户目录或工作区的原文件保留。供应商 API Key 和选中记住的远程令牌仅以系统保护的密文写入 SQLite；系统凭据存储不可用时它们只保留在本次运行的内存中，不写入磁盘（旧版本留下的明文行仍可读取，但不再新增）。也可用 `apiKeyEnv` 引用 Electron 继承的环境变量。修改配置后，下次启动本机 Core 时生效。

单独开发 Web 页面：

```sh
cd desktop
npm run dev:web
```

浏览器模式只提供远程连接。Core 必须使用受信任的 TLS 证书并允许准确的网页来源；浏览器不会保存访问令牌，页面刷新后需要重新输入。

## 单命令启动本机 Web

构建时设置 `LUNE_EMBED_WEB=1` 可将静态资源嵌入 `lune`。构建机需要 Node.js 和 npm，运行随包的 `lune` 和 `lune-sandbox` 时无需 Node.js 或静态资源目录。

```sh
LUNE_EMBED_WEB=1 ./scripts/build-linux.sh
./bin/lune web --workspace /path/to/workspace
```

PowerShell 构建使用 `$env:LUNE_EMBED_WEB = '1'` 后运行 `./scripts/build.ps1`。默认构建无需 Node.js。Web 版从 Lune 用户配置目录加载模型设置，使用工作区的 `.lune/`，并默认使用 `workspace` 权限。在同一台机器上通过浏览器打开时，无需 TLS 或手工配置令牌。使用 `--no-open` 可在终端获得一步启动链接；配对码只能使用一次，当前标签页刷新可继续使用。按 Ctrl+C 停止。

## 连接 Web Core

部署 Core 的 `serve` 端点和 HTTPS 静态站点。Core 的令牌文件必须包含非空随机令牌，证书应由浏览器信任：

```sh
lune-core serve --workspace /path/to/workspace --listen 0.0.0.0:18092 \
  --tls-cert /path/to/server.crt --tls-key /path/to/server.key \
  --token-file /path/to/runtime.token \
  --web-origins https://app.example.com
```

网页连接 `wss://<host>:18092/v1/runtime`，并通过 Runtime Protocol v1 定义的 WebSocket 子协议传递令牌。令牌只保存在当前页面内存中，不写入 URL、静态资源或浏览器存储。Core 与 Web 页面之间必须使用 HTTPS/WSS；不要把 Core 端口暴露给不受信任的网络。

构建可由任意静态 HTTPS 服务托管的 Web 资源：

```sh
npm run build:web
```

生成文件在 `desktop/dist/`。部署 Core 时，将真实网页源站完整加入 `--web-origins`，不要用通配来源代替来源校验。

## Electron 打包

Electron 本机模式随包携带 Core 和沙箱 helper，使用应用设置中的模型供应商配置。构建和打包命令会针对目标平台生成 Core：

```sh
npm run package:linux
npm run package:windows
```

当前桌面包支持 Linux x64 与 Windows x64。Windows 安装包建议在 Windows 上构建；从 Linux 交叉打包 NSIS 还需要 Wine。macOS、ARM64 桌面包尚未配置。

Electron 主进程持有远程令牌，并通过 context-isolated preload 暴露有限的 Runtime Protocol 方法。令牌只保存在进程内存中；断开连接时可清除，应用退出后需要重新输入。浏览器与 Electron 的本机连接、凭证保存行为不同，这是各宿主安全模型的边界。

## Core 版本

桌面本机模式依赖 `luneCore/cmd/lune-core` 和 `luneCore/cmd/lune-sandbox`。根目录 `go.mod` 固定到包含工作区管理与只读文件接口的 `luneCore` revision `888b2e3`。默认构建脚本使用该固定版本，可在独立检出中构建。构建脚本优先使用干净且恰好位于固定 revision 的兄弟 Core 检出，否则从固定 Go module source 构建。不要加入本地路径 `replace`。Runtime Protocol 约定保存在 Core 仓库的 `docs/runtime-protocol-v1.md`。

同一工作区中开发尚未固定到 `go.mod` 的 Core 功能时，使用 `LUNE_CORE_DIR` 显式指定 checkout：

```sh
cd desktop
LUNE_CORE_DIR="$(realpath ../../luneCore)" npm run build:core
npm run dev
```

本地打包可对 `package:linux` 或 `package:windows` 设置相同的 `LUNE_CORE_DIR`。远程模式需要连接支持扩展的 Core 才会启用扩展入口。


## 本地验收记录

2026-11-23 在 Lune `da7fd49` 与 Core `f9bd12e` 上进行本地验收。临时工作区、TLS 证书、令牌和模拟 Responses API 都保存在 `/tmp`。

- 在未注入 Electron preload 的 Chromium 页面运行环境中，页面使用原生 WebSocket 子协议认证连接 WSS Core；打包的 Electron 应用使用 Bearer 认证连接同一 Core。两个客户端各自创建会话，也能读取同一个显式指定的会话。
- Core 验收通过独立会话隔离、流式回复、无效令牌和未允许来源拒绝、断线后事件重放，以及重复提交 ID 不重复执行。
- Linux AppImage 中的 Electron 分别通过远程 WSS 与本机 stdio Core 完成一轮模拟模型请求。打包的 Core/helper 来自固定的 `a326fa3` 模块源码。
- `GOWORK=off` 独立检出下载与 Go 构建、前端 TypeScript 检查、Web 构建、Linux x64 AppImage 打包、Windows x64 Core/helper 交叉编译均通过。
- Windows NSIS 安装包未在此环境中构建或运行；没有可用的 Windows 构建环境或 Wine。

前端类型检查和构建：

```sh
npm run typecheck
npm run build:web
npm run build
```

### 会话与附件扩展验收

以本地 Core 源码构建，使用临时工作区、TLS 证书和模拟 Responses 服务。Chromium 通过 WSS 完成附件发送、归档正文搜索、分叉和回退。Linux AppImage 使用随包 Core 完成同类对话，在 X11 下实际触发全局快捷键，并从显示器截图到消息草稿。Electron 远程模式读取了浏览器创建的归档会话和附件。

Core 的 WSS 测试检查了过期回退预览、Plan 工具筛选、逐项取消排队、附件限制和重复提交。Windows x64 Core 和沙箱 helper 交叉构建通过。当前 Linux 环境中的 Wine 无法运行 NSIS 生成卸载器所需的程序，未获得可用的 Windows 安装包。

### 默认 Core 与窄屏会话验收

在没有兄弟 Core 检出的临时目录，以 `GOWORK=off` 从 `go.mod` 下载固定模块并打包 Linux AppImage。随包 Core 和沙箱辅助程序与构建输出一致。本机 stdio 模式已验证 Plan、附件发送和带附件分叉，另用旧版 Core 验证基础聊天和扩展入口的能力协商。

360px Chromium 页面和 Electron 紧凑窗口均通过会话分页、归档正文搜索、切换和新建、Esc 关闭、焦点返回与深色主题检查。Web 上还验证了列表过期时在抽屉内显示错误，以及抽屉打开时对话继续执行。Electron 重新载入后能恢复紧凑窗口状态并返回宽屏侧栏。Windows 安装包验收挂起。

### SQLite 与内嵌 Web 验收

Linux AppImage 使用配套 Core 源码打包。固定依赖更新后，另在独立检出验证了从 Go module 源码构建 Core 和内嵌 Web 二进制。在两个没有 `config.json` 的目录中验证了首次配置、默认模型继承和工作区覆盖、导入预览与保存、重启后的会话恢复，以及命名 WSS 连接。测试环境没有安全凭据库，环境变量引用可在重启后使用，本次运行的 API Key 和远程令牌未写入 SQLite、数据库日志或应用诊断日志。

内嵌版已在独立目录和不含 Node.js 的 PATH 下启动，浏览器完成一次性配对、流式回复与刷新。Host、Origin 和已有配对码重用均经过拒绝测试。普通版仍可构建，`lune web` 会提示如何开启内嵌资源。
