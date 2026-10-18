<script lang="ts">
import DesktopDialogs from './lib/DesktopDialogs.svelte';
import ConversationPane from './lib/ConversationPane.svelte';
import WorkspaceChangesPane from './lib/WorkspaceChangesPane.svelte';
import ProcessesPane from './lib/ProcessesPane.svelte';
import WorktreesPane from './lib/WorktreesPane.svelte';
import WorkspaceSidebar from './lib/WorkspaceSidebar.svelte';
import { createDesktopState } from './lib/desktopState.svelte';
const desktop = createDesktopState();
</script>

<svelte:head>
  <title>{desktop.session?.title ? `${desktop.session.title} · luneAgent` : 'luneAgent'}</title>
</svelte:head>

{#if desktop.loading}
  <div class="boot"><span class="brand-mark">l</span><span>正在启动 luneAgent…</span></div>
{:else}
  <div class="app-shell" role="region" aria-label="桌面工作区拖放区域" ondragover={(event) => event.preventDefault()} ondrop={desktop.onDrop}>
    {#if desktop.theme === 'anime'}
      <div class="wallpaper-layer" aria-hidden="true">
        {#if desktop.wallpaperURL && desktop.appVisible && (desktop.activeWallpaperType !== 'dynamic' || !desktop.reducedMotion)}
          {#if desktop.activeWallpaperType === 'dynamic' && /\.(mp4|webm)(?:$|\?)/i.test(desktop.wallpaperURL)}
            <video bind:this={desktop.wallpaperVideo} src={desktop.wallpaperURL} autoplay muted loop playsinline></video>
          {:else}
            <img src={desktop.wallpaperURL} alt="" />
          {/if}
        {/if}
      </div>
    {/if}
    <nav class="navigation-rail" aria-label="主导航">
      <div class="rail-brand" aria-hidden="true">l</div>
      <div class="rail-items">
        <button class:active={desktop.sidebarView === 'sessions'} class="rail-link" aria-current={desktop.sidebarView === 'sessions' ? 'page' : undefined} aria-label="会话" title="会话" onclick={() => desktop.selectSidebarView('sessions')}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 5.5h14a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-7l-5 3v-3H5a2 2 0 0 1-2-2v-9a2 2 0 0 1 2-2Z"/><path d="M8 10h8M8 14h5"/></svg><span>会话</span>
        </button>
        <button class="rail-link" aria-label="文件" title="文件" disabled={!desktop.workspace} aria-disabled={!desktop.workspace} class:active={desktop.sidebarView === 'files'} aria-current={desktop.sidebarView === 'files' ? 'page' : undefined} onclick={() => desktop.selectSidebarView('files')}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3.5h8l4 4v13H6a2 2 0 0 1-2-2v-13a2 2 0 0 1 2-2Z"/><path d="M14 3.5v5h5M8 13h8M8 16.5h8"/></svg><span>文件</span>
        </button>
        <button class="rail-link" aria-label="变更" title="变更" disabled={!desktop.workspace} aria-disabled={!desktop.workspace} class:active={desktop.sidebarView === 'changes'} aria-current={desktop.sidebarView === 'changes' ? 'page' : undefined} onclick={() => desktop.selectSidebarView('changes')}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="6" cy="5" r="2.2"/><circle cx="18" cy="19" r="2.2"/><circle cx="18" cy="5" r="2.2"/><path d="M8.2 5h5.6A4.2 4.2 0 0 1 18 9.2v7.6M6 7.2v9.6A2.2 2.2 0 0 0 8.2 19h7.6"/></svg><span>变更</span>
        </button>
        <button class="rail-link" aria-label="工作树" title="工作树" disabled={!desktop.workspace} aria-disabled={!desktop.workspace} class:active={desktop.sidebarView === 'worktrees'} aria-current={desktop.sidebarView === 'worktrees' ? 'page' : undefined} onclick={() => desktop.selectSidebarView('worktrees')}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3.5 7.5a2 2 0 0 1 2-2h5l2 2h6a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2Z"/><path d="M3.5 10h17"/></svg><span>工作树</span>
        </button>
        <button class="rail-link" aria-label={`待处理${desktop.pendingRequestCount ? `，${desktop.pendingRequestCount} 项` : ''}`} title="待处理" disabled={!desktop.workspace} aria-disabled={!desktop.workspace} class:active={desktop.sidebarView === 'pending'} aria-current={desktop.sidebarView === 'pending' ? 'page' : undefined} onclick={() => desktop.selectSidebarView('pending')}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="4" y="4" width="16" height="16" rx="3"/><path d="m8 12 2.4 2.4L16 9"/></svg><span>待处理</span>
          {#if desktop.pendingRequestCount}<span class="rail-badge">{desktop.pendingRequestCount > 9 ? '9+' : desktop.pendingRequestCount}</span>{/if}
        </button>
        <button class="rail-link" aria-label={`进程${desktop.activeDesktopProcessCount ? `，${desktop.activeDesktopProcessCount} 项运行中` : ''}`} title="进程" disabled={!desktop.workspace} aria-disabled={!desktop.workspace} class:active={desktop.sidebarView === 'processes'} aria-current={desktop.sidebarView === 'processes' ? 'page' : undefined} onclick={() => desktop.selectSidebarView('processes')}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3.5" y="4" width="17" height="13" rx="2"/><path d="M8 21h8M12 17v4M7.5 9.5h4M7.5 12.5h7"/></svg><span>进程</span>
          {#if desktop.activeDesktopProcessCount}<span class="rail-activity-dot"></span>{/if}
        </button>
      </div>
      <div class="rail-bottom">
        <button class="rail-link rail-settings nav-settings" aria-label="设置" title="模型与偏好设置" onclick={desktop.setSettingsForm}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9.7 3.8.5-1.3h3.6l.5 1.3a2 2 0 0 0 2.5 1l1.3-.5 2.5 2.5-.5 1.3a2 2 0 0 0 1 2.5l1.3.5v3.6l-1.3.5a2 2 0 0 0-1 2.5l.5 1.3-2.5 2.5-1.3-.5a2 2 0 0 0-2.5 1l-.5 1.3h-3.6l-.5-1.3a2 2 0 0 0-2.5-1l-1.3.5-2.5-2.5.5-1.3a2 2 0 0 0-1-2.5l-1.3-.5v-3.6l1.3-.5a2 2 0 0 0 1-2.5l-.5-1.3 2.5-2.5 1.3.5a2 2 0 0 0 2.5-1Z" transform="translate(0 1) scale(.9)"/><circle cx="12" cy="12" r="3"/></svg><span>设置</span>
        </button>
        <div class="rail-local-profile" role="img" aria-label="本地工作区" title={desktop.models.configured ? `本地工作区 · 默认模型 ${desktop.models.defaultModel}` : '本地工作区 · 尚未配置模型'}>L</div>
      </div>
    </nav>
    {#if !desktop.sidebarCollapsed}<button class="sidebar-scrim" aria-label="关闭侧栏" onclick={() => desktop.sidebarCollapsed = true}></button>{/if}
    <WorkspaceSidebar {desktop} />

    <main class="main-panel">
      <header class="topbar">
        <div class="topbar-leading">
          {#if desktop.sidebarCollapsed}
            <button class="icon-button sidebar-expand-button" aria-label="展开侧栏" title="展开侧栏" onclick={() => desktop.sidebarCollapsed = false}>
              <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3.5" y="4" width="17" height="16" rx="2"/><path d="M9 4v16"/><path d="m13 9 3 3-3 3"/></svg>
            </button>
          {/if}
          <div class="topbar-heading">
            <div class="topbar-title-line">
              <strong class="topbar-title">{desktop.pageTitle}</strong>
              {#if desktop.workspace && !['changes', 'worktrees', 'processes'].includes(desktop.sidebarView)}
                <span class={`crumb-mode ${desktop.workspaceMode === 'worktree' ? 'worktree' : ''}`}>{desktop.workspaceMode === 'worktree' ? 'WORKTREE' : 'LOCAL'}</span>
              {/if}
            </div>
            {#if desktop.workspace}
              <div class="topbar-subtitle" title={desktop.workspaceMode === 'worktree' ? desktop.workingDirectory : desktop.workspace}>
                {#if desktop.workspaceMode === 'worktree'}<span class="crumb-working-path">{desktop.workingDirectory}</span>{:else}{desktop.workspace.split(/[\\/]/).at(-1) || desktop.workspace}{/if}
              </div>
            {/if}
            {#if desktop.running || desktop.pending.length > 0 || desktop.queuedMessageCount > 0}
              <div class="workspace-run-status" class:waiting={!desktop.running}>
                <span class:active={desktop.running} class:waiting={!desktop.running} class="topbar-state-dot"></span>
                <span>{desktop.running ? '执行中' : desktop.pending.length > 0 ? '等待用户操作' : '任务排队中'}</span>
                {#if desktop.running && desktop.activities.length}<span class="run-tool-count">· 已使用 {desktop.activities.length} 个工具</span>{/if}
              </div>
            {/if}
          </div>
        </div>
        <div class="top-actions">
          <div class="theme-switch" role="group" aria-label="主题快捷切换">
            <button class:active={!desktop.effectiveDarkTheme} aria-label="切换到浅色主题" aria-pressed={!desktop.effectiveDarkTheme} onclick={() => void desktop.changeTheme('light')} title="切换到浅色主题">☼ 浅色</button>
            <button class:active={desktop.effectiveDarkTheme} aria-label="切换到深色主题" aria-pressed={desktop.effectiveDarkTheme} onclick={() => void desktop.changeTheme('dark')} title="切换到深色主题">☾ 深色</button>
          </div>
          <div class="workspace-menu-anchor">
            <button class="icon-button top-overflow" aria-label="更多工作区操作" aria-expanded={desktop.workspaceMenuOpen} title="更多操作" onclick={() => desktop.workspaceMenuOpen = !desktop.workspaceMenuOpen}>⋮</button>
            {#if desktop.workspaceMenuOpen}
              <div class="topbar-menu" role="menu">
                {#if desktop.workspace}
                  <button role="menuitem" onclick={() => { desktop.workspaceMenuOpen = false; void desktop.openFolderPicker(); }}>切换工作区</button>
                  <button role="menuitem" onclick={() => { desktop.workspaceMenuOpen = false; void desktop.closeWorkspace(); }}>关闭工作区</button>
                {:else}
                  <button role="menuitem" onclick={() => { desktop.workspaceMenuOpen = false; void desktop.openFolderPicker(); }}>打开工作区</button>
                {/if}
              </div>
            {/if}
          </div>
        </div>
      </header>

      {#if !desktop.workspace}
        <section class="welcome">
          <div class="welcome-icon"><span>l</span></div>
          <p class="eyebrow">LUNEAGENT DESKTOP</p>
          <h1>把想法带进你的工作区</h1>
          <p class="welcome-copy">选择一个本地项目文件夹，与 Agent 一起阅读代码、讨论方案并完成修改。</p>
          <button class="primary-button open-workspace" onclick={desktop.openFolderPicker}><span>▰</span> 打开工作区</button>
          {#if !desktop.models.configured}<button class="inline-link welcome-config" onclick={desktop.setSettingsForm}>先配置模型以开始对话</button>{/if}
        </section>
      {:else if desktop.sidebarView === 'worktrees'}
        <WorktreesPane {desktop} />
      {:else if desktop.sidebarView === 'processes'}
        <ProcessesPane {desktop} />
      {:else if !desktop.session}
        <section class="workspace-loading"><div class="spinner"></div><span>正在载入会话…</span></section>
      {:else if desktop.sidebarView === 'changes'}
        <WorkspaceChangesPane {desktop} />
      {:else}<ConversationPane {desktop} />{/if}
    </main>
  </div>
{/if}

{#if desktop.errorMessage}<div class="toast-error" role="status">{desktop.errorMessage}<button onclick={() => desktop.errorMessage = ''}>×</button></div>{/if}

<DesktopDialogs {desktop} />
