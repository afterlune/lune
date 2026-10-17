<script lang="ts">
  import type { PendingRequestView } from '../../bindings/github.com/afterlune/lune/internal/desktop/models';

  type ElicitationField = {
    type?: string;
    title?: string;
    description?: string;
    enum?: unknown[];
    minimum?: number;
    maximum?: number;
    minLength?: number;
    maxLength?: number;
  };
  type Elicitation = {
    method?: string;
    params?: {
      mode?: string;
      message?: string;
      url?: string;
      requestedSchema?: { properties?: Record<string, ElicitationField>; required?: string[] };
    };
  };
  type ElicitationResponse = { action: 'accept' | 'decline' | 'cancel'; content?: Record<string, unknown> };

  let { request, onResolve, onOpenURL }: {
    request: PendingRequestView;
    onResolve: (response: ElicitationResponse) => Promise<void>;
    onOpenURL: (target: string) => Promise<void>;
  } = $props();

  let values = $state<Record<string, string | boolean>>({});
  let error = $state('');
  let openingURL = $state(false);
  let urlOpened = $state(false);
  let submitting = $state(false);
  const interaction = $derived(parseInteraction(request.arguments));
  const fields = $derived(Object.entries(interaction?.params?.requestedSchema?.properties ?? {}).map(([name, schema]) => ({ name, schema })));
  const requiredFields = $derived(new Set(interaction?.params?.requestedSchema?.required ?? []));
  const unsupportedFields = $derived(fields.filter(({ schema }) => !['string', 'number', 'integer', 'boolean', undefined].includes(schema.type)));

  function parseInteraction(value: unknown): Elicitation | null {
    try {
      const decoded = typeof value === 'string' ? JSON.parse(value) : value;
      if (!decoded || typeof decoded !== 'object') return null;
      const result = decoded as Elicitation;
      return result.method === 'elicitation/create' && result.params ? result : null;
    } catch {
      return null;
    }
  }

  function enumValue(fieldName: string) {
    const encoded = values[fieldName];
    if (typeof encoded !== 'string' || !encoded) return undefined;
    try { return JSON.parse(encoded) as unknown; }
    catch { return undefined; }
  }

  function buildContent() {
    const content: Record<string, unknown> = {};
    for (const { name, schema } of fields) {
      const current = values[name];
      if (schema.type === 'boolean') {
        if (current !== undefined) content[name] = current === true;
        else if (requiredFields.has(name)) content[name] = false;
        continue;
      }
      if (current === undefined || current === '') {
        if (requiredFields.has(name)) throw new Error(`请填写“${schema.title || name}”`);
        continue;
      }
      if (schema.enum?.length) {
        const selected = enumValue(name);
        if (selected === undefined) throw new Error(`请选择“${schema.title || name}”`);
        content[name] = selected;
      } else if (schema.type === 'number' || schema.type === 'integer') {
        const number = Number(current);
        if (!Number.isFinite(number) || (schema.type === 'integer' && !Number.isInteger(number))) {
          throw new Error(`“${schema.title || name}”不是有效的${schema.type === 'integer' ? '整数' : '数字'}`);
        }
        if (schema.minimum !== undefined && number < schema.minimum) throw new Error(`“${schema.title || name}”不能小于 ${schema.minimum}`);
        if (schema.maximum !== undefined && number > schema.maximum) throw new Error(`“${schema.title || name}”不能大于 ${schema.maximum}`);
        content[name] = number;
      } else {
        const value = String(current);
        if (schema.minLength !== undefined && value.length < schema.minLength) throw new Error(`“${schema.title || name}”至少需要 ${schema.minLength} 个字符`);
        if (schema.maxLength !== undefined && value.length > schema.maxLength) throw new Error(`“${schema.title || name}”最多允许 ${schema.maxLength} 个字符`);
        content[name] = value;
      }
    }
    return content;
  }

  async function resolve(action: ElicitationResponse['action']) {
    error = '';
    submitting = true;
    try {
      const response: ElicitationResponse = action === 'accept' && interaction?.params?.mode === 'form'
        ? { action, content: buildContent() }
        : { action };
      await onResolve(response);
    } catch (cause) {
      error = String(cause);
    } finally {
      submitting = false;
    }
  }

  async function openURL() {
    const target = interaction?.params?.url;
    if (!target) return;
    openingURL = true;
    error = '';
    try {
      await onOpenURL(target);
      urlOpened = true;
    } catch (cause) {
      error = String(cause);
    } finally {
      openingURL = false;
    }
  }

  function enumOptionValue(value: unknown) { return JSON.stringify(value); }
  function enumOptionLabel(value: unknown) { return typeof value === 'string' ? value : JSON.stringify(value); }
