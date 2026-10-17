<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Events } from '@wailsio/runtime';
  import Markdown from './lib/Markdown.svelte';
  import ExecutionActivity from './lib/ExecutionActivity.svelte';
  import MCPInteractionCard from './lib/MCPInteractionCard.svelte';
  import WorkspaceExplorer from './lib/WorkspaceExplorer.svelte';
  import * as Desktop from '../bindings/github.com/afterlune/lune/internal/desktop/desktopservice';
  import { TaskStatus } from '../bindings/github.com/afterlune/luneCore/internal/runtime/models';
  import type {
    Bootstrap, ConversationMessage, DesktopWorktreeView, MCPServerConfigInput, MCPServerView, ModelProfileInput, ModelSettingsView,
    SubmitInput, PendingRequestView, PendingSessionSummary as PendingSessionSummaryView, ProviderInput, SessionSummary, WorkspaceConfigInput, WorkspaceConfigView,
    WorkspaceChangesView, WorkspaceDiffView, WorkspaceFilePreview, WorkspaceOpenResult, WorkspaceState,
  } from '../bindings/github.com/afterlune/lune/internal/desktop/models';

  type LocalMessage = ConversationMessage & {
    local?: boolean; canonicalID?: string; deliveryState?: 'sending' | 'queued' | 'submitted' | 'failed' | 'cancelled';
    deliveryError?: string; request?: SubmitInput; executionID?: string; userMessageIndex?: number; queuePosition?: number; interrupted?: boolean;
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
    queuePosition?: number; messageText?: string; messageImages?: string[];
  };
  type StoredActivityRun = NonNullable<WorkspaceState['executionHistory']>[number];
  type StoredActivityTool = NonNullable<StoredActivityRun['tools']>[number];
  type ExecutionState = 'idle' | 'submitting' | 'running' | 'waiting' | 'completed' | 'failed' | 'cancelled' | 'interrupted' | 'disconnected';
  type ProviderForm = ProviderInput & { hasAPIKey: boolean };
  type MCPEnvironmentForm = { name: string; value: string; hasValue: boolean };
  type MCPServerForm = Omit<MCPServerConfigInput, 'args' | 'env'> & { args: string[]; argsText: string; env: MCPEnvironmentForm[] };
  type WorkspaceSettingsForm = Omit<WorkspaceConfigView, 'mcpServers'> & { mcpServers: MCPServerForm[] };
  type Modal = 'settings' | 'switch' | 'close' | 'edit' | 'rename-session' | 'delete-session' | 'delete-worktree' | 'mcp' | 'file-preview' | 'apply-worktree' | null;
  type EditDraft = { messageID: string; targetID: string; userMessageIndex: number; wasQueued: boolean; previousText: string; previousAttachments: { name: string; mediaType: string; data: string; preview: string }[] };
  type EditConfirmation = { draft: EditDraft; request: SubmitInput; pendingAtConfirm: boolean };
  type WorkspaceChanges = Omit<WorkspaceChangesView, 'files'> & { files: NonNullable<WorkspaceChangesView['files']> };
  type DesktopWorktree = Omit<DesktopWorktreeView, 'sessions'> & { sessions: NonNullable<DesktopWorktreeView['sessions']> };
  type WorktreeApplyConfirmation = { sessionID: string; paths: string[]; files: NonNullable<WorkspaceChangesView['files']>; destination: string };
  type PendingSessionSummary = Omit<PendingSessionSummaryView, 'requests'> & { requests: NonNullable<PendingSessionSummaryView['requests']> };
  type ElicitationResponse = { action: 'accept' | 'decline' | 'cancel'; content?: Record<string, unknown> };

  const previewVariant = new URLSearchParams(location.search).get('preview') || '';
  const previewMode = previewVariant !== '';
  let theme = $state('system');
  let recent = $state<Bootstrap['recentWorkspaces']>([]);
  let models = $state<ModelSettingsView>({ providers: [], profiles: [], defaultModel: '', maxParallelAgents: 0, configured: false });
  let workspace = $state('');
  let workingDirectory = $state('');
  let workspaceMode = $state('local');
  let worktreeAvailable = $state(false);
  let createSessionMenu = $state(false);
  let sessions = $state<SessionSummary[]>([]);
  let sessionFilter = $state('');
  let sessionActionTarget = $state<SessionSummary | null>(null);
  let sessionTitleDraft = $state('');
  let sidebarView = $state<'sessions' | 'files' | 'changes' | 'worktrees' | 'pending'>('sessions');
  let session = $state<WorkspaceState['session'] | null>(null);
  let permissionMode = $state('workspace');
  let planMode = $state(false);
  let pending = $state<PendingRequestView[]>([]);
  let pendingSessions = $state<PendingSessionSummary[]>([]);
  let pendingSessionsLoading = $state(false);
  let pendingSessionsError = $state('');
  let pendingSessionsRequest = 0;
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
  let switchTarget = $state('');
  let planReview = $state<{ executionID: string; text: string } | null>(null);
  let deferredPlanReviews = $state<{ sessionId: string; executionID: string; text: string }[]>([]);
  let answerValues = $state<Record<string, string>>({});
  let noteValues = $state<Record<string, string>>({});
  let providerForms = $state<ProviderForm[]>([]);
  let profileForms = $state<ModelProfileInput[]>([]);
  let defaultModel = $state('');
  let maxParallelAgents = $state(4);
  let settingsSaving = $state(false);
  let editDraft = $state<EditDraft | null>(null);
  let editConfirmation = $state<EditConfirmation | null>(null);
  let applyingEdit = $state(false);
  let mcpServers = $state<MCPServerView[]>([]);
  let mcpUpdatingServer = $state('');
  let workspaceSettingsForm = $state<WorkspaceSettingsForm>({
    contextWindow: 0, autoCompact: true, autoCompactThreshold: 0.8, reserveOutputTokens: 0,
    maxAttachmentFiles: 0, maxAttachmentBytes: 0, maxTotalBytes: 0, mcpServers: [],
  });
  let workspaceSettingsSaving = $state(false);
  let filePreview = $state<WorkspaceFilePreview | null>(null);
  let workspaceChanges = $state<WorkspaceChanges | null>(null);
  let workspaceChangesLoading = $state(false);
  let workspaceChangesError = $state('');
  let selectedWorkspaceChange = $state('');
  let workspaceDiff = $state<WorkspaceDiffView | null>(null);
  let workspaceDiffLoading = $state(false);
  let workspaceDiffError = $state('');
  let selectedApplyPaths = $state<string[]>([]);
  let worktreeApplyConfirmation = $state<WorktreeApplyConfirmation | null>(null);
  let applyingWorktreeChanges = $state(false);
  let worktreeApplyError = $state('');
  let worktreeApplyMessage = $state('');
  let workspaceChangesRequest = 0;
  let workspaceDiffRequest = 0;
  let workspaceChangesForSession = '';
  let workspaceChangesForDirectory = '';
  let desktopWorktrees = $state<DesktopWorktree[]>([]);
  let desktopWorktreesLoading = $state(false);
  let desktopWorktreesError = $state('');
  let desktopWorktreesRequest = 0;
  let selectedDesktopWorktreePath = $state('');
  let worktreeRemovalTarget = $state<DesktopWorktree | null>(null);
  let worktreeRemovalError = $state('');
  let cancellingQueuedExecution = $state('');
  const cancelledPreviewExecutions = new Set<string>();

  const activeQuestion = $derived(pending.find((item) => item.kind === 'user_question'));
  const approvals = $derived(pending.filter((item) => item.kind === 'approval'));
  const recoveries = $derived(pending.filter((item) => item.kind === 'tool_recovery' || item.kind === 'model_recovery' || item.kind === 'execution_blocker'));
  const userInteractions = $derived(pending.filter((item) => item.kind === 'user_interaction'));
  const canWrite = $derived(models.configured && !busy && !interrupting && pending.length === 0 && !planReview);
  const canSend = $derived(canWrite && !editDraft);
  const canEditMessages = $derived(models.configured && !busy && !interrupting && pending.length === 0 && !planReview && !applyingEdit);
  const queuedMessageCount = $derived(messages.filter((message) => message.deliveryState === 'queued' || message.deliveryState === 'sending').length);
  const workOutstanding = $derived(running || queuedMessageCount > 0);
  const workspaceHasOutstanding = $derived(workOutstanding || sessions.some((item) => ['running', 'queued', 'waiting'].includes(item.status ?? '')));
  const visibleSessions = $derived(sessions.filter((item) => item.title.toLowerCase().includes(sessionFilter.trim().toLowerCase())));
  const pendingRequestCount = $derived(pendingSessions.reduce((count, item) => count + item.requests.length, 0));
  const activeWorkspaceChange = $derived(workspaceChanges?.files.find((file) => file.path === selectedWorkspaceChange) ?? null);
  const selectedWorkspaceChanges = $derived(workspaceChanges?.files.filter((file) => selectedApplyPaths.includes(file.path)) ?? []);
  const currentSessionSummary = $derived(sessions.find((item) => item.id === session?.id));
  const worktreeApplyBlockedReason = $derived.by(() => {
    if (workspaceMode !== 'worktree') return '当前会话不是 Worktree';
    if (permissionMode === 'read-only') return '只读权限下不能写回原项目';
    if (running || queuedMessageCount > 0 || pending.length > 0 || ['running', 'queued', 'waiting'].includes(currentSessionSummary?.status ?? '')) return '任务运行、排队或等待处理时不能应用';
    if (!selectedApplyPaths.length) return '请先选择要应用的文件';
    if (workspaceChangesLoading || applyingWorktreeChanges) return '正在读取或应用变更';
    return '';
  });
  const canApplyWorktreeChanges = $derived(!worktreeApplyBlockedReason && (!previewMode || previewVariant.startsWith('changes')));
  const workspaceDiffLines = $derived(workspaceDiff?.content ? workspaceDiff.content.replace(/\r\n/g, '\n').split('\n') : []);
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
        ...(pendingMatch ?? {}),
        ...serverMessage,
        id: pendingMatch?.id ?? serverMessage.id,
        canonicalID: serverMessage.id,
        executionID: pendingMatch?.executionID,
      };
    });

    const unmatchedLocal = currentMessages.filter((message) => message.local && !consumed.has(message.id));
    return [...merged, ...unmatchedLocal];
  }

  function requestImagesFromURLs(images: string[]) {
    return images.flatMap((preview, index) => {
      const match = /^data:(image\/(?:png|jpeg|gif|webp));base64,(.+)$/i.exec(preview);
      return match ? [{ name: `image-${index + 1}`, mediaType: match[1].toLowerCase(), data: match[2] }] : [];
    });
  }

  function restorePendingExecutionMessages() {
    for (const run of activityHistory) {
      if (run.messageText === undefined && !run.messageImages?.length) continue;
      const images = run.messageImages ?? [];
      const request: SubmitInput = {
        text: run.messageText ?? '', images: requestImagesFromURLs(images), userMessageIndex: run.userMessageIndex,
      };
      const deliveryState = run.status === 'queued' ? 'queued' : run.status === 'failed' ? 'failed' : run.status === 'cancelled' ? 'cancelled' : 'submitted';
      const existingByExecution = messages.find((message) => message.executionID === run.executionId);
      if (existingByExecution) {
        updateMessage(existingByExecution.id, {
          deliveryState, queuePosition: run.queuePosition,
          deliveryError: deliveryState === 'failed' ? '任务执行失败' : undefined,
          request,
        });
        continue;
      }
      const targetIndex = Math.max(0, Math.min(run.userMessageIndex, messages.filter((message) => message.role === 'user').length));
      const existingAtIndex = messages.filter((message) => message.role === 'user')[targetIndex];
      if (existingAtIndex && existingAtIndex.text === request.text &&
        (existingAtIndex.images ?? []).length === images.length &&
        (existingAtIndex.images ?? []).every((image, index) => image === images[index])) {
        updateMessage(existingAtIndex.id, {
          executionID: run.executionId, userMessageIndex: run.userMessageIndex,
          deliveryState, queuePosition: run.queuePosition, request,
          deliveryError: deliveryState === 'failed' ? '任务执行失败' : undefined,
        });
        continue;
      }
      const restored: LocalMessage = {
        id: `execution-${run.executionId}`, role: 'user', text: request.text, images, local: true,
        deliveryState, executionID: run.executionId, userMessageIndex: run.userMessageIndex,
        queuePosition: run.queuePosition, request,
        deliveryError: deliveryState === 'failed' ? '任务执行失败' : undefined,
      };
      let insertion = messages.length;
      let seenUsers = 0;
      for (let index = 0; index < messages.length; index++) {
        if (messages[index].role !== 'user') continue;
        if (seenUsers === targetIndex) { insertion = index; break; }
        seenUsers++;
      }
      messages = [...messages.slice(0, insertion), restored, ...messages.slice(insertion)];
    }
  }

  function attachQueuedState(state: WorkspaceState) {
    const queued = state.runtimeState?.Queue ?? [];
    if (!queued.length) return;
    const queuedPositions = new Map(queued.filter((turn) => turn.Status === 'queued').map((turn, index) => [turn.ExecutionID, index + 1]));
    activityHistory = activityHistory.map((run) => {
      const item = queued.find((turn) => turn.ExecutionID === run.executionId);
      if (!item) return run;
      const isQueued = item.Status === 'queued';
      const isActive = item.Status === 'active';
      const position = isQueued ? queuedPositions.get(item.ExecutionID) : undefined;
      return {
        ...run,
        status: isQueued ? 'queued' : isActive ? 'running' : run.status,
        statusLabel: isQueued ? `排队中 · 第 ${position} 位` : isActive ? '正在处理请求' : run.statusLabel,
        queuePosition: position,
      };
    });
    for (const turn of queued) {
      if (turn.Status === 'queued') updateMessageForExecution(turn.ExecutionID, { deliveryState: 'queued', queuePosition: queuedPositions.get(turn.ExecutionID) });
      else if (turn.Status === 'active') updateMessageForExecution(turn.ExecutionID, { deliveryState: 'submitted' });
    }
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
        messageImages: run.messageImages ?? undefined,
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
    if (status === 'queued') return '任务排队中';
    if (status === 'completed') return '任务已完成';
    if (status === 'failed') return '任务执行失败';
    if (status === 'cancelled') return '排队消息已撤回';
    if (status === 'interrupted' || status === 'discarded') return '任务已中断';
    if (status === 'paused') return '任务已暂停';
    return '正在处理请求';
  }

  function currentUserMessageIndex() {
    const message = messages.find((item) => item.executionID === executionID);
    if (message?.userMessageIndex !== undefined) return message.userMessageIndex;
    const run = activityHistory.find((item) => item.executionId === executionID);
    return run?.userMessageIndex ?? -1;
  }

  function upsertActivityRun(update: Partial<ActivityRun> & { executionId?: string; key?: string }) {
    const key = update.key || update.executionId || activeActivityRunKey || 'current';
    const index = activityHistory.findIndex((run) => run.key === key || (!!update.executionId && run.executionId === update.executionId));
    const previous = index >= 0 ? activityHistory[index] : undefined;
    const message = update.executionId ? messages.find((item) => item.executionID === update.executionId) : undefined;
    const next: ActivityRun = {
      key,
      executionId: update.executionId ?? previous?.executionId ?? '',
      userMessageIndex: update.userMessageIndex !== undefined && update.userMessageIndex >= 0
        ? update.userMessageIndex : previous?.userMessageIndex ?? message?.userMessageIndex ?? -1,
      status: update.status ?? previous?.status ?? 'running',
      statusLabel: update.statusLabel ?? previous?.statusLabel ?? currentStatus,
      startedAt: update.startedAt ?? previous?.startedAt ?? new Date().toISOString(),
      finishedAt: update.finishedAt ?? previous?.finishedAt,
      tools: update.tools ?? previous?.tools ?? (update.executionId === executionID ? activities : []),
      expanded: update.expanded ?? previous?.expanded ?? false,
      elapsedSeconds: update.elapsedSeconds ?? previous?.elapsedSeconds,
      queuePosition: update.queuePosition ?? previous?.queuePosition,
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
    const key = activeActivityRunKey || executionID || 'current';
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
    workspace: 'D:\\Git\\luneAgent', projectRoot: 'D:\\Git\\luneAgent', workspaceMode: 'local', workingDirectory: 'D:\\Git\\luneAgent', permissionMode: 'workspace',
    session: {
      id: 'session-preview', title: '重构桌面端提问流程', updatedAt: '2026-10-16T10:36:00+08:00',
      settings: { model_profile: '工作模型' },
      messages: [
        { id: 'm1', role: 'user', text: '帮我检查 Question 面板的交互，并把截图测试流程接入桌面端。' },
        { id: 'm2', role: 'assistant', text: '我会先把窗口结构和消息流搭好，再补上浏览器预览与真实窗口启动检查。' },
      ],
    },
    sessions: [{ id: 'session-preview', title: '重构桌面端提问流程', updatedAt: '2026-10-16T10:36:00+08:00', mode: 'local', workingDirectory: 'D:\\Git\\luneAgent', status: 'idle' }],
    runtimeState: { Task: { Status: TaskStatus.TaskIdle, SessionID: 'session-preview', TaskID: '', ExecutionID: '', QueueID: '', ContextWindow: 0, ReserveOutputTokens: 0, AutoCompact: false, AutoCompactThreshold: 0, Goal: '', MaxToolRounds: 0, ModelOverride: '', ModelProfile: '工作模型', Provider: 'deepseek', Model: 'deepseek-chat', ReasoningEffort: '', Execution: {} }, Queue: [] },
    pending: [],
    executionHistory: [],
  };

  const previewWorkspaceChanges: WorkspaceChanges = {
    available: true, message: '', added: 24, removed: 8, refreshedAt: '2026-10-17T02:35:00Z',
    files: [
      { path: 'cmd/desktop/frontend/src/App.svelte', status: ' M', added: 6, removed: 3, binary: false },
      { path: 'internal/desktop/workspace_changes.go', status: 'M ', added: 16, removed: 4, binary: false },
      { path: 'testdata/sample.bin', status: '??', added: 2, removed: 0, binary: true },
    ],
  };
  const previewWorkspaceDiffs: Record<string, WorkspaceDiffView> = {
    'cmd/desktop/frontend/src/App.svelte': {
      path: 'cmd/desktop/frontend/src/App.svelte', status: ' M', binary: false, truncated: false,
      content: 'diff --git a/cmd/desktop/frontend/src/App.svelte b/cmd/desktop/frontend/src/App.svelte\n--- a/cmd/desktop/frontend/src/App.svelte\n+++ b/cmd/desktop/frontend/src/App.svelte\n@@ -55,7 +55,7 @@\n-  let sidebarView = $state<\'sessions\' | \'files\'>(\'sessions\');\n+  let sidebarView = $state<\'sessions\' | \'files\' | \'changes\'>(\'sessions\');\n',
    },
    'internal/desktop/workspace_changes.go': {
      path: 'internal/desktop/workspace_changes.go', status: 'M ', binary: false, truncated: false,
      content: 'diff --git a/internal/desktop/workspace_changes.go b/internal/desktop/workspace_changes.go\nindex 08a410a..fd10b21 100644\n--- a/internal/desktop/workspace_changes.go\n+++ b/internal/desktop/workspace_changes.go\n@@ -62,7 +62,10 @@ func (s *DesktopService) ListWorkspaceChanges() (WorkspaceChangesView, error) {\n-\tresult := WorkspaceChangesView{Files: []WorkspaceChangeView{}}\n+\tresult := WorkspaceChangesView{\n+\t\tFiles:     []WorkspaceChangeView{},\n+\t\tRefreshed: time.Now().UTC().Format(time.RFC3339Nano),\n+\t}\n \tif _, err := exec.LookPath("git"); err != nil {\n-\t\treturn WorkspaceChangesView{}, err\n+\t\tresult.Message = "找不到 Git，请安装 Git 后重试。"\n+\t\treturn result, nil\n \t}\n',
    },
    'testdata/sample.bin': {
      path: 'testdata/sample.bin', status: '??', binary: true, truncated: false, content: '',
    },
  };
  function previewPendingRequest(kind: string, id: string, fields: Partial<PendingRequestView> = {}): PendingRequestView {
    return { kind, id, executionId: `exec-${id}`, runId: `run-${id}`, invocationId: `invoke-${id}`, callId: `call-${id}`, tool: '', ...fields };
  }
  const previewPendingSessions: PendingSessionSummary[] = [
    {
      id: 'session-background', title: '后台会话需要你处理', mode: 'local', workingDirectory: 'D:\\Git\\luneAgent', updatedAt: '2026-10-17T08:15:00+08:00',
      requests: [
        { kind: 'approval', tool: 'exec_command' }, { kind: 'user_question', tool: '' },
        { kind: 'model_recovery', tool: '' }, { kind: 'tool_recovery', tool: 'write_file' },
        { kind: 'user_interaction', tool: 'docs' }, { kind: 'execution_blocker', tool: 'command' },
      ],
    },
    {
      id: 'session-worktree', title: 'Worktree 文档登录', mode: 'worktree', workingDirectory: 'D:\\Git\\luneAgent\\.worktrees\\docs', updatedAt: '2026-10-17T08:12:00+08:00',
      requests: [{ kind: 'approval', tool: 'apply_patch' }, { kind: 'user_interaction', tool: 'private-docs' }],
    },
  ];
  const previewPendingRequests: Record<string, PendingRequestView[]> = {
    'session-background': [
      previewPendingRequest('approval', 'approval-preview', { tool: 'exec_command', arguments: { command: 'git status --short' } }),
      previewPendingRequest('user_question', 'question-background', { questions: [{ id: 'next-step', question: '接下来优先检查哪一部分？', options: [{ label: '测试', description: '先确认当前改动的行为。' }, { label: '实现', description: '继续完成主要功能。' }] }] }),
      previewPendingRequest('model_recovery', 'model-background', { reason: 'rate_limit' }),
      previewPendingRequest('tool_recovery', 'tool-background', { tool: 'write_file', arguments: { path: 'docs/desktop.md' } }),
      previewPendingRequest('user_interaction', 'interaction-form', {
        tool: 'docs', arguments: { method: 'elicitation/create', params: { mode: 'form', message: '请输入文档服务的一次性验证码。', requestedSchema: { type: 'object', properties: { code: { type: 'string', title: '验证码', minLength: 4 }, remember: { type: 'boolean', title: '记住此设备' }, retries: { type: 'integer', title: '重试次数', minimum: 1, maximum: 5 }, account: { type: 'string', title: '账户类型', enum: ['个人', '工作'] } }, required: ['code'] } } },
      }),
      previewPendingRequest('execution_blocker', 'blocker-background', { tool: 'command', reason: '前一条命令仍在执行，任务暂停以等待检查。' }),
    ],
    'session-worktree': [
      previewPendingRequest('approval', 'approval-worktree', { tool: 'apply_patch' }),
      previewPendingRequest('user_interaction', 'interaction-url', {
        tool: 'private-docs', arguments: { method: 'elicitation/create', params: { mode: 'url', message: '请在文档服务中完成登录。', url: 'https://docs.example.test/sign-in?token=hidden' } },
      }),
    ],
  };

  function previewPendingWorkspaceState(id: string): WorkspaceState {
    const target = previewPendingSessions.find((item) => item.id === id) ?? previewPendingSessions[0];
    return {
      ...previewState,
      workspaceMode: target.mode,
      workingDirectory: target.workingDirectory,
      session: { ...previewState.session, id: target.id, title: target.title, updatedAt: target.updatedAt },
      sessions: previewPendingSessions.map((item) => ({
        id: item.id, title: item.title, updatedAt: item.updatedAt, mode: item.mode,
        workingDirectory: item.workingDirectory, status: 'waiting',
        ...(item.mode === 'worktree' ? { worktreePath: item.workingDirectory } : {}),
      })),
      runtimeState: {
        ...previewState.runtimeState,
        Task: { ...previewState.runtimeState.Task, SessionID: target.id, Status: TaskStatus.TaskPaused },
      },
      pending: previewPendingRequests[target.id] ?? [],
    };
  }

  function previewChangesForVariant(): WorkspaceChanges {
    if (previewVariant === 'changes-empty') return { ...previewWorkspaceChanges, files: [], added: 0, removed: 0 };
    if (previewVariant === 'changes-unavailable') return { ...previewWorkspaceChanges, available: false, message: '当前会话目录不在可用的 Git 仓库中。', files: [], added: 0, removed: 0 };
    return previewWorkspaceChanges;
  }

  function previewDesktopWorktrees(): DesktopWorktree[] {
    const root = 'C:\\Users\\Code\\AppData\\Local\\luneAgent\\worktrees\\4b86f2c7';
    return [
      { path: `${root}\\thread-a`, gitRegistered: true, available: true, orphaned: false, gitStatus: 'clean', changedFiles: 0, sessions: [{ id: 'wt-session-a', title: '修复任务队列', status: 'completed' }], requiresLocalSwitch: previewVariant === 'worktrees-active', canRemove: true },
      { path: `${root}\\thread-b`, gitRegistered: true, available: true, orphaned: true, gitStatus: 'clean', changedFiles: 0, sessions: [], requiresLocalSwitch: false, canRemove: true },
      { path: `${root}\\thread-c`, gitRegistered: true, available: true, orphaned: false, gitStatus: 'dirty', changedFiles: 2, sessions: [{ id: 'wt-session-c', title: '保留中的本地改动', status: 'idle' }], requiresLocalSwitch: false, canRemove: false, blockReason: '包含 2 项暂存、未暂存、未跟踪或忽略文件' },
      { path: `${root}\\thread-d`, gitRegistered: false, available: false, orphaned: false, gitStatus: 'unavailable', changedFiles: 0, sessions: [], requiresLocalSwitch: false, canRemove: false, blockReason: 'Git 中未登记此缓存目录，请先手动检查' },
    ];
  }

  function previewChangesWorkspaceState(): WorkspaceState {
    const workingDirectory = `${previewState.workspace}\\.worktrees\\preview`;
    const waiting = previewVariant === 'changes-waiting';
    const permission = previewVariant === 'changes-readonly' ? 'read-only' : previewVariant === 'changes-ask' ? 'ask' : 'workspace';
    const id = 'session-worktree-preview';
    return {
      ...previewState,
      workspaceMode: 'worktree', workingDirectory, worktreePath: workingDirectory, permissionMode: permission,
      session: { ...previewState.session, id, title: 'Worktree 结果审查' },
      sessions: [{
        id, title: 'Worktree 结果审查', updatedAt: previewState.session.updatedAt, mode: 'worktree',
        workingDirectory, worktreePath: workingDirectory, status: waiting ? 'waiting' : 'completed',
      }],
      runtimeState: {
        ...previewState.runtimeState,
        Task: { ...previewState.runtimeState.Task, SessionID: id, Status: waiting ? TaskStatus.TaskPaused : TaskStatus.TaskCompleted },
      },
    };
  }

  function sameWorkspacePath(left: string, right: string) {
    const normalizedLeft = left.replace(/[\\/]+$/, '').replace(/\\/g, '/');
    const normalizedRight = right.replace(/[\\/]+$/, '').replace(/\\/g, '/');
    return navigator.userAgent.includes('Windows')
      ? normalizedLeft.toLocaleLowerCase() === normalizedRight.toLocaleLowerCase()
      : normalizedLeft === normalizedRight;
  }

  function sessionSharesActiveDirectory(sessionId: string) {
    if (sessionId === session?.id) return true;
    const completedSession = sessions.find((item) => item.id === sessionId);
    const directory = completedSession?.workingDirectory || completedSession?.worktreePath || '';
    return !!directory && !!workingDirectory && sameWorkspacePath(directory, workingDirectory);
  }

  async function refreshWorkspaceChanges() {
    const request = ++workspaceChangesRequest;
    ++workspaceDiffRequest;
    const requestSession = session?.id ?? '';
    const requestDirectory = workingDirectory;
    if (workspaceChangesForSession !== requestSession || workspaceChangesForDirectory !== requestDirectory) {
      workspaceChanges = null;
      selectedWorkspaceChange = '';
      selectedApplyPaths = [];
      worktreeApplyMessage = '';
      workspaceChangesForSession = '';
      workspaceChangesForDirectory = '';
    }
    workspaceChangesLoading = true;
    workspaceChangesError = '';
    workspaceDiffLoading = false;
    workspaceDiffError = '';
    workspaceDiff = null;
    if (!requestSession || !requestDirectory) {
      workspaceChanges = null;
      selectedWorkspaceChange = '';
      workspaceChangesLoading = false;
      return;
    }
    try {
      const rawResult = previewMode ? previewChangesForVariant() : await Desktop.ListWorkspaceChanges();
      const result: WorkspaceChanges = { ...rawResult, files: rawResult.files ?? [] };
      if (request !== workspaceChangesRequest || session?.id !== requestSession || workingDirectory !== requestDirectory || sidebarView !== 'changes') return;
      workspaceChanges = result;
      workspaceChangesForSession = requestSession;
      workspaceChangesForDirectory = requestDirectory;
      selectedApplyPaths = selectedApplyPaths.filter((path) => result.files.some((file) => file.path === path));
      const nextPath = result.available
        ? result.files.find((file) => file.path === selectedWorkspaceChange)?.path ?? result.files[0]?.path ?? ''
        : '';
      selectedWorkspaceChange = nextPath;
      if (nextPath) void loadWorkspaceDiff(nextPath);
    } catch (error) {
      if (request === workspaceChangesRequest && session?.id === requestSession && workingDirectory === requestDirectory) {
        workspaceChangesError = String(error);
        workspaceChanges = null;
        selectedWorkspaceChange = '';
      }
    } finally {
      if (request === workspaceChangesRequest) workspaceChangesLoading = false;
    }
  }

  async function refreshDesktopWorktrees() {
    const request = ++desktopWorktreesRequest;
    const requestProject = workspace;
    desktopWorktreesLoading = true;
    desktopWorktreesError = '';
    try {
      const result = previewMode ? previewDesktopWorktrees() : await Desktop.ListDesktopWorktrees();
      if (request !== desktopWorktreesRequest || workspace !== requestProject || sidebarView !== 'worktrees') return;
      desktopWorktrees = (result ?? []).map((item) => ({ ...item, sessions: item.sessions ?? [] }));
      if (!desktopWorktrees.some((item) => item.path === selectedDesktopWorktreePath)) {
        selectedDesktopWorktreePath = desktopWorktrees[0]?.path ?? '';
      }
    } catch (error) {
      if (request === desktopWorktreesRequest && workspace === requestProject) {
        desktopWorktreesError = String(error);
        desktopWorktrees = [];
      }
    } finally {
      if (request === desktopWorktreesRequest) desktopWorktreesLoading = false;
    }
  }

  function beginDesktopWorktreeRemoval(worktree: DesktopWorktree) {
    if (!worktree.canRemove || busy) return;
    worktreeRemovalTarget = worktree;
    worktreeRemovalError = '';
    modal = 'delete-worktree';
  }

  function desktopWorktreeName(worktree: DesktopWorktreeView) {
    return worktree.path.split(/[\\/]/).at(-1) || worktree.path;
  }

  function desktopWorktreeStatusLabel(worktree: DesktopWorktreeView) {
    if (!worktree.gitRegistered || !worktree.available || worktree.gitStatus === 'unavailable') return '不可用';
    return worktree.gitStatus === 'dirty' ? `${worktree.changedFiles} 项变更` : 'Git 干净';
  }

  function desktopWorktreeSessionStatusLabel(status: string) {
    return ({ idle: '空闲', completed: '已完成', failed: '失败', cancelled: '已取消', interrupted: '已中断', running: '运行中', queued: '排队中', waiting: '等待处理', unavailable: '不可用', 'in use': '被其他实例占用', missing: '目录缺失' } as Record<string, string>)[status] ?? status;
  }

  async function confirmDesktopWorktreeRemoval() {
    const target = worktreeRemovalTarget;
    if (!target || busy) return;
    busy = true;
    worktreeRemovalError = '';
    try {
      if (target.sessions.some((item) => item.id === session?.id)) {
        const localSession = sessions.find((item) => item.mode === 'local');
        if (previewMode) {
          await createSession('local');
        } else if (localSession) {
          installWorkspaceState(await Desktop.OpenSession(localSession.id));
        } else {
          installWorkspaceState(await Desktop.CreateSession('local'));
        }
      }
      if (previewMode) {
        desktopWorktrees = desktopWorktrees.filter((item) => item.path !== target.path);
        selectedDesktopWorktreePath = desktopWorktrees[0]?.path ?? '';
      } else {
        await Desktop.RemoveDesktopWorktree(target.path);
        modal = null;
        worktreeRemovalTarget = null;
        try {
          if (session) installWorkspaceState(await Desktop.RefreshSession());
        } catch (error) {
          showError(error);
        }
        try {
          const refreshed = await Desktop.ListDesktopWorktrees();
          desktopWorktrees = (refreshed ?? []).map((item) => ({ ...item, sessions: item.sessions ?? [] }));
          selectedDesktopWorktreePath = desktopWorktrees[0]?.path ?? '';
        } catch (error) {
          desktopWorktreesError = String(error);
        }
        return;
      }
      modal = null;
      worktreeRemovalTarget = null;
    } catch (error) {
      worktreeRemovalError = error instanceof Error ? error.message : String(error);
      if (!previewMode) {
        try {
          const refreshed = await Desktop.ListDesktopWorktrees();
          desktopWorktrees = (refreshed ?? []).map((item) => ({ ...item, sessions: item.sessions ?? [] }));
          const updatedTarget = desktopWorktrees.find((item) => sameWorkspacePath(item.path, target.path));
          if (updatedTarget) worktreeRemovalTarget = updatedTarget;
        } catch { /* Keep the original removal error visible in the confirmation. */ }
      }
    } finally {
      busy = false;
    }
  }

  function toggleApplyPath(path: string) {
    selectedApplyPaths = selectedApplyPaths.includes(path)
      ? selectedApplyPaths.filter((selected) => selected !== path)
      : [...selectedApplyPaths, path];
    worktreeApplyMessage = '';
  }

  function toggleAllApplyPaths() {
    const availablePaths = workspaceChanges?.files.map((file) => file.path) ?? [];
    selectedApplyPaths = availablePaths.length > 0 && availablePaths.every((path) => selectedApplyPaths.includes(path))
      ? []
      : availablePaths;
    worktreeApplyMessage = '';
  }

  function beginWorktreeApply() {
    if (!canApplyWorktreeChanges || !session) return;
    worktreeApplyError = '';
    worktreeApplyConfirmation = {
      sessionID: session.id,
      paths: selectedWorkspaceChanges.map((file) => file.path),
      files: [...selectedWorkspaceChanges],
      destination: workspace,
    };
    modal = 'apply-worktree';
  }

  async function confirmWorktreeApply() {
    const confirmation = worktreeApplyConfirmation;
    if (!confirmation || applyingWorktreeChanges) return;
    applyingWorktreeChanges = true;
    worktreeApplyError = '';
    try {
      let alreadyPresent = false;
      if (previewMode) {
        if (previewVariant === 'changes-conflict') throw new Error('原项目存在冲突，未应用任何文件: conflict.txt');
      } else {
        const result = await Desktop.ApplyWorktreeChanges(confirmation.sessionID, confirmation.paths);
        alreadyPresent = result.alreadyPresent;
      }
      worktreeApplyMessage = alreadyPresent
        ? `所选 ${confirmation.paths.length} 个文件已存在于原项目：${confirmation.destination}`
        : `已将 ${confirmation.paths.length} 个文件应用到原项目：${confirmation.destination}`;
      selectedApplyPaths = [];
      worktreeApplyConfirmation = null;
      modal = null;
    } catch (error) {
      worktreeApplyError = error instanceof Error ? error.message : String(error);
    } finally {
      applyingWorktreeChanges = false;
    }
  }

  async function loadWorkspaceDiff(path: string) {
    const request = ++workspaceDiffRequest;
    const requestSession = session?.id ?? '';
    const requestDirectory = workingDirectory;
    selectedWorkspaceChange = path;
    workspaceDiffLoading = true;
    workspaceDiffError = '';
    workspaceDiff = null;
    try {
      const result = previewMode
        ? previewWorkspaceDiffs[path] ?? { path, status: activeWorkspaceChange?.status ?? '', content: '', binary: false, truncated: false }
        : await Desktop.ReadWorkspaceDiff(path);
      if (request !== workspaceDiffRequest || session?.id !== requestSession || workingDirectory !== requestDirectory || selectedWorkspaceChange !== path) return;
      workspaceDiff = result;
    } catch (error) {
      if (request === workspaceDiffRequest && session?.id === requestSession && selectedWorkspaceChange === path) workspaceDiffError = String(error);
    } finally {
      if (request === workspaceDiffRequest) workspaceDiffLoading = false;
    }
  }

  function changeStatusLabel(status: string) {
    if (status === '??' || status.includes('A')) return 'A';
    if (status.includes('D')) return 'D';
    if (status.includes('R')) return 'R';
    if (status.includes('C')) return 'C';
    if (status.includes('U')) return 'U';
    return 'M';
  }

  function changeStatusDescription(status: string) {
    if (status === '??') return '未跟踪';
    const staged = status[0] !== ' ';
    const unstaged = status[1] !== ' ';
    if (status.includes('A')) return staged ? '已暂存新增' : '新增';
    if (status.includes('D')) return staged ? '已暂存删除' : '删除';
    if (status.includes('R')) return '重命名';
    if (status.includes('U')) return '冲突';
    return staged && unstaged ? '暂存及未暂存修改' : staged ? '已暂存修改' : '未暂存修改';
  }

  async function refreshPendingSessions() {
    const request = ++pendingSessionsRequest;
    const requestWorkspace = workspace;
    if (!requestWorkspace) {
      pendingSessions = [];
      pendingSessionsError = '';
      pendingSessionsLoading = false;
      return;
    }
    pendingSessionsLoading = true;
    pendingSessionsError = '';
    try {
      const raw = previewMode
        ? previewVariant === 'pending-center' ? pendingSessions : []
        : await Desktop.ListPendingSessions();
      const result: PendingSessionSummary[] = (raw ?? []).map((item) => ({ ...item, requests: item.requests ?? [] })).filter((item) => item.requests.length > 0);
      if (request !== pendingSessionsRequest || workspace !== requestWorkspace) return;
      pendingSessions = result;
    } catch (cause) {
      if (request === pendingSessionsRequest && workspace === requestWorkspace) {
        pendingSessions = [];
        pendingSessionsError = String(cause);
      }
    } finally {
      if (request === pendingSessionsRequest) pendingSessionsLoading = false;
    }
  }

  function pendingKindLabel(kind: string) {
    switch (kind) {
      case 'approval': return '授权';
      case 'user_question': return '提问';
      case 'model_recovery': return '模型恢复';
      case 'tool_recovery': return '工具恢复';
      case 'user_interaction': return 'MCP 交互';
      case 'execution_blocker': return '执行阻塞';
      default: return '待处理';
    }
  }

  function removePreviewPendingSummary(request: PendingRequestView) {
    if (!previewMode || !session) return;
    let removed = false;
    pendingSessions = pendingSessions.map((item) => {
      if (item.id !== session?.id) return item;
      const requests = item.requests.filter((summary) => {
        if (!removed && summary.kind === request.kind && summary.tool === request.tool) {
          removed = true;
          return false;
        }
        return true;
      });
      return { ...item, requests };
    }).filter((item) => item.requests.length > 0);
  }

  function installWorkspaceState(state: WorkspaceState, preservePlanReview = false) {
    if (planReview && session && session.id !== state.session.id) {
      deferredPlanReviews = [...deferredPlanReviews, { sessionId: session.id, ...planReview }];
    }
    const currentPlanReview = preservePlanReview && session?.id === state.session.id ? planReview : null;
    clearElapsedTimer();
    workspace = state.projectRoot || state.workspace;
    workingDirectory = state.workingDirectory || state.workspace;
    workspaceMode = state.workspaceMode || 'local';
    session = state.session;
    sessions = state.sessions ?? [];
    permissionMode = state.permissionMode || 'workspace';
    if (state.models) models = state.models;
    pending = state.pending ?? [];
    messages = [...(state.session.messages ?? [])];
    activities = [];
    activityHistory = mergeExecutionHistory(state.executionHistory ?? [], []);
    attachQueuedState(state);
    restorePendingExecutionMessages();
    streamText = '';
    planReview = currentPlanReview;
    const status = state.runtimeState?.Task?.Status;
    running = status === 'running';
    executionID = running ? state.runtimeState?.Task?.ExecutionID ?? '' : '';
    executionState = running ? 'running' : pending.length ? 'waiting' : 'idle';
    currentStatus = running ? '正在处理请求' : pending.length ? '等待你的处理' : queuedMessageCount ? '任务排队中' : '';
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
    presentDeferredPlanReview();
    if (!previewMode) void refreshPendingSessions();
    if (sidebarView === 'changes' && !previewMode) void refreshWorkspaceChanges();
  }

  function applySessionRefresh(state: WorkspaceState) {
    session = state.session;
    sessions = state.sessions ?? sessions;
    permissionMode = state.permissionMode || permissionMode;
    if (state.models) models = state.models;
    pending = state.pending ?? [];
    messages = mergeMessagesFromSession(state.session.messages ?? [], messages);
    activityHistory = mergeExecutionHistory(state.executionHistory ?? [], activityHistory);
    attachQueuedState(state);
    restorePendingExecutionMessages();
    scrollToLatest();

    const task = state.runtimeState?.Task;
    const taskStatus = task?.Status;
    const wasRunning = running;
    const snapshotRunning = taskStatus === 'running';
    const localExecutionActive = executionState === 'submitting' || executionState === 'running' || executionState === 'disconnected';
    const localExecutionTerminal = executionState === 'completed' || executionState === 'failed' || executionState === 'cancelled' || executionState === 'interrupted';
    running = !localExecutionTerminal && (snapshotRunning || localExecutionActive);
    if (snapshotRunning && task?.ExecutionID) executionID = task.ExecutionID;
    else if (!localExecutionActive) executionID = '';

    if (running && snapshotRunning) {
      if (!wasRunning && executionState === 'idle') {
        setExecutionStatus('running', '正在处理请求', { expand: true });
      } else if (elapsedTimer === undefined && turnStartedAt === 0) {
        startElapsedTimer(true);
      } else if (executionState === 'waiting' && pending.length === 0) {
        startElapsedTimer(false);
        setExecutionStatus('running', '正在继续处理', { expand: true });
      }
    } else if (pending.length > 0 && executionState !== 'completed' && executionState !== 'failed' && executionState !== 'cancelled') {
      running = false;
      executionState = 'waiting';
      stopElapsedTimer();
      const waitingStatus = pending.some((item) => item.kind === 'approval')
        ? '等待你的授权'
        : pending.some((item) => item.kind === 'user_question')
          ? '等待你的回答'
          : '等待你处理恢复请求';
      setExecutionStatus('waiting', waitingStatus, { expand: true });
    } else if (!running && queuedMessageCount > 0 && !localExecutionTerminal) {
      executionState = 'idle';
      currentStatus = '任务排队中';
      stopElapsedTimer();
    } else if (!running && !localExecutionActive && !localExecutionTerminal) {
      executionState = 'idle';
      currentStatus = '';
      stopElapsedTimer();
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
      worktreeAvailable = true;
      recent = previewBootstrap.recentWorkspaces;
      models = previewBootstrap.models;
      const variant = previewVariant;
      const activeWorktreePreview: WorkspaceState = {
        ...previewState,
        workspaceMode: 'worktree', workingDirectory: `${previewState.workspace}\\.worktrees\\thread-a`,
        worktreePath: `${previewState.workspace}\\.worktrees\\thread-a`,
        session: { ...previewState.session, id: 'wt-session-a', title: '修复任务队列' },
        sessions: [{ id: 'wt-session-a', title: '修复任务队列', updatedAt: previewState.session.updatedAt, mode: 'worktree', workingDirectory: `${previewState.workspace}\\.worktrees\\thread-a`, worktreePath: `${previewState.workspace}\\.worktrees\\thread-a`, status: 'completed' }],
      };
      installWorkspaceState(variant.startsWith('changes') ? previewChangesWorkspaceState() : variant === 'worktrees-active' ? activeWorktreePreview : previewState);
      if (variant === 'pending-center') pendingSessions = previewPendingSessions;
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
      if (variant === 'running' || variant === 'interrupt' || variant === 'queue' || variant === 'queue-edit') {
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
      workingDirectory = data.workspace;
      permissionMode = data.permissionMode || 'workspace';
      applyTheme(theme);
      Events.On('agent-event', (event: { data: AgentEvent }) => onAgentEvent(event.data));
      Events.On('turn-result', (event: { data: TurnResultEvent }) => onTurnResult(event.data));
      Events.On('pending-changed', (event: { data: string }) => {
        void refreshPendingSessions();
        if (event.data === session?.id) void refreshSession();
      });
      Events.On('agent-error', (event: { data: { message: string } }) => showError(event.data.message));
      void refreshPendingSessions();
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

  function startExecution(event: AgentEvent) {
    if (!event.executionId) return;
    executionID = event.executionId;
    activeActivityRunKey = event.executionId;
    const currentRun = activityHistory.find((run) => run.executionId === event.executionId);
    activities = currentRun?.tools ?? [];
    activitySequence = 0;
    streamText = '';
    updateMessageForExecution(event.executionId, { deliveryState: 'submitted' });
    running = true;
    startElapsedTimer(true);
    const label = '正在等待模型响应';
    upsertActivityRun({
      key: event.executionId, executionId: event.executionId, status: 'running', statusLabel: label,
      userMessageIndex: currentRun?.userMessageIndex ?? currentUserMessageIndex(), tools: activities,
    });
    setExecutionStatus('running', label, { expand: true });
  }

  function updateMessageForExecution(execution: string, update: Partial<LocalMessage>) {
    messages = messages.map((message) => message.executionID === execution ? { ...message, ...update } : message);
  }

  function insertAssistantForUser(userMessageIndex: number, assistant: LocalMessage) {
    if (messages.some((message) => message.executionID === assistant.executionID && message.role === 'assistant')) return;
    let seenUsers = 0;
    let insertAt = messages.length;
    for (let index = 0; index < messages.length; index++) {
      const message = messages[index];
      if (message.role === 'user') {
        if (seenUsers === userMessageIndex) {
          insertAt = index + 1;
          while (insertAt < messages.length && messages[insertAt].role === 'assistant' && messages[insertAt].executionID === assistant.executionID) insertAt++;
          break;
        }
        seenUsers++;
      }
    }
    messages = [...messages.slice(0, insertAt), assistant, ...messages.slice(insertAt)];
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
    const finishedExecutionID = executionID;
    const currentRun = activityHistory.find((run) => run.key === activeActivityRunKey || (!!finishedExecutionID && run.executionId === finishedExecutionID));
    if (currentRun?.status === 'interrupted' && (normalized === 'paused' || normalized === 'discarded')) return;
    if (normalized === 'paused') {
      if (interruptRequestedExecutionID && (interruptRequestedExecutionID === finishedExecutionID || interruptRequestedExecutionID === activeActivityRunKey)) {
        setExecutionStatus('running', '正在中断任务');
        return;
      }
      running = false;
      stopElapsedTimer();
      executionState = 'waiting';
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
      const interrupted = normalized !== 'cancelled' || interruptRequestedExecutionID === finishedExecutionID;
      executionState = interrupted ? 'interrupted' : 'cancelled';
      updateMessageForExecution(finishedExecutionID, { deliveryState: 'submitted' });
      setExecutionStatus(interrupted ? 'interrupted' : 'cancelled', interrupted ? '任务已中断' : '任务已取消', { expand: false });
      return;
    }
    if (normalized === 'failed') {
      executionState = 'failed';
      updateMessageForExecution(finishedExecutionID, { deliveryState: 'failed', deliveryError: message || '任务执行失败' });
      setExecutionStatus('failed', message ? `任务失败：${message}` : '任务执行失败', { expand: false });
      return;
    }
    executionState = 'completed';
    updateMessageForExecution(finishedExecutionID, { deliveryState: 'submitted', deliveryError: undefined });
    setExecutionStatus('completed', '任务已完成', { expand: false });
  }

  function queuedExecution(event: AgentEvent) {
    if (!event.executionId) return;
    const message = messages.find((item) => item.executionID === event.executionId);
    const existing = activityHistory.find((run) => run.executionId === event.executionId);
    const position = message?.queuePosition ?? existing?.queuePosition ?? Math.max(1, queuedMessageCount);
    updateMessageForExecution(event.executionId, { deliveryState: 'queued', queuePosition: position });
    upsertActivityRun({
      key: event.executionId, executionId: event.executionId,
      userMessageIndex: message?.userMessageIndex ?? existing?.userMessageIndex ?? -1,
      status: 'queued', statusLabel: `排队中 · 第 ${position || 1} 位`, queuePosition: position,
      tools: existing?.tools ?? [],
    });
    if (!running && !executionID) currentStatus = '任务排队中';
  }

  function onAgentEvent(event: AgentEvent) {
    if (previewMode && cancelledPreviewExecutions.has(event.executionId)) return;
    updateSessionStatusFromEvent(event);
    if (event.sessionId !== session?.id) return;
    if (event.kind === 'task_queued') {
      queuedExecution(event);
      return;
    }
    if (event.kind === 'task_started') {
      startExecution(event);
      return;
    }
    if (!event.executionId || event.executionId !== executionID) return;

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
    if (event.kind === 'approval_required' || event.kind === 'user_question_required' || event.kind === 'execution_blocked' ||
      event.kind === 'user_interaction_required' || event.kind === 'model_recovery_required' ||
      event.kind === 'tool_recovery_required') {
      running = true;
      stopElapsedTimer();
      const label = event.kind === 'approval_required' ? '等待你的授权'
        : event.kind === 'user_question_required' ? '等待你的回答'
          : event.kind === 'user_interaction_required' ? '等待外部服务响应'
            : event.kind === 'execution_blocked' ? '执行受阻，等待你处理'
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
    if (previewMode && cancelledPreviewExecutions.has(result.executionId)) return;
    updateSessionStatus(result.sessionId, result.status === 'paused' ? 'waiting' : result.status || (result.error ? 'failed' : 'completed'));
    if (!previewMode) void refreshPendingSessions();
    if (sidebarView === 'changes' && sessionSharesActiveDirectory(result.sessionId)) void refreshWorkspaceChanges();
    if (result.sessionId !== session?.id) {
      if (result.plan) deferredPlanReviews = [...deferredPlanReviews, { sessionId: result.sessionId, executionID: result.executionId, text: result.plan }];
      return;
    }
    const currentRun = activityHistory.find((run) => run.executionId === result.executionId || run.key === result.executionId);
    const interrupted = interruptRequestedExecutionID === result.executionId || currentRun?.status === 'interrupted';
    const isCurrentExecution = result.executionId === executionID;
    const partialText = result.status === 'paused' && interrupted && isCurrentExecution ? streamText.trim() : '';
    const responseText = result.text || partialText;
    if (isCurrentExecution) {
      streamText = '';
      finishExecution(result.status || (result.error ? 'failed' : 'completed'), result.error);
    }
    if (result.error && result.status !== 'paused' && !interrupted) showError(result.error);
    if (responseText) {
      const run = activityHistory.find((item) => item.executionId === result.executionId);
      const userMessage = messages.find((item) => item.role === 'user' && item.executionID === result.executionId);
      insertAssistantForUser(run?.userMessageIndex ?? userMessage?.userMessageIndex ?? -1, {
        id: `assistant-${result.executionId}${partialText ? '-interrupted' : ''}`, role: 'assistant', text: responseText,
        local: true, deliveryState: 'submitted', executionID: result.executionId, interrupted: !!partialText,
      });
    }
    if (result.plan) deferredPlanReviews = [...deferredPlanReviews, { sessionId: result.sessionId, executionID: result.executionId, text: result.plan }];
    presentDeferredPlanReview();
    scrollToLatest();
    void refreshSession();
  }

  function updateSessionStatus(sessionId: string, status: string) {
    if (!sessionId || !status) return;
    sessions = sessions.map((item) => item.id === sessionId ? { ...item, status } : item);
  }

  function updateSessionStatusFromEvent(event: AgentEvent) {
    if (event.kind === 'task_queued') updateSessionStatus(event.sessionId, 'queued');
    else if (event.kind === 'task_started' || event.kind === 'model_recovery_resumed') updateSessionStatus(event.sessionId, 'running');
    else if (event.kind === 'task_finished') updateSessionStatus(event.sessionId, event.status === 'paused' ? 'waiting' : event.status);
    else if (event.kind === 'task_discarded') updateSessionStatus(event.sessionId, 'interrupted');
    else if (['approval_required', 'user_question_required', 'user_interaction_required', 'model_recovery_required', 'tool_recovery_required', 'execution_blocked'].includes(event.kind)) {
      updateSessionStatus(event.sessionId, 'waiting');
    }
  }

  function presentDeferredPlanReview() {
    if (!session || planReview || running || queuedMessageCount > 0 || pending.length > 0) return;
    const index = deferredPlanReviews.findIndex((item) => item.sessionId === session?.id);
    if (index < 0) return;
    planReview = deferredPlanReviews[index];
    deferredPlanReviews = deferredPlanReviews.filter((_, itemIndex) => itemIndex !== index);
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

  function workspaceSettingsFromView(config: WorkspaceConfigView): WorkspaceSettingsForm {
    return {
      contextWindow: config.contextWindow,
      autoCompact: config.autoCompact,
      autoCompactThreshold: config.autoCompactThreshold,
      reserveOutputTokens: config.reserveOutputTokens,
      maxAttachmentFiles: config.maxAttachmentFiles,
      maxAttachmentBytes: config.maxAttachmentBytes,
      maxTotalBytes: config.maxTotalBytes,
      mcpServers: (config.mcpServers ?? []).map((server) => ({
        name: server.name, originalName: server.name, transport: server.transport, command: server.command,
        args: server.args ?? [], argsText: (server.args ?? []).join('\n'),
        url: server.url, clientMetadataURL: server.clientMetadataURL,
        env: (server.env ?? []).map((item) => ({ name: item.name, value: '', hasValue: item.hasValue })),
      })),
    };
  }

  function addMCPServer() {
    const name = 'server-' + (workspaceSettingsForm.mcpServers.length + 1);
    workspaceSettingsForm.mcpServers = [...workspaceSettingsForm.mcpServers, {
      name, originalName: '', transport: 'stdio', command: '', args: [], argsText: '', url: '', clientMetadataURL: '', env: [],
    }];
  }

  function addMCPEnvironment(server: MCPServerForm) {
    server.env = [...server.env, { name: '', value: '', hasValue: false }];
  }

  function mcpStatus(name: string) {
    return mcpServers.find((server) => server.name === name);
  }

  async function saveWorkspaceSettings() {
    if (workspaceSettingsSaving || workOutstanding || pending.length || mcpUpdatingServer) return;
    workspaceSettingsSaving = true;
    try {
      const input: WorkspaceConfigInput = {
        contextWindow: workspaceSettingsForm.contextWindow,
        autoCompact: workspaceSettingsForm.autoCompact,
        autoCompactThreshold: workspaceSettingsForm.autoCompactThreshold,
        reserveOutputTokens: workspaceSettingsForm.reserveOutputTokens,
        maxAttachmentFiles: workspaceSettingsForm.maxAttachmentFiles,
        maxAttachmentBytes: workspaceSettingsForm.maxAttachmentBytes,
        maxTotalBytes: workspaceSettingsForm.maxTotalBytes,
        mcpServers: workspaceSettingsForm.mcpServers.map((server) => {
          const stdio = server.transport === 'stdio';
          return {
            name: server.name, originalName: server.originalName, transport: server.transport,
            command: stdio ? server.command : '',
            args: stdio ? server.argsText.split(/\r?\n/).filter((argument) => argument.length > 0) : [],
            url: stdio ? '' : server.url, clientMetadataURL: stdio ? '' : server.clientMetadataURL,
            env: stdio ? server.env.filter((item) => item.name.trim()).map(({ name, value }) => ({ name, value })) : [],
          };
        }),
      };
      if (previewMode) {
        workspaceSettingsForm = { ...workspaceSettingsForm, mcpServers: workspaceSettingsForm.mcpServers.map((server) => ({ ...server, args: server.argsText.split(/\r?\n/).filter(Boolean) })) };
      } else {
        const saved = await Desktop.SaveWorkspaceConfig(input);
        workspaceSettingsForm = workspaceSettingsFromView(saved);
        mcpServers = await Desktop.ListMCPServers() ?? [];
      }
      errorMessage = '';
    } catch (error) { showError(error); }
    finally { workspaceSettingsSaving = false; }
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
      const selectedSessionID = session?.id;
      const input = {
        providers: providerForms.map(({ name, protocol, baseURL, apiKey, serverCompaction }) => ({ name, protocol, baseURL, apiKey, serverCompaction })),
        profiles: profileForms.map(({ name, provider, model, maxOutputTokens, supportsInputAudio }) => ({ name, provider, model, maxOutputTokens, supportsInputAudio })),
        defaultModel, maxParallelAgents,
      };
      if (!previewMode) models = await Desktop.SaveModelSettings(input);
      else models = { ...models, configured: true, defaultModel };
      modal = null;
      if (workspace && !previewMode) {
        if (selectedSessionID) installWorkspaceState(await Desktop.OpenSession(selectedSessionID));
        else await completeWorkspaceOpen(await Desktop.OpenWorkspace(workspace));
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
    if (workspace && workspaceHasOutstanding) {
      switchTarget = path;
      modal = 'switch';
      return;
    }
    try {
      const result = previewMode ? { workspace: path, sessions: [], permissionMode: 'workspace', worktreeAvailable: true } as WorkspaceOpenResult : await Desktop.OpenWorkspace(path);
      await completeWorkspaceOpen(result);
    } catch (error) { showError(error); }
  }

  async function completeWorkspaceOpen(result: WorkspaceOpenResult) {
    workspace = result.workspace;
    workingDirectory = result.workspace;
    workspaceMode = 'local';
    worktreeAvailable = result.worktreeAvailable ?? false;
    desktopWorktrees = [];
    desktopWorktreesError = '';
    selectedDesktopWorktreePath = '';
    createSessionMenu = false;
    if (result.models) models = result.models;
    sessions = result.sessions ?? [];
    permissionMode = result.permissionMode || 'workspace';
    if (sessions.length > 0) await openSession(sessions[0].id);
    else if (!previewMode) installWorkspaceState(await Desktop.CreateSession('local'));
    else installWorkspaceState({ ...previewState, workspace: result.workspace });
  }

  async function confirmWorkspaceSwitch() {
    busy = true;
    try {
      if (!previewMode) await Desktop.CloseWorkspace(true);
      workspace = '';
      workingDirectory = '';
      workspaceMode = 'local';
      pendingSessions = [];
      pendingSessionsError = '';
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
    if (workspaceHasOutstanding) { modal = 'close'; return; }
    if (!previewMode) {
      try { await Desktop.CloseWorkspace(false); }
      catch (error) { showError(error); return; }
    }
    workspace = '';
    workingDirectory = '';
    workspaceMode = 'local';
    pendingSessions = [];
    pendingSessionsError = '';
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
      workingDirectory = '';
      workspaceMode = 'local';
      pendingSessions = [];
      pendingSessionsError = '';
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
      installWorkspaceState(previewVariant === 'pending-center' ? previewPendingWorkspaceState(id) : previewState);
      return;
    }
    try { installWorkspaceState(await Desktop.OpenSession(id)); }
    catch (error) { showError(error); }
  }

  async function openPendingSession(id: string) {
    sidebarView = 'sessions';
    await openSession(id);
  }

  async function createSession(mode: 'local' | 'worktree' = 'local') {
    createSessionMenu = false;
    if (previewMode) {
      const path = 'D:\\Git\\sample-project';
      installWorkspaceState({
        ...previewState,
        projectRoot: previewState.workspace,
        workspaceMode: mode,
        workingDirectory: mode === 'worktree' ? `${path}\\.worktrees\\preview` : previewState.workspace,
        worktreePath: mode === 'worktree' ? `${path}\\.worktrees\\preview` : undefined,
      });
      return;
    }
    try { installWorkspaceState(await Desktop.CreateSession(mode)); }
    catch (error) { showError(error); }
  }

  function toggleCreateSessionMenu() {
    if (!workspace) {
      void openFolderPicker();
      return;
    }
    createSessionMenu = !createSessionMenu;
  }

  function beginRenameSession(item: SessionSummary) {
    sessionActionTarget = item;
    sessionTitleDraft = item.title;
    modal = 'rename-session';
  }

  function beginDeleteSession(item: SessionSummary) {
    sessionActionTarget = item;
    modal = 'delete-session';
  }

  async function saveSessionTitle() {
    const target = sessionActionTarget;
    if (!target || busy) return;
    const title = sessionTitleDraft.trim();
    busy = true;
    try {
      if (!previewMode) await Desktop.SetSessionTitle(target.id, title);
      sessions = sessions.map((item) => item.id === target.id ? { ...item, title } : item);
      if (session?.id === target.id) session = { ...session, title };
      modal = null;
      sessionActionTarget = null;
    } catch (error) { showError(error); }
    finally { busy = false; }
  }

  async function confirmDeleteSession() {
    const target = sessionActionTarget;
    if (!target || busy) return;
    busy = true;
    try {
      if (previewMode) {
        sessions = sessions.filter((item) => item.id !== target.id);
      } else {
        if (session?.id === target.id) {
          const fallback = sessions.find((item) => item.id !== target.id);
          installWorkspaceState(fallback ? await Desktop.OpenSession(fallback.id) : await Desktop.CreateSession('local'));
        }
        await Desktop.DeleteSession(target.id);
        sessions = sessions.filter((item) => item.id !== target.id);
        await refreshSession();
      }
      modal = null;
      sessionActionTarget = null;
    } catch (error) { showError(error); }
    finally { busy = false; }
  }

  async function openMCPManager() {
    if (!workspace) return;
    if (previewMode) {
      mcpServers = [];
      workspaceSettingsForm = {
        contextWindow: 0, autoCompact: true, autoCompactThreshold: 0.8, reserveOutputTokens: 0,
        maxAttachmentFiles: 0, maxAttachmentBytes: 0, maxTotalBytes: 0, mcpServers: [],
      };
      modal = 'mcp';
      return;
    }
    try {
      workspaceSettingsForm = workspaceSettingsFromView(await Desktop.GetWorkspaceConfig());
      mcpServers = await Desktop.ListMCPServers() ?? [];
      modal = 'mcp';
    } catch (error) { showError(error); }
  }

  async function setMCPServerEnabled(server: MCPServerView, enabled: boolean) {
    if (workOutstanding || pending.length || mcpUpdatingServer) return;
    mcpUpdatingServer = server.name;
    try {
      if (!previewMode) {
        await Desktop.SetMCPServerEnabled(server.name, enabled);
        mcpServers = await Desktop.ListMCPServers() ?? [];
      } else {
        mcpServers = mcpServers.map((item) => item.name === server.name ? { ...item, enabled, connected: enabled } : item);
      }
    } catch (error) { showError(error); }
    finally { mcpUpdatingServer = ''; }
  }

  async function authenticateMCPServer(server: MCPServerView) {
    if (workOutstanding || pending.length || mcpUpdatingServer) return;
    mcpUpdatingServer = server.name;
    try {
      if (!previewMode) {
        await Desktop.AuthenticateMCPServer(server.name);
        mcpServers = await Desktop.ListMCPServers() ?? [];
      }
    } catch (error) { showError(error); }
    finally { mcpUpdatingServer = ''; }
  }

  async function openWorkspaceFile(path: string) {
    if (previewMode) {
      filePreview = { path, content: `// ${path}\n// 文件预览示例`, truncated: false };
      modal = 'file-preview';
      return;
    }
    try {
      filePreview = await Desktop.ReadWorkspaceFile(path);
      modal = 'file-preview';
    } catch (error) { showError(error); }
  }

  async function openFileInDefaultApp() {
    if (!filePreview || previewMode) return;
    try { await Desktop.OpenWorkspaceFile(filePreview.path); }
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
    if (editDraft) {
      requestEditConfirmation();
      return;
    }
    const text = composerText.trim();
    const interactivePreview = ['send', 'send-failure', 'interrupt', 'queue', 'queue-edit', 'edit'].includes(previewVariant);
    if ((!text && attachments.length === 0) || !canSend || (previewMode && !interactivePreview)) return;
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
    await submitUserMessage(id, request);
  }

  async function submitUserMessage(messageID: string, request: SubmitInput) {
    busy = true;
    if (!running && !activityHistory.some((run) => run.status === 'queued')) {
      executionState = 'submitting';
      currentStatus = '正在提交请求';
    }
    await tick();
    scrollToLatest();
    try {
      const submission = previewMode ? await submitPreviewRequest(request) : await Desktop.Submit(request);
      const queued = submission.Status === 'queued';
      const queuedPosition = queued ? messages.filter((message) => message.deliveryState === 'queued').length + 1 : undefined;
      updateMessage(messageID, {
        deliveryState: queued ? 'queued' : 'submitted', deliveryError: undefined,
        executionID: submission.ExecutionID, queuePosition: queuedPosition,
      });
      const localMessage = messages.find((message) => message.id === messageID);
      upsertActivityRun({
        key: submission.ExecutionID, executionId: submission.ExecutionID,
        userMessageIndex: localMessage?.userMessageIndex ?? request.userMessageIndex ?? -1,
        status: queued ? 'queued' : 'running',
        statusLabel: queued ? `排队中 · 第 ${queuedPosition} 位` : '正在等待模型响应',
        queuePosition: queuedPosition,
      });
      if (!queued && !running && !executionID) {
        executionID = submission.ExecutionID;
        activeActivityRunKey = submission.ExecutionID;
      }
      if (queued && !running && !executionID) currentStatus = '任务排队中';
    } catch (error) {
      const reason = error instanceof Error ? error.message : String(error);
      updateMessage(messageID, { deliveryState: 'failed', deliveryError: reason });
      if (!running) {
        executionState = 'failed';
        currentStatus = '消息发送失败';
        stopElapsedTimer();
      }
    }
    finally { busy = false; }
  }

  async function submitPreviewRequest(_request: SubmitInput) {
    await new Promise((resolve) => window.setTimeout(resolve, 650));
    if (previewVariant === 'send-failure' && !previewFailedOnce) {
      previewFailedOnce = true;
      throw new Error('模拟提交失败，请重试');
    }

    if (previewVariant === 'queue' || previewVariant === 'queue-edit') {
      const queuedExecutionID = 'exec-preview-queued';
      onAgentEvent({ kind: 'task_queued', sessionId: previewState.session.id, taskId: 'task-preview-queued', executionId: queuedExecutionID, status: 'queued' });
      window.setTimeout(() => {
        onAgentEvent({ kind: 'task_finished', sessionId: previewState.session.id, taskId: 'task-preview-running', executionId: 'exec-preview-running', status: 'completed' });
        onTurnResult({ sessionId: previewState.session.id, executionId: 'exec-preview-running', status: 'completed', text: '第一条消息已完成。' });
        onAgentEvent({ kind: 'task_started', sessionId: previewState.session.id, taskId: 'task-preview-queued', executionId: queuedExecutionID, status: 'running' });
        window.setTimeout(() => {
          onAgentEvent({ kind: 'model_stream', sessionId: previewState.session.id, taskId: 'task-preview-queued', executionId: queuedExecutionID, status: 'running', stream: { type: 'text_delta', delta: '队列消息已执行。' } });
          onAgentEvent({ kind: 'task_finished', sessionId: previewState.session.id, taskId: 'task-preview-queued', executionId: queuedExecutionID, status: 'completed' });
          onTurnResult({ sessionId: previewState.session.id, executionId: queuedExecutionID, status: 'completed', text: '队列消息已执行。' });
        }, 550);
      }, previewVariant === 'queue-edit' ? 4500 : 700);
      return { Status: 'queued', QueueID: 'queue-preview', ExecutionID: queuedExecutionID, Position: 1 };
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
    return { Status: 'accepted', QueueID: '', ExecutionID: previewExecutionID, Position: 0 };
  }

  function startEditingMessage(message: LocalMessage, userMessageIndex: number) {
    if (!canEditMessages || message.role !== 'user' || userMessageIndex < 0 || message.deliveryState === 'sending') return;
    editDraft = {
      messageID: message.id, targetID: message.canonicalID || message.executionID || message.id,
      userMessageIndex, wasQueued: message.deliveryState === 'queued',
      previousText: composerText, previousAttachments: attachments,
    };
    composerText = message.text;
    attachments = (message.images ?? []).flatMap((preview, index) => {
      const match = /^data:(image\/(?:png|jpeg|gif|webp));base64,(.+)$/i.exec(preview);
      return match ? [{ name: `image-${index + 1}`, mediaType: match[1].toLowerCase(), data: match[2], preview }] : [];
    });
    scrollToLatest(true);
  }

  function cancelEditingMessage() {
    if (editDraft) {
      composerText = editDraft.previousText;
      attachments = editDraft.previousAttachments;
    }
    editDraft = null;
  }

  function requestEditConfirmation() {
    if (!editDraft || !canWrite) return;
    const text = composerText.trim();
    if (!text && attachments.length === 0) { showError('请输入内容或保留至少一张图片'); return; }
    const request: SubmitInput = {
      text,
      images: attachments.map(({ name, mediaType, data }) => ({ name, mediaType, data })),
      userMessageIndex: editDraft.userMessageIndex,
    };
    editConfirmation = {
      draft: editDraft, request,
      pendingAtConfirm: workOutstanding,
    };
    modal = 'edit';
  }

  function confirmMessageEdit() {
    if (!editConfirmation) return;
    const edit = editConfirmation;
    editConfirmation = null;
    modal = null;
    composerText = '';
    attachments = [];
    editDraft = null;
    void applyUserMessageEdit(edit);
  }

  async function applyUserMessageEdit(edit: EditConfirmation) {
    if (applyingEdit) return;
    const targetIndex = messages.findIndex((message) => message.id === edit.draft.messageID);
    if (targetIndex < 0) { showError('要编辑的消息已从当前会话中移除'); return; }
    const message = messages[targetIndex];
    if (message.role !== 'user') { showError('只能编辑用户消息'); return; }
    if (message.deliveryState === 'failed') { showError('这条消息执行失败，请先重试后再编辑历史消息'); return; }
    applyingEdit = true;
    busy = true;
    try {
      const request = { ...edit.request, userMessageIndex: edit.draft.userMessageIndex };
      if (previewMode && edit.pendingAtConfirm && !edit.draft.wasQueued) {
        for (const run of activityHistory) {
          if (run.status === 'running' || run.status === 'queued') cancelledPreviewExecutions.add(run.executionId);
        }
      }
      const submission = previewMode
        ? edit.draft.wasQueued
          ? { Status: 'queued', QueueID: 'queue-preview-edit', ExecutionID: edit.draft.targetID, Position: message.queuePosition ?? 1 }
          : { Status: 'accepted', QueueID: '', ExecutionID: `exec-preview-edit-${Date.now()}`, Position: 0 }
        : await Desktop.EditUserMessage(edit.draft.targetID, request);
      const queued = submission.Status === 'queued';
      const queuedPosition = queued ? messages.slice(0, targetIndex).filter((item) => item.deliveryState === 'queued').length + 1 : undefined;
      const edited: LocalMessage = queued
        ? {
            ...message, text: request.text, images: attachmentsForRequest(request), local: true,
            deliveryState: 'queued', deliveryError: undefined, request,
            userMessageIndex: edit.draft.userMessageIndex, executionID: submission.ExecutionID,
            queuePosition: queuedPosition,
          }
        : {
            id: `user-${crypto.randomUUID()}`, role: 'user', text: request.text,
            images: attachmentsForRequest(request), local: true, deliveryState: 'submitted',
            request, userMessageIndex: edit.draft.userMessageIndex, executionID: submission.ExecutionID,
          };
      messages = [...messages.slice(0, targetIndex), edited];
      activityHistory = activityHistory.filter((run) => run.userMessageIndex < edit.draft.userMessageIndex || run.executionId === submission.ExecutionID);
      if (!queued) {
        planReview = null;
        deferredPlanReviews = [];
        streamText = '';
        running = true;
        executionID = submission.ExecutionID;
        activeActivityRunKey = submission.ExecutionID;
        executionState = 'running';
        currentStatus = '正在等待模型响应';
        startElapsedTimer(true);
      }
      upsertActivityRun({
        key: submission.ExecutionID, executionId: submission.ExecutionID,
        userMessageIndex: edit.draft.userMessageIndex,
        status: queued ? 'queued' : 'running',
        statusLabel: queued ? `排队中 · 第 ${queuedPosition} 位` : '正在等待模型响应',
        queuePosition: queuedPosition,
      });
      if (previewMode && !queued) {
        onAgentEvent({ kind: 'task_started', sessionId: previewState.session.id, taskId: 'task-preview-edit', executionId: submission.ExecutionID, status: 'running' });
        window.setTimeout(() => onTurnResult({ sessionId: previewState.session.id, executionId: submission.ExecutionID, status: 'completed', text: '修改后的回复。' }), 450);
      }
      scrollToLatest(true);
      void refreshSession();
    } catch (error) {
      showError(error);
      if (!previewMode) void refreshSession();
    } finally {
      busy = false;
      applyingEdit = false;
    }
  }

  function attachmentsForRequest(request: SubmitInput) {
    return (request.images ?? []).map((image) => `data:${image.mediaType};base64,${image.data}`);
  }

  async function retryMessage(message: LocalMessage) {
    if (!message.request || message.deliveryState !== 'failed' || busy || pending.length > 0 || planReview) return;
    updateMessage(message.id, { deliveryState: 'sending', deliveryError: undefined });
    await submitUserMessage(message.id, message.request);
  }

  async function cancelQueuedMessage(message: LocalMessage) {
    const targetID = message.executionID;
    if (!targetID || message.deliveryState !== 'queued' || cancellingQueuedExecution) return;
    cancellingQueuedExecution = targetID;
    try {
      if (previewMode) cancelledPreviewExecutions.add(targetID);
      else await Desktop.CancelQueuedTurn(targetID);
      updateMessage(message.id, { deliveryState: 'cancelled', queuePosition: undefined, request: undefined });
      upsertActivityRun({
        key: targetID, executionId: targetID, status: 'cancelled', statusLabel: '排队消息已撤回',
        userMessageIndex: message.userMessageIndex ?? -1, queuePosition: undefined, finishedAt: new Date().toISOString(),
      });
      await refreshSession();
    } catch (error) {
      showError(error);
      await refreshSession();
    } finally { cancellingQueuedExecution = ''; }
  }

  async function interruptTask() {
    if (interrupting) return;
    const targetExecutionID = executionID;
    const partialTextAtInterrupt = streamText.trim();
    interrupting = true;
    interruptRequestedExecutionID = targetExecutionID || activeActivityRunKey;
    setExecutionStatus('running', '正在中断任务');
    try {
      const interrupted = previewMode ? true : await Desktop.Interrupt();
      if (!interrupted) {
        showError('无法完成中断，正在同步当前任务状态');
        await refreshSession();
        return;
      }
      if (executionID === targetExecutionID) {
        running = false;
        stopElapsedTimer();
        finishExecution('interrupted');
      }
      if (partialTextAtInterrupt) {
        const partialID = `assistant-${targetExecutionID}-interrupted`;
        if (!messages.some((message) => message.id === partialID)) {
          const run = activityHistory.find((item) => item.executionId === targetExecutionID);
          insertAssistantForUser(run?.userMessageIndex ?? -1, { id: partialID, role: 'assistant', text: partialTextAtInterrupt, local: true, deliveryState: 'submitted', executionID: targetExecutionID, interrupted: true });
        }
        if (executionID === targetExecutionID) streamText = '';
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
      void refreshPendingSessions();
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
      void refreshPendingSessions();
    } catch (error) { showError(error); }
  }

  async function resolveUserInteraction(request: PendingRequestView, response: ElicitationResponse) {
    try {
      if (!previewMode) await Desktop.ResolveUserInteraction(request.id, JSON.stringify(response));
      pending = pending.filter((item) => item.id !== request.id);
      removePreviewPendingSummary(request);
      resumeExecution('MCP 响应已提交，正在继续处理');
      void refreshPendingSessions();
    } catch (error) { showError(error); }
  }

  async function openMCPInteractionURL(request: PendingRequestView, target: string) {
    if (previewMode) return;
    await Desktop.OpenMCPURL(request.tool, target);
  }

  async function recover(request: PendingRequestView) {
    try {
      if (request.kind === 'model_recovery' || request.kind === 'execution_blocker') await Desktop.RecoverModel();
      else await Desktop.ResolveToolRecovery(request.invocationId, 'retry');
      pending = pending.filter((item) => item.id !== request.id);
      resumeExecution('正在恢复任务');
      void refreshPendingSessions();
    } catch (error) { showError(error); }
  }

  async function dismissRecovery(request: PendingRequestView) {
    try {
      if (request.kind === 'tool_recovery') await Desktop.ResolveToolRecovery(request.invocationId, 'skip');
      else if (!previewMode && !await Desktop.Interrupt()) throw new Error('无法丢弃当前暂停的任务');
      pending = pending.filter((item) => item.id !== request.id);
      if (request.kind === 'tool_recovery') resumeExecution('正在跳过失败步骤');
      else finishExecution('interrupted');
      void refreshPendingSessions();
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
        sessions = [{ id: result.sessionId, title: '实施方案', updatedAt: new Date().toISOString(), mode: workspaceMode, workingDirectory, status: 'running' }, ...sessions];
        await openSession(result.sessionId);
      }
      running = true;
      executionID = result.executionId;
    } catch (error) { showError(error); }
    finally { busy = false; }
  }

  function stayInPlan() {
    planReview = null;
    presentDeferredPlanReview();
  }

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

      <button class="new-chat" onclick={toggleCreateSessionMenu} aria-haspopup="menu" aria-expanded={createSessionMenu}>
        <span class="plus">＋</span>{workspace ? '新建会话' : '打开工作区'}<kbd>⌘ K</kbd>
      </button>
      {#if workspace && createSessionMenu}
        <div class="session-mode-menu" role="menu" aria-label="选择会话工作区模式">
          <button role="menuitem" onclick={() => void createSession('local')}>
            <strong>当前工作区</strong><span>Local · 直接使用当前目录</span>
          </button>
          <button role="menuitem" disabled={!worktreeAvailable} title={worktreeAvailable ? '从当前已提交 HEAD 创建独立工作树' : 'Worktree 模式需要 Git 仓库和 Git 命令'} onclick={() => void createSession('worktree')}>
            <strong>隔离工作树</strong><span>{worktreeAvailable ? 'Worktree · 从当前 HEAD 创建' : '需要 Git 仓库'}</span>
          </button>
        </div>
      {/if}
      <div class="sidebar-section"><span>工作区</span><button class="tiny-add" aria-label="打开工作区" onclick={openFolderPicker}>＋</button></div>
      {#if workspace}
        <button class="workspace-row active" title={workspace} onclick={() => void closeWorkspace()}>
          <span class="folder-icon">▰</span><span class="workspace-name">{workspace.split(/[\\/]/).at(-1) || workspace}</span><span class="workspace-menu">⌄</span>
        </button>
        <div class="sidebar-view-tabs">
          <button class:active={sidebarView === 'sessions'} onclick={() => sidebarView = 'sessions'}>会话</button>
          <button class:active={sidebarView === 'files'} onclick={() => sidebarView = 'files'}>文件</button>
          <button class:active={sidebarView === 'changes'} onclick={() => { sidebarView = 'changes'; void refreshWorkspaceChanges(); }}>变更</button>
          <button class:active={sidebarView === 'worktrees'} onclick={() => { sidebarView = 'worktrees'; void refreshDesktopWorktrees(); }}>工作树</button>
          <button class:active={sidebarView === 'pending'} aria-label={`待处理${pendingRequestCount ? `，${pendingRequestCount} 项` : ''}`} onclick={() => { sidebarView = 'pending'; void refreshPendingSessions(); }}>待处理{#if pendingRequestCount}<span class="pending-count-badge">{pendingRequestCount}</span>{/if}</button>
          {#if sidebarView === 'sessions'}<button class="tiny-add" aria-label="新建会话" onclick={toggleCreateSessionMenu}>＋</button>{/if}
        </div>
        {#if sidebarView === 'sessions'}
          <input class="session-search" bind:value={sessionFilter} placeholder="搜索会话" aria-label="搜索会话" />
          <div class="session-list">
            {#each visibleSessions as item (item.id)}
              <div class:chosen={session?.id === item.id} class="session-entry">
                <button class="session-row" title={`${item.title || '新会话'}${item.mode === 'worktree' ? ` · ${item.workingDirectory || item.worktreePath || ''}` : ''}`} onclick={() => void openSession(item.id)}>
                  <span class="session-dot">{session?.id === item.id ? '●' : '○'}</span><span class="session-name">{item.title || '新会话'}</span>
                  <span class={`session-mode-badge ${item.mode === 'worktree' ? 'worktree' : ''}`}>{item.mode === 'worktree' ? 'WT' : 'LOCAL'}</span>
                  {#if ['running', 'queued', 'waiting'].includes(item.status ?? '')}<span class={`session-activity ${item.status}`} title={item.status === 'waiting' ? '等待处理' : item.status === 'queued' ? '排队中' : '正在运行'}></span>{/if}
                </button>
                <div class="session-row-actions">
                  <button aria-label={`重命名会话 ${item.title || '新会话'}`} title="重命名" disabled={busy} onclick={(event) => { event.stopPropagation(); beginRenameSession(item); }}>✎</button>
                  <button aria-label={`删除会话 ${item.title || '新会话'}`} title="删除" disabled={busy || ['running', 'queued', 'waiting'].includes(item.status ?? '')} onclick={(event) => { event.stopPropagation(); beginDeleteSession(item); }}>×</button>
                </div>
              </div>
            {/each}
            {#if !visibleSessions.length}<div class="session-search-empty">没有匹配的会话</div>{/if}
          </div>
        {:else if sidebarView === 'pending'}
          <section class="pending-session-list" aria-label="待处理会话">
            <div class="pending-list-summary">{pendingRequestCount} 项待处理</div>
            {#if pendingSessionsLoading && !pendingSessions.length}<div class="pending-list-state"><span class="spinner"></span>正在读取其他会话…</div>
            {:else if pendingSessionsError}<div class="pending-list-state error">{pendingSessionsError}<button class="small-button" onclick={() => void refreshPendingSessions()}>重试</button></div>
            {:else if !pendingSessions.length}<div class="pending-list-state">当前工作区没有需要处理的会话。</div>
            {:else}
              {#each pendingSessions as item (item.id)}
                <button class:chosen={session?.id === item.id} class="pending-session-row" aria-label={`${item.title || '新会话'}，${item.requests.length} 项待处理`} title={`${item.title || '新会话'} · ${item.workingDirectory}`} onclick={() => void openPendingSession(item.id)}>
                  <span class="pending-session-count">{item.requests.length}</span>
                  <span class="pending-session-main">
                    <strong>{item.title || '新会话'}</strong>
                    <small>{item.mode === 'worktree' ? 'Worktree' : 'Local'} · {item.workingDirectory.split(/[\\/]/).at(-1) || item.workingDirectory}</small>
                    <span class="pending-session-kinds">{#each item.requests as request, index (index)}<i>{pendingKindLabel(request.kind)}{request.tool ? ` · ${request.tool}` : ''}</i>{/each}</span>
                  </span>
                  <span class="pending-session-arrow">›</span>
                </button>
              {/each}
            {/if}
          </section>
        {:else if sidebarView === 'worktrees'}
          <section class="desktop-worktree-sidebar" aria-label="Desktop Worktree 管理">
            <div class="desktop-worktree-sidebar-heading"><span>当前项目的 Desktop 工作树</span><button class="workspace-select-all" aria-label="刷新工作树列表" disabled={desktopWorktreesLoading} onclick={() => void refreshDesktopWorktrees()}>{#if desktopWorktreesLoading}<span class="spinner"></span>{:else}↻{/if}</button></div>
            {#if desktopWorktreesLoading && !desktopWorktrees.length}<div class="workspace-change-empty"><span class="spinner"></span>正在读取 Worktree…</div>
            {:else if desktopWorktreesError}<div class="workspace-change-empty error">{desktopWorktreesError}<button class="small-button" onclick={() => void refreshDesktopWorktrees()}>重试</button></div>
            {:else if !desktopWorktrees.length}<div class="workspace-change-empty">当前项目没有 Desktop 管理的 Git Worktree。</div>
            {:else}
              <div class="desktop-worktree-sidebar-list">
                {#each desktopWorktrees as worktree (worktree.path)}
                  <button class:chosen={selectedDesktopWorktreePath === worktree.path} class="desktop-worktree-sidebar-row" title={worktree.path} onclick={() => selectedDesktopWorktreePath = worktree.path}>
                    <span class="desktop-worktree-sidebar-name">{desktopWorktreeName(worktree)}<small>{worktree.orphaned ? '孤儿' : worktree.sessions.length ? `${worktree.sessions.length} 个会话` : worktree.gitRegistered ? 'Git 工作树' : '未登记目录'}</small></span>
                    <span class:dirty={worktree.gitStatus === 'dirty'} class:unavailable={!worktree.available || !worktree.gitRegistered} class="desktop-worktree-status">{desktopWorktreeStatusLabel(worktree)}</span>
                  </button>
                {/each}
              </div>
            {/if}
          </section>
        {:else if sidebarView === 'changes'}
          <section class="workspace-changes-sidebar" aria-label="工作区变更文件">
            {#if workspaceChangesLoading && !workspaceChanges}<div class="workspace-change-empty"><span class="spinner"></span>正在读取 Git 状态…</div>
            {:else if workspaceChangesError}<div class="workspace-change-empty error">{workspaceChangesError}</div>
            {:else if workspaceChanges && !workspaceChanges.available}<div class="workspace-change-empty">{workspaceChanges.message}</div>
            {:else if workspaceChanges?.files.length === 0}<div class="workspace-change-empty">当前工作区没有相对 HEAD 的变更。</div>
            {:else if workspaceChanges}
              <div class="workspace-change-sidebar-summary">
                <span>{workspaceChanges.files.length} 个文件 · +{workspaceChanges.added} −{workspaceChanges.removed}</span>
                {#if workspaceMode === 'worktree'}<button class="workspace-select-all" onclick={toggleAllApplyPaths}>{selectedApplyPaths.length === workspaceChanges.files.length ? '清空' : '全选'}</button>{/if}
              </div>
              <div class="workspace-change-file-list">
                {#each workspaceChanges.files as file (file.path)}
                  <div class="workspace-change-row-wrap">
                    {#if workspaceMode === 'worktree'}<input class="workspace-change-select" type="checkbox" checked={selectedApplyPaths.includes(file.path)} aria-label={`选择 ${file.path} 应用到原项目`} onchange={() => toggleApplyPath(file.path)} />{/if}
                    <button class:chosen={selectedWorkspaceChange === file.path} class="workspace-change-row" title={`${file.path} · ${changeStatusDescription(file.status)}`} aria-label={`${file.path}，${changeStatusDescription(file.status)}`} onclick={() => void loadWorkspaceDiff(file.path)}>
                      <span class={`workspace-change-status ${changeStatusLabel(file.status).toLowerCase()}`}>{changeStatusLabel(file.status)}</span>
                      <span class="workspace-change-path">{file.path}</span>
                      <span class="workspace-change-stats"><i>+{file.added}</i><i>−{file.removed}</i></span>
                    </button>
                  </div>
                {/each}
              </div>
            {/if}
          </section>
        {:else}
          {#key workingDirectory}<WorkspaceExplorer previewMode={previewMode} onPreviewFile={openWorkspaceFile} onError={showError} />{/key}
        {/if}
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
        {#if workspace}<button class="settings-entry" onclick={() => void openMCPManager()}><span>⎇</span> 项目设置</button>{/if}
        <button class="settings-entry" onclick={setSettingsForm}><span>⚙</span> 模型与偏好设置 <span class="settings-shortcut">⌘ ,</span></button>
      </div>
    </aside>

    <main class="main-panel">
      <header class="topbar">
        <div class="crumbs">
          {#if workspace}<span class="crumb-root">{workspace.split(/[\\/]/).at(-1)}</span><span class="crumb-separator">/</span><span class="crumb-current">{sidebarView === 'changes' ? '变更审查' : sidebarView === 'worktrees' ? '工作树管理' : sidebarView === 'pending' ? '待处理' : session?.title || '新会话'}</span>{#if sidebarView !== 'changes' && sidebarView !== 'worktrees' && sidebarView !== 'pending'}<span class={`crumb-mode ${workspaceMode === 'worktree' ? 'worktree' : ''}`}>{workspaceMode === 'worktree' ? 'WORKTREE' : 'LOCAL'}</span>{#if workspaceMode === 'worktree'}<span class="crumb-working-path" title={workingDirectory}>{workingDirectory}</span>{/if}{/if}{:else}<span class="crumb-current">桌面工作区</span>{/if}
        </div>
        <div class="top-actions">
          {#if workspace}
            <button class="subtle-button" onclick={() => void closeWorkspace()} title="关闭工作区">关闭工作区</button>
            <span class="top-divider"></span>
            <label class="permission-select"><span>权限</span><select value={permissionMode} disabled={planMode || workOutstanding} onchange={(event) => void setPermission(event.currentTarget.value)}>
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
      {:else if sidebarView === 'worktrees'}
        <section class="workspace-changes-page desktop-worktrees-page" aria-label="Desktop 工作树管理">
          <header class="workspace-changes-heading">
            <div>
              <p class="eyebrow">DESKTOP · GIT WORKTREES</p>
              <h1>工作树管理</h1>
              <p>只显示当前项目由 Desktop 管理的工作树。清理前会再次检查 Git 状态，忽略文件也会阻止清理。</p>
            </div>
            <div class="workspace-changes-actions">
              <button class="subtle-button workspace-change-refresh" aria-label="刷新工作树列表" disabled={desktopWorktreesLoading} onclick={() => void refreshDesktopWorktrees()}>
                {#if desktopWorktreesLoading}<span class="spinner"></span>{:else}<span>↻</span>{/if}刷新
              </button>
            </div>
          </header>

          {#if desktopWorktreesError}
            <div class="workspace-changes-state error"><strong>读取工作树失败</strong><span>{desktopWorktreesError}</span><button class="small-button" onclick={() => void refreshDesktopWorktrees()}>重试</button></div>
          {:else if desktopWorktreesLoading && !desktopWorktrees.length}
            <div class="workspace-changes-state"><span class="spinner"></span><span>正在读取 Git Worktree 列表…</span></div>
          {:else if !desktopWorktrees.length}
            <div class="workspace-changes-state"><strong>没有 Desktop 工作树</strong><span>此项目当前没有由 Desktop 创建的 Git Worktree。</span></div>
          {:else}
            <div class="desktop-worktree-card-list">
              {#each desktopWorktrees as worktree (worktree.path)}
                <article class:chosen={selectedDesktopWorktreePath === worktree.path} class="desktop-worktree-card">
                  <header class="desktop-worktree-card-heading">
                    <div class="desktop-worktree-card-title">
                      <div class="desktop-worktree-card-badges"><span class:orphan={worktree.orphaned} class:unavailable={!worktree.gitRegistered || !worktree.available} class="desktop-worktree-kind">{worktree.orphaned ? '孤儿' : worktree.gitRegistered ? '关联会话' : '未登记目录'}</span><span class:dirty={worktree.gitStatus === 'dirty'} class:unavailable={worktree.gitStatus === 'unavailable'} class="desktop-worktree-status">{desktopWorktreeStatusLabel(worktree)}</span></div>
                      <h2>{desktopWorktreeName(worktree)}</h2>
                      <code title={worktree.path}>{worktree.path}</code>
                    </div>
                    <button class="danger-button desktop-worktree-remove" disabled={!worktree.canRemove || busy} title={worktree.blockReason || ''} onclick={() => beginDesktopWorktreeRemoval(worktree)}>{worktree.orphaned ? '清理孤儿工作树' : worktree.sessions.length ? `删除 ${worktree.sessions.length} 个会话并清理` : '清理工作树'}</button>
                  </header>
                  {#if worktree.sessions.length}
                    <div class="desktop-worktree-sessions">
                      <strong>关联会话 · {worktree.sessions.length}</strong>
                      {#each worktree.sessions as associated (associated.id)}<div><span>{associated.title || '新会话'}</span><small>{desktopWorktreeSessionStatusLabel(associated.status)}</small></div>{/each}
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
      {:else if !session}
        <section class="workspace-loading"><div class="spinner"></div><span>正在载入会话…</span></section>
      {:else if sidebarView === 'changes'}
        <section class="workspace-changes-page" aria-label="工作区变更审查">
          <header class="workspace-changes-heading">
            <div>
              <p class="eyebrow">GIT · 相对 HEAD</p>
              <h1>工作区变更</h1>
              <p>{workspaceMode === 'worktree' ? '查看隔离工作树中的改动，并选择要写回原项目的文件。' : '查看当前会话目录中的暂存、未暂存与未跟踪文件。'}</p>
            </div>
            <div class="workspace-changes-actions">
              {#if workspaceMode === 'worktree'}<button class="primary-button workspace-change-apply" disabled={!canApplyWorktreeChanges} title={worktreeApplyBlockedReason} onclick={beginWorktreeApply}>应用所选文件{#if selectedApplyPaths.length} · {selectedApplyPaths.length}{/if}</button>{/if}
              <button class="subtle-button workspace-change-refresh" aria-label="刷新工作区变更" disabled={workspaceChangesLoading} onclick={() => void refreshWorkspaceChanges()}>
                {#if workspaceChangesLoading}<span class="spinner"></span>{:else}<span>↻</span>{/if}刷新
              </button>
            </div>
          </header>

          {#if worktreeApplyMessage}<div class="workspace-apply-message" role="status">{worktreeApplyMessage}</div>{/if}

          {#if workspaceChangesError}
            <div class="workspace-changes-state error"><strong>读取变更失败</strong><span>{workspaceChangesError}</span><button class="small-button" onclick={() => void refreshWorkspaceChanges()}>重试</button></div>
          {:else if workspaceChangesLoading && !workspaceChanges}
            <div class="workspace-changes-state"><span class="spinner"></span><span>正在读取 Git 工作区状态…</span></div>
          {:else if workspaceChanges && !workspaceChanges.available}
            <div class="workspace-changes-state"><strong>无法读取 Git 变更</strong><span>{workspaceChanges.message}</span></div>
          {:else if workspaceChanges?.files.length === 0}
            <div class="workspace-changes-state"><strong>工作区干净</strong><span>当前会话目录没有相对 HEAD 的文件变更。</span></div>
          {:else if workspaceChanges}
            <section class="workspace-diff-panel">
              <header class="workspace-diff-heading">
                <div class="workspace-diff-path-group">
                  {#if activeWorkspaceChange}<span class={`workspace-change-status ${changeStatusLabel(activeWorkspaceChange.status).toLowerCase()}`}>{changeStatusLabel(activeWorkspaceChange.status)}</span>{/if}
                  <strong title={activeWorkspaceChange?.path}>{activeWorkspaceChange?.path || '选择一个文件'}</strong>
                  {#if activeWorkspaceChange?.originalPath}<span class="workspace-diff-original">来自 {activeWorkspaceChange.originalPath}</span>{/if}
                </div>
                {#if activeWorkspaceChange}<span class="workspace-diff-stats"><i>+{activeWorkspaceChange.added}</i><i>−{activeWorkspaceChange.removed}</i><small>{changeStatusDescription(activeWorkspaceChange.status)}</small></span>{/if}
              </header>
              <div class="workspace-diff-scroll">
                {#if workspaceDiffLoading}<div class="workspace-diff-state"><span class="spinner"></span>正在读取文件差异…</div>
                {:else if workspaceDiffError}<div class="workspace-diff-state error">{workspaceDiffError}<button class="small-button" onclick={() => activeWorkspaceChange && void loadWorkspaceDiff(activeWorkspaceChange.path)}>重试</button></div>
                {:else if !activeWorkspaceChange}<div class="workspace-diff-state">从左侧选择一个文件以查看差异。</div>
                {:else if workspaceDiff?.binary}<div class="workspace-diff-state"><strong>二进制文件</strong><span>此文件无法作为文本差异显示。</span></div>
                {:else if workspaceDiff}
                  <pre class="workspace-diff-code" aria-label="Unified diff">{#each workspaceDiffLines as line, index}<span class:diff-added={line.startsWith('+') && !line.startsWith('+++')} class:diff-removed={line.startsWith('-') && !line.startsWith('---')} class:diff-hunk={line.startsWith('@@')} class:diff-meta={line.startsWith('diff --git') || line.startsWith('index ') || line.startsWith('--- ') || line.startsWith('+++ ') || line.startsWith('new file mode')}>{line || '\u00a0'}</span>{/each}</pre>
                  {#if workspaceDiff.truncated}<div class="workspace-preview-truncated">差异较大，仅显示前 8 MiB。</div>{/if}
                {/if}
              </div>
            </section>
          {/if}
          {#if workspaceChanges?.available && workspaceChanges.files.length > 0}<footer class="workspace-changes-footer">{workspaceChanges.files.length} 个文件 · +{workspaceChanges.added} −{workspaceChanges.removed}{#if workspaceChanges.refreshedAt}<span>刷新于 {new Date(workspaceChanges.refreshedAt).toLocaleTimeString()}</span>{/if}</footer>{/if}
        </section>
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
                  {:else if row.message.deliveryState === 'queued'}
                    <div class="message-delivery-pending">排队中{row.message.queuePosition !== undefined ? ` · 第 ${row.message.queuePosition} 位` : ''}</div>
                  {:else if row.message.deliveryState === 'sending'}
                    <div class="message-delivery-pending">正在发送…</div>
                  {:else if row.message.deliveryState === 'cancelled'}
                    <div class="message-delivery-cancelled">排队消息已撤回</div>
                  {/if}
                  {#if row.message.role === 'user'}
                    <div class="message-actions">
                      {#if row.message.deliveryState === 'queued'}<button class="message-cancel-button" aria-label="撤回排队消息" disabled={cancellingQueuedExecution === row.message.executionID} onclick={() => void cancelQueuedMessage(row.message)}>{cancellingQueuedExecution === row.message.executionID ? '正在撤回…' : '撤回'}</button>{/if}
                      {#if canEditMessages && (row.message.deliveryState === 'queued' || row.message.deliveryState === 'submitted' || row.message.deliveryState === undefined)}<button class="message-edit-button" aria-label="编辑消息" onclick={() => startEditingMessage(row.message, row.userMessageIndex)}>编辑</button>{/if}
                    </div>
                  {/if}
                </div>
              </article>
              {#each row.activityRuns as run (run.key)}
                <ExecutionActivity {run} active={run.executionId === executionID && running} elapsedSeconds={run.executionId === executionID ? elapsedSeconds : run.elapsedSeconds ?? 0} onToggle={() => toggleActivityRun(run.key)} onOpenFile={openWorkspaceFile} />
              {/each}
            {/each}
            {#each orphanActivityRuns as run (run.key)}
              <ExecutionActivity {run} active={run.executionId === executionID && running} elapsedSeconds={run.executionId === executionID ? elapsedSeconds : run.elapsedSeconds ?? 0} onToggle={() => toggleActivityRun(run.key)} onOpenFile={openWorkspaceFile} />
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
                <section class="request-card recovery-card"><strong>{request.kind === 'model_recovery' ? '模型请求未完成' : request.kind === 'execution_blocker' ? '执行受阻' : '工具执行暂停'}</strong><p>{request.reason || request.tool || '此操作需要恢复。'}</p><div class="request-actions"><button class="quiet-button" onclick={() => void dismissRecovery(request)}>{request.kind === 'tool_recovery' ? '跳过失败步骤' : '丢弃本轮'}</button><button class="primary-button" onclick={() => void recover(request)}>{request.kind === 'execution_blocker' ? '恢复执行' : '重试'}</button></div></section>
              {/each}
            {/if}

            {#each userInteractions as request (request.id)}
              <MCPInteractionCard {request} onResolve={(response) => resolveUserInteraction(request, response)} onOpenURL={(target) => openMCPInteractionURL(request, target)} />
            {/each}

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
          {#if editDraft}<div class="edit-mode-banner">正在编辑第 {editDraft.userMessageIndex + 1} 条用户消息。保存后会移除该消息之后的对话并重新运行。</div>{/if}
          {#if attachments.length}
            <div class="attachment-strip">{#each attachments as attachment, index}<div class="attachment-chip"><img src={attachment.preview} alt={attachment.name} /><span>{attachment.name}</span><button aria-label="移除图片" disabled={!canWrite} onclick={() => attachments = attachments.filter((_, i) => i !== index)}>×</button></div>{/each}</div>
          {/if}
          <div class:composer-disabled={!models.configured} class="composer">
            <textarea bind:value={composerText} onkeydown={handleComposerKey} placeholder={editDraft ? '编辑消息…' : !models.configured ? '先配置模型以开始对话' : pending.length ? '请先处理上方的请求' : planReview ? '请先选择方案操作' : '发送消息…'} disabled={!canWrite} rows="1" aria-label="消息输入"></textarea>
            <div class="composer-toolbar">
              <div class="composer-left">
                <button class="composer-tool" title="添加图片" onclick={() => document.getElementById('image-picker')?.click()} disabled={!canWrite}><span>＋</span><span>添加图片</span></button>
                <input id="image-picker" type="file" accept="image/png,image/jpeg,image/gif,image/webp" multiple hidden onchange={(event) => event.currentTarget.files && void addFiles(event.currentTarget.files)} />
                <span class="composer-separator"></span>
                <button class:plan-active={planMode} class="plan-toggle" onclick={() => void setPlanMode(!planMode)} disabled={workOutstanding || !!editDraft}><span>◈</span> Plan 模式</button>
              </div>
              <div class="composer-right">
                {#if models.configured && models.profiles?.length && session}
                  <select class="model-profile-select" value={session.settings.model_profile || models.defaultModel} disabled={workOutstanding} aria-label="会话模型" onchange={(event) => void setSessionModel(event.currentTarget.value)}>
                    {#each models.profiles as profile}<option value={profile.name}>{profile.name}</option>{/each}
                  </select>
                {:else}<span class="model-label">未配置模型</span>{/if}
                {#if running}<button class="stop-button" onclick={() => void interruptTask()} disabled={interrupting} title="中断任务">{interrupting ? '正在中断…' : '■ 中断'}</button>{/if}
                {#if editDraft}
                  <button class="quiet-button" onclick={cancelEditingMessage} disabled={busy}>取消</button>
                  <button class="send-button" onclick={() => void sendMessage()} disabled={!canWrite || (!composerText.trim() && !attachments.length)} aria-label="保存修改">保存</button>
                {:else}
                  <button class="send-button" onclick={() => void sendMessage()} disabled={!canSend || (!composerText.trim() && !attachments.length)} aria-label="发送"><span>↑</span></button>
                {/if}
              </div>
            </div>
          </div>
          <div class="composer-footnote">luneAgent 可能会出错，请检查重要信息。<span>Enter 发送 · Shift+Enter 换行{running || queuedMessageCount > 0 ? ' · 运行中发送将排队' : ''}</span></div>
        </div>
      {/if}
    </main>
  </div>
{/if}

{#if errorMessage}<div class="toast-error" role="status">{errorMessage}<button onclick={() => errorMessage = ''}>×</button></div>{/if}

{#if modal}
  <div class="modal-backdrop" role="presentation" onclick={(event) => { if (event.target === event.currentTarget) { modal = null; editConfirmation = null; worktreeApplyConfirmation = null; worktreeApplyError = ''; worktreeRemovalTarget = null; worktreeRemovalError = ''; } }}>
          {#if modal === 'settings'}
      <div class="modal settings-modal" role="dialog" aria-modal="true" aria-labelledby="settings-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">PREFERENCES</span><h2 id="settings-title">模型与偏好设置</h2></div><div class="modal-header-actions">{#if workspace}<button class="small-button" onclick={() => void openMCPManager()}>项目设置</button>{/if}<button class="icon-button" aria-label="关闭" onclick={() => modal = null}>×</button></div></div>
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
    {:else if modal === 'apply-worktree' && worktreeApplyConfirmation}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="worktree-apply-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">WORKTREE RESULT</span><h2 id="worktree-apply-title">将所选改动应用到原项目？</h2></div><button class="icon-button" aria-label="关闭" onclick={() => { modal = null; worktreeApplyConfirmation = null; worktreeApplyError = ''; }}>×</button></div>
        <div class="modal-body worktree-apply-body">
          <p>目标目录：<code>{worktreeApplyConfirmation.destination}</code></p>
          <p>将应用 {worktreeApplyConfirmation.files.length} 个文件。应用前会检查整批变更；有冲突时不会修改原项目。改动不会暂存或提交，Worktree 会保留。</p>
          {#if permissionMode === 'ask'}<p class="worktree-apply-permission">当前为“每次询问”。确认即授权本次将以下改动写入原项目。</p>{/if}
          <ul class="worktree-apply-file-list">
            {#each worktreeApplyConfirmation.files as file (file.path)}
              <li><span class={`workspace-change-status ${changeStatusLabel(file.status).toLowerCase()}`}>{changeStatusLabel(file.status)}</span><code>{file.path}</code>{#if file.originalPath}<small>来自 {file.originalPath}</small>{/if}</li>
            {/each}
          </ul>
          {#if worktreeApplyError}<div class="worktree-apply-error" role="alert">{worktreeApplyError}</div>{/if}
        </div>
        <div class="modal-footer"><button class="quiet-button" disabled={applyingWorktreeChanges} onclick={() => { modal = null; worktreeApplyConfirmation = null; worktreeApplyError = ''; }}>取消</button><button class="primary-button" disabled={applyingWorktreeChanges} onclick={() => void confirmWorktreeApply()}>{applyingWorktreeChanges ? '正在检查并应用…' : '确认应用'}</button></div>
      </div>
    {:else if modal === 'rename-session' && sessionActionTarget}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="rename-session-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">SESSION</span><h2 id="rename-session-title">重命名会话</h2></div><button class="icon-button" aria-label="关闭" onclick={() => { modal = null; sessionActionTarget = null; }}>×</button></div>
        <div class="modal-body"><label class="session-title-field">会话名称<input bind:value={sessionTitleDraft} maxlength="120" onkeydown={(event) => { if (event.key === 'Enter') void saveSessionTitle(); }} /></label></div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => { modal = null; sessionActionTarget = null; }}>取消</button><button class="primary-button" disabled={busy} onclick={() => void saveSessionTitle()}>{busy ? '正在保存…' : '保存'}</button></div>
      </div>
    {:else if modal === 'delete-session' && sessionActionTarget}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="delete-session-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">SESSION</span><h2 id="delete-session-title">删除会话？</h2></div><button class="icon-button" aria-label="关闭" onclick={() => { modal = null; sessionActionTarget = null; }}>×</button></div>
        <div class="modal-body"><p>“{sessionActionTarget.title || '新会话'}”的对话、执行记录和待运行消息将被删除。此操作无法撤销。Worktree 会话的 Git 工作目录会保留，需自行用 Git 检查和清理。删除当前会话时会自动切换到其他会话；若这是最后一个会话，会先创建一个新会话。</p></div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => { modal = null; sessionActionTarget = null; }}>取消</button><button class="danger-button" disabled={busy} onclick={() => void confirmDeleteSession()}>{busy ? '正在删除…' : '删除会话'}</button></div>
      </div>
    {:else if modal === 'delete-worktree' && worktreeRemovalTarget}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="delete-worktree-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">DESKTOP WORKTREE</span><h2 id="delete-worktree-title">{worktreeRemovalTarget.sessions.length ? '删除关联会话并清理工作树？' : '清理孤儿工作树？'}</h2></div><button class="icon-button" aria-label="关闭" disabled={busy} onclick={() => { modal = null; worktreeRemovalTarget = null; worktreeRemovalError = ''; }}>×</button></div>
        <div class="modal-body desktop-worktree-confirm-body">
          <p>目标路径：<code>{worktreeRemovalTarget.path}</code></p>
          {#if worktreeRemovalTarget.requiresLocalSwitch}<p>当前会话位于此工作树；确认后会先切换到已有 Local 会话，没有时会创建一个。</p>{/if}
          {#if worktreeRemovalTarget.sessions.length}
            <p>此操作会删除该工作树下所有关联会话的对话、执行历史、待运行消息和 Core 状态，再调用 Git 清理工作树：</p>
            <ul>{#each worktreeRemovalTarget.sessions as associated (associated.id)}<li>{associated.title || '新会话'} <small>({desktopWorktreeSessionStatusLabel(associated.status)})</small></li>{/each}</ul>
          {:else}
            <p>此工作树没有关联的 Desktop 会话。只会调用 Git 清理此工作树目录。</p>
          {/if}
          <p>仅干净且已停止的工作树可清理。暂存、未暂存、未跟踪或忽略文件都会阻止操作并保留原文件。</p>
          {#if worktreeRemovalError}<div class="worktree-apply-error" role="alert">{worktreeRemovalError}</div>{/if}
        </div>
        <div class="modal-footer"><button class="quiet-button" disabled={busy} onclick={() => { modal = null; worktreeRemovalTarget = null; worktreeRemovalError = ''; }}>取消</button><button class="danger-button" disabled={busy || !worktreeRemovalTarget.canRemove} onclick={() => void confirmDesktopWorktreeRemoval()}>{busy ? '正在清理…' : worktreeRemovalTarget.sessions.length ? `删除 ${worktreeRemovalTarget.sessions.length} 个会话并清理工作树` : '清理孤儿工作树'}</button></div>
      </div>
    {:else if modal === 'file-preview' && filePreview}
      <div class="modal file-preview-modal" role="dialog" aria-modal="true" aria-labelledby="file-preview-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">WORKSPACE FILE</span><h2 id="file-preview-title">{filePreview.path}</h2></div><button class="icon-button" aria-label="关闭" onclick={() => modal = null}>×</button></div>
        <div class="modal-body"><pre class="workspace-file-preview">{filePreview.content}</pre>{#if filePreview.truncated}<div class="workspace-preview-truncated">文件较大，仅显示前 1 MiB。</div>{/if}</div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => modal = null}>关闭</button><button class="primary-button" disabled={previewMode} onclick={() => void openFileInDefaultApp()}>在默认应用中打开</button></div>
      </div>
    {:else if modal === 'mcp'}
      <div class="modal mcp-modal" role="dialog" aria-modal="true" aria-labelledby="mcp-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">PROJECT SETTINGS</span><h2 id="mcp-title">项目设置与 MCP</h2></div><button class="icon-button" aria-label="关闭" onclick={() => modal = null}>×</button></div>
        <div class="modal-body mcp-body">
          <p class="mcp-description">Desktop 按项目路径将这些运行设置保存在用户数据库中。TUI/CLI 继续读取 config.json；两边互不导入或改写配置。</p>
          <section class="settings-section project-runtime-section">
            <div class="settings-section-heading"><div><h3>上下文与附件</h3><p>所有会话使用同一套项目运行参数；数值为 0 时采用 Core 默认值。</p></div></div>
            <div class="form-grid">
              <label>上下文窗口 Token<input type="number" min="0" step="1000" bind:value={workspaceSettingsForm.contextWindow} /></label>
              <label>预留输出 Token<input type="number" min="0" step="1000" bind:value={workspaceSettingsForm.reserveOutputTokens} /></label>
              <label class="check-label"><input type="checkbox" bind:checked={workspaceSettingsForm.autoCompact} /> 自动压缩上下文</label>
              <label>压缩阈值（0–1）<input type="number" min="0" max="1" step="0.05" bind:value={workspaceSettingsForm.autoCompactThreshold} disabled={!workspaceSettingsForm.autoCompact} /></label>
              <label>单条消息最多图片数<input type="number" min="0" bind:value={workspaceSettingsForm.maxAttachmentFiles} /></label>
              <label>单张图片上限（字节）<input type="number" min="0" step="1048576" bind:value={workspaceSettingsForm.maxAttachmentBytes} /></label>
              <label class="wide-field">图片总大小上限（字节）<input type="number" min="0" step="1048576" bind:value={workspaceSettingsForm.maxTotalBytes} /></label>
            </div>
          </section>
          <section class="settings-section">
            <div class="settings-section-heading"><div><h3>MCP 服务</h3><p>本地环境变量仅显示名称，留空保留已保存的值；移除变量行即可清除。</p></div><button class="small-button" onclick={addMCPServer}>＋ 添加服务</button></div>
            {#if workspaceSettingsForm.mcpServers.length}
              <div class="mcp-server-list">
                {#each workspaceSettingsForm.mcpServers as server, index (index)}
                  {@const status = mcpStatus(server.name)}
                  <article class="mcp-server-card">
                    <div class="mcp-server-main">
                      <div><strong>{server.name || '未命名服务'}</strong><span>{status ? (status.transport === 'stdio' ? '本地进程' : '远程 HTTP') + ' · ' + (status.enabled ? (status.connected ? `已连接 · ${status.toolCount} 个工具` : '已启用 · 未连接') : '已停用') : '尚未应用'}</span></div>
                      {#if status}<label class="mcp-switch"><input type="checkbox" checked={status.enabled} disabled={workspaceHasOutstanding || pending.length > 0 || !!mcpUpdatingServer || workspaceSettingsSaving} onchange={(event) => void setMCPServerEnabled(status, event.currentTarget.checked)} /><span>{status.enabled ? '已启用' : '已停用'}</span></label>{/if}
                    </div>
                    <div class="form-grid mcp-config-grid">
                      <label>名称<input bind:value={server.name} /></label>
                      <label>连接方式<select bind:value={server.transport}><option value="stdio">本地进程</option><option value="streamable-http">远程 HTTP</option></select></label>
                      {#if server.transport === 'stdio'}
                        <label class="wide-field">可执行程序<input bind:value={server.command} placeholder="例如 npx" /></label>
                        <label class="wide-field">参数（每行一个）<textarea bind:value={server.argsText} rows="3" placeholder="每行填写一个参数，例如 -y"></textarea></label>
                        <div class="wide-field mcp-env-list">
                          <div class="settings-section-heading"><strong>环境变量</strong><button class="small-button" onclick={() => addMCPEnvironment(server)}>＋ 添加变量</button></div>
                          {#each server.env as item, envIndex (envIndex)}
                            <div class="mcp-env-row"><input aria-label="变量名称" bind:value={item.name} placeholder="变量名" /><input aria-label="变量值" type="password" bind:value={item.value} placeholder={item.hasValue ? '留空保留已保存的值' : '变量值'} autocomplete="new-password" /><button class="remove-env" aria-label="删除环境变量" onclick={() => server.env = server.env.filter((_, i) => i !== envIndex)}>×</button></div>
                          {/each}
                        </div>
                      {:else}
                        <label class="wide-field">服务 URL<input bind:value={server.url} placeholder="https://example.com/mcp" /></label>
                        <label class="wide-field">OAuth Client Metadata URL<input bind:value={server.clientMetadataURL} placeholder="可选的 HTTPS 地址" /></label>
                      {/if}
                    </div>
                    {#if status?.transport === 'streamable-http' && status.enabled}<div class="mcp-server-actions"><button class="small-button" disabled={workspaceHasOutstanding || pending.length > 0 || !!mcpUpdatingServer || workspaceSettingsSaving} onclick={() => void authenticateMCPServer(status)}>{mcpUpdatingServer === status.name ? '正在连接…' : '授权 / 重新连接'}</button></div>{/if}
                    <div class="mcp-server-actions"><button class="remove-server" disabled={workspaceSettingsSaving} onclick={() => workspaceSettingsForm.mcpServers = workspaceSettingsForm.mcpServers.filter((_, i) => i !== index)}>删除服务</button></div>
                  </article>
                {/each}
              </div>
            {:else}
              <div class="mcp-empty">尚未添加 MCP 服务。</div>
            {/if}
          </section>
          {#if mcpUpdatingServer}<div class="mcp-progress">正在处理 {mcpUpdatingServer}…</div>{/if}
        </div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => modal = null}>关闭</button><button class="primary-button" disabled={workspaceSettingsSaving || workspaceHasOutstanding || pending.length > 0 || !!mcpUpdatingServer} onclick={() => void saveWorkspaceSettings()}>{workspaceSettingsSaving ? '正在应用…' : '应用项目设置'}</button></div>
      </div>
    {:else if modal === 'switch' || modal === 'close'}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">TASK IN PROGRESS</span><h2>{modal === 'switch' ? '中断当前任务并切换？' : '中断当前任务并关闭？'}</h2></div><button class="icon-button" aria-label="关闭" onclick={() => modal = null}>×</button></div>
        <div class="modal-body"><p>当前任务还在运行。确认后会请求中断，并等待 Core 完成清理后再{modal === 'switch' ? '打开另一个工作区' : '关闭工作区'}。</p></div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => modal = null}>继续等待</button><button class="danger-button" disabled={busy} onclick={() => modal === 'switch' ? void confirmWorkspaceSwitch() : void confirmCloseWorkspace()}>{busy ? '正在停止…' : '中断并继续'}</button></div>
      </div>
    {:else if modal === 'edit' && editConfirmation}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="edit-confirm-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">EDIT CONVERSATION</span><h2 id="edit-confirm-title">确认编辑这条消息？</h2></div><button class="icon-button" aria-label="关闭" onclick={() => { modal = null; editConfirmation = null; }}>×</button></div>
        <div class="modal-body"><p>{editConfirmation.draft.wasQueued
          ? '如果这条消息仍在队列中，会保留当前任务和它之前的队列项，在原位置替换，并取消它之后的排队消息；如果它已开始，系统会中断当前任务并从此消息重跑。'
          : editConfirmation.pendingAtConfirm
            ? '确认后会立即中断当前任务并取消所有排队消息，再从这条消息开始回退并重跑。'
            : '系统会从这条消息开始回退并重新运行。'}已发生的文件操作不会撤销。</p></div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => { modal = null; editConfirmation = null; }}>继续当前对话</button><button class="danger-button" onclick={confirmMessageEdit}>{editConfirmation.draft.wasQueued ? '替换队列消息' : editConfirmation.pendingAtConfirm ? '中断并回退重跑' : '回退并重跑'}</button></div>
      </div>
    {/if}
  </div>
{/if}
