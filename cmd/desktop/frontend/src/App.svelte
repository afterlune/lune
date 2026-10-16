<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Events } from '@wailsio/runtime';
  import Markdown from './lib/Markdown.svelte';
  import ExecutionActivity from './lib/ExecutionActivity.svelte';
  import * as Desktop from '../bindings/github.com/afterlune/lune/internal/desktop/desktopservice';
  import { TaskStatus } from '../bindings/github.com/afterlune/luneCore/internal/runtime/models';
  import type {
    Bootstrap, ConversationMessage, MigrationPreview, ModelProfileInput, ModelSettingsView, SubmitInput,
    PendingRequestView, ProviderInput, SessionSummary, WorkspaceOpenResult, WorkspaceState,
  } from '../bindings/github.com/afterlune/lune/internal/desktop/models';

  type LocalMessage = ConversationMessage & {
    local?: boolean; canonicalID?: string; deliveryState?: 'sending' | 'submitted' | 'failed';
    deliveryError?: string; request?: SubmitInput; executionID?: string; userMessageIndex?: number; interrupted?: boolean;
  };
  type Activity = {
    id: string; invocationId: string; agentId?: string; label: string; detail: string;
    arguments?: unknown; details?: { output?: string; command?: { text: string; output?: string; status?: string; exitCode?: number | null; truncated?: boolean; error?: string }; changes?: { path: string; added: number; removed: number; lines?: { kind: string; oldLine?: number; newLine?: number; text: string }[]; truncated?: boolean }[]; truncated?: boolean };
    truncated?: boolean;
    status: 'running' | 'done' | 'failed' | 'cancelled' | 'info' | 'waiting';
    durationMs?: number; error?: string;
  };
  type ActivityRun = {
    key: string; executionId: string; userMessageIndex: number; status: string; statusLabel: string;
    startedAt: string; finishedAt?: string | null; tools: Activity[]; expanded: boolean; elapsedSeconds?: number;
  };
  type StoredActivityRun = NonNullable<WorkspaceState['executionHistory']>[number];
  type StoredActivityTool = NonNullable<StoredActivityRun['tools']>[number];
  type ExecutionState = 'idle' | 'submitting' | 'running' | 'waiting' | 'completed' | 'failed' | 'cancelled' | 'interrupted' | 'disconnected';
  type ProviderForm = ProviderInput & { hasAPIKey: boolean };
  type Modal = 'settings' | 'migration' | 'switch' | 'close' | null;

  const previewVariant = new URLSearchParams(location.search).get('preview') || '';
  const previewMode = previewVariant !== '';
  let theme = $state('system');
  let recent = $state<Bootstrap['recentWorkspaces']>([]);
  let models = $state<ModelSettingsView>({ providers: [], profiles: [], defaultModel: '', maxParallelAgents: 0, configured: false });
  let workspace = $state('');
  let sessions = $state<SessionSummary[]>([]);
  let session = $state<WorkspaceState['session'] | null>(null);
  let permissionMode = $state('workspace');
  let planMode = $state(false);
  let pending = $state<PendingRequestView[]>([]);
  let messages = $state<LocalMessage[]>([]);
  let activities = $state<Activity[]>([]);
  let activityHistory = $state<ActivityRun[]>([]);
  let streamText = $state('');
  let running = $state(false);
  let executionID = $state('');
  let executionState = $state<ExecutionState>('idle');
  let currentStatus = $state('');
  let elapsedSeconds = $state(0);
  let turnStartedAt = $state(0);
  let elapsedTimer: number | undefined;
  let submitPendingMessageID = '';
  let activeActivityRunKey = '';
  let interrupting = $state(false);
  let interruptRequestedExecutionID = '';
  let previewFailedOnce = false;
  let composerText = $state('');
  let attachments = $state<{ name: string; mediaType: string; data: string; preview: string }[]>([]);
  let loading = $state(true);
  let busy = $state(false);
  let refreshing = false;
  let errorMessage = $state('');
  let modal = $state<Modal>(null);
  let migration = $state<MigrationPreview | null>(null);
  let migrationChoices = $state<Record<string, string>>({});
  let migrationPath = $state('');
  let switchTarget = $state('');
  let planReview = $state<{ executionID: string; text: string } | null>(null);
  let answerValues = $state<Record<string, string>>({});
  let noteValues = $state<Record<string, string>>({});
  let providerForms = $state<ProviderForm[]>([]);
  let profileForms = $state<ModelProfileInput[]>([]);
  let defaultModel = $state('');
  let maxParallelAgents = $state(4);
  let settingsSaving = $state(false);

  const activeQuestion = $derived(pending.find((item) => item.kind === 'user_question'));
  const approvals = $derived(pending.filter((item) => item.kind === 'approval'));
  const recoveries = $derived(pending.filter((item) => item.kind === 'tool_recovery' || item.kind === 'model_recovery'));
  const canSend = $derived(models.configured && !running && !busy && !interrupting && pending.length === 0 && !planReview);
  const transcriptRows = $derived.by(() => {
    let userMessageIndex = -1;
    return messages.map((message) => {
      if (message.role === 'user') userMessageIndex++;
      const index = message.role === 'user' ? userMessageIndex : -1;
      return {
        message,
        userMessageIndex: index,
        activityRuns: index < 0 ? [] : activityHistory.filter((run) => run.userMessageIndex === index),
      };
    });
  });
  const userMessageCount = $derived(messages.filter((message) => message.role === 'user').length);
  const orphanActivityRuns = $derived(activityHistory.filter((run) => run.userMessageIndex < 0 || run.userMessageIndex >= userMessageCount));

  function applyTheme(value: string) {
    theme = value;
    document.documentElement.dataset.theme = value;
  }

  function sameMessageContent(left: ConversationMessage, right: ConversationMessage) {
    const leftImages = left.images ?? [];
    const rightImages = right.images ?? [];
    return left.role === right.role && left.text === right.text &&
      leftImages.length === rightImages.length && leftImages.every((image, index) => image === rightImages[index]);
  }

  function mergeMessagesFromSession(serverMessages: ConversationMessage[], currentMessages: LocalMessage[]): LocalMessage[] {
    const consumed = new Set<string>();
    const merged = serverMessages.map((serverMessage): LocalMessage => {
      const byCanonicalID = currentMessages.find((message) =>
        message.canonicalID === serverMessage.id || message.id === serverMessage.id,
      );
      const pendingMatch = byCanonicalID ?? currentMessages.find((message) =>
        message.local && message.deliveryState !== 'failed' && !consumed.has(message.id) &&
        sameMessageContent(message, serverMessage),
      );
      if (pendingMatch) consumed.add(pendingMatch.id);
      return {
        ...serverMessage,
        id: pendingMatch?.id ?? serverMessage.id,
        canonicalID: serverMessage.id,
        executionID: pendingMatch?.executionID,
      };
    });

    const unmatchedLocal = currentMessages.filter((message) => message.local && !consumed.has(message.id));
    return [...merged, ...unmatchedLocal];
  }

  function updateMessage(id: string, update: Partial<LocalMessage>) {
    messages = messages.map((message) => message.id === id ? { ...message, ...update } : message);
  }

  function mergeToolActivities(serverTools: Activity[], localTools: Activity[]) {
    const localByID = new Map(localTools.map((tool) => [tool.invocationId || tool.id, tool]));
    const merged = serverTools.map((tool) => {
      const previous = localByID.get(tool.invocationId || tool.id);
      if (previous) localByID.delete(tool.invocationId || tool.id);
      return { ...tool, ...previous, arguments: tool.arguments ?? previous?.arguments, details: tool.details ?? previous?.details };
    });
    return [...merged, ...localByID.values()];
  }

  function mergeExecutionHistory(serverRuns: WorkspaceState['executionHistory'], localRuns: ActivityRun[]) {
    const localByID = new Map(localRuns.filter((run) => run.executionId).map((run) => [run.executionId, run]));
    const merged = (serverRuns ?? []).map((run) => {
      const previous = localByID.get(run.executionId);
      if (previous) localByID.delete(run.executionId);
      const tools = (run.tools ?? []).map(activityFromView);
      const previousStatusIsTerminal = previous && ['completed', 'failed', 'cancelled', 'interrupted'].includes(previous.status);
      return {
        key: run.executionId,
        ...run,
        status: previousStatusIsTerminal ? previous.status : run.status,
        statusLabel: previous?.statusLabel || activityRunStatusLabel(run.status),
        tools: mergeToolActivities(tools, previous?.tools ?? []),
        expanded: previous?.expanded ?? false,
        elapsedSeconds: previous?.elapsedSeconds,
      };
    });
    const unmatchedLocal = localRuns.filter((run) => !run.executionId || localByID.has(run.executionId));
    return [...merged, ...unmatchedLocal];
  }

  function activityFromView(activity: StoredActivityTool): Activity {
    return {
      id: activity.id,
      invocationId: activity.invocationId,
      agentId: activity.agentId,
      label: toolLabel(activity.name),
      detail: compactActivityDetail(activity.arguments),
      arguments: activity.arguments,
      details: activity.details ? {
        output: activity.details.output,
        command: activity.details.command ? {
          text: activity.details.command.text,
          output: activity.details.command.output,
          status: activity.details.command.status,
          exitCode: activity.details.command.exitCode,
          truncated: activity.details.command.truncated,
          error: activity.details.command.error,
        } : undefined,
        changes: activity.details.changes?.map((change) => ({
          path: change.path, added: change.added, removed: change.removed,
          lines: change.lines ?? undefined, truncated: change.truncated,
        })) ?? undefined,
        truncated: activity.details.truncated,
      } : undefined,
      status: activity.status === 'running' ? 'running' : activity.status === 'failed' ? 'failed' : activity.status === 'cancelled' ? 'cancelled' : 'done',
      durationMs: activity.durationMs,
      error: activity.error,
      truncated: activity.truncated,
    };
  }

  function activityRunStatusLabel(status: string) {
    if (status === 'completed') return '任务已完成';
    if (status === 'failed') return '任务执行失败';
    if (status === 'interrupted' || status === 'discarded' || status === 'cancelled') return '任务已中断';
    if (status === 'paused') return '任务已暂停';
    return '正在处理请求';
  }

  function currentUserMessageIndex() {
    const message = messages.find((item) => item.id === submitPendingMessageID);
    return message?.userMessageIndex ?? messages.filter((item) => item.role === 'user').length - 1;
  }

  function upsertActivityRun(update: Partial<ActivityRun> & { executionId?: string; key?: string }) {
    const key = update.key || update.executionId || activeActivityRunKey || `local:${submitPendingMessageID || 'current'}`;
    const index = activityHistory.findIndex((run) => run.key === key || (!!update.executionId && run.executionId === update.executionId));
    const previous = index >= 0 ? activityHistory[index] : undefined;
    const next: ActivityRun = {
      key,
      executionId: update.executionId ?? previous?.executionId ?? '',
      userMessageIndex: update.userMessageIndex !== undefined && update.userMessageIndex >= 0
        ? update.userMessageIndex : previous?.userMessageIndex ?? -1,
      status: update.status ?? previous?.status ?? 'running',
      statusLabel: update.statusLabel ?? previous?.statusLabel ?? currentStatus,
      startedAt: update.startedAt ?? previous?.startedAt ?? new Date().toISOString(),
      finishedAt: update.finishedAt ?? previous?.finishedAt,
      tools: update.tools ?? previous?.tools ?? activities,
      expanded: update.expanded ?? previous?.expanded ?? false,
      elapsedSeconds: update.elapsedSeconds ?? previous?.elapsedSeconds,
    };
    if (index < 0) activityHistory = [...activityHistory, next];
    else activityHistory = activityHistory.map((run, runIndex) => runIndex === index ? next : run);
    return next;
  }

  function clearElapsedTimer() {
    if (elapsedTimer !== undefined) {
      window.clearInterval(elapsedTimer);
      elapsedTimer = undefined;
    }
  }

  function startElapsedTimer(reset = false) {
    if (reset || turnStartedAt === 0) {
      turnStartedAt = Date.now();
      elapsedSeconds = 0;
    }
    clearElapsedTimer();
    elapsedTimer = window.setInterval(() => {
      elapsedSeconds = Math.floor((Date.now() - turnStartedAt) / 1000);
    }, 1000);
  }

  function stopElapsedTimer() {
    if (turnStartedAt !== 0) elapsedSeconds = Math.floor((Date.now() - turnStartedAt) / 1000);
    clearElapsedTimer();
  }

  function scrollToLatest(force = false) {
    const scroll = document.getElementById('conversation-scroll');
    if (!scroll) return;
    const nearBottom = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 120;
    if (!force && !nearBottom) return;
    requestAnimationFrame(() => {
      scroll.scrollTo({ top: scroll.scrollHeight, behavior: force ? 'smooth' : 'auto' });
    });
  }

  let activitySequence = 0;
  function addActivity(activity: Omit<Activity, 'id' | 'invocationId'> & { id?: string; invocationId?: string }) {
    const id = activity.id || `activity-${++activitySequence}`;
    const invocationId = activity.invocationId || id;
    const index = activities.findIndex((item) => item.invocationId === invocationId || item.id === id);
    const previous = index >= 0 ? activities[index] : undefined;
    const next: Activity = {
      ...previous, ...activity, id, invocationId,
      arguments: activity.arguments ?? previous?.arguments,
      details: activity.details ?? previous?.details,
    };
    if (index >= 0) activities = activities.map((item, itemIndex) => itemIndex === index ? next : item);
    else activities = [...activities, next];
    if (activeActivityRunKey) {
      upsertActivityRun({ key: activeActivityRunKey, executionId: executionID, tools: activities });
    }
  }

  function setExecutionStatus(state: ExecutionState, label: string, options: { expand?: boolean; record?: boolean } = {}) {
    executionState = state;
    currentStatus = label;
    const runStatus = state === 'completed' ? 'completed'
      : state === 'failed' || state === 'disconnected' ? 'failed'
        : state === 'interrupted' || state === 'cancelled' ? 'interrupted'
          : state === 'waiting' ? 'paused' : 'running';
    const key = activeActivityRunKey || executionID || `local:${submitPendingMessageID || 'current'}`;
    activeActivityRunKey = key;
	const run = upsertActivityRun({
      key, executionId: executionID, userMessageIndex: currentUserMessageIndex(),
      status: runStatus, statusLabel: label, tools: activities,
      elapsedSeconds: turnStartedAt ? Math.floor((Date.now() - turnStartedAt) / 1000) : undefined,
    });
    if (options.expand === false && run.expanded) {
      activityHistory = activityHistory.map((item) => item.key === key ? { ...item, expanded: false } : item);
    }
    scrollToLatest();
  }

  function toolLabel(name: string) {
    const labels: Record<string, string> = {
      read_file: '读取文件', list_dir: '列出目录', glob: '查找文件', grep: '搜索文件内容',
      edit_file: '编辑文件', write_file: '写入文件', command: '运行命令',
      command_status: '检查命令状态', command_stop: '停止命令', web_search: '搜索网页', web_fetch: '读取网页',
    };
    return labels[name] ?? `运行工具 ${name || '操作'}`;
  }

  function compactActivityDetail(value: unknown) {
    const detail = compactArguments(value).replace(/\s+/g, ' ').trim();
    return detail.length > 180 ? `${detail.slice(0, 177)}…` : detail;
  }

  function activitySummary(activity: Activity) {
    const raw = activity.arguments;
    try {
      const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw;
      if (parsed && typeof parsed === 'object') {
        const record = parsed as Record<string, unknown>;
        for (const key of ['command', 'path', 'pattern', 'glob', 'query', 'directory', 'dir', 'url', 'file']) {
          const value = record[key];
          if (typeof value === 'string' && value.trim()) return value.trim();
        }
      }
    } catch { /* compactActivityDetail handles opaque arguments. */ }
    return compactActivityDetail(raw);
  }

  function formatActivityArguments(value: unknown) {
    try { return JSON.stringify(typeof value === 'string' ? JSON.parse(value) : value, null, 2); }
    catch { return String(value ?? ''); }
  }

  function toggleActivityRun(key: string) {
    activityHistory = activityHistory.map((run) => run.key === key ? { ...run, expanded: !run.expanded } : run);
  }

  const previewBootstrap: Bootstrap = {
    theme: 'light', recentWorkspaces: [], models: {
      providers: [{ name: 'deepseek', protocol: 'responses', baseURL: 'https://api.deepseek.com', hasAPIKey: true, serverCompaction: false }],
      profiles: [{ name: '工作模型', provider: 'deepseek', model: 'deepseek-chat', maxOutputTokens: 0, supportsInputAudio: false }],
      defaultModel: '工作模型', maxParallelAgents: 4, configured: true,
    }, workspace: 'D:\\Git\\luneAgent', sessionId: 'session-preview', permissionMode: 'workspace',
  };

  const previewState: WorkspaceState = {
    workspace: 'D:\\Git\\luneAgent', permissionMode: 'workspace',
    session: {
      id: 'session-preview', title: '重构桌面端提问流程', updatedAt: '2026-10-16T10:36:00+08:00',
      settings: { model_profile: '工作模型' },
      messages: [
        { id: 'm1', role: 'user', text: '帮我检查 Question 面板的交互，并把截图测试流程接入桌面端。' },
        { id: 'm2', role: 'assistant', text: '我会先把窗口结构和消息流搭好，再补上浏览器预览与真实窗口启动检查。' },
      ],
    },
    sessions: [{ id: 'session-preview', title: '重构桌面端提问流程', updatedAt: '2026-10-16T10:36:00+08:00' }],
    runtimeState: { Task: { Status: TaskStatus.TaskIdle, SessionID: 'session-preview', TaskID: '', ExecutionID: '', QueueID: '', ContextWindow: 0, ReserveOutputTokens: 0, AutoCompact: false, AutoCompactThreshold: 0, Goal: '', MaxToolRounds: 0, ModelOverride: '', ModelProfile: '工作模型', Provider: 'deepseek', Model: 'deepseek-chat', ReasoningEffort: '', Execution: {} }, Queue: [] },
    pending: [],
    executionHistory: [],
  };

  function installWorkspaceState(state: WorkspaceState, preservePlanReview = false) {
    const currentPlanReview = preservePlanReview && session?.id === state.session.id ? planReview : null;
    clearElapsedTimer();
    workspace = state.workspace;
    session = state.session;
    sessions = state.sessions ?? [];
    permissionMode = state.permissionMode || 'workspace';
    if (state.models) models = state.models;
    pending = state.pending ?? [];
    messages = [...(state.session.messages ?? [])];
    activities = [];
    activityHistory = mergeExecutionHistory(state.executionHistory ?? [], []);
    streamText = '';
    planReview = currentPlanReview;
    const status = state.runtimeState?.Task?.Status;
    running = status === 'running' || status === 'queued';
    executionID = state.runtimeState?.Task?.ExecutionID ?? '';
    executionState = running ? 'running' : 'idle';
    currentStatus = running ? (status === 'queued' ? '任务排队中' : '正在处理请求') : '';
    elapsedSeconds = 0;
    turnStartedAt = 0;
    activeActivityRunKey = running ? executionID : '';
    if (running) {
      const currentRun = activityHistory.find((run) => run.executionId === executionID);
      if (currentRun) {
        activities = currentRun.tools;
        currentStatus = currentRun.statusLabel || currentStatus;
        activeActivityRunKey = currentRun.key;
        const startedAt = Date.parse(currentRun.startedAt);
        if (Number.isFinite(startedAt)) {
          turnStartedAt = startedAt;
          elapsedSeconds = Math.floor((Date.now() - startedAt) / 1000);
          startElapsedTimer(false);
        } else startElapsedTimer(true);
      } else startElapsedTimer(true);
    }
  }

  function applySessionRefresh(state: WorkspaceState) {
    session = state.session;
    sessions = state.sessions ?? sessions;
    permissionMode = state.permissionMode || permissionMode;
    if (state.models) models = state.models;
    pending = state.pending ?? [];
    messages = mergeMessagesFromSession(state.session.messages ?? [], messages);
    activityHistory = mergeExecutionHistory(state.executionHistory ?? [], activityHistory);
    scrollToLatest();

    const task = state.runtimeState?.Task;
    const taskStatus = task?.Status;
    const wasRunning = running;
    const snapshotRunning = taskStatus === 'running' || taskStatus === 'queued';
    const localExecutionActive = executionState === 'submitting' || executionState === 'running' || executionState === 'disconnected';
    const localExecutionTerminal = executionState === 'completed' || executionState === 'failed' || executionState === 'cancelled' || executionState === 'interrupted';
    running = !localExecutionTerminal && (snapshotRunning || localExecutionActive);
    if (task?.ExecutionID) executionID = task.ExecutionID;

    if (running && snapshotRunning) {
      if (!wasRunning && executionState === 'idle') {
        setExecutionStatus('running', taskStatus === 'queued' ? '任务排队中' : '正在处理请求', { expand: true });
      } else if (elapsedTimer === undefined && turnStartedAt === 0) {
        startElapsedTimer(true);
      } else if (executionState === 'waiting' && pending.length === 0) {
        startElapsedTimer(false);
        setExecutionStatus('running', '正在继续处理', { expand: true });
      }
    } else if (pending.length > 0 && executionState !== 'completed' && executionState !== 'failed' && executionState !== 'cancelled') {
      running = false;
      stopElapsedTimer();
      const waitingStatus = pending.some((item) => item.kind === 'approval')
        ? '等待你的授权'
        : pending.some((item) => item.kind === 'user_question')
          ? '等待你的回答'
          : '等待你处理恢复请求';
      setExecutionStatus('waiting', waitingStatus, { expand: true });
    }
  }

  async function refreshSession() {
    if (previewMode || refreshing || !session) return;
    const refreshingSessionID = session.id;
    refreshing = true;
    try {
      const state = await Desktop.RefreshSession();
      if (session?.id !== refreshingSessionID || state.session.id !== refreshingSessionID) return;
      applySessionRefresh(state);
    } catch (error) {
      showError(error);
    } finally {
      refreshing = false;
    }
  }

  async function initialize() {
    if (previewMode) {
      await new Promise((resolve) => setTimeout(resolve, 80));
      theme = previewBootstrap.theme;
      recent = previewBootstrap.recentWorkspaces;
      models = previewBootstrap.models;
      installWorkspaceState(previewState);
      const variant = previewVariant;
      activities = [];
      if (variant === 'empty') {
        messages = [];
        workspace = 'D:\\Git\\sample-project';
      }
      if (variant === 'markdown') {
        messages = [
          { id: 'markdown-user', role: 'user', text: '整理一下检查结果。' },
          { id: 'markdown-assistant', role: 'assistant', text: '检查完成，主要结论如下：\n\n## 发现的问题\n\n- 会话刷新会覆盖尚未持久化的用户消息。\n- 流式状态缺少明确阶段。\n\n| 状态 | 当前表现 |\n| --- | --- |\n| 消息 | 延迟出现 |\n| 模型 | 没有明确进度 |\n\n```go\nfunc ready() bool {\n    return true\n}\n```\n\n> 修复后，用户消息应立即显示。\n\n<scr' + 'ipt>window.__markdownXSS = true</scr' + 'ipt>\n[危险链接](javascript:alert(1))' },
        ];
      }
      if (variant === 'running' || variant === 'interrupt') {
        running = true;
        executionID = 'exec-preview-running';
        executionState = 'running';
        currentStatus = '模型正在分析请求';
        activeActivityRunKey = executionID;
        turnStartedAt = Date.now() - 12_000;
        elapsedSeconds = 12;
        activities = [
          {
            id: 'tool-preview', invocationId: 'invocation-preview', label: '读取文件', detail: 'internal/desktop/service.go',
            arguments: { path: 'internal/desktop/service.go' },
            details: { output: 'package desktop\n\nfunc service() {}' }, status: 'running',
          },
        ];
        activityHistory = [{
          key: executionID, executionId: executionID, userMessageIndex: 0, status: 'running', statusLabel: currentStatus,
          startedAt: new Date(turnStartedAt).toISOString(), tools: activities, expanded: false, elapsedSeconds,
        }];
        streamText = '我会先检查消息的乐观更新和会话刷新流程。';
      }
      if (variant === 'activity-history') {
        const historyTool: Activity = {
          id: 'history-read', invocationId: 'history-read', label: '读取文件', detail: 'internal/desktop/service.go',
          arguments: { path: 'internal/desktop/service.go' },
          details: { output: 'package desktop\n\nfunc persistedDetails() {}' }, status: 'done', durationMs: 420,
        };
        activityHistory = [{
          key: 'exec-preview-history', executionId: 'exec-preview-history', userMessageIndex: 0, status: 'interrupted',
          statusLabel: '任务已中断', startedAt: '2026-10-17T00:03:00+08:00',
          tools: [historyTool], expanded: false,
        }];
      }
      if (variant === 'question') {
        pending = [{
          kind: 'user_question', id: 'question-preview', executionId: 'exec-preview', runId: 'run-preview',
          invocationId: 'invocation-preview', callId: 'call-preview', tool: '',
          questions: [{
            id: 'workspace-switch', question: '任务失败或取消后，Tab 标题怎么显示？', options: [
              { label: '保留结果状态', description: '成功显示已完成，失败显示警告，取消恢复普通标题。' },
              { label: '只标记成功', description: '失败和取消立即恢复普通标题。' },
              { label: '全部恢复普通标题', description: '任何结果都立即显示普通任务标题。' },
            ],
          }],
        }];
      }
      if (variant === 'plan') {
        planMode = true;
        permissionMode = 'read-only';
        planReview = {
          executionID: 'exec-preview',
          text: '## 桌面端启动流程\n- 首次打开时显示最近工作区和模型设置入口。\n- Core 按当前工作区初始化，前端监听持久事件与流式内容。\n\n## 验证方式\n- 使用浏览器预览和 Playwright 固定截图覆盖主要界面状态。',
        };
      }
      if (variant === 'plan-refresh' || variant === 'plan-refresh-session-switch') {
        planMode = true;
        permissionMode = 'read-only';
        onTurnResult({
          sessionId: previewState.session.id,
          executionId: 'exec-preview-refresh',
          status: 'completed',
          text: '',
          plan: 'Plan review remains open after refresh.',
        });
        const refreshedState = variant === 'plan-refresh-session-switch'
          ? { ...previewState, session: { ...previewState.session, id: 'session-next' } }
          : previewState;
        installWorkspaceState(refreshedState, true);
      }
      loading = false;
      applyTheme(theme);
      return;
    }
    try {
      const data = await Desktop.Bootstrap();
      theme = data.theme || 'system';
      recent = data.recentWorkspaces ?? [];
      models = data.models;
      workspace = data.workspace;
      permissionMode = data.permissionMode || 'workspace';
      applyTheme(theme);
      Events.On('agent-event', (event: { data: AgentEvent }) => onAgentEvent(event.data));
      Events.On('turn-result', (event: { data: TurnResultEvent }) => onTurnResult(event.data));
      Events.On('pending-changed', (event: { data: string }) => {
        if (event.data === session?.id) void refreshSession();
      });
      Events.On('agent-error', (event: { data: { message: string } }) => showError(event.data.message));
    } catch (error) {
      showError(error);
    } finally {
      loading = false;
    }
  }

  type AgentEvent = {
    kind: string; sessionId: string; taskId: string; executionId: string; status: string; error?: string;
    approvalId?: string; tool?: {
      id: string; invocationId?: string; agentId?: string; name: string; arguments?: unknown;
      details?: Activity['details']; started?: boolean; failed: boolean; canceled: boolean; durationMs: number;
      truncated?: boolean;
    };
    stream?: { type: string; delta: string };
  };
  type TurnResultEvent = { sessionId: string; executionId: string; status: string; text: string; plan?: string; error?: string };

  function pendingMessageForExecution(event: AgentEvent) {
    const message = messages.find((item) => item.id === submitPendingMessageID && item.local);
    if (message && event.executionId) updateMessage(message.id, { executionID: event.executionId });
  }

  function startExecution(event: AgentEvent) {
    const previousRunKey = activeActivityRunKey;
    if (event.executionId) executionID = event.executionId;
    if (event.executionId && activeActivityRunKey.startsWith('local:')) {
      const localKey = activeActivityRunKey;
      const localRun = activityHistory.find((run) => run.key === localKey);
      if (localRun) {
        activityHistory = activityHistory.map((run) => run.key === localKey ? { ...run, key: event.executionId, executionId: event.executionId } : run);
      }
      activeActivityRunKey = event.executionId;
    } else if (event.executionId) {
      activeActivityRunKey = event.executionId;
    }
    if (interruptRequestedExecutionID === previousRunKey && event.executionId) interruptRequestedExecutionID = event.executionId;
    pendingMessageForExecution(event);
    running = true;
    if (turnStartedAt === 0) startElapsedTimer(true);
    const label = event.kind === 'task_queued' ? '任务排队中' : '正在等待模型响应';
    setExecutionStatus('running', label, { expand: true });
  }

  function handleToolEvent(event: AgentEvent) {
    const tool = event.tool;
    const id = tool?.id || `${event.taskId}-${activities.length}`;
    const rawName = tool?.name || 'tool';
    const label = toolLabel(rawName);
    const detail = tool?.arguments ? compactActivityDetail(tool.arguments) : '';
    const isStarted = tool?.started ?? event.kind === 'tool_started';
    const status: Activity['status'] = isStarted ? 'running' : tool?.failed ? 'failed' : tool?.canceled ? 'cancelled' : 'done';
    addActivity({
      id: `tool-${id}`, invocationId: tool?.invocationId || id, agentId: tool?.agentId,
      label, detail, arguments: tool?.arguments, details: tool?.details, status,
      durationMs: tool?.durationMs, error: event.error, truncated: tool?.truncated,
    });
    running = true;
    if (isStarted) {
      setExecutionStatus('running', '正在执行工具', { expand: true });
    } else {
      setExecutionStatus('running', '工具已完成，继续处理', { expand: true });
    }
  }

  function finishExecution(status: string, message = '') {
    const normalized = status.toLowerCase();
    const currentRun = activityHistory.find((run) => run.key === activeActivityRunKey || (!!executionID && run.executionId === executionID));
    if (currentRun?.status === 'interrupted' && (normalized === 'paused' || normalized === 'discarded')) return;
    if (normalized === 'paused') {
      if (interruptRequestedExecutionID && (interruptRequestedExecutionID === executionID || interruptRequestedExecutionID === activeActivityRunKey)) {
        setExecutionStatus('running', '正在中断任务');
        return;
      }
      running = false;
      stopElapsedTimer();
      const waitingLabel = pending.some((item) => item.kind === 'approval')
        ? '等待你的授权'
        : pending.some((item) => item.kind === 'user_question')
          ? '等待你的回答'
          : '等待你处理恢复请求';
      setExecutionStatus('waiting', waitingLabel, { expand: true });
      return;
    }
    running = false;
    stopElapsedTimer();
    if (normalized === 'interrupted' || normalized === 'discarded' || normalized === 'cancelled') {
      const interrupted = normalized !== 'cancelled' || interruptRequestedExecutionID === executionID;
      setExecutionStatus(interrupted ? 'interrupted' : 'cancelled', interrupted ? '任务已中断' : '任务已取消', { expand: false });
      return;
    }
    if (normalized === 'failed') {
      setExecutionStatus('failed', message ? `任务失败：${message}` : '任务执行失败', { expand: false });
      return;
    }
    setExecutionStatus('completed', '任务已完成', { expand: false });
  }

  function onAgentEvent(event: AgentEvent) {
    if (event.sessionId !== session?.id) return;
    if (executionID && event.executionId && executionID !== event.executionId &&
      event.kind !== 'task_started' && event.kind !== 'task_queued') return;
    if (!executionID && event.executionId && event.kind !== 'task_started' && event.kind !== 'task_queued') return;

    if (event.kind === 'task_started' || event.kind === 'task_queued') {
      if (executionID && event.executionId !== executionID) {
        activities = [];
        streamText = '';
        activitySequence = 0;
      }
      startExecution(event);
    }

    if (event.kind === 'agent_started') {
      running = true;
      setExecutionStatus('running', '正在处理子任务', { expand: true });
    }
    if (event.kind === 'agent_finished') {
      if (event.status === 'completed') setExecutionStatus('running', '正在继续处理', { expand: true });
    }
    if (event.kind === 'model_stream' && event.stream?.type === 'reasoning_delta') {
      running = true;
      setExecutionStatus('running', '模型正在分析请求', { expand: true });
    }
    if (event.kind === 'model_stream' && event.stream?.type === 'text_delta') {
      running = true;
      streamText += event.stream.delta;
      setExecutionStatus('running', '正在生成回复', { expand: true });
      scrollToLatest();
    }
    if (event.kind === 'tool_started' || event.kind === 'tool_finished' || event.kind === 'tool_details') {
      handleToolEvent(event);
    }

    if (event.kind === 'model_retry') {
      running = true;
      setExecutionStatus('running', '模型请求暂时失败，正在重试', { expand: true });
    }
    if (event.kind === 'approval_required' || event.kind === 'user_question_required' ||
      event.kind === 'user_interaction_required' || event.kind === 'model_recovery_required' ||
      event.kind === 'tool_recovery_required') {
      running = true;
      stopElapsedTimer();
      const label = event.kind === 'approval_required' ? '等待你的授权'
        : event.kind === 'user_question_required' ? '等待你的回答'
          : event.kind === 'user_interaction_required' ? '等待外部服务响应'
            : '等待你处理恢复请求';
      setExecutionStatus('waiting', label, { expand: true });
    }
    if (event.kind === 'model_recovery_resumed') {
      startExecution({ ...event, kind: 'task_started' });
    }
    if (event.kind === 'approval_cancelled' || event.kind === 'user_interaction_cancelled') {
      running = true;
      setExecutionStatus('running', '操作已拒绝，正在结束本轮', { expand: true });
    }
    if (event.kind === 'task_finished') {
      finishExecution(event.status, event.error);
    }
    if (event.kind === 'task_discarded') {
      finishExecution('discarded');
    }
    if (event.kind === 'runtime_stream_failed') {
      setExecutionStatus('disconnected', '状态连接中断，正在同步任务', { expand: true });
      void refreshSession();
    }
    if (event.error && event.status !== 'paused' && !(interruptRequestedExecutionID && interruptRequestedExecutionID === event.executionId)) showError(event.error);
  }

  function onTurnResult(result: TurnResultEvent) {
    if (result.sessionId !== session?.id) return;
    if (executionID && result.executionId && executionID !== result.executionId) return;
    executionID = result.executionId;
    const currentRun = activityHistory.find((run) => run.executionId === result.executionId || run.key === activeActivityRunKey);
    const interrupted = interruptRequestedExecutionID === result.executionId || currentRun?.status === 'interrupted';
    const partialText = result.status === 'paused' && interrupted ? streamText.trim() : '';
    const responseText = result.text || partialText;
    streamText = '';
    finishExecution(result.status || (result.error ? 'failed' : 'completed'), result.error);
    if (result.error && result.status !== 'paused' && !interrupted) showError(result.error);
    if (responseText && !messages.some((message) => message.role === 'assistant' && message.text === responseText && message.executionID === result.executionId)) {
      messages = [...messages, {
        id: `assistant-${result.executionId}${partialText ? '-interrupted' : ''}`, role: 'assistant', text: responseText,
        local: true, deliveryState: 'submitted', executionID: result.executionId, interrupted: !!partialText,
      }];
    }
    if (result.plan) planReview = { executionID: result.executionId, text: result.plan };
    scrollToLatest();
    void refreshSession();
  }

  function compactArguments(value: unknown) {
    try {
      const parsed = typeof value === 'string' ? JSON.parse(value) : value;
      if (typeof parsed === 'string') return parsed;
      if (parsed && typeof parsed === 'object') {
        const record = parsed as Record<string, unknown>;
        for (const key of ['command', 'path', 'pattern', 'glob', 'query', 'directory', 'dir', 'url', 'file']) {
          const target = record[key];
          if (typeof target === 'string' && target.trim()) return target;
        }
      }
      return JSON.stringify(parsed);
    } catch { return String(value); }
  }

  function formatArguments(value: unknown) {
    try { return JSON.stringify(typeof value === 'string' ? JSON.parse(value) : value, null, 2); }
    catch { return String(value); }
  }

  function showError(error: unknown) {
    errorMessage = error instanceof Error ? error.message : String(error);
    window.setTimeout(() => { if (errorMessage) errorMessage = ''; }, 6500);
  }

  function setSettingsForm() {
    providerForms = (models.providers ?? []).map((provider) => ({ ...provider, apiKey: '' }));
    profileForms = (models.profiles ?? []).map((profile) => ({ ...profile }));
    if (providerForms.length === 0) providerForms = [{ name: 'deepseek', protocol: 'responses', baseURL: 'https://api.deepseek.com', apiKey: '', hasAPIKey: false, serverCompaction: false }];
    if (profileForms.length === 0) profileForms = [{ name: 'default', provider: providerForms[0].name, model: '', maxOutputTokens: 0, supportsInputAudio: false }];
    defaultModel = models.defaultModel || profileForms[0]?.name || 'default';
    maxParallelAgents = models.maxParallelAgents || 4;
    modal = 'settings';
  }

  function addProvider() {
    providerForms = [...providerForms, { name: `provider-${providerForms.length + 1}`, protocol: 'responses', baseURL: 'https://api.openai.com/v1', apiKey: '', hasAPIKey: false, serverCompaction: false }];
  }

  function addProfile() {
    profileForms = [...profileForms, { name: `model-${profileForms.length + 1}`, provider: providerForms[0]?.name ?? '', model: '', maxOutputTokens: 0, supportsInputAudio: false }];
  }

  async function saveSettings() {
    settingsSaving = true;
    try {
      const input = {
        providers: providerForms.map(({ name, protocol, baseURL, apiKey, serverCompaction }) => ({ name, protocol, baseURL, apiKey, serverCompaction })),
        profiles: profileForms.map(({ name, provider, model, maxOutputTokens, supportsInputAudio }) => ({ name, provider, model, maxOutputTokens, supportsInputAudio })),
        defaultModel, maxParallelAgents,
      };
      if (!previewMode) models = await Desktop.SaveModelSettings(input);
      else models = { ...models, configured: true, defaultModel };
      modal = null;
      if (workspace && !previewMode) {
        const result = await Desktop.OpenWorkspace(workspace);
        await completeWorkspaceOpen(result);
      }
    } catch (error) { showError(error); }
    finally { settingsSaving = false; }
  }

  async function changeTheme(value: string) {
    applyTheme(value);
    if (!previewMode) {
      try { await Desktop.SetTheme(value); }
      catch (error) { showError(error); }
    }
  }

  async function openFolderPicker() {
    if (previewMode) return;
    try {
      const path = await Desktop.PickWorkspace();
      if (path) await openWorkspace(path);
    } catch (error) { showError(error); }
  }

  async function openWorkspace(path: string) {
    if (workspace && running) {
      switchTarget = path;
      modal = 'switch';
      return;
    }
    try {
      const result = previewMode ? { workspace: path, sessions: [], permissionMode: 'workspace', needsMigration: false } as WorkspaceOpenResult : await Desktop.OpenWorkspace(path);
      await completeWorkspaceOpen(result);
    } catch (error) { showError(error); }
  }

  async function completeWorkspaceOpen(result: WorkspaceOpenResult) {
    workspace = result.workspace;
    if (result.models) models = result.models;
    if (result.needsMigration && result.migration) {
      migration = result.migration;
      migrationPath = result.workspace;
      migrationChoices = Object.fromEntries((result.migration.conflicts ?? []).map((item) => [`${item.kind}:${item.name}`, 'keep']));
      modal = 'migration';
      return;
    }
    sessions = result.sessions ?? [];
    permissionMode = result.permissionMode || 'workspace';
    if (sessions.length > 0) await openSession(sessions[0].id);
    else if (!previewMode) installWorkspaceState(await Desktop.CreateSession());
    else installWorkspaceState({ ...previewState, workspace: result.workspace });
  }

  async function finishMigration() {
    if (!migration) return;
    busy = true;
    try {
      const result = await Desktop.CompleteModelMigration(migrationPath, migrationChoices);
      migration = null;
      modal = null;
      await completeWorkspaceOpen(result);
    } catch (error) { showError(error); }
    finally { busy = false; }
  }

  async function openProjectModels() {
    if (previewMode) return;
    busy = true;
    try {
      const result = await Desktop.OpenWorkspaceWithProjectModels(migrationPath);
      migration = null;
      modal = null;
      await completeWorkspaceOpen(result);
    } catch (error) { showError(error); }
    finally { busy = false; }
  }

  async function confirmWorkspaceSwitch() {
    busy = true;
    try {
      if (!previewMode) await Desktop.CloseWorkspace(true);
      workspace = '';
      session = null;
      messages = [];
      running = false;
      modal = null;
      const result = await Desktop.OpenWorkspace(switchTarget);
      await completeWorkspaceOpen(result);
    } catch (error) { showError(error); }
    finally { busy = false; switchTarget = ''; }
  }

  async function closeWorkspace() {
    if (running) { modal = 'close'; return; }
    if (!previewMode) {
      try { await Desktop.CloseWorkspace(false); }
      catch (error) { showError(error); return; }
    }
    workspace = '';
    session = null;
    messages = [];
    pending = [];
    modal = null;
  }

  async function confirmCloseWorkspace() {
    busy = true;
    try {
      if (!previewMode) await Desktop.CloseWorkspace(true);
      workspace = '';
      session = null;
      messages = [];
      pending = [];
      running = false;
      modal = null;
    } catch (error) { showError(error); }
    finally { busy = false; }
  }

  async function openSession(id: string) {
    if (previewMode) {
      installWorkspaceState(previewState);
      return;
    }
    try { installWorkspaceState(await Desktop.OpenSession(id)); }
    catch (error) { showError(error); }
  }

  async function createSession() {
    if (previewMode) return;
    try { installWorkspaceState(await Desktop.CreateSession()); }
    catch (error) { showError(error); }
  }

  async function setPermission(value: string) {
    permissionMode = value;
    if (!previewMode) {
      try { await Desktop.SetPermissionMode(value); }
      catch (error) { showError(error); }
    }
  }

  async function setSessionModel(value: string) {
    if (!session) return;
    if (!previewMode) {
      try { await Desktop.SetSessionModel(value); }
      catch (error) { showError(error); return; }
    }
    session = { ...session, settings: { ...session.settings, model_profile: value } };
  }

  async function setPlanMode(enabled: boolean) {
    planMode = enabled;
    if (!previewMode) {
      try { await Desktop.SetPlanMode(enabled); }
      catch (error) { showError(error); }
    }
  }

  async function sendMessage() {
    const text = composerText.trim();
    const interactivePreview = previewVariant === 'send' || previewVariant === 'send-failure' || previewVariant === 'interrupt';
    if ((!text && attachments.length === 0) || running || !models.configured || (previewMode && !interactivePreview)) return;
    const id = `user-${crypto.randomUUID()}`;
    const userMessageIndex = messages.filter((message) => message.role === 'user').length;
    const request: SubmitInput = { text, images: attachments.map(({ name, mediaType, data }) => ({ name, mediaType, data })), userMessageIndex };
    const message: LocalMessage = {
      id, role: 'user', text, images: attachments.map((item) => item.preview), local: true,
      deliveryState: 'sending', request, userMessageIndex,
    };
    messages = [...messages, message];
    composerText = '';
    attachments = [];
    submitPendingMessageID = id;
    await submitUserMessage(id, request);
  }

  async function submitUserMessage(messageID: string, request: SubmitInput) {
    busy = true;
    executionID = '';
    activeActivityRunKey = `local:${messageID}`;
    const existingRun = activityHistory.find((run) => run.key === activeActivityRunKey);
    activities = existingRun?.tools ?? [];
    streamText = '';
    activitySequence = 0;
    running = true;
    setExecutionStatus('submitting', '正在提交请求', { expand: true });
    startElapsedTimer(true);
    await tick();
    scrollToLatest(true);
    try {
      const submission = previewMode ? await submitPreviewRequest(request) : await Desktop.Submit(request);
      executionID = submission.ExecutionID;
      updateMessage(messageID, { deliveryState: 'submitted', deliveryError: undefined, executionID: submission.ExecutionID });
      running = executionState === 'interrupted' ? false : true;
      if (executionState === 'submitting') setExecutionStatus('running', '正在等待模型响应', { expand: true });
    } catch (error) {
      running = false;
      stopElapsedTimer();
      const reason = error instanceof Error ? error.message : String(error);
      updateMessage(messageID, { deliveryState: 'failed', deliveryError: reason });
      setExecutionStatus('failed', '消息发送失败', { expand: false });
    }
    finally { busy = false; }
  }

  async function submitPreviewRequest(_request: SubmitInput) {
    await new Promise((resolve) => window.setTimeout(resolve, 650));
    if (previewVariant === 'send-failure' && !previewFailedOnce) {
      previewFailedOnce = true;
      throw new Error('模拟提交失败，请重试');
    }

    const previewExecutionID = 'exec-preview-send';
    const runningState = {
      ...previewState,
      runtimeState: {
        ...previewState.runtimeState,
        Task: { ...previewState.runtimeState.Task, Status: TaskStatus.TaskRunning, ExecutionID: previewExecutionID },
      },
    };
    onAgentEvent({ kind: 'task_started', sessionId: previewState.session.id, taskId: 'task-preview', executionId: previewExecutionID, status: 'running' });
    applySessionRefresh(runningState);

    window.setTimeout(() => onAgentEvent({ kind: 'model_stream', sessionId: previewState.session.id, taskId: 'task-preview', executionId: previewExecutionID, status: 'running', stream: { type: 'reasoning_delta', delta: 'not rendered' } }), 350);
    window.setTimeout(() => onAgentEvent({ kind: 'tool_started', sessionId: previewState.session.id, taskId: 'task-preview', executionId: previewExecutionID, status: 'running', tool: { id: 'tool-preview', invocationId: 'tool-preview-invocation', name: 'read_file', arguments: { path: 'internal/desktop/service.go' }, started: true, failed: false, canceled: false, durationMs: 0 } }), 800);
    window.setTimeout(() => onAgentEvent({ kind: 'tool_finished', sessionId: previewState.session.id, taskId: 'task-preview', executionId: previewExecutionID, status: 'running', tool: { id: 'tool-preview', invocationId: 'tool-preview-invocation', name: 'read_file', arguments: { path: 'internal/desktop/service.go' }, details: { output: 'package desktop\n\nfunc Submit(input SubmitInput) {}' }, started: false, failed: false, canceled: false, durationMs: 420 } }), 1200);
    window.setTimeout(() => onAgentEvent({ kind: 'model_stream', sessionId: previewState.session.id, taskId: 'task-preview', executionId: previewExecutionID, status: 'running', stream: { type: 'text_delta', delta: '已读取服务代码，正在整理结果。' } }), 1500);
    window.setTimeout(() => onTurnResult({ sessionId: previewState.session.id, executionId: previewExecutionID, status: 'completed', text: '## 检查结果\n\n已完成读取。', }), 2600);
    return { ExecutionID: previewExecutionID };
  }

  async function retryMessage(message: LocalMessage) {
    if (!message.request || message.deliveryState !== 'failed' || busy || running) return;
    updateMessage(message.id, { deliveryState: 'sending', deliveryError: undefined });
    submitPendingMessageID = message.id;
    await submitUserMessage(message.id, message.request);
  }

  async function interruptTask() {
    if (interrupting) return;
    interrupting = true;
    interruptRequestedExecutionID = executionID || activeActivityRunKey;
    setExecutionStatus('running', '正在中断任务');
    try {
      const interrupted = previewMode ? true : await Desktop.Interrupt();
      if (!interrupted) {
        showError('无法完成中断，正在同步当前任务状态');
        await refreshSession();
        return;
      }
      running = false;
      stopElapsedTimer();
      finishExecution('interrupted');
      if (streamText.trim()) {
        const partialText = streamText.trim();
        const partialID = `assistant-${executionID}-interrupted`;
        if (!messages.some((message) => message.id === partialID)) {
          messages = [...messages, { id: partialID, role: 'assistant', text: partialText, local: true, deliveryState: 'submitted', executionID, interrupted: true }];
        }
        streamText = '';
      }
      await refreshSession();
    } catch (error) {
      showError(error);
      await refreshSession();
    } finally {
      interrupting = false;
      interruptRequestedExecutionID = '';
    }
  }

  async function addFiles(files: FileList | File[]) {
    const selected = Array.from(files).filter((file) => file.type.startsWith('image/'));
    if (attachments.length + selected.length > 5) { showError('每条消息最多添加 5 张图片'); return; }
    for (const file of selected) {
      const dataURL = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result));
        reader.onerror = () => reject(new Error('图片读取失败'));
        reader.readAsDataURL(file);
      });
      const comma = dataURL.indexOf(',');
      attachments = [...attachments, { name: file.name, mediaType: file.type, data: dataURL.slice(comma + 1), preview: dataURL }];
    }
  }

  function onDrop(event: DragEvent) {
    event.preventDefault();
    if (event.dataTransfer?.files) void addFiles(event.dataTransfer.files);
  }

  function handleComposerKey(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      void sendMessage();
    }
  }

  async function resolveApproval(request: PendingRequestView, allowed: boolean) {
    try {
      await Desktop.ResolveApproval(request.id, allowed);
      if (allowed) resumeExecution('授权已完成，正在继续处理');
      await refreshSession();
    } catch (error) { showError(error); }
  }

  function resumeExecution(label: string) {
    running = true;
    startElapsedTimer(false);
    setExecutionStatus('running', label, { expand: true });
  }

  async function answerQuestion(request: PendingRequestView) {
    const answers = (request.questions ?? []).map((question) => {
      const option = answerValues[question.id] || '';
      const notes = noteValues[question.id]?.trim() || '';
      const answer = option ? `${option}${notes ? `：${notes}` : ''}` : notes;
      return { question_id: question.id, answer };
    });
    if (answers.some((answer) => !answer.answer.trim())) { showError('请先回答每个问题'); return; }
    try {
      await Desktop.AnswerQuestion({ executionId: request.executionId, runId: request.runId, invocationId: request.invocationId, callId: request.callId, answers });
      pending = pending.filter((item) => item.id !== request.id);
      resumeExecution('回答已提交，正在继续处理');
    } catch (error) { showError(error); }
  }

  async function recover(request: PendingRequestView) {
    try {
      if (request.kind === 'model_recovery') await Desktop.RecoverModel();
      else await Desktop.ResolveToolRecovery(request.invocationId, 'retry');
      pending = pending.filter((item) => item.id !== request.id);
      resumeExecution('正在恢复任务');
    } catch (error) { showError(error); }
  }

  async function dismissRecovery(request: PendingRequestView) {
    try {
      await Desktop.ResolveToolRecovery(request.invocationId, 'skip');
      pending = pending.filter((item) => item.id !== request.id);
      resumeExecution('正在跳过失败步骤');
    } catch (error) { showError(error); }
  }

  async function implementPlan(mode: 'same_context' | 'new_context') {
    if (!planReview || previewMode) return;
    busy = true;
    try {
      const result = await Desktop.ImplementPlan(planReview.executionID, mode);
      planReview = null;
      planMode = false;
      if (result.sessionId !== session?.id) {
        sessions = [{ id: result.sessionId, title: '实施方案', updatedAt: new Date().toISOString() }, ...sessions];
        await openSession(result.sessionId);
      }
      running = true;
      executionID = result.executionId;
    } catch (error) { showError(error); }
    finally { busy = false; }
  }

  function stayInPlan() { planReview = null; }

  onMount(() => {
    void initialize();
    return () => clearElapsedTimer();
  });