</script>

<section class="request-card mcp-interaction-card">
  <div class="question-kicker">MCP 服务需要你的响应 · {request.tool || '外部服务'}</div>
  {#if interaction?.params?.message}<p class="mcp-interaction-message">{interaction.params.message}</p>{/if}

  {#if interaction?.params?.mode === 'form' && interaction.params.requestedSchema}
    <form class="mcp-interaction-form" onsubmit={(event) => { event.preventDefault(); void resolve('accept'); }}>
      {#each fields as field (field.name)}
        <label class="mcp-interaction-field">
          <span>{field.schema.title || field.name}{#if requiredFields.has(field.name)} <b>*</b>{/if}</span>
          {#if field.schema.enum?.length}
            <select value={String(values[field.name] ?? '')} required={requiredFields.has(field.name)} onchange={(event) => values = { ...values, [field.name]: event.currentTarget.value }}>
              <option value="">选择…</option>
              {#each field.schema.enum as option}<option value={enumOptionValue(option)}>{enumOptionLabel(option)}</option>{/each}
            </select>
          {:else if field.schema.type === 'boolean'}
            <input type="checkbox" checked={values[field.name] === true} onchange={(event) => values = { ...values, [field.name]: event.currentTarget.checked }} />
          {:else}
            <input type={field.schema.type === 'number' || field.schema.type === 'integer' ? 'number' : 'text'} step={field.schema.type === 'integer' ? '1' : 'any'} min={field.schema.minimum} max={field.schema.maximum} minlength={field.schema.minLength} maxlength={field.schema.maxLength} required={requiredFields.has(field.name)} value={String(values[field.name] ?? '')} oninput={(event) => values = { ...values, [field.name]: event.currentTarget.value }} />
          {/if}
          {#if field.schema.description}<small>{field.schema.description}</small>{/if}
        </label>
      {/each}
      {#if unsupportedFields.length}<p class="mcp-interaction-warning">暂不支持字段：{unsupportedFields.map((field) => field.schema.title || field.name).join('、')}。请取消请求或联系 MCP 服务配置者。</p>{/if}
      {#if error}<p class="mcp-interaction-error" role="alert">{error}</p>{/if}
      <div class="request-actions">
        <button class="quiet-button" type="button" disabled={submitting} onclick={() => void resolve('decline')}>拒绝</button>
        <button class="quiet-button" type="button" disabled={submitting} onclick={() => void resolve('cancel')}>取消</button>
        <button class="primary-button" type="submit" disabled={submitting || unsupportedFields.length > 0}>提交响应</button>
      </div>
    </form>
  {:else if interaction?.params?.mode === 'url' && interaction.params.url}
    <div class="mcp-interaction-url">
      <span>需要在浏览器中完成外部验证。链接只会在你点击后打开。</span>
      <button class="small-button" type="button" disabled={openingURL || submitting} onclick={() => void openURL()}>{openingURL ? '正在打开…' : urlOpened ? '重新打开验证链接' : '打开验证链接'}</button>
    </div>
    {#if error}<p class="mcp-interaction-error" role="alert">{error}</p>{/if}
    <div class="request-actions">
      <button class="quiet-button" disabled={submitting} onclick={() => void resolve('decline')}>拒绝</button>
      <button class="quiet-button" disabled={submitting} onclick={() => void resolve('cancel')}>取消</button>
      <button class="primary-button" disabled={submitting || !urlOpened} onclick={() => void resolve('accept')}>验证完成，继续</button>
    </div>
  {:else}
    <p class="mcp-interaction-warning">此 MCP 交互请求格式不受支持，无法安全地接受。</p>
    {#if error}<p class="mcp-interaction-error" role="alert">{error}</p>{/if}
    <div class="request-actions"><button class="quiet-button" disabled={submitting} onclick={() => void resolve('cancel')}>取消请求</button></div>
  {/if}
</section>
