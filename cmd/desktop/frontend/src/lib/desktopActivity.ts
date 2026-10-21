import type { Activity, StoredActivityTool } from './desktopStateTypes';

export function activityFromView(activity: StoredActivityTool): Activity {
  return {
    id: activity.id,
    invocationId: activity.invocationId,
    agentId: activity.agentId,
    label: toolLabel(activity.name),
    detail: activityDetail(activity.name, activity.arguments),
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

export function activityRunStatusLabel(status: string) {
  if (status === 'queued') return '任务排队中';
  if (status === 'completed') return '任务已完成';
  if (status === 'failed') return '任务执行失败';
  if (status === 'cancelled') return '排队消息已撤回';
  if (status === 'interrupted' || status === 'discarded') return '任务已中断';
  if (status === 'paused') return '任务已暂停';
  return '正在处理请求';
}

export function toolLabel(name: string) {
  const labels: Record<string, string> = {
    read_file: '读取文件', list_dir: '列出目录', glob: '查找文件', grep: '搜索文件内容',
    edit_file: '编辑文件', write_file: '写入文件', command: '运行命令',
    command_status: '检查命令状态', command_stop: '停止命令', web_search: '搜索网页', web_fetch: '读取网页',
  };
  return labels[name] ?? `运行工具 ${name || '操作'}`;
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

function compactActivityDetail(value: unknown) {
  const detail = compactArguments(value).replace(/\s+/g, ' ').trim();
  return detail.length > 180 ? `${detail.slice(0, 177)}…` : detail;
}

export function activityDetail(name: string, value: unknown) {
  if (name === 'command_status' || name === 'command_stop') return '';
  return compactActivityDetail(value);
}

export function formatArguments(value: unknown) {
  try { return JSON.stringify(typeof value === 'string' ? JSON.parse(value) : value, null, 2); }
  catch { return String(value); }
}
