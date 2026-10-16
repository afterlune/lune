<script lang="ts">
  import { onMount } from 'svelte';
  import { Events } from '@wailsio/runtime';
  import * as Desktop from '../bindings/github.com/afterlune/lune/internal/desktop/desktopservice';
  import { TaskStatus } from '../bindings/github.com/afterlune/luneCore/internal/runtime/models';
  import type {
    Bootstrap, ConversationMessage, MigrationPreview, ModelProfileInput, ModelSettingsView,
    PendingRequestView, ProviderInput, SessionSummary, WorkspaceOpenResult, WorkspaceState,
  } from '../bindings/github.com/afterlune/lune/internal/desktop/models';

  type LocalMessage = ConversationMessage & { local?: boolean };
  type Activity = { id: string; label: string; detail: string; status: 'running' | 'done' | 'failed' | 'cancelled' };
  type ProviderForm = ProviderInput & { hasAPIKey: boolean };
  type Modal = 'settings' | 'migration' | 'switch' | 'close' | null;

  const previewMode = new URLSearchParams(location.search).has('preview');
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
  let streamText = $state('');
  let running = $state(false);
  let executionID = $state('');
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
  const canSend = $derived(models.configured && !running && !busy && pending.length === 0 && !planReview);

  function applyTheme(value: string) {
    theme = value;
    document.documentElement.dataset.theme = value;
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
  };

  function installWorkspaceState(state: WorkspaceState, preservePlanReview = false) {
    const currentPlanReview = preservePlanReview && session?.id === state.session.id ? planReview : null;
    workspace = state.workspace;
    session = state.session;
    sessions = state.sessions ?? [];
    permissionMode = state.permissionMode || 'workspace';
    if (state.models) models = state.models;
    pending = state.pending ?? [];
    messages = [...(state.session.messages ?? [])];
    activities = [];
    streamText = '';
    planReview = currentPlanReview;
    const status = state.runtimeState?.Task?.Status;
    running = status === 'running' || status === 'queued';
    executionID = state.runtimeState?.Task?.ExecutionID ?? '';
  }

  async function refreshSession() {
    if (previewMode || refreshing || !session) return;
    const refreshingSessionID = session.id;
    refreshing = true;
    try {
      const state = await Desktop.RefreshSession();
      if (session?.id !== refreshingSessionID || state.session.id !== refreshingSessionID) return;
      installWorkspaceState(state, true);
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
      const variant = new URLSearchParams(location.search).get('preview');
      activities = [
        { id: 'a1', label: 'Read', detail: 'internal/desktop/service.go', status: 'done' },
        { id: 'a2', label: 'Searched', detail: 'pending request and event handlers', status: 'done' },
      ];
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
      Events.On('pending-changed', () => void refreshSession());
      Events.On('agent-error', (event: { data: { message: string } }) => showError(event.data.message));
    } catch (error) {
      showError(error);
    } finally {
      loading = false;
    }
  }

  type AgentEvent = {
    kind: string; sessionId: string; taskId: string; executionId: string; status: string; error?: string;
    approvalId?: string; tool?: { id: string; name: string; arguments?: unknown; failed: boolean; canceled: boolean; durationMs: number };
    stream?: { type: string; delta: string };
  };
  type TurnResultEvent = { sessionId: string; executionId: string; status: string; text: string; plan?: string; error?: string };

  function onAgentEvent(event: AgentEvent) {
    if (event.sessionId !== session?.id) return;
    if (event.kind === 'task_started' || event.kind === 'task_queued') {
      running = true;
      executionID = event.executionId;
      streamText = '';
    }
    if (event.kind === 'model_stream' && event.stream?.type === 'text_delta') streamText += event.stream.delta;
    if (event.kind === 'tool_started' || event.kind === 'tool_finished') {
      const id = event.tool?.id || `${event.taskId}-${activities.length}`;
      const label = event.tool?.name || '工具';
      const status: Activity['status'] = event.kind === 'tool_started' ? 'running' : event.tool?.failed ? 'failed' : event.tool?.canceled ? 'cancelled' : 'done';
      const args = event.tool?.arguments ? compactArguments(event.tool.arguments) : '';
      const previous = activities.findIndex((item) => item.id === id);
      const activity = { id, label, detail: args ? compactArguments(args) : '', status };
      if (previous >= 0) activities[previous] = activity;
      else activities = [...activities, activity];
    }
    if (event.kind === 'task_finished' || event.kind === 'task_discarded' || event.kind === 'runtime_stream_failed') {
      running = false;
      void refreshSession();
    }
    if (event.error) showError(event.error);
  }

  function onTurnResult(result: TurnResultEvent) {
    if (result.sessionId !== session?.id) return;
    running = false;
    executionID = result.executionId;
    streamText = '';
    if (result.error) showError(result.error);
    if (result.text) messages = [...messages, { id: `assistant-${result.executionId}`, role: 'assistant', text: result.text }];
    if (result.plan) planReview = { executionID: result.executionId, text: result.plan };
    void refreshSession();
  }

  function compactArguments(value: unknown) {
    try {
      const parsed = typeof value === 'string' ? JSON.parse(value) : value;
      if (typeof parsed === 'string') return parsed;
      if (parsed.command) return String(parsed.command);
      if (parsed.path) return String(parsed.path);
      if (parsed.query) return String(parsed.query);
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
    if ((!text && attachments.length === 0) || running || !models.configured || previewMode) return;
    busy = true;
    streamText = '';
    messages = [...messages, { id: `user-${Date.now()}`, role: 'user', text, images: attachments.map((item) => item.preview), local: true }];
    const request = { text, images: attachments.map(({ name, mediaType, data }) => ({ name, mediaType, data })) };
    composerText = '';
    attachments = [];
    try {
      const submission = await Desktop.Submit(request);
      executionID = submission.ExecutionID;
      running = true;
    } catch (error) { showError(error); }
    finally { busy = false; }
  }

  async function interruptTask() {
    if (previewMode) { running = false; return; }
    try { await Desktop.Interrupt(); }
    catch (error) { showError(error); }
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
      await refreshSession();
    } catch (error) { showError(error); }
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
    } catch (error) { showError(error); }
  }

  async function recover(request: PendingRequestView) {
    try {
      if (request.kind === 'model_recovery') await Desktop.RecoverModel();
      else await Desktop.ResolveToolRecovery(request.invocationId, 'retry');
      pending = pending.filter((item) => item.id !== request.id);
      running = true;
    } catch (error) { showError(error); }
  }

  async function dismissRecovery(request: PendingRequestView) {
    try {
      await Desktop.ResolveToolRecovery(request.invocationId, 'skip');
      pending = pending.filter((item) => item.id !== request.id);
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

  onMount(() => { void initialize(); });
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
              <div class="conversation-intro"><span class="intro-mark">l</span><div><strong>你好，我是 luneAgent。</strong><p>我可以在 <b>{workspace.split(/[\\/]/).at(-1)}</b> 中帮你阅读和修改代码。</p></div></div>
            {/if}
            {#each messages as message (message.id)}
              <article class:from-user={message.role === 'user'} class="message-row">
                {#if message.role === 'assistant'}<div class="message-avatar">l</div>{/if}
                <div class="message-body">
                  {#if message.role === 'user'}<span class="message-author">你</span>{:else}<span class="message-author">luneAgent</span>{/if}
                  <div class="message-text">{message.text}</div>
                  {#if message.images?.length}<div class="message-images">{#each message.images as image}<img src={image} alt="消息附件" />{/each}</div>{/if}
                </div>
              </article>
            {/each}

            {#if activities.length > 0}
              <section class="activity-list" aria-label="工具活动">
                {#each activities as activity (activity.id)}
                  <div class="activity-row"><span class:activity-live={activity.status === 'running'} class="activity-dot">{activity.status === 'failed' ? '!' : activity.status === 'cancelled' ? '–' : '•'}</span><span class="activity-label">{activity.label}</span><span class="activity-detail">{activity.detail}</span>{#if activity.status === 'running'}<span class="activity-spinner"></span>{/if}</div>
                {/each}
              </section>
            {/if}

            {#if running && streamText}
              <article class="message-row"><div class="message-avatar">l</div><div class="message-body"><span class="message-author">luneAgent</span><div class="message-text">{streamText}<span class="stream-caret"></span></div></div></article>
            {:else if running && activities.length === 0}
              <div class="thinking-row"><span class="thinking-dots"><i></i><i></i><i></i></span><span>正在思考</span></div>
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
            <textarea bind:value={composerText} onkeydown={handleComposerKey} placeholder={!models.configured ? '先配置模型以开始对话' : pending.length ? '请先处理上方的请求' : planReview ? '请先选择方案操作' : '给 luneAgent 发送消息…'} disabled={!canSend} rows="1" aria-label="消息输入"></textarea>
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
                {#if running}<button class="stop-button" onclick={() => void interruptTask()} title="中断任务">■ 中断</button>{:else}<button class="send-button" onclick={() => void sendMessage()} disabled={!canSend || (!composerText.trim() && !attachments.length)} aria-label="发送"><span>↑</span></button>{/if}
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
