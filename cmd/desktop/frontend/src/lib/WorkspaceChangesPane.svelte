<script lang="ts">
import type { DesktopState } from './desktopState.svelte';
let { desktop }: { desktop: DesktopState } = $props();
</script>
<section class="workspace-changes-page" aria-label="工作区变更审查">
          <header class="workspace-changes-heading">
            <div>
              <p class="eyebrow">GIT · 相对 HEAD</p>
              <h1>工作区变更</h1>
              <p>{desktop.workspaceMode === 'worktree' ? '查看隔离工作树中的改动，并选择要写回原项目的文件。' : '查看当前会话目录中的暂存、未暂存与未跟踪文件。'}</p>
            </div>
            <div class="workspace-changes-actions">
              {#if desktop.workspaceMode === 'worktree'}<button class="primary-button workspace-change-apply" disabled={!desktop.canApplyWorktreeChanges} title={desktop.worktreeApplyBlockedReason} onclick={desktop.beginWorktreeApply}>应用所选文件{#if desktop.selectedApplyPaths.length} · {desktop.selectedApplyPaths.length}{/if}</button>{/if}
              <button class="subtle-button workspace-change-refresh" aria-label="刷新工作区变更" disabled={desktop.workspaceChangesLoading} onclick={() => void desktop.refreshWorkspaceChanges()}>
                {#if desktop.workspaceChangesLoading}<span class="spinner"></span>{:else}<span>↻</span>{/if}刷新
              </button>
            </div>
          </header>

          {#if desktop.worktreeApplyMessage}<div class="workspace-apply-message" role="status">{desktop.worktreeApplyMessage}</div>{/if}

          {#if desktop.workspaceChangesError}
            <div class="workspace-changes-state error"><strong>读取变更失败</strong><span>{desktop.workspaceChangesError}</span><button class="small-button" onclick={() => void desktop.refreshWorkspaceChanges()}>重试</button></div>
          {:else if desktop.workspaceChangesLoading && !desktop.workspaceChanges}
            <div class="workspace-changes-state"><span class="spinner"></span><span>正在读取 Git 工作区状态…</span></div>
          {:else if desktop.workspaceChanges && !desktop.workspaceChanges.available}
            <div class="workspace-changes-state"><strong>无法读取 Git 变更</strong><span>{desktop.workspaceChanges.message}</span></div>
          {:else if desktop.workspaceChanges?.files.length === 0}
            <div class="workspace-changes-state"><strong>工作区干净</strong><span>当前会话目录没有相对 HEAD 的文件变更。</span></div>
          {:else if desktop.workspaceChanges}
            <section class="workspace-diff-panel">
              <header class="workspace-diff-heading">
                <div class="workspace-diff-path-group">
                  {#if desktop.activeWorkspaceChange}<span class={`workspace-change-status ${desktop.changeStatusLabel(desktop.activeWorkspaceChange.status).toLowerCase()}`}>{desktop.changeStatusLabel(desktop.activeWorkspaceChange.status)}</span>{/if}
                  <strong title={desktop.activeWorkspaceChange?.path}>{desktop.activeWorkspaceChange?.path || '选择一个文件'}</strong>
                  {#if desktop.activeWorkspaceChange?.originalPath}<span class="workspace-diff-original">来自 {desktop.activeWorkspaceChange.originalPath}</span>{/if}
                </div>
                {#if desktop.activeWorkspaceChange}<span class="workspace-diff-stats"><i>+{desktop.activeWorkspaceChange.added}</i><i>−{desktop.activeWorkspaceChange.removed}</i><small>{desktop.changeStatusDescription(desktop.activeWorkspaceChange.status)}</small></span>{/if}
              </header>
              <div class="workspace-diff-scroll">
                {#if desktop.workspaceDiffLoading}<div class="workspace-diff-state"><span class="spinner"></span>正在读取文件差异…</div>
                {:else if desktop.workspaceDiffError}<div class="workspace-diff-state error">{desktop.workspaceDiffError}<button class="small-button" onclick={() => desktop.activeWorkspaceChange && void desktop.loadWorkspaceDiff(desktop.activeWorkspaceChange.path)}>重试</button></div>
                {:else if !desktop.activeWorkspaceChange}<div class="workspace-diff-state">从左侧选择一个文件以查看差异。</div>
                {:else if desktop.workspaceDiff?.binary}<div class="workspace-diff-state"><strong>二进制文件</strong><span>此文件无法作为文本差异显示。</span></div>
                {:else if desktop.workspaceDiff}
                  <pre class="workspace-diff-code" aria-label="Unified diff">{#each desktop.workspaceDiffLines as line, index}<span class:diff-added={line.startsWith('+') && !line.startsWith('+++')} class:diff-removed={line.startsWith('-') && !line.startsWith('---')} class:diff-hunk={line.startsWith('@@')} class:diff-meta={line.startsWith('diff --git') || line.startsWith('index ') || line.startsWith('--- ') || line.startsWith('+++ ') || line.startsWith('new file mode')}>{line || '\u00a0'}</span>{/each}</pre>
                  {#if desktop.workspaceDiff.truncated}<div class="workspace-preview-truncated">差异较大，仅显示前 8 MiB。</div>{/if}
                {/if}
              </div>
            </section>
          {/if}
          {#if desktop.workspaceChanges?.available && desktop.workspaceChanges.files.length > 0}<footer class="workspace-changes-footer">{desktop.workspaceChanges.files.length} 个文件 · +{desktop.workspaceChanges.added} −{desktop.workspaceChanges.removed}{#if desktop.workspaceChanges.refreshedAt}<span>刷新于 {new Date(desktop.workspaceChanges.refreshedAt).toLocaleTimeString()}</span>{/if}</footer>{/if}
        </section>
