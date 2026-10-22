import type {
  ConversationMessage,
  PendingRequestView,
  SubmitInput,
  WorkspaceState,
} from '../../bindings/github.com/afterlune/lune/internal/desktop/models';

export type LocalMessage = ConversationMessage & {
  local?: boolean;
  canonicalID?: string;
  deliveryState?: 'sending' | 'queued' | 'submitted' | 'failed' | 'cancelled';
  deliveryError?: string;
  request?: SubmitInput;
  executionID?: string;
  userMessageIndex?: number;
  queuePosition?: number;
  interrupted?: boolean;
};

export type Activity = {
  id: string;
  invocationId: string;
  agentId?: string;
  label: string;
  detail: string;
  arguments?: unknown;
  details?: {
    output?: string;
    command?: { text: string; output?: string; status?: string; exitCode?: number | null; truncated?: boolean; error?: string };
    changes?: { path: string; added: number; removed: number; lines?: { kind: string; oldLine?: number; newLine?: number; text: string }[]; truncated?: boolean }[];
    truncated?: boolean;
  };
  truncated?: boolean;
  status: 'running' | 'done' | 'failed' | 'cancelled' | 'info' | 'waiting';
  durationMs?: number;
  error?: string;
};

export type ActivityRun = {
  key: string;
  executionId: string;
  userMessageIndex: number;
  status: string;
  statusLabel: string;
  startedAt: string;
  finishedAt?: string | null;
  tools: Activity[];
  expanded: boolean;
  elapsedSeconds?: number;
  queuePosition?: number;
  messageText?: string;
  messageImages?: string[];
};

export type ExecutionTimelineBlock = {
  key: string;
  executionId: string;
  userMessageIndex: number;
  userMessage: LocalMessage | null;
  assistantMessages: LocalMessage[];
  activityRuns: ActivityRun[];
  pendingRequests: PendingRequestView[];
  planReview?: { executionID: string; text: string };
  streamText: string;
};

export type StoredActivityRun = NonNullable<WorkspaceState['executionHistory']>[number];
export type StoredActivityTool = NonNullable<StoredActivityRun['tools']>[number];
export type ExecutionState = 'idle' | 'submitting' | 'running' | 'waiting' | 'completed' | 'failed' | 'cancelled' | 'interrupted' | 'disconnected';

export type DesktopProcess = {
  id: string;
  sessionId: string;
  sessionTitle: string;
  workingDirectory: string;
  executionId: string;
  command: string;
  status: string;
  startedAt: string;
  finishedAt?: string | null;
  exitCode?: number | null;
};

export type DesktopProcessSession = { id: string; title: string; workingDirectory: string; status: string; reason: string };
export type DesktopProcessList = { processes: DesktopProcess[]; unavailableSessions: DesktopProcessSession[] };
