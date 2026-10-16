<script lang="ts">
  import { renderMarkdown } from './renderMarkdown';

  let { source, live = false }: { source: string; live?: boolean } = $props();
  let html = $state('');
  let lastRenderAt = 0;

  $effect(() => {
    const currentSource = source;
    if (!live) {
      html = renderMarkdown(currentSource);
      lastRenderAt = Date.now();
      return;
    }

    const delay = Math.max(0, 80 - (Date.now() - lastRenderAt));
    if (delay === 0) {
      html = renderMarkdown(currentSource);
      lastRenderAt = Date.now();
      return;
    }

    const timer = window.setTimeout(() => {
      html = renderMarkdown(source);
      lastRenderAt = Date.now();
    }, delay);
    return () => window.clearTimeout(timer);
  });
</script>

<div class="markdown-body">{@html html}</div>
