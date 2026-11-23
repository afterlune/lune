# Web 与 Electron 前端

`desktop/` 使用一套 React/TypeScript 聊天界面，连接独立运行的 `lune-core` Runtime Protocol v1。Web 页面只连接远程 WSS Core；Electron 可以启动本机 Core 的 stdio 子进程，也可以连接远程 WSS Core。Core 拥有工作区、配置、权限、会话和执行状态；前端为每个 RPC 明确传入 `sessionId`。

一个 Core 可供多个前端和设备连接。新建会话会得到独立的历史与任务队列；在另一设备选择同一会话 ID 时，两端会共享历史、待处理请求和运行事件。

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

桌面本机模式依赖 `luneCore/cmd/lune-core` 和 `luneCore/cmd/lune-sandbox`。当前工作区根目录 `go.mod` 暂时固定到实现 Runtime Protocol v1 的 `luneCore` revision `a326fa3`；该 revision 尚未出现在 GitHub 远端，干净环境不能从 GitHub 直连下载。发布 Lune 前，必须先发布/推送此 Core revision 到可用的 Go module source，再验证干净环境能够下载，然后才发布 Lune。与尚未发布的兄弟仓库联调时，可以用未跟踪的 `go.work`；不要加入本地路径 `replace`。Runtime Protocol 约定保存在 Core 仓库的 `docs/runtime-protocol-v1.md`。

前端类型检查和构建：

```sh
npm run typecheck
npm run build:web
npm run build
```
