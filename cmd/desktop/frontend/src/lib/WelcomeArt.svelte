<script lang="ts">
  let { class: className = '' }: { class?: string } = $props();
</script>

<svg class={className} viewBox="0 0 420 300" fill="none" aria-hidden="true">
  <defs>
    <radialGradient id="welcome-aura" cx="0" cy="0" r="1" gradientUnits="userSpaceOnUse" gradientTransform="translate(210 126) rotate(90) scale(150)">
      <stop stop-color="var(--art-blue)" stop-opacity=".2" />
      <stop offset=".58" stop-color="var(--art-lilac)" stop-opacity=".07" />
      <stop offset="1" stop-color="var(--art-blue)" stop-opacity="0" />
    </radialGradient>
    <linearGradient id="welcome-orbit" x1="83" y1="72" x2="342" y2="244" gradientUnits="userSpaceOnUse">
      <stop stop-color="var(--art-blue)" stop-opacity=".7" />
      <stop offset=".5" stop-color="var(--art-lilac)" stop-opacity=".24" />
      <stop offset="1" stop-color="var(--art-warm)" stop-opacity=".4" />
    </linearGradient>
    <filter id="welcome-blur" x="-40%" y="-40%" width="180%" height="180%">
      <feGaussianBlur stdDeviation="18" />
    </filter>
  </defs>
  <ellipse cx="210" cy="128" rx="184" ry="130" fill="url(#welcome-aura)" />
  <g opacity=".55">
    <ellipse cx="210" cy="146" rx="164" ry="76" transform="rotate(-18 210 146)" stroke="url(#welcome-orbit)" stroke-width="1" stroke-dasharray="2 9" />
    <ellipse cx="210" cy="146" rx="124" ry="58" transform="rotate(24 210 146)" stroke="var(--line)" stroke-width="1" stroke-dasharray="1 11" />
    <circle cx="210" cy="146" r="74" stroke="var(--line)" stroke-width=".8" stroke-dasharray="1 13" />
  </g>
  <g filter="url(#welcome-blur)" opacity=".42">
    <circle cx="292" cy="105" r="38" fill="var(--art-blue)" />
    <circle cx="139" cy="183" r="30" fill="var(--art-warm)" />
  </g>
  <g opacity=".9">
    <circle cx="118" cy="96" r="2" fill="var(--art-blue)" />
    <circle cx="315" cy="205" r="2.4" fill="var(--art-lilac)" />
    <circle cx="98" cy="207" r="1.4" fill="var(--text)" opacity=".35" />
    <circle cx="352" cy="126" r="1.2" fill="var(--art-blue)" opacity=".55" />
    <path d="m184 61 2.2 6.2 6.2 2.2-6.2 2.2-2.2 6.2-2.2-6.2-6.2-2.2 6.2-2.2Z" fill="var(--text)" opacity=".22" />
    <path d="m123 226 1.5 4.2 4.2 1.5-4.2 1.5-1.5 4.2-1.5-4.2-4.2-1.5 4.2-1.5Z" fill="var(--art-warm)" opacity=".5" />
  </g>
  <g opacity=".4">
    <path d="M52 152h34M68 136v32" stroke="var(--line)" stroke-width=".8" />
    <path d="M340 65h20M350 55v20" stroke="var(--line)" stroke-width=".8" />
  </g>
</svg>
