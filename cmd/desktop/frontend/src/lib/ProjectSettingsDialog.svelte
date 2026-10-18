<script lang="ts">
import Icon from './Icon.svelte';
import type { DesktopState } from './desktopState.svelte';
let { desktop }: { desktop:DesktopState } = $props();
</script>

      <div class="modal mcp-modal" role="dialog" aria-modal="true" aria-labelledby="mcp-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">PROJECT SETTINGS</span><h2 id="mcp-title">项目设置与 MCP</h2></div><button class="icon-button" aria-label="关闭" onclick={() => desktop.modal = null}><Icon name="close" size={16} /></button></div>
        <div class="modal-body mcp-body">
          <p class="mcp-description">Desktop 按项目路径将这些运行设置保存在用户数据库中。TUI/CLI 继续读取 config.json；两边互不导入或改写配置。</p>
          <section class="settings-section project-runtime-section">
            <div class="settings-section-heading"><div><h3>上下文与附件</h3><p>所有会话使用同一套项目运行参数；数值为 0 时采用 Core 默认值。</p></div></div>
            <div class="form-grid">
              <label>上下文窗口 Token<input type="number" min="0" step="1000" bind:value={desktop.workspaceSettingsForm.contextWindow} /></label>
              <label>预留输出 Token<input type="number" min="0" step="1000" bind:value={desktop.workspaceSettingsForm.reserveOutputTokens} /></label>
              <label class="check-label"><input type="checkbox" bind:checked={desktop.workspaceSettingsForm.autoCompact} /> 自动压缩上下文</label>
              <label>压缩阈值（0–1）<input type="number" min="0" max="1" step="0.05" bind:value={desktop.workspaceSettingsForm.autoCompactThreshold} disabled={!desktop.workspaceSettingsForm.autoCompact} /></label>
              <label>单条消息最多图片数<input type="number" min="0" bind:value={desktop.workspaceSettingsForm.maxAttachmentFiles} /></label>
              <label>单张图片上限（字节）<input type="number" min="0" step="1048576" bind:value={desktop.workspaceSettingsForm.maxAttachmentBytes} /></label>
              <label class="wide-field">图片总大小上限（字节）<input type="number" min="0" step="1048576" bind:value={desktop.workspaceSettingsForm.maxTotalBytes} /></label>
            </div>
          </section>
          <section class="settings-section command-toolchains-section">
            <div class="settings-section-heading">
              <div><h3>命令工具链</h3><p>检测只查询当前 PATH，不会运行程序或自动授权。获准目录只提供读取与执行权限，命令仍受项目沙箱和网络策略约束。</p></div>
              <div class="mcp-server-actions">
                <button class="small-button" disabled={desktop.previewMode || desktop.toolchainDetectionLoading || desktop.workspaceSettingsSaving} onclick={() => void desktop.detectCommandToolchains()}>{desktop.toolchainDetectionLoading ? '正在检测…' : '重新检测 PATH'}</button>
                <button class="small-button" disabled={desktop.previewMode || desktop.workspaceSettingsSaving} onclick={() => void desktop.pickCommandToolchainFolder()}><Icon name="plus" size={13} /> 选择目录</button>
              </div>
            </div>
            {#if desktop.workspaceSettingsForm.commandToolchainPaths.length}
              <div class="mcp-server-list">
                {#each desktop.workspaceSettingsForm.commandToolchainPaths as path (path)}
                  <article class="mcp-server-card">
                    <div class="mcp-server-main"><div><strong>已授权目录</strong><code>{path}</code></div><button class="remove-server" disabled={desktop.workspaceSettingsSaving} onclick={() => desktop.removeToolchainPath(path)}>移除</button></div>
                  </article>
                {/each}
              </div>
            {:else}
              <div class="mcp-empty">尚未授权命令工具链目录。未授权时，项目沙箱无法访问工作区以外的工具安装目录。</div>
            {/if}
            {#if desktop.toolchainCandidates.length}
              <div class="mcp-server-list command-toolchain-candidates">
                {#each desktop.toolchainCandidates as candidate (`${candidate.name}:${candidate.executable}`)}
                  {@const authorized = candidate.root ? desktop.toolchainPathAuthorized(candidate.root) : false}
                  <article class="mcp-server-card">
                    <div class="mcp-server-main">
                      <div><strong>{candidate.name}</strong><code>{candidate.executable}</code><span>{candidate.root ? `授权根目录：${candidate.root}` : candidate.note}</span></div>
                      <button class="small-button" disabled={desktop.previewMode || desktop.workspaceSettingsSaving || !candidate.root || authorized} onclick={() => desktop.addToolchainPath(candidate.root)}>{authorized ? '已在列表中' : '加入待授权列表'}</button>
                    </div>
                  </article>
                {/each}
              </div>
            {:else}
              <div class="mcp-empty">未检测到常见工具，或尚未进行 PATH 检测。也可以手动选择安装目录。</div>
            {/if}
          </section>
          <section class="settings-section">
            <div class="settings-section-heading"><div><h3>MCP 服务</h3><p>本地环境变量仅显示名称，留空保留已保存的值；移除变量行即可清除。</p></div><button class="small-button" onclick={desktop.addMCPServer}><Icon name="plus" size={13} /> 添加服务</button></div>
            {#if desktop.workspaceSettingsForm.mcpServers.length}
              <div class="mcp-server-list">
                {#each desktop.workspaceSettingsForm.mcpServers as server, index (index)}
                  {@const status = desktop.mcpStatus(server.name)}
                  <article class="mcp-server-card">
                    <div class="mcp-server-main">
                      <div><strong>{server.name || '未命名服务'}</strong><span>{status ? (status.transport === 'stdio' ? '本地进程' : '远程 HTTP') + ' · ' + (status.enabled ? (status.connected ? `已连接 · ${status.toolCount} 个工具` : '已启用 · 未连接') : '已停用') : '尚未应用'}</span></div>
                      {#if status}<label class="mcp-switch"><input type="checkbox" checked={status.enabled} disabled={desktop.workspaceHasOutstanding || desktop.pending.length > 0 || !!desktop.mcpUpdatingServer || desktop.workspaceSettingsSaving} onchange={(event) => void desktop.setMCPServerEnabled(status, event.currentTarget.checked)} /><span>{status.enabled ? '已启用' : '已停用'}</span></label>{/if}
                    </div>
                    <div class="form-grid mcp-config-grid">
                      <label>名称<input bind:value={server.name} /></label>
                      <label>连接方式<select bind:value={server.transport}><option value="stdio">本地进程</option><option value="streamable-http">远程 HTTP</option></select></label>
                      {#if server.transport === 'stdio'}
                        <label class="wide-field">可执行程序<input bind:value={server.command} placeholder="例如 npx" /></label>
                        <label class="wide-field">参数（每行一个）<textarea bind:value={server.argsText} rows="3" placeholder="每行填写一个参数，例如 -y"></textarea></label>
                        <div class="wide-field mcp-env-list">
                          <div class="settings-section-heading"><strong>环境变量</strong><button class="small-button" onclick={() => desktop.addMCPEnvironment(server)}><Icon name="plus" size={13} /> 添加变量</button></div>
                          {#each server.env as item, envIndex (envIndex)}
                            <div class="mcp-env-row"><input aria-label="变量名称" bind:value={item.name} placeholder="变量名" /><input aria-label="变量值" type="password" bind:value={item.value} placeholder={item.hasValue ? '留空保留已保存的值' : '变量值'} autocomplete="new-password" /><button class="remove-env" aria-label="删除环境变量" onclick={() => server.env = server.env.filter((_, i) => i !== envIndex)}><Icon name="close" size={13} /></button></div>
                          {/each}
                        </div>
                      {:else}
                        <label class="wide-field">服务 URL<input bind:value={server.url} placeholder="https://example.com/mcp" /></label>
                        <label class="wide-field">OAuth Client Metadata URL<input bind:value={server.clientMetadataURL} placeholder="可选的 HTTPS 地址" /></label>
                      {/if}
                    </div>
                    {#if status?.transport === 'streamable-http' && status.enabled}<div class="mcp-server-actions"><button class="small-button" disabled={desktop.workspaceHasOutstanding || desktop.pending.length > 0 || !!desktop.mcpUpdatingServer || desktop.workspaceSettingsSaving} onclick={() => void desktop.authenticateMCPServer(status)}>{desktop.mcpUpdatingServer === status.name ? '正在连接…' : '授权 / 重新连接'}</button></div>{/if}
                    <div class="mcp-server-actions"><button class="remove-server" disabled={desktop.workspaceSettingsSaving} onclick={() => desktop.workspaceSettingsForm.mcpServers = desktop.workspaceSettingsForm.mcpServers.filter((_, i) => i !== index)}>删除服务</button></div>
                  </article>
                {/each}
              </div>
            {:else}
              <div class="mcp-empty">尚未添加 MCP 服务。</div>
            {/if}
          </section>
          {#if desktop.mcpUpdatingServer}<div class="mcp-progress">正在处理 {desktop.mcpUpdatingServer}…</div>{/if}
        </div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => desktop.modal = null}>关闭</button><button class="primary-button" disabled={desktop.workspaceSettingsSaving || desktop.workspaceHasOutstanding || desktop.pending.length > 0 || !!desktop.mcpUpdatingServer} onclick={() => void desktop.saveWorkspaceSettings()}>{desktop.workspaceSettingsSaving ? '正在应用…' : '应用项目设置'}</button></div>
      </div>
    
