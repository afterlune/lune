<script lang="ts">
  let { size = 20, class: className = '' }: { size?: number; class?: string } = $props();
</script>

<svg class={className} width={size} height={size} viewBox="0 0 32 32" fill="none" aria-hidden="true">
  <path d="M21.4 3.6A12.4 12.4 0 1 0 28.7 25 9.6 9.6 0 0 1 21.4 3.6Z" fill="currentColor" />
  <circle cx="25.3" cy="8.1" r="2.15" fill="currentColor" opacity=".48" />
  <circle cx="9.2" cy="23.9" r="1.05" fill="currentColor" opacity=".38" />
</svg>
