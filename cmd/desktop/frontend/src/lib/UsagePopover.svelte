<script lang="ts">
import { onMount, tick } from 'svelte';
import Icon from './Icon.svelte';
import type { DesktopState } from './desktopState.svelte';

let { desktop }: { desktop: DesktopState } = $props();
let popoverRoot = $state<HTMLElement | null>(null);

function formatTokens(value: number | undefined): string {
  const n = value ?? 0;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

onMount(() => {
  const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  void tick().then(() => popoverRoot?.focus());
  const closeOnOutsidePointer = (event: PointerEvent) => {
    const target = event.target as Element | null;
    if (target?.closest('.usage-popover, .usage-chip')) return;
    desktop.usagePopoverOpen = false;
  };
  const closeOnEscape = (event: KeyboardEvent) => {
    if (event.key === 'Escape') desktop.usagePopoverOpen = false;
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

<div class="process-popover usage-popover" role="dialog" aria-label="上下文与用量" tabindex="-1" bind:this={popoverRoot}>
  <header class="process-popover-heading">
    <div>
      <strong>上下文与用量</strong>
      <span>
        {#if desktop.effectiveContextBudget > 0}
          当前上下文 {formatTokens(desktop.sessionUsage.lastInputTokens || desktop.sessionUsage.lastEstimatedInput)} / {formatTokens(desktop.effectiveContextBudget)}
          {#if desktop.contextUsagePercent !== null}（{desktop.contextUsagePercent}%）{/if}
        {:else}
          {desktop.sessionUsage.requests} 次请求 · {formatTokens(desktop.sessionUsage.total)} tokens
        {/if}
      </span>
    </div>
    <div class="process-popover-actions">
      <button class="icon-button" aria-label="关闭上下文与用量" title="关闭" onclick={() => (desktop.usagePopoverOpen = false)}><Icon name="close" size={17} /></button>
    </div>
  </header>

  <div class="usage-popover-body">
    {#if desktop.effectiveContextBudget > 0}
      <div class="usage-meter-section">
        <div class="usage-meter-bar" aria-hidden="true">
          <div class="usage-meter-fill" class:high={(desktop.contextUsagePercent ?? 0) >= 80} style={`width: ${desktop.contextUsagePercent ?? 0}%`}></div>
        </div>
        <div class="usage-meter-meta">
          <span>上下文窗口 {formatTokens(desktop.sessionUsage.contextWindow)}</span>
          <span>自动压缩 {desktop.sessionUsage.autoCompact ? `开启（${Math.round((desktop.sessionUsage.autoCompactThreshold ?? 0.85) * 100)}%）` : '关闭'}</span>
        </div>
      </div>
    {/if}

    <dl class="usage-stats-grid">
      <div><dt>请求次数</dt><dd>{desktop.sessionUsage.requests}（实测 {desktop.sessionUsage.measured}）</dd></div>
      <div><dt>累计输入</dt><dd>{formatTokens(desktop.sessionUsage.input)}</dd></div>
      <div><dt>缓存命中</dt><dd>{formatTokens(desktop.sessionUsage.cached)}</dd></div>
      <div><dt>累计输出</dt><dd>{formatTokens(desktop.sessionUsage.output)}</dd></div>
      <div><dt>推理用量</dt><dd>{formatTokens(desktop.sessionUsage.reasoning)}</dd></div>
      <div><dt>累计总计</dt><dd>{formatTokens(desktop.sessionUsage.total)}</dd></div>
    </dl>

    {#if desktop.compactionNotice}
      <div class="usage-compaction-note" class:error={desktop.compactionNotice.stage === 'failed'}>
        {#if desktop.compactionNotice.stage === 'started'}
          正在压缩历史上下文…
        {:else if desktop.compactionNotice.stage === 'failed'}
          上下文压缩失败：{desktop.compactionNotice.error || '未知错误'}
        {:else if desktop.compactionNotice.stage === 'skipped'}
          当前历史无需压缩（约 {formatTokens(desktop.compactionNotice.beforeTokens)} tokens）。
        {:else}
          已压缩上下文：{formatTokens(desktop.compactionNotice.beforeTokens)} → {formatTokens(desktop.compactionNotice.afterTokens)} tokens
          {#if desktop.compactionNotice.turns}（保留最近 {desktop.compactionNotice.turns} 轮）{/if}
        {/if}
      </div>
    {/if}

    <div class="usage-popover-footer">
      <button class="primary-button" disabled={desktop.compactingSession || desktop.running || !desktop.session} onclick={() => void desktop.compactSession()}>
        {desktop.compactingSession ? '压缩中…' : '压缩上下文'}
      </button>
    </div>
  </div>
</div>
