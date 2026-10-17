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
  type FileChange = NonNullable<NonNullable<ActivityTool['details']>['changes']>[number];
  type ChangeGroup = { path: string; changes: { label: string; change: FileChange }[]; added: number; removed: number };
  type CommandSummary = { tool: ActivityTool; command: NonNullable<NonNullable<ActivityTool['details']>['command']> };

  let { run, active = false, elapsedSeconds = 0, onToggle, onOpenFile }: {
    run: ActivityRun;
    active?: boolean;
    elapsedSeconds?: number;
    onToggle: () => void;
    onOpenFile?: (path: string) => void;
  } = $props();

  const latestTool = $derived(run.tools.at(-1));
  const commandRuns = $derived.by((): CommandSummary[] => {
    const summaries: CommandSummary[] = [];
    for (const tool of run.tools) {
      const command = tool.details?.command;
      if (!command) continue;
      if (tool.label === '检查命令状态') {
        const pending = summaries.findLastIndex((item) =>
          item.command.text === command.text && commandOutcome(item) === 'running',
        );
        if (pending >= 0) {
          summaries[pending] = { tool, command };
          continue;
        }
      }
      summaries.push({ tool, command });
    }
    return summaries;
  });
  const changedFiles = $derived.by(() => {
    const groups = new Map<string, ChangeGroup>();
    for (const tool of run.tools) {
      for (const change of tool.details?.changes ?? []) {
        let group = groups.get(change.path);
        if (!group) {
          group = { path: change.path, changes: [], added: 0, removed: 0 };
          groups.set(change.path, group);
        }
        group.changes.push({ label: tool.label, change });
        group.added += change.added;
        group.removed += change.removed;
      }
    }
    return [...groups.values()];
  });
  let changesExpanded = $state(false);

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

  function commandOutcome(item: CommandSummary) {
    const status = (item.command.status ?? '').toLowerCase();
    if (item.tool.status === 'running' || status.includes('running') || status.includes('started')) return 'running';
    if (item.tool.status === 'cancelled' || status.includes('cancel')) return 'cancelled';
    if (item.tool.status === 'failed' || item.command.error || (item.command.exitCode !== undefined && item.command.exitCode !== null && item.command.exitCode !== 0) || status.includes('fail') || status.includes('error')) return 'failed';
    if (item.command.exitCode === 0 || item.tool.status === 'done' || status.includes('complete') || status.includes('success')) return 'completed';
    return 'unknown';
  }

  function commandOutcomeLabel(item: CommandSummary) {
    const outcome = commandOutcome(item);
    if (outcome === 'running') return '运行中';
    if (outcome === 'failed') return '失败';
    if (outcome === 'cancelled') return '已取消';
    if (outcome === 'completed') return '已完成';
    return '结果待同步';
  }

  function showArguments(tool: ActivityTool) {
    return tool.label !== '检查命令状态' && tool.label !== '停止命令';
  }

</script>

<section class:status-queued={run.status === 'queued'} class:status-waiting={run.status === 'paused'} class:status-failed={run.status === 'failed'} class:status-interrupted={run.status === 'interrupted' || run.status === 'cancelled'} class="turn-progress" aria-label="本轮执行状态">
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
  {#if changedFiles.length}
    <section class="activity-review" aria-label="本轮文件改动">
      <div class="activity-review-heading">
        <strong>本轮改动</strong><span>{changedFiles.length} 个文件</span>
        <span class="activity-review-paths" title={changedFiles.map((file) => file.path).join('\n')}>
          {changedFiles.slice(0, 2).map((file) => file.path).join(' · ')}{changedFiles.length > 2 ? ` · +${changedFiles.length - 2}` : ''}
        </span>
        <button class="activity-review-toggle" aria-expanded={changesExpanded} onclick={() => changesExpanded = !changesExpanded}>{changesExpanded ? '收起差异' : '查看差异'}</button>
      </div>
      {#if changesExpanded}
        <div class="activity-review-list">
          {#each changedFiles as file (file.path)}
            <details class="activity-review-file" open>
              <summary><code>{file.path}</code><span>+{file.added} / −{file.removed}</span>{#if onOpenFile}<button class="activity-open-file" onclick={(event) => { event.preventDefault(); event.stopPropagation(); onOpenFile?.(file.path); }}>打开文件</button>{/if}</summary>
              {#each file.changes as item, index (`${file.path}:${index}`)}
                <div class="activity-review-source">{item.label}</div>
                {#if item.change.lines?.length}
                  <pre>{item.change.lines.map((line) => `${line.kind === 'add' ? '+' : line.kind === 'remove' ? '−' : ' '} ${line.text}`).join('\n')}</pre>
                {:else}<div class="activity-review-unavailable">此步骤未返回行级差异</div>{/if}
              {/each}
            </details>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
  {#if commandRuns.length}
    <section class="execution-command-summary" aria-label="已运行命令结果">
      <div class="execution-command-summary-heading"><strong>命令记录</strong><span>{commandRuns.length} 条</span></div>
      {#each commandRuns as item (`${item.tool.invocationId}:command`)}
        <div class="execution-command-row">
          <code title={item.command.text}>{item.command.text}</code>
          <span class={`execution-command-state execution-command-${commandOutcome(item)}`}>{commandOutcomeLabel(item)}</span>
          {#if item.command.exitCode !== undefined && item.command.exitCode !== null}<small>退出码 {item.command.exitCode}</small>{/if}
        </div>
      {/each}
    </section>
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
              {#if activity.arguments && showArguments(activity)}<section><span>调用参数</span><pre>{formatArguments(activity.arguments)}</pre></section>{/if}
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
