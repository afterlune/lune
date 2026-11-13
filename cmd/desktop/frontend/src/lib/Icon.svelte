<script lang="ts">
  type IconName =
    | 'plus' | 'send' | 'stop' | 'search' | 'settings' | 'folder'
    | 'chevron-down' | 'chevron-right' | 'more' | 'close' | 'refresh'
    | 'edit' | 'trash' | 'chat' | 'pending' | 'process' | 'sun' | 'moon'
    | 'monitor' | 'sparkles' | 'flower' | 'sidebar-collapse' | 'sidebar-expand'
    | 'image' | 'check' | 'copy' | 'fork' | 'download';

  let {
    name,
    size = 18,
    strokeWidth = 1.75,
    class: className = '',
  }: { name: IconName; size?: number; strokeWidth?: number; class?: string } = $props();
</script>

<svg
  class={className}
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width={strokeWidth}
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
>
  {#if name === 'plus'}
    <path d="M12 5v14M5 12h14" />
  {:else if name === 'send'}
    <path d="M12 19V5" /><path d="m5 12 7-7 7 7" />
  {:else if name === 'stop'}
    <rect x="7" y="7" width="10" height="10" rx="1.6" fill="currentColor" stroke="none" />
  {:else if name === 'search'}
    <circle cx="11" cy="11" r="6.5" /><path d="m16 16 4 4" />
  {:else if name === 'settings'}
    <path d="M4 7h10M18 7h2M4 17h2M10 17h10" />
    <circle cx="16" cy="7" r="2.2" /><circle cx="8" cy="17" r="2.2" />
  {:else if name === 'folder'}
    <path d="M3.5 7.5a2 2 0 0 1 2-2h5l2 2h6a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2Z" />
  {:else if name === 'chevron-down'}
    <path d="m6 9 6 6 6-6" />
  {:else if name === 'chevron-right'}
    <path d="m9 6 6 6-6 6" />
  {:else if name === 'more'}
    <circle cx="5" cy="12" r="1.2" fill="currentColor" stroke="none" />
    <circle cx="12" cy="12" r="1.2" fill="currentColor" stroke="none" />
    <circle cx="19" cy="12" r="1.2" fill="currentColor" stroke="none" />
  {:else if name === 'close'}
    <path d="m6 6 12 12M18 6 6 18" />
  {:else if name === 'refresh'}
    <path d="M20 11a8 8 0 1 0-2.3 5.7" /><path d="M20 5v6h-6" />
  {:else if name === 'edit'}
    <path d="M4 20h4l10.5-10.5a2.1 2.1 0 0 0-3-3L5 17Z" /><path d="m13.5 6.5 3 3" />
  {:else if name === 'trash'}
    <path d="M4 7h16M9 7V5h6v2M6.5 7l.8 13h9.4l.8-13M10 11v5M14 11v5" />
  {:else if name === 'chat'}
    <path d="M5 5.5h14a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-7l-5 3v-3H5a2 2 0 0 1-2-2v-9a2 2 0 0 1 2-2Z" />
    <path d="M8 10h8M8 14h5" />
  {:else if name === 'pending'}
    <rect x="4" y="4" width="16" height="16" rx="3" /><path d="m8 12 2.4 2.4L16 9" />
  {:else if name === 'process'}
    <rect x="3.5" y="4.5" width="17" height="15" rx="2" /><path d="m7 9 3 3-3 3M13 15h4" />
  {:else if name === 'sun'}
    <circle cx="12" cy="12" r="4" />
    <path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.2 5.2l1.4 1.4M17.4 17.4l1.4 1.4M18.8 5.2l-1.4 1.4M6.6 17.4l-1.4 1.4" />
  {:else if name === 'moon'}
    <path d="M20 14.5A8.5 8.5 0 0 1 9.5 4 8.5 8.5 0 1 0 20 14.5Z" />
  {:else if name === 'monitor'}
    <rect x="3.5" y="4.5" width="17" height="12" rx="2" /><path d="M9 20h6M12 16.5V20" />
  {:else if name === 'sparkles'}
    <path d="m12 3 1.5 4.5L18 9l-4.5 1.5L12 15l-1.5-4.5L6 9l4.5-1.5Z" /><path d="M18.5 15.5 19 17l1.5.5L19 18l-.5 1.5-.5-1.5L16.5 17l1.5-.5Z" />
  {:else if name === 'flower'}
    <circle cx="12" cy="12" r="2.2" />
    <path d="M12 9.8c0-3 1-4.8 3.2-4.8 1.5 0 2.3 1.2 2.3 2.5 0 2.3-2.2 3.2-5.5 3.2M14.2 12c3 0 4.8 1 4.8 3.2 0 1.5-1.2 2.3-2.5 2.3-2.3 0-3.2-2.2-3.2-5.5M12 14.2c0 3-1 4.8-3.2 4.8-1.5 0-2.3-1.2-2.3-2.5 0-2.3 2.2-3.2 5.5-3.2M9.8 12c-3 0-4.8-1-4.8-3.2 0-1.5 1.2-2.3 2.5-2.3 2.3 0 3.2 2.2 3.2 5.5" />
  {:else if name === 'sidebar-collapse'}
    <rect x="3.5" y="4" width="17" height="16" rx="2" /><path d="M9 4v16M15 9l-3 3 3 3" />
  {:else if name === 'sidebar-expand'}
    <rect x="3.5" y="4" width="17" height="16" rx="2" /><path d="M9 4v16m4 1 3-3-3-3" />
  {:else if name === 'image'}
    <rect x="3.5" y="4.5" width="17" height="15" rx="2" /><circle cx="8.5" cy="9" r="1.5" /><path d="m4.5 17 5-5 3.5 3.5 2.5-2.5 4 4" />
  {:else if name === 'check'}
    <path d="m5 12 4.5 4.5L19 7" />
  {:else if name === 'copy'}
    <rect x="8" y="8" width="11" height="11" rx="2" /><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" />
  {:else if name === 'fork'}
    <circle cx="6" cy="6" r="2.5" /><circle cx="18" cy="6" r="2.5" /><circle cx="6" cy="18" r="2.5" /><path d="M6 8.5v7M18 8.5v1a4.5 4.5 0 0 1-4.5 4.5H6" />
  {:else if name === 'download'}
    <path d="M12 4v11m0 0-4-4m4 4 4-4" /><path d="M5 17v2a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-2" />
  {/if}
</svg>
