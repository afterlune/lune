<script lang="ts">
import { tick } from 'svelte';
import ProjectSettingsDialog from './ProjectSettingsDialog.svelte';
import ModelSettingsDialog from './ModelSettingsDialog.svelte';
import type { DesktopState } from './desktopState.svelte';
let { desktop }: { desktop: DesktopState } = $props();
let modalRoot = $state<HTMLElement | null>(null);
let wasOpen = false;
let previousFocus: HTMLElement | null = null;

function closeDialog() {
  if (desktop.modal === 'settings') {
    desktop.cancelSettings();
    return;
  }
  desktop.modal = null;
  desktop.editConfirmation = null;
  desktop.sessionActionTarget = null;
}

function focusableElements() {
  if (!modalRoot) return [];
  return [...modalRoot.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])')]
    .filter((element) => element.getClientRects().length > 0);
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    closeDialog();
    return;
  }
  if (event.key !== 'Tab') return;
  const focusables = focusableElements();
  if (!focusables.length) return;
  const first = focusables[0];
  const last = focusables.at(-1)!;
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

$effect(() => {
  const open = desktop.modal !== null;
  if (open === wasOpen) return;
  wasOpen = open;
  if (open) {
    previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    void tick().then(() => {
      if (!modalRoot) return;
      const target = modalRoot.querySelector<HTMLElement>('input:not([disabled]), textarea:not([disabled]), select:not([disabled])')
        ?? modalRoot.querySelector<HTMLElement>('.modal-footer .quiet-button:not([disabled])')
        ?? modalRoot.querySelector<HTMLElement>('button:not([disabled])');
      target?.focus();
    });
  } else {
    previousFocus?.focus();
    previousFocus = null;
  }
});
</script>
{#if desktop.modal}
  <div class="modal-backdrop" role="presentation" bind:this={modalRoot} onkeydown={handleKeydown} onclick={(event) => {
    if (event.target !== event.currentTarget) return;
    closeDialog();
  }}>
    {#if desktop.modal === 'settings'}<ModelSettingsDialog {desktop} />{:else if desktop.modal === 'rename-session' && desktop.sessionActionTarget}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="rename-session-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">SESSION</span><h2 id="rename-session-title">重命名会话</h2></div><button class="icon-button" aria-label="关闭" onclick={() => { desktop.modal = null; desktop.sessionActionTarget = null; }}>×</button></div>
        <div class="modal-body"><label class="session-title-field">会话名称<input bind:value={desktop.sessionTitleDraft} maxlength="120" onkeydown={(event) => { if (event.key === 'Enter') void desktop.saveSessionTitle(); }} /></label></div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => { desktop.modal = null; desktop.sessionActionTarget = null; }}>取消</button><button class="primary-button" disabled={desktop.busy} onclick={() => void desktop.saveSessionTitle()}>{desktop.busy ? '正在保存…' : '保存'}</button></div>
      </div>
    {:else if desktop.modal === 'delete-session' && desktop.sessionActionTarget}
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="delete-session-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">SESSION</span><h2 id="delete-session-title">删除会话？</h2></div><button class="icon-button" aria-label="关闭" onclick={() => { desktop.modal = null; desktop.sessionActionTarget = null; }}>×</button></div>
        <div class="modal-body"><p>“{desktop.sessionActionTarget.title || '新会话'}”的对话、执行记录和待运行消息将被删除。此操作无法撤销。删除当前会话时会自动切换到其他会话；若这是最后一个会话，会先创建一个新会话。</p></div>
        <div class="modal-footer"><button class="quiet-button" onclick={() => { desktop.modal = null; desktop.sessionActionTarget = null; }}>取消</button><button class="danger-button" disabled={desktop.busy} onclick={() => void desktop.confirmDeleteSession()}>{desktop.busy ? '正在删除…' : '删除会话'}</button></div>
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
