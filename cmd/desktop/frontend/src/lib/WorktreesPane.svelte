<script lang="ts">
import type { DesktopState } from './desktopState.svelte';
let { desktop }: { desktop: DesktopState } = $props();
</script>
<section class="workspace-changes-page desktop-worktrees-page" aria-label="Desktop 工作树管理">
          <header class="workspace-changes-heading">
            <div>
              <p class="eyebrow">DESKTOP · GIT WORKTREES</p>
              <h1>工作树管理</h1>
              <p>只显示当前项目由 Desktop 管理的工作树。清理前会再次检查 Git 状态，忽略文件也会阻止清理。</p>
            </div>
            <div class="workspace-changes-actions">
              <button class="subtle-button workspace-change-refresh" aria-label="刷新工作树列表" disabled={desktop.desktopWorktreesLoading} onclick={() => void desktop.refreshDesktopWorktrees()}>
                {#if desktop.desktopWorktreesLoading}<span class="spinner"></span>{:else}<span>↻</span>{/if}刷新
              </button>
            </div>
          </header>

          {#if desktop.desktopWorktreesError}
            <div class="workspace-changes-state error"><strong>读取工作树失败</strong><span>{desktop.desktopWorktreesError}</span><button class="small-button" onclick={() => void desktop.refreshDesktopWorktrees()}>重试</button></div>
          {:else if desktop.desktopWorktreesLoading && !desktop.desktopWorktrees.length}
            <div class="workspace-changes-state"><span class="spinner"></span><span>正在读取 Git Worktree 列表…</span></div>
          {:else if !desktop.desktopWorktrees.length}
            <div class="workspace-changes-state"><strong>没有 Desktop 工作树</strong><span>此项目当前没有由 Desktop 创建的 Git Worktree。</span></div>
          {:else}
            <div class="desktop-worktree-card-list">
              {#each desktop.desktopWorktrees as worktree (worktree.path)}
                <article class:chosen={desktop.selectedDesktopWorktreePath === worktree.path} class="desktop-worktree-card">
                  <header class="desktop-worktree-card-heading">
                    <div class="desktop-worktree-card-title">
                      <div class="desktop-worktree-card-badges"><span class:orphan={worktree.orphaned} class:unavailable={!worktree.gitRegistered || !worktree.available} class="desktop-worktree-kind">{worktree.orphaned ? '孤儿' : worktree.gitRegistered ? '关联会话' : '未登记目录'}</span><span class:dirty={worktree.gitStatus === 'dirty'} class:unavailable={worktree.gitStatus === 'unavailable'} class="desktop-worktree-status">{desktop.desktopWorktreeStatusLabel(worktree)}</span></div>
                      <h2>{desktop.desktopWorktreeName(worktree)}</h2>
                      <code title={worktree.path}>{worktree.path}</code>
                    </div>
                    <button class="danger-button desktop-worktree-remove" disabled={!worktree.canRemove || desktop.busy} title={worktree.blockReason || ''} onclick={() => desktop.beginDesktopWorktreeRemoval(worktree)}>{worktree.orphaned ? '清理孤儿工作树' : worktree.sessions.length ? `删除 ${worktree.sessions.length} 个会话并清理` : '清理工作树'}</button>
                  </header>
                  {#if worktree.sessions.length}
                    <div class="desktop-worktree-sessions">
                      <strong>关联会话 · {worktree.sessions.length}</strong>
                      {#each worktree.sessions as associated (associated.id)}<div><span>{associated.title || '新会话'}</span><small>{desktop.desktopWorktreeSessionStatusLabel(associated.status)}</small></div>{/each}
                    </div>
                  {:else if worktree.orphaned}
                    <div class="desktop-worktree-empty-association">没有关联的 Desktop 会话；删除会话时保留的 Git 工作树会显示在这里。</div>
                  {/if}
                  {#if worktree.requiresLocalSwitch}<p class="desktop-worktree-block-reason">当前会话位于此工作树；确认后会先切换到 Local。</p>{/if}
                  {#if worktree.blockReason}<p class="desktop-worktree-block-reason">{worktree.blockReason}</p>{/if}
                </article>
              {/each}
            </div>
          {/if}
        </section>
