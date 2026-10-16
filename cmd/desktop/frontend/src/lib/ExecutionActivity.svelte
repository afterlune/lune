<script lang="ts">
  type ActivityTool = {
    id: string;
    invocationId: string;
    label: string;
    detail: string;
    arguments?: unknown;
    details?: {
      output?: string;
      command?: { text: string; output?: string; status?: string; exitCode?: number | null; truncated?: boolean; error?: string };
      changes?: { path: string; added: number; removed: number; lines?: { kind: string; oldLine?: number; newLine?: number; text: string }[]; truncated?: boolean }[];
      truncated?: boolean;
    };
    status: 'running' | 'done' | 'failed' | 'cancelled' | 'info' | 'waiting';
    durationMs?: number;
    error?: string;
    truncated?: boolean;
  };
  type ActivityRun = {
    key: string;
    executionId: string;
    userMessageIndex: number;
    status: string;
    statusLabel: string;
    startedAt: string;
    finishedAt?: string | null;
    tools: ActivityTool[];
    expanded: boolean;
    elapsedSeconds?: number;
  };

  let { run, active = false, elapsedSeconds = 0, onToggle }: {
    run: ActivityRun;
    active?: boolean;
    elapsedSeconds?: number;
    onToggle: () => void;
  } = $props();

  const latestTool = $derived(run.tools.at(-1));

  function formatElapsed(seconds: number) {
    if (seconds < 60) return `用时 ${seconds} 秒`;
    return `用时 ${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
  }

  function formatArguments(value: unknown) {
    try { return JSON.stringify(typeof value === 'string' ? JSON.parse(value) : value, null, 2); }
    catch { return String(value ?? ''); }
  }

  function toolStatus(status: ActivityTool['status']) {
    if (status === 'running') return '进行中';
    if (status === 'failed') return '失败';
    if (status === 'cancelled') return '已取消';
    return '完成';
  }

</script>

<section class:status-waiting={run.status === 'paused'} class:status-failed={run.status === 'failed'} class:status-interrupted={run.status === 'interrupted' || run.status === 'cancelled'} class="turn-progress" aria-label="本轮执行状态">
  <div class="turn-status-row">
    <span class:status-spinner-active={active && run.status === 'running'} class:status-indicator={true} aria-hidden="true"></span>
    <span class="turn-status-text" role="status" aria-live="polite">{run.statusLabel}</span>
    {#if active && elapsedSeconds > 0}<span class="turn-elapsed">{formatElapsed(elapsedSeconds)}</span>{/if}
    {#if run.tools.length}
      <button class="activity-toggle" aria-expanded={run.expanded} onclick={onToggle}>
        {run.tools.length} 步骤 <span aria-hidden="true">{run.expanded ? '⌃' : '⌄'}</span>
      </button>
    {/if}
  </div>
  {#if latestTool}
    <div class="activity-current">
      <span>{latestTool.status === 'running' ? '当前操作' : '最近操作'}</span>
      <strong>{latestTool.label}</strong>
      {#if latestTool.detail}<code>{latestTool.detail}</code>{/if}
    </div>
  {/if}
  {#if run.expanded && run.tools.length}
    <ol class="activity-list" aria-label="本轮执行详情">
      {#each run.tools as activity (activity.invocationId || activity.id)}
        <li class:activity-failed={activity.status === 'failed'} class:activity-cancelled={activity.status === 'cancelled'} class="activity-row">
          <span class:activity-dot-live={activity.status === 'running'} class="activity-dot" aria-hidden="true">{activity.status === 'failed' ? '!' : activity.status === 'cancelled' ? '–' : activity.status === 'done' ? '✓' : '•'}</span>
          <details class="activity-tool-details">
            <summary>
              <span class="activity-label">{activity.label}</span>
              {#if activity.detail}<code class="activity-detail">{activity.detail}</code>{/if}
              <span class="activity-result">{toolStatus(activity.status)}</span>
            </summary>
            <div class="activity-tool-extra">
              {#if activity.arguments}<section><span>调用参数</span><pre>{formatArguments(activity.arguments)}</pre></section>{/if}
              {#if activity.details?.command}
                <section><span>命令</span><pre>{activity.details.command.text}</pre></section>
                {#if activity.details.command.output}<section><span>命令输出</span><pre>{activity.details.command.output}</pre></section>{/if}
                {#if activity.details.command.status}<span>状态：{activity.details.command.status}</span>{/if}
                {#if activity.details.command.exitCode !== undefined && activity.details.command.exitCode !== null}<span>退出码：{activity.details.command.exitCode}</span>{/if}
              {/if}
              {#if activity.details?.output}<section><span>执行结果</span><pre>{activity.details.output}</pre></section>{/if}
              {#each activity.details?.changes ?? [] as change (change.path)}
                <section class="activity-change"><span>{change.path} · +{change.added} / −{change.removed}</span>
                  {#if change.lines?.length}<pre>{change.lines.map((line) => `${line.kind === 'add' ? '+' : line.kind === 'remove' ? '−' : ' '} ${line.text}`).join('\n')}</pre>{/if}
                </section>
              {/each}
              {#if activity.durationMs}<span>用时 {activity.durationMs < 1000 ? `${activity.durationMs} 毫秒` : `${(activity.durationMs / 1000).toFixed(1)} 秒`}</span>{/if}
              {#if activity.error}<p>{activity.error}</p>{/if}
              {#if activity.truncated || activity.details?.truncated || activity.details?.command?.truncated}<span class="activity-truncated">详情已截断</span>{/if}
            </div>
          </details>
        </li>
      {/each}
    </ol>
  {/if}
</section>
