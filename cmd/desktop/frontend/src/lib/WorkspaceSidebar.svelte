<script lang="ts">
import type { DesktopState } from './desktopState.svelte';
import WorkspaceExplorer from './WorkspaceExplorer.svelte';
let { desktop }: { desktop: DesktopState } = $props();
</script>
<aside class="sidebar" class:sidebar-collapsed={desktop.sidebarCollapsed} aria-label="工作区内容侧栏">
      <div class="brand-row">
        <div class="brand-copy"><strong>luneAgent</strong><span>本地 Agent 工作台</span></div>
        <button class="icon-button sidebar-collapse-button" aria-label="收起侧栏" title="收起侧栏" onclick={() => desktop.sidebarCollapsed = true}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3.5" y="4" width="17" height="16" rx="2"/><path d="M9 4v16"/><path d="m15 9-3 3 3 3"/></svg>
        </button>
      </div>

      <button class="new-chat" onclick={desktop.toggleCreateSessionMenu} aria-haspopup="menu" aria-expanded={desktop.createSessionMenu}>
        <span class="plus">＋</span>{desktop.workspace ? '新建会话' : '打开工作区'}<kbd>⌘ K</kbd>
      </button>
      {#if desktop.workspace && desktop.createSessionMenu}
        <div class="session-mode-menu" role="menu" aria-label="选择会话工作区模式">
          <button role="menuitem" onclick={() => void desktop.createSession('local')}>
            <strong>当前工作区</strong><span>Local · 直接使用当前目录</span>
          </button>
          <button role="menuitem" disabled={!desktop.worktreeAvailable} title={desktop.worktreeAvailable ? '从当前已提交 HEAD 创建独立工作树' : 'Worktree 模式需要 Git 仓库和 Git 命令'} onclick={() => void desktop.createSession('worktree')}>
            <strong>隔离工作树</strong><span>{desktop.worktreeAvailable ? 'Worktree · 从当前 HEAD 创建' : '需要 Git 仓库'}</span>
          </button>
        </div>
      {/if}
      {#if desktop.workspace}
        <button class="workspace-row active" title={`切换工作区 · ${desktop.workspace}`} aria-label={`切换工作区 ${desktop.workspace}`} onclick={desktop.openFolderPicker}>
          <span class="folder-icon">▰</span><span class="workspace-name">{desktop.workspace.split(/[\\/]/).at(-1) || desktop.workspace}</span><span class="workspace-menu">⌄</span>
        </button>
        {#if desktop.sidebarView === 'sessions'}
          <input class="session-search" value={desktop.sessionFilter} oninput={(event) => desktop.updateSessionSearch(event.currentTarget.value)} onkeydown={(event) => { if (event.key === 'Escape') desktop.updateSessionSearch(''); }} placeholder="搜索会话和消息" aria-label="搜索会话和消息" />
          {#if desktop.sessionFilter.trim()}
            <div class="session-search-results" aria-label="会话搜索结果" aria-live="polite">
              {#if desktop.sessionSearchLoading}<div class="session-search-state"><span class="spinner"></span>正在搜索历史…</div>
              {:else if desktop.sessionSearchError}<div class="session-search-state error">搜索失败<button class="small-button" onclick={() => desktop.updateSessionSearch(desktop.sessionFilter)}>重试</button></div>
              {:else if !desktop.sessionSearchResults.length}<div class="session-search-state">没有匹配的会话或消息。</div>
              {:else}
                {#if desktop.sessionSearchResults.length >= 100}<div class="session-search-summary">显示最新 100 条结果，可缩小搜索词。</div>{/if}
                {#each desktop.sessionSearchResults as result, index (`${result.sessionId}:${result.messageId || result.matchKind}:${index}`)}
                  <button class="session-search-result" title={`${result.sessionTitle || '新会话'} · ${result.workingDirectory}`} onclick={() => void desktop.openSessionSearchResult(result)}>
                    <span class="session-search-result-heading">
                      <strong>{result.sessionTitle || '新会话'}</strong>
                      <small>{result.mode === 'worktree' ? 'Worktree' : 'Local'} · {result.matchKind === 'title' ? '标题' : result.role === 'user' ? '用户消息' : '助手消息'}</small>
                    </span>
                    <span class="session-search-snippet"><span>{result.snippetBefore}</span><mark>{result.snippetMatch}</mark><span>{result.snippetAfter}</span></span>
                  </button>
                {/each}
              {/if}
            </div>
          {:else}
            <div class="session-list">
              {#each desktop.visibleSessions as item (item.id)}
                <div class:chosen={desktop.session?.id === item.id} class="session-entry">
                  <button class="session-row" title={`${item.title || '新会话'}${item.mode === 'worktree' ? ` · ${item.workingDirectory || item.worktreePath || ''}` : ''}`} onclick={() => void desktop.openSession(item.id)}>
                    <span class="session-dot">{desktop.session?.id === item.id ? '●' : '○'}</span><span class="session-name">{item.title || '新会话'}</span>
                    <span class={`session-mode-badge ${item.mode === 'worktree' ? 'worktree' : ''}`}>{item.mode === 'worktree' ? 'WT' : 'LOCAL'}</span>
                    {#if ['running', 'queued', 'waiting'].includes(item.status ?? '')}<span class={`session-activity ${item.status}`} title={item.status === 'waiting' ? '等待处理' : item.status === 'queued' ? '排队中' : '正在运行'}></span>{/if}
                  </button>
                  <div class="session-row-actions">
                    <button aria-label={`重命名会话 ${item.title || '新会话'}`} title="重命名" disabled={desktop.busy} onclick={(event) => { event.stopPropagation(); desktop.beginRenameSession(item); }}>✎</button>
                    <button aria-label={`删除会话 ${item.title || '新会话'}`} title="删除" disabled={desktop.busy || ['running', 'queued', 'waiting'].includes(item.status ?? '')} onclick={(event) => { event.stopPropagation(); desktop.beginDeleteSession(item); }}>×</button>
                  </div>
                </div>
              {/each}
              {#if !desktop.visibleSessions.length}<div class="session-search-empty">没有匹配的会话</div>{/if}
            </div>
          {/if}
        {:else if desktop.sidebarView === 'pending'}
          <section class="pending-session-list" aria-label="待处理会话">
            <div class="pending-list-summary">{desktop.pendingRequestCount} 项待处理</div>
            {#if desktop.pendingSessionsLoading && !desktop.pendingSessions.length}<div class="pending-list-state"><span class="spinner"></span>正在读取其他会话…</div>
            {:else if desktop.pendingSessionsError}<div class="pending-list-state error">{desktop.pendingSessionsError}<button class="small-button" onclick={() => void desktop.refreshPendingSessions()}>重试</button></div>
            {:else if !desktop.pendingSessions.length}<div class="pending-list-state">当前工作区没有需要处理的会话。</div>
            {:else}
              {#each desktop.pendingSessions as item (item.id)}
                <button class:chosen={desktop.session?.id === item.id} class="pending-session-row" aria-label={`${item.title || '新会话'}，${item.requests.length} 项待处理`} title={`${item.title || '新会话'} · ${item.workingDirectory}`} onclick={() => void desktop.openPendingSession(item.id)}>
                  <span class="pending-session-count">{item.requests.length}</span>
                  <span class="pending-session-main">
                    <strong>{item.title || '新会话'}</strong>
                    <small>{item.mode === 'worktree' ? 'Worktree' : 'Local'} · {item.workingDirectory.split(/[\\/]/).at(-1) || item.workingDirectory}</small>
                    <span class="pending-session-kinds">{#each item.requests as request, index (index)}<i>{desktop.pendingKindLabel(request.kind)}{request.tool ? ` · ${request.tool}` : ''}</i>{/each}</span>
                  </span>
                  <span class="pending-session-arrow">›</span>
                </button>
              {/each}
            {/if}
          </section>
        {:else if desktop.sidebarView === 'processes'}
          <section class="desktop-process-sidebar" aria-label="后台进程列表">
            <div class="desktop-process-sidebar-heading"><span>当前工作区 · {desktop.desktopProcesses.length} 个进程</span><button class="workspace-select-all" aria-label="刷新进程列表" disabled={desktop.desktopProcessesLoading} onclick={() => void desktop.refreshDesktopProcesses()}>{#if desktop.desktopProcessesLoading}<span class="spinner"></span>{:else}↻{/if}</button></div>
            {#if desktop.desktopProcessesLoading && !desktop.desktopProcesses.length}<div class="workspace-change-empty"><span class="spinner"></span>正在读取进程…</div>
            {:else if desktop.desktopProcessesError}<div class="workspace-change-empty error">{desktop.desktopProcessesError}<button class="small-button" onclick={() => void desktop.refreshDesktopProcesses()}>重试</button></div>
            {:else if !desktop.desktopProcesses.length && !desktop.unavailableProcessSessions.length}<div class="workspace-change-empty">当前工作区没有后台进程。</div>
            {:else}
              <div class="desktop-process-sidebar-list">
                {#each desktop.desktopProcessGroups as group (group.id)}
                  <div class="desktop-process-session-group">
                    <div class="desktop-process-session-heading"><strong>{group.title}</strong><small>{group.mode === 'worktree' ? 'Worktree' : 'Local'}</small></div>
                    {#each group.processes as process (process.id)}
                      <button class:chosen={desktop.selectedDesktopProcessKey === desktop.desktopProcessKey(process)} class="desktop-process-sidebar-row" title={process.command} aria-label={`${process.command}，${desktop.processStatusLabel(process.status)}，${group.title}`} onclick={() => void desktop.selectDesktopProcess(process)}>
                        <span class:active={desktop.processStatusActive(process.status)} class:failed={process.status === 'failed'} class="desktop-process-status-dot"></span>
                        <span class="desktop-process-sidebar-main"><strong>{process.command}</strong><small>{desktop.processStatusLabel(process.status)} · {new Date(process.startedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</small></span>
                      </button>
                    {/each}
                  </div>
                {/each}
                {#if desktop.unavailableProcessSessions.length}<div class="desktop-process-unavailable-sidebar"><strong>不可用会话 · {desktop.unavailableProcessSessions.length}</strong>{#each desktop.unavailableProcessSessions as item (item.id)}<small title={item.reason}>{item.title || item.id} · 不可用</small>{/each}</div>{/if}
              </div>
            {/if}
          </section>
        {:else if desktop.sidebarView === 'worktrees'}
          <section class="desktop-worktree-sidebar" aria-label="Desktop Worktree 管理">
            <div class="desktop-worktree-sidebar-heading"><span>当前项目的 Desktop 工作树</span><button class="workspace-select-all" aria-label="刷新工作树列表" disabled={desktop.desktopWorktreesLoading} onclick={() => void desktop.refreshDesktopWorktrees()}>{#if desktop.desktopWorktreesLoading}<span class="spinner"></span>{:else}↻{/if}</button></div>
            {#if desktop.desktopWorktreesLoading && !desktop.desktopWorktrees.length}<div class="workspace-change-empty"><span class="spinner"></span>正在读取 Worktree…</div>
            {:else if desktop.desktopWorktreesError}<div class="workspace-change-empty error">{desktop.desktopWorktreesError}<button class="small-button" onclick={() => void desktop.refreshDesktopWorktrees()}>重试</button></div>
            {:else if !desktop.desktopWorktrees.length}<div class="workspace-change-empty">当前项目没有 Desktop 管理的 Git Worktree。</div>
            {:else}
              <div class="desktop-worktree-sidebar-list">
                {#each desktop.desktopWorktrees as worktree (worktree.path)}
                  <button class:chosen={desktop.selectedDesktopWorktreePath === worktree.path} class="desktop-worktree-sidebar-row" title={worktree.path} onclick={() => desktop.selectedDesktopWorktreePath = worktree.path}>
                    <span class="desktop-worktree-sidebar-name">{desktop.desktopWorktreeName(worktree)}<small>{worktree.orphaned ? '孤儿' : worktree.sessions.length ? `${worktree.sessions.length} 个会话` : worktree.gitRegistered ? 'Git 工作树' : '未登记目录'}</small></span>
                    <span class:dirty={worktree.gitStatus === 'dirty'} class:unavailable={!worktree.available || !worktree.gitRegistered} class="desktop-worktree-status">{desktop.desktopWorktreeStatusLabel(worktree)}</span>
                  </button>
                {/each}
              </div>
            {/if}
          </section>
        {:else if desktop.sidebarView === 'changes'}
          <section class="workspace-changes-sidebar" aria-label="工作区变更文件">
            {#if desktop.workspaceChangesLoading && !desktop.workspaceChanges}<div class="workspace-change-empty"><span class="spinner"></span>正在读取 Git 状态…</div>
            {:else if desktop.workspaceChangesError}<div class="workspace-change-empty error">{desktop.workspaceChangesError}</div>
            {:else if desktop.workspaceChanges && !desktop.workspaceChanges.available}<div class="workspace-change-empty">{desktop.workspaceChanges.message}</div>
            {:else if desktop.workspaceChanges?.files.length === 0}<div class="workspace-change-empty">当前工作区没有相对 HEAD 的变更。</div>
            {:else if desktop.workspaceChanges}
              <div class="workspace-change-sidebar-summary">
                <span>{desktop.workspaceChanges.files.length} 个文件 · +{desktop.workspaceChanges.added} −{desktop.workspaceChanges.removed}</span>
                {#if desktop.workspaceMode === 'worktree'}<button class="workspace-select-all" onclick={desktop.toggleAllApplyPaths}>{desktop.selectedApplyPaths.length === desktop.workspaceChanges.files.length ? '清空' : '全选'}</button>{/if}
              </div>
              <div class="workspace-change-file-list">
                {#each desktop.workspaceChanges.files as file (file.path)}
                  <div class="workspace-change-row-wrap">
                    {#if desktop.workspaceMode === 'worktree'}<input class="workspace-change-select" type="checkbox" checked={desktop.selectedApplyPaths.includes(file.path)} aria-label={`选择 ${file.path} 应用到原项目`} onchange={() => desktop.toggleApplyPath(file.path)} />{/if}
                    <button class:chosen={desktop.selectedWorkspaceChange === file.path} class="workspace-change-row" title={`${file.path} · ${desktop.changeStatusDescription(file.status)}`} aria-label={`${file.path}，${desktop.changeStatusDescription(file.status)}`} onclick={() => void desktop.loadWorkspaceDiff(file.path)}>
                      <span class={`workspace-change-status ${desktop.changeStatusLabel(file.status).toLowerCase()}`}>{desktop.changeStatusLabel(file.status)}</span>
                      <span class="workspace-change-path">{file.path}</span>
                      <span class="workspace-change-stats"><i>+{file.added}</i><i>−{file.removed}</i></span>
                    </button>
                  </div>
                {/each}
              </div>
            {/if}
          </section>
        {:else}
          {#key desktop.workingDirectory}<WorkspaceExplorer sessionID={desktop.session?.id || ''} previewMode={desktop.previewMode} onPreviewFile={desktop.openWorkspaceFile} onError={desktop.showError} />{/key}
        {/if}
      {:else}
        <div class="sidebar-empty">打开一个本地文件夹开始工作。</div>
        {#if desktop.recent?.length}
          <div class="sidebar-section"><span>最近打开</span></div>
          {#each desktop.recent as item (item.path)}
            <button class="recent-row" title={item.path} onclick={() => void desktop.openWorkspace(item.path)}>
              <span class="folder-icon">▰</span><span>{item.path.split(/[\\/]/).at(-1)}</span>
            </button>
          {/each}
        {/if}
      {/if}

      <div class="sidebar-bottom">
        {#if desktop.workspace}<button class="settings-entry" onclick={() => void desktop.openMCPManager()}><span>⎇</span> 项目设置</button>{/if}
      </div>
    </aside>
