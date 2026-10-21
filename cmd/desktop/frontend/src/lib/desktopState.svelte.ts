import { createDesktopPreview } from './desktopPreview';
import { onMount, tick } from 'svelte';
import { Events } from '@wailsio/runtime';
import * as Desktop from './desktopApi';
import { createDesktopSettingsState, defaultAppearance } from './desktopSettingsState.svelte';
import {
  activityDetail, activityFromView, activityRunStatusLabel, formatArguments, toolLabel,
} from './desktopActivity';
import { createDesktopProcessState } from './desktopProcessState.svelte';
import type {
  Activity, ActivityRun, DesktopProcess, DesktopProcessSession, ExecutionState, ExecutionTimelineBlock,
  LocalMessage, StoredActivityRun,
} from './desktopStateTypes';
import { TaskStatus } from '../../bindings/github.com/afterlune/luneCore/internal/runtime/models';
import type {
    Bootstrap, ConversationMessage, ModelSettingsView,
    SessionControlsView, SubmitInput, PendingRequestView, PendingSessionSummary as PendingSessionSummaryView, SessionSearchResult, SessionSummary,
    WorkspaceOpenResult, WorkspaceState,
  } from '../../bindings/github.com/afterlune/lune/internal/desktop/models';

export function createDesktopState() {

  
  
  
  
  
  
  
  
  

  type Modal = 'settings' | 'switch' | 'close' | 'edit' | 'rename-session' | 'delete-session' | 'mcp' | null;
  type EditDraft = { messageID: string; targetID: string; userMessageIndex: number; wasQueued: boolean; previousText: string; previousAttachments: { name: string; mediaType: string; data: string; preview: string }[] };
  type EditConfirmation = { draft: EditDraft; request: SubmitInput; pendingAtConfirm: boolean };
  type PendingSessionSummary = Omit<PendingSessionSummaryView, 'requests'> & { requests: NonNullable<PendingSessionSummaryView['requests']> };
  type ElicitationResponse = { action: 'accept' | 'decline' | 'cancel'; content?: Record<string, unknown> };

  const unsubscribeEvents: (() => void)[] = [];
  const previewVariant = new URLSearchParams(location.search).get('preview') || '';
  const previewMode = previewVariant !== '';
  let appVisible = $state(true);
  let reducedMotion = $state(false);
  let systemPrefersDark = $state(false);
  let copiedMessageID = $state('');
  let copyResetTimer: number | undefined;
  let recent = $state<Bootstrap['recentWorkspaces']>([]);
  let models = $state<ModelSettingsView>({ providers: [], profiles: [], defaultModel: '', maxParallelAgents: 0, configured: false });
  let workspace = $state('');
  let workspaceMenuOpen = $state(false);
  let sidebarCollapsed = $state(window.matchMedia('(max-width: 960px)').matches);
  let sessions = $state<SessionSummary[]>([]);
  let sessionFilter = $state('');
  let sessionSearchResults = $state<SessionSearchResult[]>([]);
  let sessionSearchLoading = $state(false);
  let sessionSearchError = $state('');
  let sessionSearchRequest = 0;
  let sessionSearchTimer: number | undefined;
  let sessionSearchHighlightSession = $state('');
  let sessionSearchHighlightMessage = $state('');
  let sessionSearchHighlightTimer: number | undefined;
  let sessionActionTarget = $state<SessionSummary | null>(null);
  let sessionTitleDraft = $state('');
  let sidebarView = $state<'sessions' | 'pending'>('sessions');
  let session = $state<WorkspaceState['session'] | null>(null);
  let permissionMode = $state('workspace');
  let basePermissionMode = $state('workspace');
  let controlsPending = $state(false);
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
  let workspaceEpoch = 0;
  let switchingSession = $state(false);
  let nextSessionTarget = '';
  let sessionSwitch: Promise<void> | null = null;
  let errorMessage = $state('');
  let modal = $state<Modal>(null);
  let switchTarget = $state('');
  let planReview = $state<{ executionID: string; text: string } | null>(null);
  let deferredPlanReviews = $state<{ sessionId: string; executionID: string; text: string }[]>([]);
  let answerValues = $state<Record<string, string>>({});
  let noteValues = $state<Record<string, string>>({});
  let editDraft = $state<EditDraft | null>(null);
  let editConfirmation = $state<EditConfirmation | null>(null);
  let applyingEdit = $state(false);
  let cancellingQueuedExecution = $state('');
  const cancelledPreviewExecutions = new Set<string>();
  const processState = createDesktopProcessState({
    workspace: () => workspace,
    previewMode: () => previewMode,
  });
  const settingsState = createDesktopSettingsState({
    previewMode: () => previewMode,
    models: () => models,
    setModels: (value) => { models = value; },
    workspace: () => workspace,
    sessionID: () => session?.id || '',
    workOutstanding: () => workOutstanding,
    hasPendingRequests: () => pending.length > 0,
    appVisible: () => appVisible,
    reducedMotion: () => reducedMotion,
    setModal: (value) => { modal = value; },
    clearError: () => { errorMessage = ''; },
    showError,
    openWorkspace,
    installWorkspaceState,
    completeWorkspaceOpen,
  });

  const activeQuestion = $derived(pending.find((item) => item.kind === 'user_question'));
  const canWrite = $derived(models.configured && !controlsPending && !switchingSession && !busy && !interrupting && pending.length === 0 && !planReview);
  const canSend = $derived(canWrite && !editDraft);
  const canEditMessages = $derived(models.configured && !controlsPending && !switchingSession && !busy && !interrupting && pending.length === 0 && !planReview && !applyingEdit);
  const effectiveDarkTheme = $derived(settingsState.theme === 'dark' || (settingsState.theme === 'system' && systemPrefersDark));
  const pageTitle = $derived(!workspace ? '桌面工作区' : sidebarView === 'pending' ? '待处理' : session?.title || '新会话');
  const queuedMessageCount = $derived(messages.filter((message) => message.deliveryState === 'queued' || message.deliveryState === 'sending').length);
  const workOutstanding = $derived(running || queuedMessageCount > 0);
  const workspaceHasOutstanding = $derived(workOutstanding || sessions.some((item) => ['running', 'queued', 'waiting'].includes(item.status ?? '')));
  const visibleSessions = $derived(sessions.filter((item) => item.title.toLowerCase().includes(sessionFilter.trim().toLowerCase())));
  const pendingRequestCount = $derived(pendingSessions.reduce((count, item) => count + item.requests.length, 0));
  const executionTimeline = $derived.by(() => {
    const leadingMessages: LocalMessage[] = [];
    const blocks: ExecutionTimelineBlock[] = [];
    let current: ExecutionTimelineBlock | undefined;
    let userMessageIndex = -1;
    const createBlock = (index: number, userMessage: LocalMessage | null, id = ''): ExecutionTimelineBlock => ({
      key: id || userMessage?.id || `execution-${index}-${blocks.length}`,
      executionId: id || userMessage?.executionID || '', userMessageIndex: index, userMessage,
      assistantMessages: [], activityRuns: [], pendingRequests: [], streamText: '',
    });
    for (const message of messages) {
      if (message.role === 'user') {
        userMessageIndex++;
        current = createBlock(userMessageIndex, message);
        blocks.push(current);
      } else if (current) {
        current.assistantMessages.push(message);
      } else {
        leadingMessages.push(message);
      }
    }
    const findBlock = (id: string) => id ? blocks.find((block) =>
      block.executionId === id || block.userMessage?.executionID === id || block.activityRuns.some((run) => run.executionId === id),
    ) : undefined;
    for (const run of activityHistory) {
      let block = run.userMessageIndex >= 0 ? blocks[run.userMessageIndex] : undefined;
      block ??= findBlock(run.executionId);
      if (!block) {
        block = createBlock(run.userMessageIndex, null, run.executionId);
        blocks.push(block);
      }
      block.activityRuns.push(run);
      if (!block.executionId) block.executionId = run.executionId;
    }
    const activeBlock = findBlock(executionID) ?? blocks.at(-1);
    for (const request of pending) {
      const block = findBlock(request.executionId) ?? activeBlock ?? (blocks.length === 1 ? blocks[0] : undefined);
      if (block) block.pendingRequests.push(request);
      else {
        const unattached = createBlock(-1, null, request.executionId);
        unattached.pendingRequests.push(request);
        blocks.push(unattached);
      }
    }
    if (planReview) {
      const block = findBlock(planReview.executionID) ?? activeBlock ?? blocks.at(-1);
      if (block) block.planReview = planReview;
      else {
        const unattached = createBlock(-1, null, planReview.executionID);
        unattached.planReview = planReview;
        blocks.push(unattached);
      }
    }
    if (streamText && activeBlock) activeBlock.streamText = streamText;
    return { leadingMessages, blocks };
  });

  function executionBlockState(block: ExecutionTimelineBlock) {
    if (block.pendingRequests.length || block.planReview) return 'waiting';
    if (block.userMessage?.deliveryState === 'failed') return 'failed';
    if (block.userMessage?.deliveryState === 'cancelled') return 'interrupted';
    if (block.userMessage?.deliveryState === 'queued') return 'queued';
    if (block.userMessage?.deliveryState === 'sending') return 'submitting';
    if (block.executionId === executionID && running) return 'running';
    const run = block.activityRuns.at(-1);
    if (run?.status === 'queued') return 'queued';
    if (run?.status === 'running') return 'running';
    if (run?.status === 'paused') return 'waiting';
    if (run?.status === 'failed') return 'failed';
    if (run?.status === 'interrupted' || run?.status === 'cancelled' || run?.status === 'discarded') return 'interrupted';
    if (run?.status === 'completed' || block.assistantMessages.length) return 'completed';
    return 'ready';
  }

  function executionBlockLabel(block: ExecutionTimelineBlock) {
    const state = executionBlockState(block);
    if (state === 'waiting') return '待处理';
    if (state === 'queued') return '待执行';
    if (state === 'submitting') return '提交中';
    if (state === 'running') return '执行中';
    if (state === 'completed') return '已完成';
    if (state === 'failed') return '失败';
    if (state === 'interrupted') return '已中断';
    return '准备执行';
  }

  function executionBlockStateClass(block: ExecutionTimelineBlock) {
    return `execution-state-${executionBlockState(block)}`;
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

  function toggleActivityRun(key: string) {
    activityHistory = activityHistory.map((run) => run.key === key ? { ...run, expanded: !run.expanded } : run);
  }

  const { previewBootstrap, previewState, previewPendingRequest, previewPendingSessions, previewPendingRequests, previewPendingWorkspaceState } = createDesktopPreview(previewVariant, defaultAppearance);

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

  function selectSidebarView(view: typeof sidebarView) {
    if (!workspace && view !== 'sessions') return;
    sidebarView = view;
    workspaceMenuOpen = false;
    if (window.matchMedia('(max-width: 960px)').matches) sidebarCollapsed = false;
    if (view === 'pending') void refreshPendingSessions();
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
    busy = false; interrupting = false; applyingEdit = false; controlsPending = false; interruptRequestedExecutionID = ""; cancellingQueuedExecution = "";
    if (session?.id !== state.session.id) { if (editDraft) cancelEditingMessage(); }
    if (planReview && session && session.id !== state.session.id) {
      deferredPlanReviews = [...deferredPlanReviews, { sessionId: session.id, ...planReview }];
    }
    const currentPlanReview = preservePlanReview && session?.id === state.session.id ? planReview : null;
    clearElapsedTimer();
    const nextWorkspace = state.workspace;
    if (workspace && nextWorkspace && workspace.toLowerCase() !== nextWorkspace.toLowerCase()) clearSessionSearch();
    workspace = nextWorkspace;
    session = state.session;
    sessions = state.sessions ?? [];
    installControls(state.controls);
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
  }

  function applySessionRefresh(state: WorkspaceState) {
    session = state.session;
    sessions = state.sessions ?? sessions;
    installControls(state.controls);
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
    const epoch = workspaceEpoch;
    refreshing = true;
    try {
      const state = await Desktop.RefreshSession(session?.id || '');
      if (epoch !== workspaceEpoch || session?.id !== refreshingSessionID || state.session.id !== refreshingSessionID) return;
      applySessionRefresh(state);
    } catch (error) {
      showError(error);
    } finally {
      refreshing = false;
    }
  }

  async function restoreLastWorkspace(path: string, sessionID: string) {
    try {
      const result = await Desktop.OpenWorkspace(path);
      await completeWorkspaceOpen(result, sessionID);
    } catch {
      try { await Desktop.CloseWorkspace(false); } catch { /* workspace was not open */ }
      workspace = '';
      session = null;
      sessions = [];
      messages = [];
      pending = [];
      try { await Desktop.ForgetDesktopLocation(); } catch { /* best effort */ }
    }
  }

  async function initialize() {
    if (previewMode) {
      await new Promise((resolve) => setTimeout(resolve, 80));
      settingsState.applyAppearance(previewBootstrap.appearance);
      recent = previewBootstrap.recentWorkspaces;
      models = previewBootstrap.models;
      const variant = previewVariant;
      if (variant === 'anime') {
        settingsState.applyAppearance({ ...settingsState.appearance, theme: 'anime' });
      }
      if (variant === 'welcome') {
        workspace = '';
        session = null;
        sessions = [];
        messages = [];
        recent = [
          { path: 'D:\\Git\\luneAgent', lastUsed: '2026-10-18T00:00:00Z' },
          { path: 'D:\\Git\\agent-dashboard', lastUsed: '2026-10-17T23:30:00Z' },
        ];
      } else {
        installWorkspaceState(previewState);
      }
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
      if (variant === 'execution-summary') {
        const writeTool: Activity = {
          id: 'summary-write', invocationId: 'summary-write', label: '编辑文件', detail: 'calc.go',
          arguments: { path: 'calc.go' },
          details: { changes: [{ path: 'calc.go', added: 1, removed: 1, lines: [{ kind: 'remove', oldLine: 1, text: 'return 0' }, { kind: 'add', newLine: 1, text: 'return a + b' }] }] },
          status: 'done', durationMs: 84,
        };
        const testTool: Activity = {
          id: 'summary-test', invocationId: 'summary-test', label: '运行命令', detail: 'go test ./...',
          arguments: { command: 'go test ./...' },
          details: { command: { text: 'go test ./...', status: 'running' } },
          status: 'done', durationMs: 1480,
        };
        const commandStatusTool: Activity = {
          id: 'summary-test-status', invocationId: 'summary-test-status', label: '检查命令状态',
          detail: '', arguments: { command_id: 'summary-command-id', cursor: 240, wait_ms: 5000 },
          details: { command: { text: 'go test ./...', output: 'ok  example.test/calc  0.012s', status: 'completed', exitCode: 0 } },
          status: 'done', durationMs: 200,
        };
        messages = [
          { id: 'summary-user', role: 'user', text: '实现 Add 并运行测试。', executionID: 'exec-preview-summary', deliveryState: 'submitted' },
          { id: 'summary-assistant', role: 'assistant', text: '已实现 Add，`go test ./...` 通过。', executionID: 'exec-preview-summary' },
        ];
        activityHistory = [{
          key: 'exec-preview-summary', executionId: 'exec-preview-summary', userMessageIndex: 0,
          status: 'completed', statusLabel: '执行完成', startedAt: '2026-10-17T10:00:00Z', finishedAt: '2026-10-17T10:00:02Z',
          tools: [writeTool, testTool, commandStatusTool], expanded: false, elapsedSeconds: 2,
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
      if (variant.startsWith('processes')) {
        processState.processPopoverOpen = true;
        void processState.refreshDesktopProcesses();
      }
      loading = false;
      return;
    }
    try {
      const data = await Desktop.Bootstrap();
      settingsState.applyAppearance(data.appearance ?? { ...defaultAppearance, theme: data.theme || 'system' });
      recent = data.recentWorkspaces ?? [];
      models = data.models;
      workspace = data.workspace;
      if (data.controls?.effectivePermissionMode) installControls(data.controls);
      unsubscribeEvents.push(Events.On('agent-event', (event: { data: AgentEvent }) => onAgentEvent(event.data)));
      unsubscribeEvents.push(Events.On('turn-result', (event: { data: TurnResultEvent }) => onTurnResult(event.data)));
      unsubscribeEvents.push(Events.On('pending-changed', (event: { data: string }) => {
        void refreshPendingSessions();
        if (event.data === session?.id) void refreshSession();
      }));
      unsubscribeEvents.push(Events.On('agent-error', (event: { data: { message: string } }) => showError(event.data.message)));
      if (!workspace && data.lastWorkspace) {
        await restoreLastWorkspace(data.lastWorkspace, data.lastSessionId || '');
      }
      void refreshPendingSessions();
      void processState.refreshDesktopProcesses();
    } catch (error) {
      showError(error);
    } finally {
      loading = false;
    }
  }

  type AgentEvent = {
    kind: string; sessionId: string; taskId: string; executionId: string; status: string; error?: string;
    commandId?: string; commandStatus?: string; commandExitCode?: number;
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
    const detail = tool?.arguments ? activityDetail(rawName, tool.arguments) : '';
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
    if (event.kind === 'command_started' || event.kind === 'command_finished') {
      if (!previewMode) void processState.refreshDesktopProcesses();
      return;
    }
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

  function showError(error: unknown) {
    errorMessage = error instanceof Error ? error.message : String(error);
    window.setTimeout(() => { if (errorMessage) errorMessage = ''; }, 6500);
  }

  async function openWorkspace(path: string, preferredSessionID = '') {
    if (workspace && workspaceHasOutstanding) {
      switchTarget = path;
      modal = 'switch';
      return;
    }
    invalidateWorkspaceRequests();
    try {
      const result = previewMode ? { workspace: path, sessions: [], permissionMode: 'workspace' } as WorkspaceOpenResult : await Desktop.OpenWorkspace(path);
      await completeWorkspaceOpen(result, preferredSessionID);
    } catch (error) { showError(error); }
  }

  async function completeWorkspaceOpen(result: WorkspaceOpenResult, preferredSessionID = '') {
    workspace = result.workspace;
    if (result.models) models = result.models;
    sessions = result.sessions ?? [];
    permissionMode = result.permissionMode || 'workspace';
    const preferred = sessions.find((item) => item.id === preferredSessionID) ?? sessions[0];
    if (preferred) await openSession(preferred.id);
    else if (!previewMode) installWorkspaceState(await Desktop.CreateSession());
    else installWorkspaceState({ ...previewState, workspace: result.workspace });
    void processState.refreshDesktopProcesses();
  }

  async function confirmWorkspaceSwitch() {
    invalidateWorkspaceRequests();
    busy = true;
    try {
      if (!previewMode) await Desktop.CloseWorkspace(true);
      workspace = '';
      processState.clearDesktopProcesses();
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
    invalidateWorkspaceRequests();
    if (!previewMode) {
      try { await Desktop.CloseWorkspace(false); }
      catch (error) { showError(error); return; }
    }
    workspace = '';
    processState.clearDesktopProcesses();
    pendingSessions = [];
    pendingSessionsError = '';
    session = null;
    messages = [];
    pending = [];
    modal = null;
  }

  async function confirmCloseWorkspace() {
    invalidateWorkspaceRequests();
    busy = true;
    try {
      if (!previewMode) await Desktop.CloseWorkspace(true);
      clearSessionSearch();
      workspace = '';
      processState.clearDesktopProcesses();
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

  function invalidateWorkspaceRequests() {
    workspaceEpoch++;
    nextSessionTarget = '';
    sessionSwitch = null;
    switchingSession = false;
    controlsPending = false;
    refreshing = false;
  }

  async function openSession(id: string) {
    if (previewMode) {
      installWorkspaceState(previewVariant === 'pending-center' ? previewPendingWorkspaceState(id) : previewState);
      return;
    }
    nextSessionTarget = id;
    if (sessionSwitch) return sessionSwitch;
    const epoch = workspaceEpoch;
    switchingSession = true;
    const operation = (async () => {
      while (nextSessionTarget && epoch === workspaceEpoch) {
        const target = nextSessionTarget;
        nextSessionTarget = '';
        try {
          const state = await Desktop.OpenSession(target);
          if (epoch === workspaceEpoch && !nextSessionTarget) installWorkspaceState(state);
        } catch (error) { if (epoch === workspaceEpoch && !nextSessionTarget) showError(error); }
      }
    })();
    sessionSwitch = operation;
    try { await operation; }
    finally { if (sessionSwitch === operation) { switchingSession = false; sessionSwitch = null; } }
  }

  function clearSessionSearch() {
    sessionSearchRequest++;
    if (sessionSearchTimer !== undefined) window.clearTimeout(sessionSearchTimer);
    sessionSearchTimer = undefined;
    sessionFilter = '';
    sessionSearchResults = [];
    sessionSearchLoading = false;
    sessionSearchError = '';
  }

  function updateSessionSearch(value: string) {
    sessionFilter = value;
    const query = value.trim();
    const request = ++sessionSearchRequest;
    if (sessionSearchTimer !== undefined) window.clearTimeout(sessionSearchTimer);
    sessionSearchTimer = undefined;
    sessionSearchError = '';
    sessionSearchResults = [];
    if (!query) {
      sessionSearchLoading = false;
      return;
    }
    sessionSearchLoading = true;
    sessionSearchTimer = window.setTimeout(async () => {
      try {
        let results: SessionSearchResult[];
        if (previewMode) {
          results = sessions
            .filter((item) => item.title.toLowerCase().includes(query.toLowerCase()))
            .map((item) => ({
              sessionId: item.id, sessionTitle: item.title, workingDirectory: item.workingDirectory,
              updatedAt: item.updatedAt, matchKind: 'title', messageIndex: -1,
              snippetBefore: '', snippetMatch: item.title, snippetAfter: '',
            }));
        } else {
          results = (await Desktop.SearchWorkspaceConversation(query)) ?? [];
        }
        if (request === sessionSearchRequest) sessionSearchResults = results;
      } catch (error) {
        if (request === sessionSearchRequest) sessionSearchError = String(error);
      } finally {
        if (request === sessionSearchRequest) sessionSearchLoading = false;
      }
    }, 250);
  }

  async function openSessionSearchResult(result: SessionSearchResult) {
    if (result.matchKind !== 'message' || !result.messageId) {
      await openSession(result.sessionId);
      return;
    }
    if (sessionSearchHighlightTimer !== undefined) window.clearTimeout(sessionSearchHighlightTimer);
    sessionSearchHighlightSession = result.sessionId;
    sessionSearchHighlightMessage = result.messageId;
    await openSession(result.sessionId);
    if (session?.id !== result.sessionId) {
      sessionSearchHighlightMessage = '';
      return;
    }
    await tick();
    const target = Array.from(document.querySelectorAll<HTMLElement>('.message-row[data-message-id]'))
      .find((element) => element.dataset.messageId === result.messageId);
    target?.scrollIntoView({ behavior: 'smooth', block: 'center' });
    sessionSearchHighlightTimer = window.setTimeout(() => {
      sessionSearchHighlightMessage = '';
      sessionSearchHighlightSession = '';
      sessionSearchHighlightTimer = undefined;
    }, 1800);
  }

  async function openPendingSession(id: string) {
    sidebarView = 'sessions';
    await openSession(id);
  }

  async function createSession() {
    if (previewMode) {
      installWorkspaceState(previewState);
      return;
    }
    try { installWorkspaceState(await Desktop.CreateSession()); }
    catch (error) { showError(error); }
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
          installWorkspaceState(fallback ? await Desktop.OpenSession(fallback.id) : await Desktop.CreateSession());
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

  function installControls(controls: SessionControlsView) {
    basePermissionMode = controls.permissionMode;
    permissionMode = controls.effectivePermissionMode;
    planMode = controls.planMode;
  }

  async function setPermission(value: string) {
    const id = session?.id;
    const epoch = workspaceEpoch;
    if (!id || switchingSession || controlsPending) return;
    controlsPending = true;
    try {
      const controls = previewMode ? { permissionMode:value, effectivePermissionMode:value, planMode } : await Desktop.SetPermissionMode(id, value);
      if (epoch === workspaceEpoch && session?.id === id) installControls(controls);
    } catch (error) { if (epoch === workspaceEpoch && session?.id === id) showError(error); }
    finally { if (epoch === workspaceEpoch && session?.id === id) controlsPending = false; }
  }

  async function setSessionModel(value: string) {
    const targetSessionID = session?.id || '';
    const targetEpoch = workspaceEpoch;

    if (!session) return;
    if (!previewMode) {
      try { await Desktop.SetSessionModel(targetSessionID, value); }
      catch (error) { showError(error); return; }
    }
    if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
    session = { ...session, settings: { ...session.settings, model_profile: value } };
  }

  async function setPlanMode(enabled: boolean) {
    const id = session?.id;
    const epoch = workspaceEpoch;
    if (!id || switchingSession || controlsPending) return;
    controlsPending = true;
    try {
      const controls = previewMode ? { permissionMode: basePermissionMode, effectivePermissionMode: enabled ? 'read-only' : basePermissionMode, planMode: enabled } : await Desktop.SetPlanMode(id, enabled);
      if (epoch === workspaceEpoch && session?.id === id) installControls(controls);
    } catch (error) { if (epoch === workspaceEpoch && session?.id === id) showError(error); }
    finally { if (epoch === workspaceEpoch && session?.id === id) controlsPending = false; }
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
    const targetSessionID = session?.id || '';
    const targetEpoch = workspaceEpoch;

    busy = true;
    if (!running && !activityHistory.some((run) => run.status === 'queued')) {
      executionState = 'submitting';
      currentStatus = '正在提交请求';
    }
    await tick();
    scrollToLatest();
    try {
      const submission = previewMode ? await submitPreviewRequest(request) : await Desktop.Submit(targetSessionID, request);
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
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
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
      const reason = error instanceof Error ? error.message : String(error);
      updateMessage(messageID, { deliveryState: 'failed', deliveryError: reason });
      if (!running) {
        executionState = 'failed';
        currentStatus = '消息发送失败';
        stopElapsedTimer();
      }
    }
    finally { if (targetEpoch === workspaceEpoch && session?.id === targetSessionID) busy = false; }
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

  function previousUserForAssistant(message: LocalMessage) {
    const assistantIndex = messages.findIndex((item) => item.id === message.id);
    if (assistantIndex < 0 || assistantIndex !== messages.length - 1) return null;
    for (let index = assistantIndex - 1; index >= 0; index--) {
      if (messages[index].role === 'user') {
        return {
          message: messages[index],
          userMessageIndex: messages.slice(0, index).filter((item) => item.role === 'user').length,
        };
      }
    }
    return null;
  }

  function canRegenerateAssistant(message: LocalMessage) {
    const previous = previousUserForAssistant(message);
    return !!previous && !running && !workOutstanding && canEditMessages &&
      !message.interrupted && previous.message.deliveryState !== 'queued' && previous.message.deliveryState !== 'failed';
  }

  async function copyAssistantMessage(message: LocalMessage) {
    try {
      if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(message.text);
      else {
        const textarea = document.createElement('textarea');
        textarea.value = message.text;
        textarea.style.position = 'fixed';
        textarea.style.opacity = '0';
        document.body.appendChild(textarea);
        textarea.select();
        const copied = document.execCommand('copy');
        textarea.remove();
        if (!copied) throw new Error('复制失败');
      }
      copiedMessageID = message.id;
      if (copyResetTimer !== undefined) window.clearTimeout(copyResetTimer);
      copyResetTimer = window.setTimeout(() => { copiedMessageID = ''; copyResetTimer = undefined; }, 1600);
    } catch (error) { showError(error); }
  }

  async function regenerateAssistantMessage(message: LocalMessage) {
    const previous = previousUserForAssistant(message);
    if (!previous || !canRegenerateAssistant(message)) return;
    const images = (previous.message.images ?? []).flatMap((dataURL, index) => {
      const match = /^data:(image\/(?:png|jpeg|gif|webp));base64,(.+)$/i.exec(dataURL);
      return match ? [{ name: `image-${index + 1}`, mediaType: match[1].toLowerCase(), data: match[2] }] : [];
    });
    const targetID = previous.message.canonicalID || previous.message.executionID || previous.message.id;
    const draft: EditDraft = {
      messageID: previous.message.id, targetID, userMessageIndex: previous.userMessageIndex,
      wasQueued: false, previousText: composerText, previousAttachments: attachments,
    };
    await applyUserMessageEdit({
      draft, request: { text: previous.message.text, images, userMessageIndex: previous.userMessageIndex },
      pendingAtConfirm: false,
    });
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
    const targetSessionID = session?.id || '';
    const targetEpoch = workspaceEpoch;

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
        : await Desktop.EditUserMessage(targetSessionID, edit.draft.targetID, request);
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
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
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
      showError(error);
      if (!previewMode) void refreshSession();
    } finally {
      if (targetEpoch === workspaceEpoch && session?.id === targetSessionID) { busy = false; applyingEdit = false; }
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
    const targetSessionID = session?.id || '';
    const targetEpoch = workspaceEpoch;

    const targetID = message.executionID;
    if (!targetID || message.deliveryState !== 'queued' || cancellingQueuedExecution) return;
    cancellingQueuedExecution = targetID;
    try {
      if (previewMode) cancelledPreviewExecutions.add(targetID);
      else await Desktop.CancelQueuedTurn(targetSessionID, targetID);
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
      updateMessage(message.id, { deliveryState: 'cancelled', queuePosition: undefined, request: undefined });
      upsertActivityRun({
        key: targetID, executionId: targetID, status: 'cancelled', statusLabel: '排队消息已撤回',
        userMessageIndex: message.userMessageIndex ?? -1, queuePosition: undefined, finishedAt: new Date().toISOString(),
      });
      await refreshSession();
    } catch (error) {
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
      showError(error);
      await refreshSession();
    } finally { if (targetEpoch === workspaceEpoch && session?.id === targetSessionID) cancellingQueuedExecution = ''; }
  }

  async function interruptTask() {
    const targetSessionID = session?.id || '';
    const targetEpoch = workspaceEpoch;

    if (interrupting) return;
    const targetExecutionID = executionID;
    const partialTextAtInterrupt = streamText.trim();
    interrupting = true;
    interruptRequestedExecutionID = targetExecutionID || activeActivityRunKey;
    setExecutionStatus('running', '正在中断任务');
    try {
      const interrupted = previewMode ? true : await Desktop.Interrupt(targetSessionID, targetExecutionID);
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
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
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
      showError(error);
      await refreshSession();
    } finally {
      if (targetEpoch === workspaceEpoch && session?.id === targetSessionID) { interrupting = false; interruptRequestedExecutionID = ''; }
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
    const targetSessionID = session?.id || '';
    const targetEpoch = workspaceEpoch;

    try {
      await Desktop.ResolveApproval(targetSessionID, request.id, allowed);
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
      if (allowed) resumeExecution('授权已完成，正在继续处理');
      await refreshSession();
      void refreshPendingSessions();
    } catch (error) {
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return; showError(error); }
  }

  function resumeExecution(label: string) {
    running = true;
    startElapsedTimer(false);
    setExecutionStatus('running', label, { expand: true });
  }

  async function answerQuestion(request: PendingRequestView) {
    const targetSessionID = session?.id || '';
    const targetEpoch = workspaceEpoch;

    const answers = (request.questions ?? []).map((question) => {
      const option = answerValues[question.id] || '';
      const notes = noteValues[question.id]?.trim() || '';
      const answer = option ? `${option}${notes ? `：${notes}` : ''}` : notes;
      return { question_id: question.id, answer };
    });
    if (answers.some((answer) => !answer.answer.trim())) { showError('请先回答每个问题'); return; }
    try {
      await Desktop.AnswerQuestion(targetSessionID, { executionId: request.executionId, runId: request.runId, invocationId: request.invocationId, callId: request.callId, answers });
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
      pending = pending.filter((item) => item.id !== request.id);
      resumeExecution('回答已提交，正在继续处理');
      void refreshPendingSessions();
    } catch (error) {
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return; showError(error); }
  }

  async function resolveUserInteraction(request: PendingRequestView, response: ElicitationResponse) {
    const targetSessionID = session?.id || '';
    const targetEpoch = workspaceEpoch;

    try {
      if (!previewMode) await Desktop.ResolveUserInteraction(targetSessionID, request.id, JSON.stringify(response));
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
      pending = pending.filter((item) => item.id !== request.id);
      removePreviewPendingSummary(request);
      resumeExecution('MCP 响应已提交，正在继续处理');
      void refreshPendingSessions();
    } catch (error) {
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return; showError(error); }
  }

  async function openMCPInteractionURL(request: PendingRequestView, target: string) {
    if (previewMode) return;
    await Desktop.OpenMCPURL(session?.id || '', request.tool, target);
  }

  async function recover(request: PendingRequestView) {
    const targetSessionID = session?.id || '';
    const targetEpoch = workspaceEpoch;

    try {
      if (request.kind === 'model_recovery' || request.kind === 'execution_blocker') await Desktop.RecoverModel(targetSessionID);
      else await Desktop.ResolveToolRecovery(targetSessionID, request.invocationId, 'retry');
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
      pending = pending.filter((item) => item.id !== request.id);
      resumeExecution('正在恢复任务');
      void refreshPendingSessions();
    } catch (error) {
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return; showError(error); }
  }

  async function dismissRecovery(request: PendingRequestView) {
    const targetSessionID = session?.id || '';
    const targetEpoch = workspaceEpoch;

    try {
      if (request.kind === 'tool_recovery') await Desktop.ResolveToolRecovery(targetSessionID, request.invocationId, 'skip');
      else if (!previewMode && !await Desktop.Interrupt(targetSessionID, request.executionId)) throw new Error('无法丢弃当前暂停的任务');
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
      pending = pending.filter((item) => item.id !== request.id);
      if (request.kind === 'tool_recovery') resumeExecution('正在跳过失败步骤');
      else finishExecution('interrupted');
      void refreshPendingSessions();
    } catch (error) {
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return; showError(error); }
  }

  async function implementPlan(mode: 'same_context' | 'new_context') {
    const targetSessionID = session?.id || '';
    const targetEpoch = workspaceEpoch;

    if (!planReview || previewMode) return;
    busy = true;
    try {
      const result = await Desktop.ImplementPlan(targetSessionID, planReview.executionID, mode);
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return;
      planReview = null;
      planMode = false;
      if (result.sessionId !== session?.id) {
        sessions = [{ id: result.sessionId, title: '实施方案', updatedAt: new Date().toISOString(), workingDirectory: workspace, status: 'running' }, ...sessions];
        await openSession(result.sessionId);
        if (workspaceEpoch !== targetEpoch || session?.id !== result.sessionId) return;
      }
      running = true;
      executionID = result.executionId;
    } catch (error) {
      if (targetEpoch !== workspaceEpoch || session?.id !== targetSessionID) return; showError(error); }
    finally { if (targetEpoch === workspaceEpoch && session?.id === targetSessionID) busy = false; }
  }

  function stayInPlan() {
    planReview = null;
    presentDeferredPlanReview();
  }

  onMount(() => {
    const colorScheme = window.matchMedia('(prefers-color-scheme: dark)');
    const motionPreference = window.matchMedia('(prefers-reduced-motion: reduce)');
    const compactViewport = window.matchMedia('(max-width: 760px)');
    const syncColorScheme = () => systemPrefersDark = colorScheme.matches;
    const syncMotionPreference = () => reducedMotion = motionPreference.matches;
    const syncVisibility = () => appVisible = !document.hidden;
    const syncCompactViewport = () => sidebarCollapsed = compactViewport.matches;
    syncColorScheme();
    syncMotionPreference();
    syncVisibility();
    syncCompactViewport();
    colorScheme.addEventListener('change', syncColorScheme);
    motionPreference.addEventListener('change', syncMotionPreference);
    compactViewport.addEventListener('change', syncCompactViewport);
    document.addEventListener('visibilitychange', syncVisibility);
    void initialize();
    return () => {
      for (const unsubscribe of unsubscribeEvents) unsubscribe();
      workspaceEpoch++;
      clearElapsedTimer();
      if (sessionSearchTimer !== undefined) window.clearTimeout(sessionSearchTimer);
      if (sessionSearchHighlightTimer !== undefined) window.clearTimeout(sessionSearchHighlightTimer);
      if (copyResetTimer !== undefined) window.clearTimeout(copyResetTimer);
      colorScheme.removeEventListener('change', syncColorScheme);
      motionPreference.removeEventListener('change', syncMotionPreference);
      compactViewport.removeEventListener('change', syncCompactViewport);
      document.removeEventListener('visibilitychange', syncVisibility);
      settingsState.dispose();
    };
  });

return {
get previewMode() { return previewMode; },
get theme() { return settingsState.theme; }, set theme(value: string) { settingsState.theme = value; },
get appearanceDraft() { return settingsState.appearanceDraft; }, set appearanceDraft(value) { settingsState.appearanceDraft = value; },
get settingsTab() { return settingsState.settingsTab; }, set settingsTab(value) { settingsState.settingsTab = value; },
get wallpaperURL() { return settingsState.wallpaperURL; }, set wallpaperURL(value) { settingsState.wallpaperURL = value; },
get activeWallpaperType() { return settingsState.activeWallpaperType; }, set activeWallpaperType(value) { settingsState.activeWallpaperType = value; },
get wallpaperVideo() { return settingsState.wallpaperVideo; }, set wallpaperVideo(value) { settingsState.wallpaperVideo = value; },
get appVisible() { return appVisible; }, set appVisible(value: typeof appVisible) { appVisible = value; },
get reducedMotion() { return reducedMotion; }, set reducedMotion(value: typeof reducedMotion) { reducedMotion = value; },
get copiedMessageID() { return copiedMessageID; }, set copiedMessageID(value: typeof copiedMessageID) { copiedMessageID = value; },
get recent() { return recent; }, set recent(value: typeof recent) { recent = value; },
get models() { return models; }, set models(value: typeof models) { models = value; },
get workspace() { return workspace; }, set workspace(value: typeof workspace) { workspace = value; },
get workspaceMenuOpen() { return workspaceMenuOpen; }, set workspaceMenuOpen(value: typeof workspaceMenuOpen) { workspaceMenuOpen = value; },
get sidebarCollapsed() { return sidebarCollapsed; }, set sidebarCollapsed(value: typeof sidebarCollapsed) { sidebarCollapsed = value; },
get processPopoverOpen() { return processState.processPopoverOpen; }, set processPopoverOpen(value: boolean) { processState.processPopoverOpen = value; },
get sessionFilter() { return sessionFilter; }, set sessionFilter(value: typeof sessionFilter) { sessionFilter = value; },
get sessionSearchResults() { return sessionSearchResults; }, set sessionSearchResults(value: typeof sessionSearchResults) { sessionSearchResults = value; },
get sessionSearchLoading() { return sessionSearchLoading; }, set sessionSearchLoading(value: typeof sessionSearchLoading) { sessionSearchLoading = value; },
get sessionSearchError() { return sessionSearchError; }, set sessionSearchError(value: typeof sessionSearchError) { sessionSearchError = value; },
get sessionSearchHighlightSession() { return sessionSearchHighlightSession; }, set sessionSearchHighlightSession(value: typeof sessionSearchHighlightSession) { sessionSearchHighlightSession = value; },
get sessionSearchHighlightMessage() { return sessionSearchHighlightMessage; }, set sessionSearchHighlightMessage(value: typeof sessionSearchHighlightMessage) { sessionSearchHighlightMessage = value; },
get sessionActionTarget() { return sessionActionTarget; }, set sessionActionTarget(value: typeof sessionActionTarget) { sessionActionTarget = value; },
get sessionTitleDraft() { return sessionTitleDraft; }, set sessionTitleDraft(value: typeof sessionTitleDraft) { sessionTitleDraft = value; },
get sidebarView() { return sidebarView; }, set sidebarView(value: typeof sidebarView) { sidebarView = value; },
get session() { return session; }, set session(value: typeof session) { session = value; },
get controlsPending() { return controlsPending; },
get switchingSession() { return switchingSession; },
get permissionMode() { return permissionMode; }, set permissionMode(value: typeof permissionMode) { permissionMode = value; },
get planMode() { return planMode; }, set planMode(value: typeof planMode) { planMode = value; },
get pending() { return pending; }, set pending(value: typeof pending) { pending = value; },
get pendingSessions() { return pendingSessions; }, set pendingSessions(value: typeof pendingSessions) { pendingSessions = value; },
get pendingSessionsLoading() { return pendingSessionsLoading; }, set pendingSessionsLoading(value: typeof pendingSessionsLoading) { pendingSessionsLoading = value; },
get pendingSessionsError() { return pendingSessionsError; }, set pendingSessionsError(value: typeof pendingSessionsError) { pendingSessionsError = value; },
get desktopProcesses() { return processState.desktopProcesses; }, set desktopProcesses(value: DesktopProcess[]) { processState.desktopProcesses = value; },
get unavailableProcessSessions() { return processState.unavailableProcessSessions; }, set unavailableProcessSessions(value: DesktopProcessSession[]) { processState.unavailableProcessSessions = value; },
get desktopProcessesLoading() { return processState.desktopProcessesLoading; }, set desktopProcessesLoading(value: boolean) { processState.desktopProcessesLoading = value; },
get desktopProcessesError() { return processState.desktopProcessesError; }, set desktopProcessesError(value: string) { processState.desktopProcessesError = value; },
get selectedDesktopProcessKey() { return processState.selectedDesktopProcessKey; }, set selectedDesktopProcessKey(value: string) { processState.selectedDesktopProcessKey = value; },
get processOutput() { return processState.processOutput; }, set processOutput(value: string) { processState.processOutput = value; },
get processOutputError() { return processState.processOutputError; }, set processOutputError(value: string) { processState.processOutputError = value; },
get processOutputLoading() { return processState.processOutputLoading; }, set processOutputLoading(value: boolean) { processState.processOutputLoading = value; },
get processOutputTruncated() { return processState.processOutputTruncated; }, set processOutputTruncated(value: boolean) { processState.processOutputTruncated = value; },
get processOutputStatus() { return processState.processOutputStatus; }, set processOutputStatus(value: string) { processState.processOutputStatus = value; },
get processOutputEndCursor() { return processState.processOutputEndCursor; }, set processOutputEndCursor(value: number) { processState.processOutputEndCursor = value; },
get processLogView() { return processState.processLogView; }, set processLogView(value: HTMLElement | undefined) { processState.processLogView = value; },
get stoppingDesktopProcessKey() { return processState.stoppingDesktopProcessKey; }, set stoppingDesktopProcessKey(value: string) { processState.stoppingDesktopProcessKey = value; },
get messages() { return messages; }, set messages(value: typeof messages) { messages = value; },
get activities() { return activities; }, set activities(value: typeof activities) { activities = value; },
get running() { return running; }, set running(value: typeof running) { running = value; },
get executionID() { return executionID; }, set executionID(value: typeof executionID) { executionID = value; },
get elapsedSeconds() { return elapsedSeconds; }, set elapsedSeconds(value: typeof elapsedSeconds) { elapsedSeconds = value; },
get interrupting() { return interrupting; }, set interrupting(value: typeof interrupting) { interrupting = value; },
get composerText() { return composerText; }, set composerText(value: typeof composerText) { composerText = value; },
get attachments() { return attachments; }, set attachments(value: typeof attachments) { attachments = value; },
get loading() { return loading; }, set loading(value: typeof loading) { loading = value; },
get busy() { return busy; }, set busy(value: typeof busy) { busy = value; },
get errorMessage() { return errorMessage; }, set errorMessage(value: typeof errorMessage) { errorMessage = value; },
get modal() { return modal; }, set modal(value: typeof modal) { modal = value; },
get planReview() { return planReview; }, set planReview(value: typeof planReview) { planReview = value; },
get answerValues() { return answerValues; }, set answerValues(value: typeof answerValues) { answerValues = value; },
get noteValues() { return noteValues; }, set noteValues(value: typeof noteValues) { noteValues = value; },
get providerForms() { return settingsState.providerForms; }, set providerForms(value) { settingsState.providerForms = value; },
get profileForms() { return settingsState.profileForms; }, set profileForms(value) { settingsState.profileForms = value; },
get defaultModel() { return settingsState.defaultModel; }, set defaultModel(value) { settingsState.defaultModel = value; },
get maxParallelAgents() { return settingsState.maxParallelAgents; }, set maxParallelAgents(value) { settingsState.maxParallelAgents = value; },
get settingsSaving() { return settingsState.settingsSaving; }, set settingsSaving(value) { settingsState.settingsSaving = value; },
get editDraft() { return editDraft; }, set editDraft(value: typeof editDraft) { editDraft = value; },
get editConfirmation() { return editConfirmation; }, set editConfirmation(value: typeof editConfirmation) { editConfirmation = value; },
get mcpServers() { return settingsState.mcpServers; }, set mcpServers(value) { settingsState.mcpServers = value; },
get mcpUpdatingServer() { return settingsState.mcpUpdatingServer; }, set mcpUpdatingServer(value) { settingsState.mcpUpdatingServer = value; },
get workspaceSettingsForm() { return settingsState.workspaceSettingsForm; }, set workspaceSettingsForm(value) { settingsState.workspaceSettingsForm = value; },
get workspaceSettingsSaving() { return settingsState.workspaceSettingsSaving; }, set workspaceSettingsSaving(value) { settingsState.workspaceSettingsSaving = value; },
get toolchainCandidates() { return settingsState.toolchainCandidates; }, set toolchainCandidates(value) { settingsState.toolchainCandidates = value; },
get toolchainDetectionLoading() { return settingsState.toolchainDetectionLoading; }, set toolchainDetectionLoading(value) { settingsState.toolchainDetectionLoading = value; },
get cancellingQueuedExecution() { return cancellingQueuedExecution; }, set cancellingQueuedExecution(value: typeof cancellingQueuedExecution) { cancellingQueuedExecution = value; },
get canWrite() { return canWrite; },
get canSend() { return canSend; },
get canEditMessages() { return canEditMessages; },
get appearanceDirty() { return settingsState.appearanceDirty; },
get modelSettingsDirty() { return settingsState.modelSettingsDirty; },
get effectiveDarkTheme() { return effectiveDarkTheme; },
get pageTitle() { return pageTitle; },
get queuedMessageCount() { return queuedMessageCount; },
get workOutstanding() { return workOutstanding; },
get workspaceHasOutstanding() { return workspaceHasOutstanding; },
get visibleSessions() { return visibleSessions; },
get pendingRequestCount() { return pendingRequestCount; },
get activeDesktopProcessCount() { return processState.activeDesktopProcessCount; },
get selectedDesktopProcess() { return processState.selectedDesktopProcess; },
get executionTimeline() { return executionTimeline; },
executionBlockState,
executionBlockLabel,
executionBlockStateClass,
previewAppearanceDraft: settingsState.previewAppearanceDraft,
toggleActivityRun,
refreshPendingSessions,
desktopProcessKey: processState.desktopProcessKey,
processStatusActive: processState.processStatusActive,
processStatusLabel: processState.processStatusLabel,
unavailableProcessStatusLabel: processState.unavailableProcessStatusLabel,
refreshDesktopProcesses: processState.refreshDesktopProcesses,
selectSidebarView,
selectDesktopProcess: processState.selectDesktopProcess,
handleProcessLogScroll: processState.handleProcessLogScroll,
stopDesktopProcess: processState.stopDesktopProcess,
pendingKindLabel,
formatArguments,
showError,
setSettingsForm: settingsState.setSettingsForm,
cancelSettings: settingsState.cancelSettings,
setWallpaperType: settingsState.setWallpaperType,
resetWallpaper: settingsState.resetWallpaper,
chooseWallpaper: settingsState.chooseWallpaper,
previewBrowserWallpaper: settingsState.previewBrowserWallpaper,
addMCPServer: settingsState.addMCPServer,
addMCPEnvironment: settingsState.addMCPEnvironment,
toolchainPathAuthorized: settingsState.toolchainPathAuthorized,
addToolchainPath: settingsState.addToolchainPath,
removeToolchainPath: settingsState.removeToolchainPath,
detectCommandToolchains: settingsState.detectCommandToolchains,
pickCommandToolchainFolder: settingsState.pickCommandToolchainFolder,
mcpStatus: settingsState.mcpStatus,
saveWorkspaceSettings: settingsState.saveWorkspaceSettings,
addProvider: settingsState.addProvider,
addProfile: settingsState.addProfile,
saveSettings: settingsState.saveSettings,
changeTheme: settingsState.changeTheme,
openFolderPicker: settingsState.openFolderPicker,
openWorkspace,
confirmWorkspaceSwitch,
closeWorkspace,
confirmCloseWorkspace,
openSession,
updateSessionSearch,
openSessionSearchResult,
openPendingSession,
createSession,
beginRenameSession,
beginDeleteSession,
saveSessionTitle,
confirmDeleteSession,
openMCPManager: settingsState.openMCPManager,
setMCPServerEnabled: settingsState.setMCPServerEnabled,
authenticateMCPServer: settingsState.authenticateMCPServer,
setPermission,
setSessionModel,
setPlanMode,
sendMessage,
startEditingMessage,
cancelEditingMessage,
canRegenerateAssistant,
copyAssistantMessage,
regenerateAssistantMessage,
confirmMessageEdit,
retryMessage,
cancelQueuedMessage,
interruptTask,
addFiles,
onDrop,
handleComposerKey,
resolveApproval,
answerQuestion,
resolveUserInteraction,
openMCPInteractionURL,
recover,
dismissRecovery,
implementPlan,
stayInPlan
};
}
export type DesktopState = ReturnType<typeof createDesktopState>;
