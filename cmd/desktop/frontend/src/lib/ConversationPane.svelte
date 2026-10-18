<script lang="ts">
import Icon from './Icon.svelte';
import type { DesktopState } from './desktopState.svelte';
import Markdown from './Markdown.svelte';
import ExecutionActivity from './ExecutionActivity.svelte';
import MCPInteractionCard from './MCPInteractionCard.svelte';
import Composer from './Composer.svelte';
import EmptyConversation from './EmptyConversation.svelte';
let { desktop }: { desktop: DesktopState } = $props();
</script>
{#if desktop.session}

        <div class="conversation-scroll" id="conversation-scroll">
          <div class="conversation-content">
            {#if desktop.messages.length === 0 && desktop.activities.length === 0 && !desktop.running && desktop.pending.length === 0 && !desktop.planReview}
              <EmptyConversation />
            {:else if desktop.messages.length === 0}
              <div class="conversation-intro"><p>可以在 <b>{desktop.workspace.split(/[\\/]/).at(-1)}</b> 中阅读、修改和运行代码。</p></div>
            {/if}
            {#each desktop.executionTimeline.leadingMessages as message (message.id)}
              <article data-message-id={message.id} class:search-match={desktop.sessionSearchHighlightSession === desktop.session?.id && desktop.sessionSearchHighlightMessage === message.id} class="message-row">
                <div class="message-body">
                  <div class="message-text"><Markdown source={message.text} /></div>
                  {#if message.images?.length}<div class="message-images">{#each message.images as image}<img src={image} alt="消息附件" />{/each}</div>{/if}
                </div>
              </article>
            {/each}

            {#each desktop.executionTimeline.blocks as block (block.key)}
              <section data-execution-id={block.executionId || undefined} class:execution-unit-active={desktop.executionBlockState(block) === 'running' || desktop.executionBlockState(block) === 'waiting'} class="execution-unit" aria-label="一次任务执行">
                <header class="execution-unit-heading">
                  <span class="execution-unit-title">本次执行</span>
                  <span class={`execution-unit-status ${desktop.executionBlockStateClass(block)}`}>{desktop.executionBlockLabel(block)}</span>
                </header>
                {#if block.userMessage}
                  {@const message = block.userMessage}
                  <article data-message-id={message.id} class:search-match={desktop.sessionSearchHighlightSession === desktop.session?.id && desktop.sessionSearchHighlightMessage === message.id} class="message-row from-user">
                    <div class="message-body">
                      <div class="message-text"><div class="plain-text">{message.text}</div></div>
                      {#if message.images?.length}<div class="message-images">{#each message.images as image}<img src={image} alt="消息附件" />{/each}</div>{/if}
                      {#if message.interrupted}<span class="interrupted-message-state">本轮已中断</span>{/if}
                      {#if message.deliveryState === 'failed'}
                        <div class="message-delivery-error"><span>{message.deliveryError || '消息未能发送'}</span><button class="small-button" onclick={() => void desktop.retryMessage(message)}>重试</button></div>
                      {:else if message.deliveryState === 'queued'}
                        <div class="message-delivery-pending">排队中{message.queuePosition !== undefined ? ` · 第 ${message.queuePosition} 位` : ''}</div>
                      {:else if message.deliveryState === 'sending'}
                        <div class="message-delivery-pending">正在发送…</div>
                      {:else if message.deliveryState === 'cancelled'}
                        <div class="message-delivery-cancelled">排队消息已撤回</div>
                      {/if}
                      <div class="message-actions">
                        {#if message.deliveryState === 'queued'}<button class="message-cancel-button" aria-label="撤回排队消息" disabled={desktop.cancellingQueuedExecution === message.executionID} onclick={() => void desktop.cancelQueuedMessage(message)}>{desktop.cancellingQueuedExecution === message.executionID ? '正在撤回…' : '撤回'}</button>{/if}
                        {#if desktop.canEditMessages && (message.deliveryState === 'queued' || message.deliveryState === 'submitted' || message.deliveryState === undefined)}<button class="message-edit-button" aria-label="编辑消息" onclick={() => desktop.startEditingMessage(message, block.userMessageIndex)}>编辑</button>{/if}
                      </div>
                    </div>
                  </article>
                {/if}

                {#each block.activityRuns as run (run.key)}
                  <ExecutionActivity {run} active={run.executionId === desktop.executionID && desktop.running} elapsedSeconds={run.executionId === desktop.executionID ? desktop.elapsedSeconds : run.elapsedSeconds ?? 0} onToggle={() => desktop.toggleActivityRun(run.key)} />
                {/each}

                {#if block.streamText}
                  <article class="message-row execution-stream-row"><div class="message-body"><div class="message-text"><Markdown source={block.streamText} live /><span class="stream-caret"></span></div></div></article>
                {/if}

                {#each block.assistantMessages as message (message.id)}
                  <article data-message-id={message.id} class:search-match={desktop.sessionSearchHighlightSession === desktop.session?.id && desktop.sessionSearchHighlightMessage === message.id} class="message-row">
                    <div class="message-body">
                      <div class="message-text"><Markdown source={message.text} /></div>
                      {#if message.images?.length}<div class="message-images">{#each message.images as image}<img src={image} alt="消息附件" />{/each}</div>{/if}
                      {#if message.interrupted}<span class="interrupted-message-state">本轮已中断</span>{/if}
                      {#if message.text}
                        <div class="message-actions assistant-message-actions">
                          <button class="message-edit-button" aria-label="复制回复" title={desktop.copiedMessageID === message.id ? '已复制' : '复制回复'} onclick={() => void desktop.copyAssistantMessage(message)}><Icon name={desktop.copiedMessageID === message.id ? 'check' : 'copy'} size={14} /></button>
                          {#if desktop.canRegenerateAssistant(message)}<button class="message-edit-button" aria-label="重新生成回复" title="重新生成回复" onclick={() => void desktop.regenerateAssistantMessage(message)}><Icon name="refresh" size={14} /></button>{/if}
                        </div>
                      {/if}
                    </div>
                  </article>
                {/each}

                {#each block.pendingRequests.filter((request) => request.kind === 'approval') as approval (approval.id)}
                  <section class="request-card approval-card execution-action-card">
                    <div class="request-heading"><span class="request-icon">!</span><div><strong>需要授权</strong><p>Agent 想要运行工具 <code>{approval.tool}</code></p></div></div>
                    {#if approval.arguments}<pre>{desktop.formatArguments(approval.arguments)}</pre>{/if}
                    <div class="request-actions"><button class="quiet-button" onclick={() => void desktop.resolveApproval(approval, false)}>拒绝</button><button class="primary-button" onclick={() => void desktop.resolveApproval(approval, true)}>允许本次操作</button></div>
                  </section>
                {/each}

                {#each block.pendingRequests.filter((request) => request.kind === 'user_question') as questionRequest (questionRequest.id)}
                  <section class="request-card question-card execution-action-card">
                    <div class="question-kicker">Agent 需要你的选择</div>
                    {#each questionRequest.questions ?? [] as question (question.id)}
                      <div class="question-item">
                        <h3>{question.question}</h3>
                        {#if question.options?.length}
                          <div class="question-options">
                            {#each question.options as option (option.label)}
                              <label class:selected-option={desktop.answerValues[question.id] === option.label} class="question-option"><input type="radio" name={question.id} value={option.label} bind:group={desktop.answerValues[question.id]} /><span class="radio-mark"></span><span><b>{option.label}</b>{#if option.description}<small>{option.description}</small>{/if}</span></label>
                            {/each}
                          </div>
                        {/if}
                        <input class="notes-input" placeholder={question.options?.length ? '补充说明（可选）' : '输入你的回答'} bind:value={desktop.noteValues[question.id]} />
                      </div>
                    {/each}
                    <div class="request-actions"><span class="request-hint">你的回答会继续当前任务</span><button class="primary-button" onclick={() => void desktop.answerQuestion(questionRequest)}>提交回答</button></div>
                  </section>
                {/each}

                {#each block.pendingRequests.filter((request) => request.kind === 'tool_recovery' || request.kind === 'model_recovery' || request.kind === 'execution_blocker') as request (request.id)}
                  <section class="request-card recovery-card execution-action-card"><strong>{request.kind === 'model_recovery' ? '模型请求未完成' : request.kind === 'execution_blocker' ? '执行受阻' : '工具执行暂停'}</strong><p>{request.reason || request.tool || '此操作需要恢复。'}</p><div class="request-actions"><button class="quiet-button" onclick={() => void desktop.dismissRecovery(request)}>{request.kind === 'tool_recovery' ? '跳过失败步骤' : '丢弃本轮'}</button><button class="primary-button" onclick={() => void desktop.recover(request)}>{request.kind === 'execution_blocker' ? '恢复执行' : '重试'}</button></div></section>
                {/each}

                {#each block.pendingRequests.filter((request) => request.kind === 'user_interaction') as request (request.id)}
                  <MCPInteractionCard {request} onResolve={(response) => desktop.resolveUserInteraction(request, response)} onOpenURL={(target) => desktop.openMCPInteractionURL(request, target)} />
                {/each}

                {#if block.planReview}
                  <section class="plan-review execution-action-card">
                    <div class="plan-title"><span>方案已完成</span><span class="plan-badge">PLAN</span></div>
                    <div class="plan-body">{#each block.planReview.text.split('\n') as line}<p class:plan-heading={line.startsWith('#')} class:plan-list={line.trimStart().startsWith('- ') || line.trimStart().startsWith('* ')}>{line.replace(/^#{1,3}\s*/, '').replace(/^[-*]\s*/, '• ') || '\u00a0'}</p>{/each}</div>
                    <div class="plan-question"><strong>要开始实施这个方案吗？</strong><span>实施时会切换到工作区权限。</span></div>
                    <div class="plan-options">
                      <button onclick={() => void desktop.implementPlan('same_context')}><span class="plan-option-number">1</span><span><b>是，实施方案</b><small>在当前会话开始编码</small></span><span class="plan-arrow">↵</span></button>
                      <button onclick={() => void desktop.implementPlan('new_context')}><span class="plan-option-number">2</span><span><b>清空上下文并实施</b><small>在新会话中开始，保留本方案</small></span><span class="plan-arrow">↵</span></button>
                      <button onclick={desktop.stayInPlan}><span class="plan-option-number">3</span><span><b>继续规划</b><small>留在只读 Plan 模式</small></span><span class="plan-arrow">↵</span></button>
                    </div>
                  </section>
                {/if}
              </section>
            {/each}
          </div>
        </div>

        <Composer {desktop} />
      
{/if}
