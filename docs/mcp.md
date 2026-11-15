# MCP 客户端

项目配置中的 `mcpServers` 描述候选服务器；Core 读取配置本身不会启动进程或访问网络。Lune 会自动连接已配置的 `engram` 服务器，并仅在该服务器配置存在时向 Agent 提供自动记忆指引；其它 MCP 服务器仍需通过 `/mcp enable` 显式启用。

```json
{
  "mcpServers": {
    "local-search": {
      "transport": "stdio",
      "command": "mcp-local-search",
      "args": ["--root", "./docs"],
      "env": {"INDEX_MODE": "compact"}
    },
    "remote-docs": {
      "transport": "streamable-http",
      "url": "https://mcp.example.test/v1/mcp",
      "clientMetadataURL": "https://client.example.test/oauth/lune.json"
    }
  }
}
```

stdio 命令直接执行，不经过 shell。服务器继承 Lune 当前进程环境，再由 `env` 覆盖。HTTP 服务器使用 MCP `2026-07-28` Streamable HTTP：每个请求单独 POST，接受 JSON 和请求级 SSE 响应；不发送 `initialize`，也不回退旧 HTTP+SSE 协议。

## 管理

Core、TUI 与 CLI 均提供 MCP 管理和交互；TUI 支持深浅主题、窄屏与错误状态展示，并由 `internal/tui` 回归测试覆盖。命令为：

- `/mcp list` 查看配置服务器、启用状态、连接状态和工具数。
- `/mcp enable <name>` 连接服务器并发现工具。
- `/mcp disable <name>` 移除该服务器工具并关闭连接。
- `/mcp auth <name>` 对 Streamable HTTP 服务器执行 OAuth。

重复运行 `/mcp enable <name>` 会重新连接并刷新服务器工具；列表会展示连接错误、工具数及脱敏后的 endpoint。

CLI、TUI 与 A2A 服务端打开 Core 时都会自动启用配置中的 `engram`；其它 MCP 服务器沿用既有显式启用方式（`/mcp enable <name>`）。启用后，Agent 会在新对话开始时恢复相关项目上下文，并在重要任务完成后选择性保存持久决策、发现和待办，不保存整段对话。记忆读写仍遵循当前权限与 Plan 模式。

`/mcp disable engram` 只停止当前 Core 实例中的连接；只要 `mcpServers.engram` 仍在配置中，下一次启动就会重新连接。要持续关闭自动记忆，请从用户或项目配置中移除该项。无界面客户端不能处理 elicitation，遇到这类请求会立即返回明确错误。

所有 MCP 工具归入 `mcp` 按需发现类别，并使用 `Network` 权限类别，因此仍遵循当前 `/permissions` 档位。Plan mode 只公开服务器标注 `readOnlyHint: true` 的工具；该注释不影响实际权限批准。

## OAuth

HTTP OAuth 使用 OAuth 2.1 授权码流程和 PKCE S256。若配置了 `clientMetadataURL` 且授权服务器声明支持 CIMD，会使用该 HTTPS 文档；否则尝试 Dynamic Client Registration。CIMD 文档必须包含固定回调 `http://127.0.0.1:42477/oauth/callback`；动态注册使用本次授权的回环地址。Access/refresh token 和动态注册 client ID 只存入操作系统 keyring。keyring 不可用时授权失败，不会写明文备用文件。

`/mcp auth <name>` 会在后台打开系统浏览器，TUI 显示等待状态；按 Esc 可取消授权。授权回调校验随机 state，并在交换 code 前校验授权服务器 issuer。访问令牌过期时使用 refresh token；scope 不足时提示再次运行授权命令。

## 交互和结果

MRTR 仅处理 `elicitation/create`，每个原始请求最多交互 5 轮；重试使用新 JSON-RPC ID，并原样回传服务器的 opaque `requestState`。表单使用 Tab/Shift+Tab 移动字段、Enter 提交、Ctrl+N 拒绝、Esc 取消；URL 模式使用 `y` 打开、`n` 拒绝、Esc 取消。URL 页面只有在用户按 `y` 确认后才打开，预览会隐藏 query 与 fragment，Lune 不采集外部页面中的密码。Sampling 和 roots 请求会明确失败，subagent 不会弹出用户交互。

工具结果保留文本、图片、音频、resource 和 structured content。resource link 由同一服务器自动 `resources/read`。用户专属 audience 内容只写入用户 transcript，不进入模型上下文。无法交给模型的二进制内容显示 MIME 与大小；MCP 结果沿用附件限制：最多 5 个二进制块、单块 10 MiB、合计 20 MiB，项目 `attachments` 可覆盖。模型 profile 的 `supportsInputAudio` 默认关闭；开启时仅允许 Responses 协议 profile。
