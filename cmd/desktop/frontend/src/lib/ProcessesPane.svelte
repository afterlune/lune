<script lang="ts">
import type { DesktopState } from './desktopState.svelte';
let { desktop }: { desktop: DesktopState } = $props();
</script>
<section class="desktop-processes-page" aria-label="跨会话后台进程">
          <header class="workspace-changes-heading">
            <div>
              <p class="eyebrow">DESKTOP · PROCESS CENTER</p>
              <h1>后台进程</h1>
              <p>查看当前工作区所有会话启动的后台命令，选择进程查看实时输出。</p>
            </div>
            <div class="workspace-changes-actions">
              <button class="subtle-button workspace-change-refresh" aria-label="刷新进程列表" disabled={desktop.desktopProcessesLoading} onclick={() => void desktop.refreshDesktopProcesses()}>
                {#if desktop.desktopProcessesLoading}<span class="spinner"></span>{:else}<span>↻</span>{/if}刷新
              </button>
            </div>
          </header>

          {#if desktop.desktopProcessesError}
            <div class="workspace-changes-state error"><strong>读取进程失败</strong><span>{desktop.desktopProcessesError}</span><button class="small-button" onclick={() => void desktop.refreshDesktopProcesses()}>重试</button></div>
          {:else if desktop.desktopProcessesLoading && !desktop.desktopProcesses.length}
            <div class="workspace-changes-state"><span class="spinner"></span><span>正在汇总各会话的进程…</span></div>
          {:else if !desktop.desktopProcesses.length && !desktop.unavailableProcessSessions.length}
            <div class="workspace-changes-state"><strong>没有后台进程</strong><span>当前工作区的会话尚未启动后台命令。</span></div>
          {:else}
            <div class="desktop-process-detail-layout">
              <div class="desktop-process-detail-main">
                {#if desktop.selectedDesktopProcess}
                  <article class="desktop-process-detail-card">
                    <header class="desktop-process-detail-heading">
                      <div class="desktop-process-detail-title">
                        <div class="desktop-process-detail-badges"><span class:active={desktop.processStatusActive(desktop.selectedDesktopProcess.status)} class:failed={desktop.selectedDesktopProcess.status === 'failed'} class="desktop-process-status-label">{desktop.processStatusLabel(desktop.selectedDesktopProcess.status)}</span><span class="desktop-process-mode">{desktop.selectedDesktopProcess.mode === 'worktree' ? 'WORKTREE' : 'LOCAL'}</span><span class="desktop-process-session-title">{desktop.selectedDesktopProcess.sessionTitle}</span></div>
                        <h2 title={desktop.selectedDesktopProcess.command}>{desktop.selectedDesktopProcess.command}</h2>
                        <code title={desktop.selectedDesktopProcess.workingDirectory}>{desktop.selectedDesktopProcess.workingDirectory}</code>
                      </div>
                      <div class="desktop-process-detail-actions">
                        <button class="subtle-button workspace-change-refresh" aria-label="刷新输出" onclick={() => { const process = desktop.selectedDesktopProcess; if (process) void desktop.selectDesktopProcess(process, true); }}>{#if desktop.processOutputLoading}<span class="spinner"></span>{:else}<span>↻</span>{/if}</button>
                        {#if desktop.processStatusActive(desktop.selectedDesktopProcess.status)}<button class="danger-button desktop-process-stop" disabled={desktop.stoppingDesktopProcessKey === desktop.desktopProcessKey(desktop.selectedDesktopProcess)} onclick={() => { const process = desktop.selectedDesktopProcess; if (process) void desktop.stopDesktopProcess(process); }}>{desktop.stoppingDesktopProcessKey === desktop.desktopProcessKey(desktop.selectedDesktopProcess) ? '正在停止…' : '停止进程'}</button>{/if}
                      </div>
                    </header>
                    <section class="desktop-process-output-panel" aria-label="进程输出">
                      <header><strong>输出</strong><span>{desktop.processStatusActive(desktop.processOutputStatus) ? '实时跟随' : '进程已结束'} · {desktop.processOutputEndCursor.toLocaleString()} 字节</span></header>
                      {#if desktop.processOutputTruncated}<div class="desktop-process-output-notice">较早的输出已超出保留范围，以下显示当前可读取部分。</div>{/if}
                      {#if desktop.processOutputError}<div class="desktop-process-output-error" role="alert">{desktop.processOutputError}<button class="small-button" onclick={() => { const process = desktop.selectedDesktopProcess; if (process) void desktop.selectDesktopProcess(process, true); }}>重试读取</button></div>{/if}
                      <pre bind:this={desktop.processLogView} onscroll={desktop.handleProcessLogScroll} class="desktop-process-output">{desktop.processOutput || (desktop.processOutputLoading ? '等待进程输出…' : '此进程没有输出。')}</pre>
                    </section>
                  </article>
                {:else}
                  <div class="workspace-changes-state"><strong>选择一个进程</strong><span>从左侧列表选择命令以查看详情和输出。</span></div>
                {/if}
              </div>
              {#if desktop.unavailableProcessSessions.length}
                <aside class="desktop-process-unavailable" aria-label="不可用的会话">
                  <h2>不可用会话 · {desktop.unavailableProcessSessions.length}</h2>
                  <p>这些会话的运行时当前无法访问，因此无法读取或停止其进程。</p>
                  {#each desktop.unavailableProcessSessions as item (item.id)}
                    <article><div><strong>{item.title || '未命名会话'}</strong><span>{item.mode === 'worktree' ? 'Worktree' : 'Local'} · {desktop.unavailableProcessStatusLabel(item.status)}</span></div><code title={item.workingDirectory}>{item.workingDirectory}</code><small>{item.reason}</small></article>
                  {/each}
                </aside>
              {/if}
            </div>
          {/if}
          {#if desktop.desktopProcesses.length > 0}<footer class="workspace-changes-footer">{desktop.desktopProcesses.length} 个进程 · {desktop.activeDesktopProcessCount} 个运行中</footer>{/if}
        </section>
