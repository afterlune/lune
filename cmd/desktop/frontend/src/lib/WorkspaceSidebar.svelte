<script lang="ts">
import { onMount } from 'svelte';
import type { DesktopState } from './desktopState.svelte';
let { desktop }: { desktop: DesktopState } = $props();
let searchInput = $state<HTMLInputElement | null>(null);

onMount(() => {
  const onKeydown = (event: KeyboardEvent) => {
    if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 'k') return;
    event.preventDefault();
    desktop.sidebarCollapsed = false;
    desktop.selectSidebarView('sessions');
    window.setTimeout(() => searchInput?.focus(), 0);
  };
  window.addEventListener('keydown', onKeydown);
  return () => window.removeEventListener('keydown', onKeydown);
});
</script>
<aside class="sidebar" class:sidebar-collapsed={desktop.sidebarCollapsed} aria-label="工作区内容侧栏">
  <div class="brand-row">
    <div class="brand-copy"><strong>luneAgent</strong><span>本地 Agent 工作台</span></div>
    <button class="icon-button sidebar-collapse-button" aria-label="收起侧栏" title="收起侧栏" onclick={() => desktop.sidebarCollapsed = true}>
      <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3.5" y="4" width="17" height="16" rx="2"/><path d="M9 4v16"/><path d="m15 9-3 3 3 3"/></svg>
    </button>
  </div>

  <button class="new-chat" onclick={() => desktop.workspace ? void desktop.createSession() : desktop.openFolderPicker()}>
    <span class="plus">＋</span>{desktop.workspace ? '新建会话' : '打开工作区'}<kbd>Ctrl K</kbd>
  </button>

  {#if desktop.workspace}
    <button class="workspace-row active" title={`切换工作区 · ${desktop.workspace}`} aria-label={`切换工作区 ${desktop.workspace}`} onclick={desktop.openFolderPicker}>
      <span class="folder-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M3.5 7.5a2 2 0 0 1 2-2h5l2 2h6a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2Z"/></svg></span><span class="workspace-name">{desktop.workspace.split(/[\\/]/).at(-1) || desktop.workspace}</span><span class="workspace-menu">⌄</span>
    </button>

    <nav class="sidebar-nav" aria-label="主导航">
      <button class:active={desktop.sidebarView === 'sessions'} class="nav-row" aria-current={desktop.sidebarView === 'sessions' ? 'page' : undefined} onclick={() => desktop.selectSidebarView('sessions')}>
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 5.5h14a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-7l-5 3v-3H5a2 2 0 0 1-2-2v-9a2 2 0 0 1 2-2Z"/><path d="M8 10h8M8 14h5"/></svg>
        <span>会话</span>
      </button>
      <button class:active={desktop.sidebarView === 'pending'} class="nav-row" aria-current={desktop.sidebarView === 'pending' ? 'page' : undefined} aria-label={`待处理${desktop.pendingRequestCount ? `，${desktop.pendingRequestCount} 项` : ''}`} onclick={() => desktop.selectSidebarView('pending')}>
        <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="4" y="4" width="16" height="16" rx="3"/><path d="m8 12 2.4 2.4L16 9"/></svg>
        <span>待处理</span>
        {#if desktop.pendingRequestCount}<span class="nav-badge">{desktop.pendingRequestCount > 9 ? '9+' : desktop.pendingRequestCount}</span>{/if}
      </button>
    </nav>

    {#if desktop.sidebarView === 'sessions'}
      <input bind:this={searchInput} class="session-search" value={desktop.sessionFilter} oninput={(event) => desktop.updateSessionSearch(event.currentTarget.value)} onkeydown={(event) => { if (event.key === 'Escape') desktop.updateSessionSearch(''); }} placeholder="搜索会话" aria-label="搜索会话和消息" />
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
                  <small>{result.matchKind === 'title' ? '标题' : result.role === 'user' ? '用户消息' : '助手消息'}</small>
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
              <button class="session-row" title={item.title || '新会话'} onclick={() => void desktop.openSession(item.id)}>
                <span class="session-dot">{desktop.session?.id === item.id ? '●' : '○'}</span><span class="session-name">{item.title || '新会话'}</span>
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
                <small>{item.workingDirectory.split(/[\\/]/).at(-1) || item.workingDirectory}</small>
                <span class="pending-session-kinds">{#each item.requests as request, index (index)}<i>{desktop.pendingKindLabel(request.kind)}{request.tool ? ` · ${request.tool}` : ''}</i>{/each}</span>
              </span>
              <span class="pending-session-arrow">›</span>
            </button>
          {/each}
        {/if}
      </section>
    {/if}
  {:else}
    <div class="sidebar-empty">打开一个本地文件夹开始工作。</div>
    {#if desktop.recent?.length}
      <div class="sidebar-section"><span>最近打开</span></div>
      {#each desktop.recent as item (item.path)}
        <button class="recent-row" title={item.path} onclick={() => void desktop.openWorkspace(item.path)}>
          <span class="folder-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M3.5 7.5a2 2 0 0 1 2-2h5l2 2h6a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2Z"/></svg></span><span>{item.path.split(/[\\/]/).at(-1)}</span>
        </button>
      {/each}
    {/if}
  {/if}

  <div class="sidebar-bottom">
    {#if desktop.workspace}<button class="settings-entry" onclick={() => void desktop.openMCPManager()}><span>⎇</span> 项目设置</button>{/if}
    <button class="settings-entry nav-settings" aria-label="设置" onclick={desktop.setSettingsForm}><span>⚙</span> 设置</button>
  </div>
</aside>
