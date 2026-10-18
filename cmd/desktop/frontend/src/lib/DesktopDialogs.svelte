<script lang="ts">
import ProjectSettingsDialog from './ProjectSettingsDialog.svelte';
import ModelSettingsDialog from './ModelSettingsDialog.svelte';
import type { DesktopState } from './desktopState.svelte';
let { desktop }: { desktop: DesktopState } = $props();
</script>
{#if desktop.modal}
  <div class="modal-backdrop" role="presentation" onclick={(event) => {
    if (event.target !== event.currentTarget) return;
    if (desktop.modal === 'settings') desktop.cancelSettings();
    else { desktop.modal = null; desktop.editConfirmation = null; desktop.worktreeApplyConfirmation = null; desktop.worktreeApplyError = ''; desktop.worktreeRemovalTarget = null; desktop.worktreeRemovalError = ''; }
  }}>
    {#if desktop.modal === 'settings'}<ModelSettingsDialog {desktop} />{:else if desktop.modal === 'apply-worktree' && desktop.worktreeApplyConfirmation}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="worktree-apply-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">WORKTREE RESULT</span><h2 id="worktree-apply-title">将所选改动应用到原项目？</h2></div><button class="icon-button" aria-label="关闭" onclick={() => { desktop.modal = null; desktop.worktreeApplyConfirmation = null; desktop.worktreeApplyError = ''; }}>×</button></div>
        <div class="modal-body worktree-apply-body">
          <p>目标目录：<code>{desktop.worktreeApplyConfirmation.destination}</code></p>
          <p>将应用 {desktop.worktreeApplyConfirmation.files.length} 个文件。应用前会检查整批变更；有冲突时不会修改原项目。改动不会暂存或提交，Worktree 会保留。</p>
          {#if desktop.permissionMode === 'ask'}<p class="worktree-apply-permission">当前为“每次询问”。确认即授权本次将以下改动写入原项目。</p>{/if}
          <ul class="worktree-apply-file-list">
            {#each desktop.worktreeApplyConfirmation.files as file (file.path)}
              <li><span class={`workspace-change-status ${desktop.changeStatusLabel(file.status).toLowerCase()}`}>{desktop.changeStatusLabel(file.status)}</span><code>{file.path}</code>{#if file.originalPath}<small>来自 {file.originalPath}</small>{/if}</li>
            {/each}
          </ul>
          {#if desktop.worktreeApplyError}<div class="worktree-apply-error" role="alert">{desktop.worktreeApplyError}</div>{/if}
        </div>
        <div class="modal-footer"><button class="quiet-button" disabled={desktop.applyingWorktreeChanges} onclick={() => { desktop.modal = null; desktop.worktreeApplyConfirmation = null; desktop.worktreeApplyError = ''; }}>取消</button><button class="primary-button" disabled={desktop.applyingWorktreeChanges} onclick={() => void desktop.confirmWorktreeApply()}>{desktop.applyingWorktreeChanges ? '正在检查并应用…' : '确认应用'}</button></div>
      </div>
    {:else if desktop.modal === 'rename-session' && desktop.sessionActionTarget}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="rename-session-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">SESSION</span><h2 id="rename-session-title">重命名会话</h2></div><button class="icon-button" aria-label="关闭" onclick={() => { desktop.modal = null; desktop.sessionActionTarget = null; }}>×</button></div>
        <div class="modal-body"><label class="session-title-field">会话名称<input bind:value={desktop.sessionTitleDraft} maxlength="120" onkeydown={(event) => { if (event.key === 'Enter') void desktop.saveSessionTitle(); }} /></label></div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => { desktop.modal = null; desktop.sessionActionTarget = null; }}>取消</button><button class="primary-button" disabled={desktop.busy} onclick={() => void desktop.saveSessionTitle()}>{desktop.busy ? '正在保存…' : '保存'}</button></div>
      </div>
    {:else if desktop.modal === 'delete-session' && desktop.sessionActionTarget}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="delete-session-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">SESSION</span><h2 id="delete-session-title">删除会话？</h2></div><button class="icon-button" aria-label="关闭" onclick={() => { desktop.modal = null; desktop.sessionActionTarget = null; }}>×</button></div>
        <div class="modal-body"><p>“{desktop.sessionActionTarget.title || '新会话'}”的对话、执行记录和待运行消息将被删除。此操作无法撤销。Worktree 会话的 Git 工作目录会保留，需自行用 Git 检查和清理。删除当前会话时会自动切换到其他会话；若这是最后一个会话，会先创建一个新会话。</p></div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => { desktop.modal = null; desktop.sessionActionTarget = null; }}>取消</button><button class="danger-button" disabled={desktop.busy} onclick={() => void desktop.confirmDeleteSession()}>{desktop.busy ? '正在删除…' : '删除会话'}</button></div>
      </div>
    {:else if desktop.modal === 'delete-worktree' && desktop.worktreeRemovalTarget}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="delete-worktree-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">DESKTOP WORKTREE</span><h2 id="delete-worktree-title">{desktop.worktreeRemovalTarget.sessions.length ? '删除关联会话并清理工作树？' : '清理孤儿工作树？'}</h2></div><button class="icon-button" aria-label="关闭" disabled={desktop.busy} onclick={() => { desktop.modal = null; desktop.worktreeRemovalTarget = null; desktop.worktreeRemovalError = ''; }}>×</button></div>
        <div class="modal-body desktop-worktree-confirm-body">
          <p>目标路径：<code>{desktop.worktreeRemovalTarget.path}</code></p>
          {#if desktop.worktreeRemovalTarget.requiresLocalSwitch}<p>当前会话位于此工作树；确认后会先切换到已有 Local 会话，没有时会创建一个。</p>{/if}
          {#if desktop.worktreeRemovalTarget.sessions.length}
            <p>此操作会删除该工作树下所有关联会话的对话、执行历史、待运行消息和 Core 状态，再调用 Git 清理工作树：</p>
            <ul>{#each desktop.worktreeRemovalTarget.sessions as associated (associated.id)}<li>{associated.title || '新会话'} <small>({desktop.desktopWorktreeSessionStatusLabel(associated.status)})</small></li>{/each}</ul>
          {:else}
            <p>此工作树没有关联的 Desktop 会话。只会调用 Git 清理此工作树目录。</p>
          {/if}
          <p>仅干净且已停止的工作树可清理。暂存、未暂存、未跟踪或忽略文件都会阻止操作并保留原文件。</p>
          {#if desktop.worktreeRemovalError}<div class="worktree-apply-error" role="alert">{desktop.worktreeRemovalError}</div>{/if}
        </div>
        <div class="modal-footer"><button class="quiet-button" disabled={desktop.busy} onclick={() => { desktop.modal = null; desktop.worktreeRemovalTarget = null; desktop.worktreeRemovalError = ''; }}>取消</button><button class="danger-button" disabled={desktop.busy || !desktop.worktreeRemovalTarget.canRemove} onclick={() => void desktop.confirmDesktopWorktreeRemoval()}>{desktop.busy ? '正在清理…' : desktop.worktreeRemovalTarget.sessions.length ? `删除 ${desktop.worktreeRemovalTarget.sessions.length} 个会话并清理工作树` : '清理孤儿工作树'}</button></div>
      </div>
    {:else if desktop.modal === 'file-preview' && desktop.filePreview}
      <div class="modal file-preview-modal" role="dialog" aria-modal="true" aria-labelledby="file-preview-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">WORKSPACE FILE</span><h2 id="file-preview-title">{desktop.filePreview.path}</h2></div><button class="icon-button" aria-label="关闭" onclick={() => desktop.modal = null}>×</button></div>
        <div class="modal-body"><pre class="workspace-file-preview">{desktop.filePreview.content}</pre>{#if desktop.filePreview.truncated}<div class="workspace-preview-truncated">文件较大，仅显示前 1 MiB。</div>{/if}</div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => desktop.modal = null}>关闭</button><button class="primary-button" disabled={desktop.previewMode} onclick={() => void desktop.openFileInDefaultApp()}>在默认应用中打开</button></div>
      </div>
    {:else if desktop.modal === 'mcp'}<ProjectSettingsDialog {desktop} />{:else if desktop.modal === 'switch' || desktop.modal === 'close'}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">TASK IN PROGRESS</span><h2>{desktop.modal === 'switch' ? '中断当前任务并切换？' : '中断当前任务并关闭？'}</h2></div><button class="icon-button" aria-label="关闭" onclick={() => desktop.modal = null}>×</button></div>
        <div class="modal-body"><p>当前任务还在运行。确认后会请求中断，并等待 Core 完成清理后再{desktop.modal === 'switch' ? '打开另一个工作区' : '关闭工作区'}。</p></div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => desktop.modal = null}>继续等待</button><button class="danger-button" disabled={desktop.busy} onclick={() => desktop.modal === 'switch' ? void desktop.confirmWorkspaceSwitch() : void desktop.confirmCloseWorkspace()}>{desktop.busy ? '正在停止…' : '中断并继续'}</button></div>
      </div>
    {:else if desktop.modal === 'edit' && desktop.editConfirmation}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="edit-confirm-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">EDIT CONVERSATION</span><h2 id="edit-confirm-title">确认编辑这条消息？</h2></div><button class="icon-button" aria-label="关闭" onclick={() => { desktop.modal = null; desktop.editConfirmation = null; }}>×</button></div>
        <div class="modal-body"><p>{desktop.editConfirmation.draft.wasQueued
          ? '如果这条消息仍在队列中，会保留当前任务和它之前的队列项，在原位置替换，并取消它之后的排队消息；如果它已开始，系统会中断当前任务并从此消息重跑。'
          : desktop.editConfirmation.pendingAtConfirm
            ? '确认后会立即中断当前任务并取消所有排队消息，再从这条消息开始回退并重跑。'
            : '系统会从这条消息开始回退并重新运行。'}已发生的文件操作不会撤销。</p></div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => { desktop.modal = null; desktop.editConfirmation = null; }}>继续当前对话</button><button class="danger-button" onclick={desktop.confirmMessageEdit}>{desktop.editConfirmation.draft.wasQueued ? '替换队列消息' : desktop.editConfirmation.pendingAtConfirm ? '中断并回退重跑' : '回退并重跑'}</button></div>
      </div>
    {/if}
  </div>
{/if}
