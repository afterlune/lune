<script lang="ts">
  import { onMount } from 'svelte';
  import * as Desktop from '../../bindings/github.com/afterlune/lune/internal/desktop/desktopservice';
  import type { WorkspaceFileEntry, WorkspaceFileSearchResult } from '../../bindings/github.com/afterlune/lune/internal/desktop/models';

  let { onPreviewFile, onError, previewMode = false }: {
    onPreviewFile: (path: string) => void;
    onError: (error: unknown) => void;
    previewMode?: boolean;
  } = $props();

  let children = $state<Record<string, WorkspaceFileEntry[]>>({});
  let expanded = $state<string[]>([]);
  let searchText = $state('');
  let searchResult = $state<WorkspaceFileSearchResult | null>(null);
  let loadingPath = $state<string | null>(null);
  let searching = $state(false);
  let searchTimer: number | undefined;
  let searchRequest = 0;
  const previewDirectories: Record<string, WorkspaceFileEntry[]> = {
    '': [
      { name: 'cmd', path: 'cmd', directory: true, size: 0 },
      { name: 'docs', path: 'docs', directory: true, size: 0 },
      { name: 'README.md', path: 'README.md', directory: false, size: 1200 },
    ],
    cmd: [{ name: 'desktop', path: 'cmd/desktop', directory: true, size: 0 }],
    'cmd/desktop': [{ name: 'main.go', path: 'cmd/desktop/main.go', directory: false, size: 4200 }],
    docs: [{ name: 'desktop.md', path: 'docs/desktop.md', directory: false, size: 2300 }],
  };

  const visibleEntries = $derived.by(() => {
    const rows: { entry: WorkspaceFileEntry; depth: number }[] = [];
    const visit = (parent: string, depth: number) => {
      for (const entry of children[parent] ?? []) {
        rows.push({ entry, depth });
        if (entry.directory && expanded.includes(entry.path)) visit(entry.path, depth + 1);
      }
    };
    visit('', 0);
    return rows;
  });

  onMount(() => { void loadDirectory(''); });

  async function loadDirectory(path: string) {
    loadingPath = path;
    if (previewMode) {
      children = { ...children, [path]: previewDirectories[path] ?? [] };
      loadingPath = null;
      return;
    }
    try {
      const entries = await Desktop.ListWorkspaceFiles(path);
      children = { ...children, [path]: entries ?? [] };
    } catch (error) { onError(error); }
    finally { if (loadingPath === path) loadingPath = null; }
  }

  async function refreshTree() {
    children = {};
    expanded = [];
    await loadDirectory('');
  }

  async function toggleDirectory(entry: WorkspaceFileEntry) {
    if (expanded.includes(entry.path)) {
      expanded = expanded.filter((path) => path !== entry.path);
      return;
    }
    expanded = [...expanded, entry.path];
    if (!(entry.path in children)) await loadDirectory(entry.path);
  }

  function updateSearch(value: string) {
    searchText = value;
    const requestID = ++searchRequest;
    if (searchTimer !== undefined) window.clearTimeout(searchTimer);
    if (!value.trim()) {
      searchResult = null;
      searching = false;
      return;
    }
    if (previewMode) {
      const files = Object.values(previewDirectories).flat().filter((entry) => entry.path.toLowerCase().includes(value.trim().toLowerCase()));
      searchResult = { files, truncated: false };
      searching = false;
      return;
    }
    searching = true;
    searchTimer = window.setTimeout(async () => {
      try {
        const result = await Desktop.SearchWorkspaceFiles(value);
        if (requestID === searchRequest) searchResult = result;
      }
      catch (error) { onError(error); }
      finally { if (requestID === searchRequest) searching = false; }
    }, 180);
  }
</script>

<section class="workspace-explorer" aria-label="工作区文件">
  <div class="workspace-file-search"><span aria-hidden="true">⌕</span><input value={searchText} oninput={(event) => updateSearch(event.currentTarget.value)} onkeydown={(event) => { if (event.key === 'Escape') updateSearch(''); }} placeholder="查找文件" aria-label="查找工作区文件" /><button aria-label="刷新文件列表" title="刷新" onclick={() => void refreshTree()}>↻</button><kbd>Esc</kbd></div>
  {#if searchText.trim()}
    <div class="workspace-file-list" aria-label="文件搜索结果">
      {#if searching}<div class="workspace-file-empty">正在查找…</div>
      {:else if searchResult?.files?.length}
        {#each searchResult.files ?? [] as entry (entry.path)}
          <button class="workspace-file-row" title={entry.path} onclick={() => onPreviewFile(entry.path)}>
            <span class="workspace-file-icon">‹›</span><span>{entry.path}</span>
          </button>
        {/each}
        {#if searchResult.truncated}<div class="workspace-file-empty">结果过多，仅显示前 200 个文件</div>{/if}
      {:else}<div class="workspace-file-empty">没有匹配的文件</div>{/if}
    </div>
  {:else}
    <div class="workspace-file-list" aria-label="工作区文件树">
      {#each visibleEntries as row (row.entry.path)}
        <button class:workspace-directory={row.entry.directory} class="workspace-file-row" title={row.entry.path} style={`--file-depth:${row.depth}`} aria-expanded={row.entry.directory ? expanded.includes(row.entry.path) : undefined} onclick={() => row.entry.directory ? void toggleDirectory(row.entry) : onPreviewFile(row.entry.path)}>
          <span class="workspace-file-icon">{row.entry.directory ? (expanded.includes(row.entry.path) ? '▾' : '▸') : '‹›'}</span><span>{row.entry.name}</span>
          {#if loadingPath === row.entry.path}<span class="workspace-file-loading">…</span>{/if}
        </button>
      {/each}
      {#if loadingPath === null && children[''] && !(children[''].length)}<div class="workspace-file-empty">此文件夹为空</div>{/if}
    </div>
  {/if}
</section>
