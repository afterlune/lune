import { tick } from 'svelte';
import * as Desktop from './desktopApi';
import type { DesktopProcess, DesktopProcessList } from './desktopStateTypes';

type Dependencies = {
  workspace: () => string;
  previewMode: () => boolean;
};

export function createDesktopProcessState(dependencies: Dependencies) {
  let desktopProcesses = $state<DesktopProcess[]>([]);
  let unavailableProcessSessions = $state<DesktopProcessList['unavailableSessions']>([]);
  let desktopProcessesLoading = $state(false);
  let desktopProcessesError = $state('');
  let desktopProcessesRequest = 0;
  let selectedDesktopProcessKey = $state('');
  let processPopoverOpen = $state(false);
  let processOutput = $state('');
  let processOutputError = $state('');
  let processOutputLoading = $state(false);
  let processOutputTruncated = $state(false);
  let processOutputStatus = $state('');
  let processOutputRequest = 0;
  let processOutputCursor = 0;
  let processOutputEndCursor = $state(0);
  let processLogView = $state<HTMLElement>();
  let processLogPinned = true;
  let stoppingDesktopProcessKey = $state('');

  const activeDesktopProcessCount = $derived(desktopProcesses.filter((item) => processStatusActive(item.status)).length);
  const selectedDesktopProcess = $derived(desktopProcesses.find((item) => desktopProcessKey(item) === selectedDesktopProcessKey) ?? null);

  function desktopProcessKey(process: Pick<DesktopProcess, 'sessionId' | 'id'>) {
    return `${process.sessionId}::${process.id}`;
  }

  function processStatusActive(status: string) {
    return status === 'running' || status === 'stopping';
  }

  function processStatusLabel(status: string) {
    switch (status) {
      case 'running': return '运行中';
      case 'stopping': return '正在停止';
      case 'succeeded': return '已完成';
      case 'failed': return '失败';
      case 'cancelled': return '已取消';
      case 'timed_out': return '超时';
      case 'interrupted': return '已中断';
      default: return status || '未知';
    }
  }

  function unavailableProcessStatusLabel(status: string) {
    switch (status) {
      case 'missing': return '目录缺失';
      case 'in use': return '被其他实例占用';
      default: return '不可用';
    }
  }

  function clearDesktopProcesses() {
    desktopProcessesRequest++;
    processOutputRequest++;
    desktopProcesses = [];
    unavailableProcessSessions = [];
    desktopProcessesLoading = false;
    desktopProcessesError = '';
    selectedDesktopProcessKey = '';
    processOutput = '';
    processOutputError = '';
    processOutputStatus = '';
    processOutputCursor = 0;
    processOutputEndCursor = 0;
    processOutputLoading = false;
  }

  function previewDesktopProcessList(): DesktopProcessList {
    const now = '2026-10-18T01:52:00+08:00';
    return {
      processes: desktopProcesses.length ? desktopProcesses : [
        { id: 'process-active', sessionId: 'session-main', sessionTitle: '重构桌面端提问流程', workingDirectory: 'D:\\Git\\luneAgent', executionId: 'exec-active', command: 'go test ./internal/desktop -run TestListDesktopProcesses', status: 'running', startedAt: now },
        { id: 'process-failed', sessionId: 'session-main', sessionTitle: '重构桌面端提问流程', workingDirectory: 'D:\\Git\\luneAgent', executionId: 'exec-failed', command: 'npm run check', status: 'failed', startedAt: '2026-10-17T10:26:00+08:00', exitCode: 1 },
        { id: 'process-done', sessionId: 'session-docs', sessionTitle: '文档更新', workingDirectory: 'D:\\Git\\luneAgent\\docs', executionId: 'exec-done', command: 'git status --short', status: 'succeeded', startedAt: '2026-10-17T10:21:00+08:00', exitCode: 0 },
      ],
      unavailableSessions: [{ id: 'session-unavailable', title: '无法打开的会话', workingDirectory: 'D:\\Git\\luneAgent\\offline', status: 'unavailable', reason: '此会话的运行时未加载。' }],
    };
  }

  async function refreshDesktopProcesses() {
    const request = ++desktopProcessesRequest;
    const requestWorkspace = dependencies.workspace();
    if (!requestWorkspace) {
      desktopProcesses = [];
      unavailableProcessSessions = [];
      desktopProcessesError = '';
      desktopProcessesLoading = false;
      selectedDesktopProcessKey = '';
      processOutputRequest++;
      processOutput = '';
      return;
    }
    desktopProcessesLoading = true;
    desktopProcessesError = '';
    try {
      const raw = dependencies.previewMode() ? previewDesktopProcessList() : await Desktop.ListDesktopProcesses();
      if (request !== desktopProcessesRequest || dependencies.workspace() !== requestWorkspace) return;
      desktopProcesses = raw.processes ?? [];
      unavailableProcessSessions = raw.unavailableSessions ?? [];
      const selected = desktopProcesses.find((item) => desktopProcessKey(item) === selectedDesktopProcessKey);
      if (selected) {
        if (!selectedDesktopProcessKey) void selectDesktopProcess(selected);
      } else if (desktopProcesses.length) {
        void selectDesktopProcess(desktopProcesses[0]);
      } else {
        selectedDesktopProcessKey = '';
        processOutputRequest++;
        processOutput = '';
        processOutputStatus = '';
      }
    } catch (cause) {
      if (request === desktopProcessesRequest && dependencies.workspace() === requestWorkspace) {
        desktopProcesses = [];
        unavailableProcessSessions = [];
        desktopProcessesError = String(cause);
      }
    } finally {
      if (request === desktopProcessesRequest) desktopProcessesLoading = false;
    }
  }

  async function selectDesktopProcess(process: DesktopProcess, force = false) {
    const key = desktopProcessKey(process);
    if (!force && key === selectedDesktopProcessKey) return;
    selectedDesktopProcessKey = key;
    processOutputRequest++;
    const request = processOutputRequest;
    processOutput = '';
    processOutputError = '';
    processOutputTruncated = false;
    processOutputCursor = 0;
    processOutputEndCursor = 0;
    processOutputStatus = process.status;
    processLogPinned = true;
    processOutputLoading = true;
    if (dependencies.previewMode()) {
      processOutput = previewProcessOutput(process);
      processOutputCursor = new TextEncoder().encode(processOutput).length;
      processOutputEndCursor = processOutputCursor;
      processOutputLoading = false;
      await tick();
      scrollProcessOutputToEnd();
      return;
    }

    let cursor = 0;
    try {
      while (request === processOutputRequest && selectedDesktopProcessKey === key && processPopoverOpen) {
        const shouldWait = processStatusActive(processOutputStatus) && cursor >= processOutputEndCursor;
        const result = await Desktop.ReadDesktopProcessOutput(process.sessionId, process.id, cursor, shouldWait);
        if (request !== processOutputRequest || selectedDesktopProcessKey !== key) return;
        if (result.output) processOutput += result.output;
        processOutputTruncated ||= result.truncated;
        processOutputCursor = result.nextCursor;
        processOutputEndCursor = result.endCursor;
        processOutputStatus = result.status;
        cursor = result.nextCursor;
        if (result.error) processOutputError = result.error;
        updateDesktopProcessStatus(process.sessionId, process.id, result.status, result.exitCode);
        if (result.output && processLogPinned) {
          await tick();
          scrollProcessOutputToEnd();
        }
        if (result.error || (cursor >= result.endCursor && !processStatusActive(result.status))) break;
        if (cursor >= result.endCursor && !result.output && !processStatusActive(result.status)) break;
        if (processStatusActive(result.status)) continue;
        if (cursor >= result.endCursor || cursor === result.cursor && !result.output) break;
      }
    } catch (cause) {
      if (request === processOutputRequest) processOutputError = String(cause);
    } finally {
      if (request === processOutputRequest) processOutputLoading = false;
    }
  }

  function previewProcessOutput(process: DesktopProcess) {
    if (process.id === 'process-failed') return '$ npm run check\n\n> frontend@0.0.0 check\n> svelte-check --tsconfig ./tsconfig.json\n\nsrc/App.svelte: error: Type \'string\' is not assignable to type \'number\'.\n\nProcess exited with code 1.\n';
    if (process.id === 'process-done') return '$ git status --short\n M internal/desktop/process_center.go\n M cmd/desktop/frontend/src/App.svelte\n';
    if (process.id === 'process-cancelled') return '$ npm run build\n\n> frontend@0.0.0 build\n> vite build --mode production\n\n^C\nProcess cancelled.\n';
    return '$ go test ./internal/desktop -run TestListDesktopProcesses\n\nok  github.com/afterlune/lune/internal/desktop\n';
  }

  function updateDesktopProcessStatus(sessionID: string, processID: string, status: string, exitCode?: number | null) {
    desktopProcesses = desktopProcesses.map((item) => item.sessionId === sessionID && item.id === processID
      ? { ...item, status, exitCode }
      : item);
  }

  function handleProcessLogScroll() {
    if (!processLogView) return;
    processLogPinned = processLogView.scrollHeight - processLogView.scrollTop - processLogView.clientHeight < 28;
  }

  function scrollProcessOutputToEnd() {
    if (processLogView && processLogPinned) processLogView.scrollTop = processLogView.scrollHeight;
  }

  async function stopDesktopProcess(process: DesktopProcess) {
    const key = desktopProcessKey(process);
    stoppingDesktopProcessKey = key;
    processOutputError = '';
    try {
      if (dependencies.previewMode()) {
        updateDesktopProcessStatus(process.sessionId, process.id, 'cancelled');
        processOutputStatus = 'cancelled';
        await refreshDesktopProcesses();
        return;
      }
      await Desktop.StopDesktopProcess(process.sessionId, process.id);
      await refreshDesktopProcesses();
    } catch (cause) {
      processOutputError = String(cause);
    } finally {
      stoppingDesktopProcessKey = '';
    }
  }

  return {
    get activeDesktopProcessCount() { return activeDesktopProcessCount; },
    get desktopProcesses() { return desktopProcesses; },
    set desktopProcesses(value: DesktopProcess[]) { desktopProcesses = value; },
    get desktopProcessesLoading() { return desktopProcessesLoading; },
    set desktopProcessesLoading(value: boolean) { desktopProcessesLoading = value; },
    get desktopProcessesError() { return desktopProcessesError; },
    set desktopProcessesError(value: string) { desktopProcessesError = value; },
    get unavailableProcessSessions() { return unavailableProcessSessions; },
    set unavailableProcessSessions(value: DesktopProcessList['unavailableSessions']) { unavailableProcessSessions = value; },
    get processPopoverOpen() { return processPopoverOpen; },
    set processPopoverOpen(value: boolean) { processPopoverOpen = value; },
    get processOutput() { return processOutput; },
    set processOutput(value: string) { processOutput = value; },
    get processOutputError() { return processOutputError; },
    set processOutputError(value: string) { processOutputError = value; },
    get processOutputLoading() { return processOutputLoading; },
    set processOutputLoading(value: boolean) { processOutputLoading = value; },
    get processOutputTruncated() { return processOutputTruncated; },
    set processOutputTruncated(value: boolean) { processOutputTruncated = value; },
    get processOutputStatus() { return processOutputStatus; },
    set processOutputStatus(value: string) { processOutputStatus = value; },
    get processOutputEndCursor() { return processOutputEndCursor; },
    set processOutputEndCursor(value: number) { processOutputEndCursor = value; },
    get processLogView() { return processLogView; },
    set processLogView(value: HTMLElement | undefined) { processLogView = value; },
    get selectedDesktopProcessKey() { return selectedDesktopProcessKey; },
    set selectedDesktopProcessKey(value: string) { selectedDesktopProcessKey = value; },
    get selectedDesktopProcess() { return selectedDesktopProcess; },
    get stoppingDesktopProcessKey() { return stoppingDesktopProcessKey; },
    set stoppingDesktopProcessKey(value: string) { stoppingDesktopProcessKey = value; },
    clearDesktopProcesses,
    desktopProcessKey,
    processStatusActive,
    processStatusLabel,
    unavailableProcessStatusLabel,
    refreshDesktopProcesses,
    selectDesktopProcess,
    updateDesktopProcessStatus,
    handleProcessLogScroll,
    stopDesktopProcess,
  };
}
