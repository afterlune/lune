import { TaskStatus } from '../../bindings/github.com/afterlune/luneCore/internal/runtime/models';
import type { AppearanceView, Bootstrap, WorkspaceState, PendingRequestView, PendingSessionSummary as PendingSummary } from '../../bindings/github.com/afterlune/lune/internal/desktop/models';
type PendingSessionSummary = Omit<PendingSummary,'requests'> & {requests:NonNullable<PendingSummary['requests']>};

export function createDesktopPreview(_previewVariant:string, defaultAppearance:AppearanceView) {
  const previewBootstrap: Bootstrap = {
    theme: 'light', appearance: { ...defaultAppearance, theme: 'light' }, recentWorkspaces: [], models: {
      providers: [{ name: 'deepseek', protocol: 'responses', baseURL: 'https://api.deepseek.com', hasAPIKey: true, serverCompaction: false }],
      profiles: [{ name: '工作模型', provider: 'deepseek', model: 'deepseek-chat', maxOutputTokens: 0, supportsInputAudio: false }],
      defaultModel: '工作模型', maxParallelAgents: 4, configured: true,
    }, workspace: 'D:\\Git\\luneAgent', sessionId: 'session-preview', controls: {permissionMode:'workspace',effectivePermissionMode:'workspace',planMode:false},
  };

  const previewState: WorkspaceState = {
    workspace: 'D:\\Git\\luneAgent', controls: { permissionMode: 'workspace', effectivePermissionMode: 'workspace', planMode: false },
    session: {
      id: 'session-preview', title: '重构桌面端提问流程', updatedAt: '2026-10-16T10:36:00+08:00',
      settings: { model_profile: '工作模型' },
      messages: [
        { id: 'm1', role: 'user', text: '帮我检查 Question 面板的交互，并把截图测试流程接入桌面端。' },
        { id: 'm2', role: 'assistant', text: '我会先把窗口结构和消息流搭好，再补上浏览器预览与真实窗口启动检查。' },
      ],
    },
    sessions: [{ id: 'session-preview', title: '重构桌面端提问流程', updatedAt: '2026-10-16T10:36:00+08:00', workingDirectory: 'D:\\Git\\luneAgent', status: 'idle' }],
    runtimeState: { Goal: null, Task: { Status: TaskStatus.TaskIdle, SessionID: 'session-preview', TaskID: '', ExecutionID: '', QueueID: '', GoalID: '', GoalRevision: 0, AutomaticGoal: false, ContextWindow: 0, ReserveOutputTokens: 0, AutoCompact: false, AutoCompactThreshold: 0, Goal: '', MaxToolRounds: 0, ModelOverride: '', ModelProfile: '工作模型', Provider: 'deepseek', Model: 'deepseek-chat', ReasoningEffort: '', Execution: {} }, Queue: [] },
    pending: [],
    executionHistory: [],
  };

  function previewPendingRequest(kind: string, id: string, fields: Partial<PendingRequestView> = {}): PendingRequestView {
    return { kind, id, executionId: `exec-${id}`, runId: `run-${id}`, invocationId: `invoke-${id}`, callId: `call-${id}`, tool: '', ...fields };
  }
  const previewPendingSessions: PendingSessionSummary[] = [
    {
      id: 'session-background', title: '后台会话需要你处理', workingDirectory: 'D:\\Git\\luneAgent', updatedAt: '2026-10-17T08:15:00+08:00',
      requests: [
        { kind: 'approval', tool: 'exec_command' }, { kind: 'user_question', tool: '' },
        { kind: 'model_recovery', tool: '' }, { kind: 'tool_recovery', tool: 'write_file' },
        { kind: 'user_interaction', tool: 'docs' }, { kind: 'execution_blocker', tool: 'command' },
      ],
    },
    {
      id: 'session-docs', title: '文档登录与会话检查', workingDirectory: 'D:\\Git\\luneAgent\\docs', updatedAt: '2026-10-17T08:12:00+08:00',
      requests: [{ kind: 'approval', tool: 'apply_patch' }, { kind: 'user_interaction', tool: 'private-docs' }],
    },
  ];
  const previewPendingRequests: Record<string, PendingRequestView[]> = {
    'session-background': [
      previewPendingRequest('approval', 'approval-preview', { tool: 'exec_command', arguments: { command: 'git status --short' } }),
      previewPendingRequest('user_question', 'question-background', { questions: [{ id: 'next-step', question: '接下来优先检查哪一部分？', options: [{ label: '测试', description: '先确认当前改动的行为。', recommended: true }, { label: '实现', description: '继续完成主要功能。', recommended: false }] }] }),
      previewPendingRequest('model_recovery', 'model-background', { reason: 'rate_limit' }),
      previewPendingRequest('tool_recovery', 'tool-background', { tool: 'write_file', arguments: { path: 'docs/desktop.md' } }),
      previewPendingRequest('user_interaction', 'interaction-form', {
        tool: 'docs', arguments: { method: 'elicitation/create', params: { mode: 'form', message: '请输入文档服务的一次性验证码。', requestedSchema: { type: 'object', properties: { code: { type: 'string', title: '验证码', minLength: 4 }, remember: { type: 'boolean', title: '记住此设备' }, retries: { type: 'integer', title: '重试次数', minimum: 1, maximum: 5 }, account: { type: 'string', title: '账户类型', enum: ['个人', '工作'] } }, required: ['code'] } } },
      }),
      previewPendingRequest('execution_blocker', 'blocker-background', { tool: 'command', reason: '前一条命令仍在执行，任务暂停以等待检查。' }),
    ],
    'session-docs': [
      previewPendingRequest('approval', 'approval-docs', { tool: 'apply_patch' }),
      previewPendingRequest('user_interaction', 'interaction-url', {
        tool: 'private-docs', arguments: { method: 'elicitation/create', params: { mode: 'url', message: '请在文档服务中完成登录。', url: 'https://docs.example.test/sign-in?token=hidden' } },
      }),
    ],
  };

  function previewPendingWorkspaceState(id: string): WorkspaceState {
    const target = previewPendingSessions.find((item) => item.id === id) ?? previewPendingSessions[0];
    return {
      ...previewState,
      session: { ...previewState.session, id: target.id, title: target.title, updatedAt: target.updatedAt },
      sessions: previewPendingSessions.map((item) => ({
        id: item.id, title: item.title, updatedAt: item.updatedAt,
        workingDirectory: item.workingDirectory, status: 'waiting',
      })),
      runtimeState: {
        ...previewState.runtimeState,
        Task: { ...previewState.runtimeState.Task, SessionID: target.id, Status: TaskStatus.TaskPaused },
      },
      pending: previewPendingRequests[target.id] ?? [],
    };
  }

  return { previewBootstrap, previewState, previewPendingSessions, previewPendingWorkspaceState };
}
