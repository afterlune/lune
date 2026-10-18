<script lang="ts">
import type { DesktopState } from './desktopState.svelte';
let { desktop }: { desktop: DesktopState } = $props();
</script>

<div class="composer-wrap">
  {#if !desktop.models.configured}
    <button class="setup-notice" onclick={desktop.setSettingsForm}><span>ⓘ</span> 配置一个模型后即可开始对话 <b>打开设置 →</b></button>
  {/if}
  {#if desktop.editDraft}<div class="edit-mode-banner">正在编辑第 {desktop.editDraft.userMessageIndex + 1} 条用户消息。保存后会移除该消息之后的对话并重新运行。</div>{/if}
  {#if desktop.attachments.length}
    <div class="attachment-strip">{#each desktop.attachments as attachment, index}<div class="attachment-chip"><img src={attachment.preview} alt={attachment.name} /><span>{attachment.name}</span><button aria-label="移除图片" disabled={!desktop.canWrite} onclick={() => desktop.attachments = desktop.attachments.filter((_, i) => i !== index)}>×</button></div>{/each}</div>
  {/if}
  <div class:composer-disabled={!desktop.models.configured} class="composer">
    <textarea bind:value={desktop.composerText} onkeydown={desktop.handleComposerKey} placeholder={desktop.editDraft ? '编辑消息…' : !desktop.models.configured ? '先配置模型以开始对话' : desktop.pending.length ? '请先处理上方的请求' : desktop.planReview ? '请先选择方案操作' : '发送消息…'} disabled={!desktop.canWrite} rows="1" aria-label="消息输入"></textarea>
    <div class="composer-toolbar">
      <div class="composer-left">
        <button class="composer-tool" title="添加图片" aria-label="添加图片" onclick={() => document.getElementById('image-picker')?.click()} disabled={!desktop.canWrite}><span>＋</span><span>添加图片</span></button>
        <input id="image-picker" type="file" accept="image/png,image/jpeg,image/gif,image/webp" multiple hidden onchange={(event) => event.currentTarget.files && void desktop.addFiles(event.currentTarget.files)} />
        <span class="composer-separator"></span>
        <button class:plan-active={desktop.planMode} class="plan-toggle" onclick={() => void desktop.setPlanMode(!desktop.planMode)} disabled={desktop.switchingSession || desktop.controlsPending || desktop.workOutstanding || !!desktop.editDraft}><span>◈</span> Plan 模式</button>
      </div>
      <div class="composer-right">
        {#if desktop.workspace}
          <select class="permission-profile-select" value={desktop.permissionMode} disabled={desktop.switchingSession || desktop.controlsPending || desktop.planMode || desktop.workOutstanding} aria-label="工具权限" onchange={(event) => { const value = event.currentTarget.value; event.currentTarget.value = desktop.permissionMode; void desktop.setPermission(value); }}>
            <option value="read-only">权限 · 只读</option><option value="workspace">权限 · 工作区</option><option value="ask">权限 · 每次询问</option><option value="full-access">权限 · 完全访问</option>
          </select>
        {/if}
        {#if desktop.models.configured && desktop.models.profiles?.length && desktop.session}
          <select class="model-profile-select" value={desktop.session.settings.model_profile || desktop.models.defaultModel} disabled={desktop.switchingSession || desktop.controlsPending || desktop.workOutstanding} aria-label="会话模型" onchange={(event) => { const value = event.currentTarget.value; event.currentTarget.value = desktop.session?.settings.model_profile || desktop.models.defaultModel; void desktop.setSessionModel(value); }}>
            {#each desktop.models.profiles as profile}<option value={profile.name}>模型 · {profile.name}</option>{/each}
          </select>
        {:else}<span class="model-label">未配置模型</span>{/if}
        {#if desktop.running}<button class="stop-button" aria-label="中断任务" onclick={() => void desktop.interruptTask()} disabled={desktop.interrupting} title="中断任务">{desktop.interrupting ? '正在中断…' : '■ 中断'}</button>{/if}
        {#if desktop.editDraft}
          <button class="quiet-button" onclick={desktop.cancelEditingMessage} disabled={desktop.busy}>取消</button>
          <button class="send-button" onclick={() => void desktop.sendMessage()} disabled={!desktop.canWrite || (!desktop.composerText.trim() && !desktop.attachments.length)} aria-label="保存修改">保存</button>
        {:else}
          <button class="send-button" onclick={() => void desktop.sendMessage()} disabled={!desktop.canSend || (!desktop.composerText.trim() && !desktop.attachments.length)} aria-label="发送"><span>↑</span></button>
        {/if}
      </div>
    </div>
  </div>
  <div class="composer-footnote">luneAgent 可能会出错，请检查重要信息。<span>Enter 发送 · Shift+Enter 换行{desktop.running || desktop.queuedMessageCount > 0 ? ' · 运行中发送将排队' : ''}</span></div>
</div>
