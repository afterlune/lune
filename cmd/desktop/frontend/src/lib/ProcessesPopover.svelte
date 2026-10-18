<script lang="ts">
import { onMount, tick } from 'svelte';
import Icon from './Icon.svelte';
import type { DesktopState } from './desktopState.svelte';
let { desktop }: { desktop: DesktopState } = $props();
let popoverRoot = $state<HTMLElement | null>(null);

onMount(() => {
  const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  void tick().then(() => popoverRoot?.focus());
  const closeOnOutsidePointer = (event: PointerEvent) => {
    const target = event.target as Element | null;
    if (target?.closest('.process-popover, .process-chip')) return;
    desktop.processPopoverOpen = false;
  };
  const closeOnEscape = (event: KeyboardEvent) => {
    if (event.key === 'Escape') desktop.processPopoverOpen = false;
  };
  window.addEventListener('pointerdown', closeOnOutsidePointer, true);
  window.addEventListener('keydown', closeOnEscape);
  return () => {
    window.removeEventListener('pointerdown', closeOnOutsidePointer, true);
    window.removeEventListener('keydown', closeOnEscape);
    if (previousFocus?.isConnected) previousFocus.focus();
  };
});
</script>

<div class="process-popover" role="dialog" aria-label="后台进程" tabindex="-1" bind:this={popoverRoot}>
  <header class="process-popover-heading">
    <div>
      <strong>后台进程</strong>
      <span>{desktop.desktopProcesses.length} 个进程 · {desktop.activeDesktopProcessCount} 个运行中</span>
    </div>
    <div class="process-popover-actions">
      <button class="icon-button" aria-label="刷新进程列表" title="刷新" disabled={desktop.desktopProcessesLoading} onclick={() => void desktop.refreshDesktopProcesses()}>{#if desktop.desktopProcessesLoading}<span class="spinner"></span>{:else}<Icon name="refresh" size={17} />{/if}</button>
      <button class="icon-button" aria-label="关闭后台进程" title="关闭" onclick={() => (desktop.processPopoverOpen = false)}><Icon name="close" size={17} /></button>
    </div>
  </header>

  {#if desktop.desktopProcessesError}
    <div class="process-popover-state error">{desktop.desktopProcessesError}<button class="small-button" onclick={() => void desktop.refreshDesktopProcesses()}>重试</button></div>
  {:else if desktop.desktopProcessesLoading && !desktop.desktopProcesses.length}
    <div class="process-popover-state"><span class="spinner"></span>正在读取进程…</div>
  {:else if !desktop.desktopProcesses.length && !desktop.unavailableProcessSessions.length}
    <div class="process-popover-state">当前工作区没有后台进程。</div>
  {:else}
    <div class="process-popover-body">
      <div class="process-popover-list">
        {#each desktop.desktopProcesses as process (process.id)}
          <button class:chosen={desktop.selectedDesktopProcessKey === desktop.desktopProcessKey(process)} class="process-row" title={process.command} aria-label={`${process.command}，${desktop.processStatusLabel(process.status)}，${process.sessionTitle}`} onclick={() => void desktop.selectDesktopProcess(process)}>
            <span class:active={desktop.processStatusActive(process.status)} class:failed={process.status === 'failed'} class="process-status-dot"></span>
            <span class="process-row-main"><strong>{process.command}</strong><small>{process.sessionTitle} · {desktop.processStatusLabel(process.status)}</small></span>
          </button>
        {/each}
        {#if desktop.unavailableProcessSessions.length}
          <div class="process-unavailable">
            <strong>不可用会话</strong>
            {#each desktop.unavailableProcessSessions as item (item.id)}<small title={item.reason}>{item.title || item.id} · {desktop.unavailableProcessStatusLabel(item.status)}</small>{/each}
          </div>
        {/if}
      </div>
      {#if desktop.selectedDesktopProcess}
        <div class="process-detail">
          <header class="process-detail-heading">
            <div><strong title={desktop.selectedDesktopProcess.command}>{desktop.selectedDesktopProcess.command}</strong><small>{desktop.processStatusLabel(desktop.selectedDesktopProcess.status)} · {desktop.selectedDesktopProcess.sessionTitle}</small></div>
            <div class="process-detail-actions">
              <button class="icon-button" aria-label="刷新输出" title="刷新输出" onclick={() => { const process = desktop.selectedDesktopProcess; if (process) void desktop.selectDesktopProcess(process, true); }}>{#if desktop.processOutputLoading}<span class="spinner"></span>{:else}<Icon name="refresh" size={16} />{/if}</button>
              {#if desktop.processStatusActive(desktop.selectedDesktopProcess.status)}<button class="danger-button" disabled={desktop.stoppingDesktopProcessKey === desktop.desktopProcessKey(desktop.selectedDesktopProcess)} onclick={() => { const process = desktop.selectedDesktopProcess; if (process) void desktop.stopDesktopProcess(process); }}>{desktop.stoppingDesktopProcessKey === desktop.desktopProcessKey(desktop.selectedDesktopProcess) ? '停止中…' : '停止'}</button>{/if}
            </div>
          </header>
          {#if desktop.processOutputTruncated}<div class="process-output-notice">较早的输出已超出保留范围。</div>{/if}
          {#if desktop.processOutputError}<div class="process-output-error" role="alert">{desktop.processOutputError}</div>{/if}
          <pre bind:this={desktop.processLogView} onscroll={desktop.handleProcessLogScroll} class="process-output">{desktop.processOutput || (desktop.processOutputLoading ? '等待进程输出…' : '此进程没有输出。')}</pre>
        </div>
      {/if}
    </div>
  {/if}
</div>
