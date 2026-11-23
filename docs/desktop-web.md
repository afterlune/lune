# Web 与 Electron 前端

`desktop/` 使用一套 React/TypeScript 聊天界面，连接独立运行的 `lune-core` Runtime Protocol v1。Web 页面只连接远程 WSS Core；Electron 可以启动本机 Core 的 stdio 子进程，也可以连接远程 WSS Core。Core 拥有工作区、配置、权限、会话和执行状态；前端为每个 RPC 明确传入 `sessionId`。

一个 Core 可供多个前端和设备连接。新建会话会得到独立的历史与任务队列；在另一设备选择同一会话 ID 时，两端会共享历史、待处理请求和运行事件。

## 会话、任务与附件

- 会话侧栏支持分页、归档筛选、标题和正文搜索。会话菜单可改名、归档、删除、分叉、导出 Markdown 和调整推理强度。分叉复制当前会话历史，使用新的会话 ID 和独立队列。
- 输入区的 **Plan** 使用 Core 的只读工具筛选。运行中默认排队，使用 **Add to running task** 可向当前任务补充输入。队列中的每条消息可单独取消。手动压缩和回退须等待当前会话空闲，回退先预览待移除的轮次。
- 附件支持选择和拖放。支持 UTF-8 文本、Markdown、CSV、PNG 和 JPEG，每条消息最多 5 个文件、合计 4 MiB。图片每个最多 2 MiB，文本每个最多 128 KiB。图片入口要求所选模型 profile 配置 `"supportsInputImages": true`。附件随会话保存，其他客户端可以从同一个 Core 读回。
- Electron 的 **Screenshot** 允许选择显示器，将截图放入草稿预览。**Quick window** 使用当前窗口的紧凑模式，默认全局快捷键是 `Ctrl+Shift+Space`，可在连接设置中更改；返回主窗口时保留当前会话和草稿。

新 Core 在握手时公布支持的扩展。旧 Core 仍支持基本聊天，其扩展入口会按握手能力隐藏。TUI 继续直接使用进程内 Core。

## 外观

在连接页或聊天顶部的 **Appearance** 菜单选择 **System**、**Light** 或 **Dark**。默认跟随系统外观，系统切换时页面一起更新。Web 在当前站点保存选择，Electron 在应用用户配置中保存。外观设置与 Core 和会话无关。

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

本机 Core 从所选工作区读取 `config.json`，会话状态保存在该工作区的 `.lune/`。远程模式只需要 Electron 应用，不需要随包 Core 二进制。

单独开发 Web 页面：

```sh
cd desktop
npm run dev:web
```

浏览器模式只提供远程连接。Core 必须使用受信任的 TLS 证书并允许准确的网页来源；浏览器不会保存访问令牌，页面刷新后需要重新输入。

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

Electron 本机模式会把 Core 和沙箱 helper 放在应用资源目录中，并从工作区的 `config.json` 读取 provider 配置。构建和打包命令会针对目标平台生成 Core：

```sh
npm run package:linux
npm run package:windows
```

当前桌面包支持 Linux x64 与 Windows x64。Windows 安装包建议在 Windows 上构建；从 Linux 交叉打包 NSIS 还需要 Wine。macOS、ARM64 桌面包尚未配置。

Electron 主进程持有远程令牌，并通过 context-isolated preload 暴露有限的 Runtime Protocol 方法。令牌只保存在进程内存中；断开连接时可清除，应用退出后需要重新输入。浏览器与 Electron 的本机连接、凭证保存行为不同，这是各宿主安全模型的边界。

## Core 版本

桌面本机模式依赖 `luneCore/cmd/lune-core` 和 `luneCore/cmd/lune-sandbox`。当前工作区根目录 `go.mod` 固定到实现 Runtime Protocol v1 的 `luneCore` revision `a326fa3`，该 revision 已包含在 GitHub 远端主分支历史中。独立检出此仓库时可通过 `GOWORK=off` 下载并构建，不需要兄弟目录。构建脚本优先使用干净且恰好位于固定 revision 的兄弟 Core 检出，否则从固定 Go module source 构建。不要加入本地路径 `replace`。Runtime Protocol 约定保存在 Core 仓库的 `docs/runtime-protocol-v1.md`。

同一工作区中开发尚未固定到 `go.mod` 的 Core 功能时，使用 `LUNE_CORE_DIR` 显式指定 checkout：

```sh
cd desktop
LUNE_CORE_DIR="$(realpath ../../luneCore)" npm run build:core
npm run dev
```

本地打包可对 `package:linux` 或 `package:windows` 设置相同的 `LUNE_CORE_DIR`。Web 使用这些扩展时，远程 Core 也需要从包含扩展的本地源码构建。

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
