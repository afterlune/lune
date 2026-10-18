import { TaskStatus } from '../../bindings/github.com/afterlune/luneCore/internal/runtime/models';
import type { AppearanceView, Bootstrap, WorkspaceState, WorkspaceChangesView, WorkspaceDiffView, PendingRequestView, PendingSessionSummary as PendingSummary, DesktopWorktreeView } from '../../bindings/github.com/afterlune/lune/internal/desktop/models';
type WorkspaceChanges = Omit<WorkspaceChangesView,'files'> & { files: NonNullable<WorkspaceChangesView['files']> };
type DesktopWorktree = Omit<DesktopWorktreeView,'sessions'> & { sessions: NonNullable<DesktopWorktreeView['sessions']> };
type PendingSessionSummary = Omit<PendingSummary,'requests'> & {requests:NonNullable<PendingSummary['requests']>};

export function createDesktopPreview(previewVariant:string, defaultAppearance:AppearanceView) {
  const previewBootstrap: Bootstrap = {
    theme: 'light', appearance: { ...defaultAppearance, theme: 'light' }, recentWorkspaces: [], models: {
      providers: [{ name: 'deepseek', protocol: 'responses', baseURL: 'https://api.deepseek.com', hasAPIKey: true, serverCompaction: false }],
      profiles: [{ name: '工作模型', provider: 'deepseek', model: 'deepseek-chat', maxOutputTokens: 0, supportsInputAudio: false }],
      defaultModel: '工作模型', maxParallelAgents: 4, configured: true,
    }, workspace: 'D:\\Git\\luneAgent', sessionId: 'session-preview', controls: {permissionMode:'workspace',effectivePermissionMode:'workspace',planMode:false},
  };

  const previewState: WorkspaceState = {
    workspace: 'D:\\Git\\luneAgent', projectRoot: 'D:\\Git\\luneAgent', workspaceMode: 'local', workingDirectory: 'D:\\Git\\luneAgent', controls: { permissionMode: 'workspace', effectivePermissionMode: 'workspace', planMode: false },
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
      workspaceMode: 'worktree', workingDirectory, worktreePath: workingDirectory, controls: {permissionMode:permission, effectivePermissionMode:permission, planMode:false},
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


return { previewBootstrap, previewState, previewWorkspaceChanges, previewWorkspaceDiffs, previewPendingRequest, previewPendingSessions, previewPendingRequests, previewPendingWorkspaceState, previewChangesForVariant, previewDesktopWorktrees, previewChangesWorkspaceState };
}
