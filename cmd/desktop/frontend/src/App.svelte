<script lang="ts">
import DesktopDialogs from './lib/DesktopDialogs.svelte';
import ConversationPane from './lib/ConversationPane.svelte';
import Icon from './lib/Icon.svelte';
import ProcessesPopover from './lib/ProcessesPopover.svelte';
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
    {#if !desktop.sidebarCollapsed}<button class="sidebar-scrim" aria-label="关闭侧栏" onclick={() => desktop.sidebarCollapsed = true}></button>{/if}
    <WorkspaceSidebar {desktop} />

    <main class="main-panel">
      <header class="topbar">
        <div class="topbar-leading">
          {#if desktop.sidebarCollapsed}
            <button class="icon-button sidebar-expand-button" aria-label="展开侧栏" title="展开侧栏" onclick={() => desktop.sidebarCollapsed = false}>
              <Icon name="sidebar-expand" size={18} />
            </button>
          {/if}
          <div class="topbar-heading">
            <div class="topbar-title-line">
              <strong class="topbar-title">{desktop.pageTitle}</strong>
              {#if desktop.running || desktop.pending.length > 0 || desktop.queuedMessageCount > 0}
                <span class="workspace-run-status" class:waiting={!desktop.running}>
                  <span class:active={desktop.running} class:waiting={!desktop.running} class="topbar-state-dot"></span>
                  <span>{desktop.running ? '执行中' : desktop.pending.length > 0 ? '等待用户操作' : '任务排队中'}</span>
                  {#if desktop.running && desktop.activities.length}<span class="run-tool-count">· {desktop.activities.length} 个工具</span>{/if}
                </span>
              {/if}
            </div>
            {#if desktop.workspace}
              <div class="topbar-subtitle" title={desktop.workspace}>{desktop.workspace.split(/[\\/]/).at(-1) || desktop.workspace}</div>
            {/if}
          </div>
        </div>
        <div class="top-actions">
          {#if desktop.desktopProcesses.length || desktop.unavailableProcessSessions.length}
            <button class="process-chip" class:active={desktop.activeDesktopProcessCount > 0} aria-label={`后台进程${desktop.activeDesktopProcessCount ? `，${desktop.activeDesktopProcessCount} 项运行中` : ''}`} aria-expanded={desktop.processPopoverOpen} title="后台进程" onclick={() => desktop.processPopoverOpen = !desktop.processPopoverOpen}>
              <span class="process-chip-dot" class:running={desktop.activeDesktopProcessCount > 0}></span>
              <span>{desktop.activeDesktopProcessCount ? `${desktop.activeDesktopProcessCount} 运行中` : desktop.desktopProcesses.length ? `${desktop.desktopProcesses.length} 个进程` : '后台进程'}</span>
            </button>
          {/if}
          <div class="theme-switch" role="group" aria-label="主题快捷切换">
            <button class:active={!desktop.effectiveDarkTheme} aria-label="切换到浅色主题" aria-pressed={!desktop.effectiveDarkTheme} onclick={() => void desktop.changeTheme('light')} title="切换到浅色主题">
              <Icon name="sun" size={17} />
            </button>
            <button class:active={desktop.effectiveDarkTheme} aria-label="切换到深色主题" aria-pressed={desktop.effectiveDarkTheme} onclick={() => void desktop.changeTheme('dark')} title="切换到深色主题">
              <Icon name="moon" size={17} />
            </button>
          </div>
          <div class="workspace-menu-anchor">
            <button class="icon-button top-overflow" aria-label="更多工作区操作" aria-expanded={desktop.workspaceMenuOpen} title="更多操作" onclick={() => desktop.workspaceMenuOpen = !desktop.workspaceMenuOpen}><Icon name="more" size={18} /></button>
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
          <h1>把想法带进你的工作区</h1>
          <p class="welcome-copy">选择一个本地项目文件夹，与 Agent 一起阅读代码、讨论方案并完成修改。</p>
          <button class="primary-button open-workspace" onclick={desktop.openFolderPicker}><span class="button-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M3.5 7.5a2 2 0 0 1 2-2h5l2 2h6a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2Z"/></svg></span> 打开工作区</button>
          {#if !desktop.models.configured}<button class="inline-link welcome-config" onclick={desktop.setSettingsForm}>先配置模型以开始对话</button>{/if}
        </section>
      {:else if !desktop.session}
        <section class="workspace-loading"><div class="spinner"></div><span>正在载入会话…</span></section>
      {:else}
        <ConversationPane {desktop} />
      {/if}

      {#if desktop.processPopoverOpen}<ProcessesPopover {desktop} />{/if}
    </main>
  </div>
{/if}

{#if desktop.errorMessage}<div class="toast-error" role="status">{desktop.errorMessage}<button aria-label="关闭错误提示" onclick={() => desktop.errorMessage = ''}><Icon name="close" size={16} /></button></div>{/if}

<DesktopDialogs {desktop} />