</script>

<svelte:head>
  <title>{session?.title ? `${session.title} · luneAgent` : 'luneAgent'}</title>
</svelte:head>

{#if loading}
  <div class="boot"><span class="brand-mark">l</span><span>正在启动 luneAgent…</span></div>
{:else}
  <div class="app-shell" role="region" aria-label="桌面工作区拖放区域" ondragover={(event) => event.preventDefault()} ondrop={onDrop}>
    <aside class="sidebar">
      <div class="brand-row">
        <div class="brand-mark">l</div>
        <div class="brand-copy"><strong>luneAgent</strong><span>本地 Agent 工作台</span></div>
        <button class="icon-button sidebar-more" aria-label="设置" onclick={setSettingsForm}>···</button>
      </div>

      <button class="new-chat" onclick={workspace ? createSession : openFolderPicker}>
        <span class="plus">＋</span>{workspace ? '新建会话' : '打开工作区'}<kbd>⌘ K</kbd>
      </button>
      <div class="sidebar-section"><span>工作区</span><button class="tiny-add" aria-label="打开工作区" onclick={openFolderPicker}>＋</button></div>
      {#if workspace}
        <button class="workspace-row active" title={workspace} onclick={() => void closeWorkspace()}>
          <span class="folder-icon">▰</span><span class="workspace-name">{workspace.split(/[\\/]/).at(-1) || workspace}</span><span class="workspace-menu">⌄</span>
        </button>
        <div class="sidebar-section session-label"><span>最近会话</span><button class="tiny-add" aria-label="新建会话" onclick={createSession}>＋</button></div>
        <div class="session-list">
          {#each sessions as item (item.id)}
            <button class:chosen={session?.id === item.id} class="session-row" onclick={() => void openSession(item.id)}>
              <span class="session-dot">{session?.id === item.id ? '●' : '○'}</span>
              <span>{item.title || '新会话'}</span>
            </button>
          {/each}
        </div>
      {:else}
        <div class="sidebar-empty">打开一个本地文件夹开始工作。</div>
        {#if recent?.length}
          <div class="sidebar-section"><span>最近打开</span></div>
          {#each recent as item (item.path)}
            <button class="recent-row" title={item.path} onclick={() => void openWorkspace(item.path)}>
              <span class="folder-icon">▰</span><span>{item.path.split(/[\\/]/).at(-1)}</span>
            </button>
          {/each}
        {/if}
      {/if}

      <div class="sidebar-bottom">
        <div class="sidebar-profile"><div class="avatar">L</div><div><strong>本地工作区</strong><span>{models.configured ? `默认模型 · ${models.defaultModel}` : '尚未配置模型'}</span></div></div>
        <button class="settings-entry" onclick={setSettingsForm}><span>⚙</span> 模型与偏好设置 <span class="settings-shortcut">⌘ ,</span></button>
      </div>
    </aside>

    <main class="main-panel">
      <header class="topbar">
        <div class="crumbs">
          {#if workspace}<span class="crumb-root">{workspace.split(/[\\/]/).at(-1)}</span><span class="crumb-separator">/</span><span class="crumb-current">{session?.title || '新会话'}</span>{:else}<span class="crumb-current">桌面工作区</span>{/if}
        </div>
        <div class="top-actions">
          {#if workspace}
            <button class="subtle-button" onclick={() => void closeWorkspace()} title="关闭工作区">关闭工作区</button>
            <span class="top-divider"></span>
            <label class="permission-select"><span>权限</span><select value={permissionMode} disabled={planMode || running} onchange={(event) => void setPermission(event.currentTarget.value)}>
              <option value="read-only">只读</option><option value="workspace">工作区</option><option value="ask">每次询问</option><option value="full-access">完全访问</option>
            </select></label>
          {/if}
          <label class="theme-select" title="主题"><span>{theme === 'dark' ? '◐' : theme === 'light' ? '☼' : '◑'}</span><select value={theme} onchange={(event) => void changeTheme(event.currentTarget.value)} aria-label="主题"><option value="system">跟随系统</option><option value="light">浅色</option><option value="dark">深色</option></select></label>
          <button class="icon-button top-settings" aria-label="设置" onclick={setSettingsForm}>⚙</button>
        </div>
      </header>

      {#if !workspace}
        <section class="welcome">
          <div class="welcome-icon"><span>l</span></div>
          <p class="eyebrow">LUNEAGENT DESKTOP</p>
          <h1>把想法带进你的工作区</h1>
          <p class="welcome-copy">选择一个本地项目文件夹，与 Agent 一起阅读代码、讨论方案并完成修改。</p>
          <button class="primary-button open-workspace" onclick={openFolderPicker}><span>▰</span> 打开工作区</button>
          {#if recent?.length}
            <div class="welcome-recent"><span>最近打开</span>{#each recent.slice(0, 4) as item (item.path)}<button onclick={() => void openWorkspace(item.path)}><span>▰</span><span>{item.path}</span><b>›</b></button>{/each}</div>
          {/if}
          {#if !models.configured}<button class="inline-link welcome-config" onclick={setSettingsForm}>先配置模型以开始对话</button>{/if}
        </section>
      {:else if !session}
        <section class="workspace-loading"><div class="spinner"></div><span>正在载入会话…</span></section>
      {:else}
        <div class="conversation-scroll" id="conversation-scroll">
          <div class="conversation-content">
            {#if messages.length === 0}
              <div class="conversation-intro"><p>可以在 <b>{workspace.split(/[\\/]/).at(-1)}</b> 中阅读、修改和运行代码。</p></div>
            {/if}
            {#each transcriptRows as row (row.message.id)}
              <article class:from-user={row.message.role === 'user'} class="message-row">
                <div class="message-body">
                  <div class="message-text">
                    {#if row.message.role === 'assistant'}<Markdown source={row.message.text} />{:else}<div class="plain-text">{row.message.text}</div>{/if}
                  </div>
                  {#if row.message.images?.length}<div class="message-images">{#each row.message.images as image}<img src={image} alt="消息附件" />{/each}</div>{/if}
                  {#if row.message.interrupted}<span class="interrupted-message-state">本轮已中断</span>{/if}
                  {#if row.message.deliveryState === 'failed'}
                    <div class="message-delivery-error"><span>{row.message.deliveryError || '消息未能发送'}</span><button class="small-button" onclick={() => void retryMessage(row.message)}>重试</button></div>
                  {:else if row.message.deliveryState === 'sending'}
                    <div class="message-delivery-pending">正在发送…</div>
                  {/if}
                </div>
              </article>
              {#each row.activityRuns as run (run.key)}
                <ExecutionActivity {run} active={run.executionId === executionID && running} elapsedSeconds={run.executionId === executionID ? elapsedSeconds : run.elapsedSeconds ?? 0} onToggle={() => toggleActivityRun(run.key)} />
              {/each}
            {/each}
            {#each orphanActivityRuns as run (run.key)}
              <ExecutionActivity {run} active={run.executionId === executionID && running} elapsedSeconds={run.executionId === executionID ? elapsedSeconds : run.elapsedSeconds ?? 0} onToggle={() => toggleActivityRun(run.key)} />
            {/each}

            {#if streamText}
              <article class="message-row"><div class="message-body"><div class="message-text"><Markdown source={streamText} live /><span class="stream-caret"></span></div></div></article>
            {/if}

            {#if approvals.length}
              {#each approvals as approval (approval.id)}
                <section class="request-card approval-card">
                  <div class="request-heading"><span class="request-icon">!</span><div><strong>需要授权</strong><p>Agent 想要运行工具 <code>{approval.tool}</code></p></div></div>
                  {#if approval.arguments}<pre>{formatArguments(approval.arguments)}</pre>{/if}
                  <div class="request-actions"><button class="quiet-button" onclick={() => void resolveApproval(approval, false)}>拒绝</button><button class="primary-button" onclick={() => void resolveApproval(approval, true)}>允许本次操作</button></div>
                </section>
              {/each}
            {/if}

          {#if activeQuestion}
              <section class="request-card question-card">
                <div class="question-kicker">Agent 需要你的选择</div>
                {#each activeQuestion.questions ?? [] as question (question.id)}
                  <div class="question-item">
                    <h3>{question.question}</h3>
                    {#if question.options?.length}
                      <div class="question-options">
                        {#each question.options as option (option.label)}
                          <label class:selected-option={answerValues[question.id] === option.label} class="question-option"><input type="radio" name={question.id} value={option.label} bind:group={answerValues[question.id]} /><span class="radio-mark"></span><span><b>{option.label}</b>{#if option.description}<small>{option.description}</small>{/if}</span></label>
                        {/each}
                      </div>
                    {/if}
                    <input class="notes-input" placeholder={question.options?.length ? '补充说明（可选）' : '输入你的回答'} bind:value={noteValues[question.id]} />
                  </div>
                {/each}
                <div class="request-actions"><span class="request-hint">你的回答会继续当前任务</span><button class="primary-button" onclick={() => void answerQuestion(activeQuestion!)}>提交回答</button></div>
              </section>
            {/if}

            {#if recoveries.length}
              {#each recoveries as request (request.id)}
                <section class="request-card recovery-card"><strong>{request.kind === 'model_recovery' ? '模型请求未完成' : '工具执行暂停'}</strong><p>{request.reason || request.tool || '此操作需要恢复。'}</p><div class="request-actions"><button class="quiet-button" onclick={() => void dismissRecovery(request)}>跳过</button><button class="primary-button" onclick={() => void recover(request)}>重试</button></div></section>
              {/each}
            {/if}

            {#if planReview}
              <section class="plan-review">
                <div class="plan-title"><span>方案已完成</span><span class="plan-badge">PLAN</span></div>
                <div class="plan-body">{#each planReview.text.split('\n') as line}<p class:plan-heading={line.startsWith('#')} class:plan-list={line.trimStart().startsWith('- ') || line.trimStart().startsWith('* ')}>{line.replace(/^#{1,3}\s*/, '').replace(/^[-*]\s*/, '• ') || '\u00a0'}</p>{/each}</div>
                <div class="plan-question"><strong>要开始实施这个方案吗？</strong><span>实施时会切换到工作区权限。</span></div>
                <div class="plan-options">
                  <button onclick={() => void implementPlan('same_context')}><span class="plan-option-number">1</span><span><b>是，实施方案</b><small>在当前会话开始编码</small></span><span class="plan-arrow">↵</span></button>
                  <button onclick={() => void implementPlan('new_context')}><span class="plan-option-number">2</span><span><b>清空上下文并实施</b><small>在新会话中开始，保留本方案</small></span><span class="plan-arrow">↵</span></button>
                  <button onclick={stayInPlan}><span class="plan-option-number">3</span><span><b>继续规划</b><small>留在只读 Plan 模式</small></span><span class="plan-arrow">↵</span></button>
                </div>
              </section>
            {/if}
          </div>
        </div>

        <div class="composer-wrap">
          {#if !models.configured}
            <button class="setup-notice" onclick={setSettingsForm}><span>ⓘ</span> 配置一个模型后即可开始对话 <b>打开设置 →</b></button>
          {/if}
          {#if attachments.length}
            <div class="attachment-strip">{#each attachments as attachment, index}<div class="attachment-chip"><img src={attachment.preview} alt={attachment.name} /><span>{attachment.name}</span><button aria-label="移除图片" onclick={() => attachments = attachments.filter((_, i) => i !== index)}>×</button></div>{/each}</div>
          {/if}
          <div class:composer-disabled={!models.configured} class="composer">
            <textarea bind:value={composerText} onkeydown={handleComposerKey} placeholder={!models.configured ? '先配置模型以开始对话' : pending.length ? '请先处理上方的请求' : planReview ? '请先选择方案操作' : '发送消息…'} disabled={!canSend} rows="1" aria-label="消息输入"></textarea>
            <div class="composer-toolbar">
              <div class="composer-left">
                <button class="composer-tool" title="添加图片" onclick={() => document.getElementById('image-picker')?.click()} disabled={!models.configured}><span>＋</span><span>添加图片</span></button>
                <input id="image-picker" type="file" accept="image/png,image/jpeg,image/gif,image/webp" multiple hidden onchange={(event) => event.currentTarget.files && void addFiles(event.currentTarget.files)} />
                <span class="composer-separator"></span>
                <button class:plan-active={planMode} class="plan-toggle" onclick={() => void setPlanMode(!planMode)} disabled={running}><span>◈</span> Plan 模式</button>
              </div>
              <div class="composer-right">
                {#if models.configured && models.profiles?.length && session}
                  <select class="model-profile-select" value={session.settings.model_profile || models.defaultModel} disabled={running} aria-label="会话模型" onchange={(event) => void setSessionModel(event.currentTarget.value)}>
                    {#each models.profiles as profile}<option value={profile.name}>{profile.name}</option>{/each}
                  </select>
                {:else}<span class="model-label">未配置模型</span>{/if}
                {#if running}<button class="stop-button" onclick={() => void interruptTask()} disabled={interrupting} title="中断任务">{interrupting ? '正在中断…' : '■ 中断'}</button>{:else}<button class="send-button" onclick={() => void sendMessage()} disabled={!canSend || (!composerText.trim() && !attachments.length)} aria-label="发送"><span>↑</span></button>{/if}
              </div>
            </div>
          </div>
          <div class="composer-footnote">luneAgent 可能会出错，请检查重要信息。<span>Enter 发送 · Shift+Enter 换行</span></div>
        </div>
      {/if}
    </main>
  </div>
{/if}

{#if errorMessage}<div class="toast-error" role="status">{errorMessage}<button onclick={() => errorMessage = ''}>×</button></div>{/if}

{#if modal}
  <div class="modal-backdrop" role="presentation" onclick={(event) => event.target === event.currentTarget && (modal = null)}>
          {#if modal === 'settings'}
      <div class="modal settings-modal" role="dialog" aria-modal="true" aria-labelledby="settings-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">PREFERENCES</span><h2 id="settings-title">模型与偏好设置</h2></div><button class="icon-button" aria-label="关闭" onclick={() => modal = null}>×</button></div>
        <div class="modal-body settings-body">
          <section class="settings-section"><div class="settings-section-heading"><div><h3>模型提供方</h3><p>API 密钥使用当前 Windows 用户 DPAPI 加密保存。</p></div><button class="small-button" onclick={addProvider}>＋ 添加提供方</button></div>
            {#each providerForms as provider, index (index)}
              <div class="provider-form">
                <div class="form-grid">
                  <label>名称<input bind:value={provider.name} /></label>
                  <label>协议<select bind:value={provider.protocol}><option value="responses">Responses API</option><option value="anthropic-messages">Anthropic Messages</option></select></label>
                  <label class="wide-field">API 地址<input bind:value={provider.baseURL} placeholder="https://api.example.com/v1" /></label>
                  <label class="wide-field">API 密钥<input type="password" bind:value={provider.apiKey} placeholder={provider.hasAPIKey ? '留空则保留已保存的密钥' : '粘贴 API 密钥'} autocomplete="new-password" /></label>
                  <label class="check-label"><input type="checkbox" bind:checked={provider.serverCompaction} /> 服务端上下文压缩</label>
                </div>
                {#if providerForms.length > 1}<button class="remove-row" onclick={() => providerForms = providerForms.filter((_, i) => i !== index)}>移除提供方</button>{/if}
              </div>
            {/each}
          </section>
          <section class="settings-section"><div class="settings-section-heading"><div><h3>模型档案</h3><p>档案名称会显示在会话的模型选择器中。</p></div><button class="small-button" onclick={addProfile}>＋ 添加模型</button></div>
            {#each profileForms as profile, index (index)}
              <div class="profile-form">
                <label>档案名称<input bind:value={profile.name} /></label>
                <label>提供方<select bind:value={profile.provider}>{#each providerForms as provider}<option value={provider.name}>{provider.name}</option>{/each}</select></label>
                <label>模型 ID<input bind:value={profile.model} placeholder="例如 deepseek-chat" /></label>
                <label>最大输出 Token<input type="number" min="0" bind:value={profile.maxOutputTokens} /></label>
                {#if profileForms.length > 1}<button class="remove-row" onclick={() => profileForms = profileForms.filter((_, i) => i !== index)}>移除模型</button>{/if}
              </div>
            {/each}
            <div class="form-grid bottom-grid"><label>默认模型<select bind:value={defaultModel}>{#each profileForms as profile}<option value={profile.name}>{profile.name}</option>{/each}</select></label><label>并行 Agent 数<input type="number" min="1" max="32" bind:value={maxParallelAgents} /></label></div>
          </section>
        </div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => modal = null}>取消</button><button class="primary-button" disabled={settingsSaving} onclick={() => void saveSettings()}>{settingsSaving ? '正在保存…' : '保存设置'}</button></div>
      </div>
    {:else if modal === 'migration' && migration}
      <div class="modal migration-modal" role="dialog" aria-modal="true" aria-labelledby="migration-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">WORKSPACE SETUP</span><h2 id="migration-title">迁移项目模型设置</h2></div><button class="icon-button" aria-label="关闭" onclick={() => modal = null}>×</button></div>
        <div class="modal-body"><p class="migration-summary">项目配置中发现模型设置。迁移后，提供方、模型档案和默认模型会保存在当前 Windows 用户的全局设置中；API 密钥会加密，项目配置中的模型字段会在同一操作成功后移除。也可以保留项目配置直接打开工作区。</p>
          <div class="migration-counts"><span><b>{migration.providers?.length ?? 0}</b> 个提供方</span><span><b>{migration.models?.length ?? 0}</b> 个模型档案</span></div>
          {#if migration.conflicts?.length}
            <h3 class="conflict-title">同名设置冲突</h3>
            <div class="conflict-list">{#each migration.conflicts as conflict (`${conflict.kind}:${conflict.name}`)}
              <div class="conflict-row"><div><strong>{conflict.name}</strong><span>{conflict.kind === 'provider' ? '提供方' : conflict.kind === 'model' ? '模型档案' : '默认模型'}</span><div class="conflict-values"><small>全局：{conflict.existing}</small><small>项目：{conflict.incoming}</small></div></div><select bind:value={migrationChoices[`${conflict.kind}:${conflict.name}`]}><option value="keep">保留全局设置</option><option value="replace">使用项目设置</option></select></div>
            {/each}</div>
          {:else}
            <div class="no-conflicts">没有同名冲突，可以直接导入这些设置。</div>
          {/if}
        </div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => { modal = null; migration = null; workspace = ''; session = null; sessions = []; }}>取消</button><button class="quiet-button" disabled={busy} onclick={() => void openProjectModels()}>保留项目设置打开</button><button class="primary-button" disabled={busy} onclick={() => void finishMigration()}>{busy ? '正在迁移…' : '迁移并打开'}</button></div>
      </div>
    {:else if modal === 'switch' || modal === 'close'}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">TASK IN PROGRESS</span><h2>{modal === 'switch' ? '中断当前任务并切换？' : '中断当前任务并关闭？'}</h2></div><button class="icon-button" aria-label="关闭" onclick={() => modal = null}>×</button></div>
        <div class="modal-body"><p>当前任务还在运行。确认后会请求中断，并等待 Core 完成清理后再{modal === 'switch' ? '打开另一个工作区' : '关闭工作区'}。</p></div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => modal = null}>继续等待</button><button class="danger-button" disabled={busy} onclick={() => modal === 'switch' ? void confirmWorkspaceSwitch() : void confirmCloseWorkspace()}>{busy ? '正在停止…' : '中断并继续'}</button></div>
      </div>
    {/if}
  </div>
{/if}
