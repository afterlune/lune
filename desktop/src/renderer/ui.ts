export function cn(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(' ')
}

export const btnPrimary =
  'inline-flex items-center justify-center gap-1.5 min-h-[38px] px-4 text-accent-fg border-0 rounded-[12px] bg-accent shadow-elev-sm cursor-pointer text-[13.5px] font-medium tracking-[-0.01em] transition-[background,transform,opacity] duration-120 hover:not-disabled:bg-accent-hover active:not-disabled:scale-[0.985] disabled:opacity-40 disabled:cursor-not-allowed'

export const btnPrimaryCompact =
  'inline-flex items-center justify-center gap-1.5 min-h-[32px] px-3 text-accent-fg border-0 rounded-[10px] bg-accent cursor-pointer text-[12.5px] font-medium tracking-[-0.01em] transition-[background,transform,opacity] duration-120 hover:not-disabled:bg-accent-hover active:not-disabled:scale-[0.985] disabled:opacity-40 disabled:cursor-not-allowed'

export const btnPrimaryDanger =
  'inline-flex items-center justify-center gap-1.5 min-h-[32px] px-3 text-white border-0 rounded-[10px] bg-danger cursor-pointer text-[12.5px] font-medium transition-[opacity,transform] duration-120 hover:not-disabled:opacity-90 active:not-disabled:scale-[0.985] disabled:opacity-40 disabled:cursor-not-allowed'

export const btnSecondary =
  'inline-flex items-center justify-center gap-1.5 min-h-[32px] px-3 text-ink border-[0.5px] border-line rounded-[10px] bg-transparent cursor-pointer text-[12.5px] font-medium transition-[background,border-color,transform] duration-120 hover:not-disabled:bg-hover active:not-disabled:scale-[0.985] disabled:opacity-40 disabled:cursor-not-allowed'

export const btnPlain =
  'inline-flex items-center justify-center gap-1.5 min-h-[28px] px-[9px] text-ink-secondary border-0 rounded-[8px] bg-transparent cursor-pointer text-[12.5px] font-medium transition-[background,color] duration-120 hover:not-disabled:text-ink hover:not-disabled:bg-hover disabled:opacity-40 disabled:cursor-not-allowed'

export const btnPlainDanger =
  'inline-flex items-center justify-center gap-1.5 min-h-[28px] px-[9px] text-ink-secondary border-0 rounded-[8px] bg-transparent cursor-pointer text-[12.5px] font-medium transition-[background,color] duration-120 hover:not-disabled:text-danger hover:not-disabled:bg-danger-bg disabled:opacity-40 disabled:cursor-not-allowed'

export const btnIcon =
  'inline-grid place-items-center w-[32px] h-[32px] text-ink-secondary border-0 rounded-[8px] bg-transparent cursor-pointer transition-[background,color] duration-120 hover:not-disabled:text-ink hover:not-disabled:bg-hover aria-[pressed=true]:text-ink aria-[pressed=true]:bg-hover aria-[expanded=true]:text-ink aria-[expanded=true]:bg-hover disabled:opacity-40 disabled:cursor-not-allowed'

export const btnSubtleIcon =
  'inline-grid place-items-center w-[26px] h-[26px] text-ink-secondary border-0 rounded-[7px] bg-transparent cursor-pointer transition-[background,color] duration-120 hover:not-disabled:text-ink hover:not-disabled:bg-hover disabled:opacity-40 disabled:cursor-not-allowed'

export const fieldInput =
  'w-full min-w-0 h-[36px] px-[12px] text-ink border-[0.5px] border-line-strong rounded-[12px] bg-canvas text-[13px] font-normal transition-[border-color,box-shadow,background] duration-120 hover:not-disabled:border-line-strong focus:outline-none focus:border-focal focus:shadow-[0_0_0_2px_var(--focus-halo)] disabled:opacity-60'

export const fieldSelect =
  'lune-select w-full min-w-0 h-[36px] pl-[12px] pr-[30px] text-ink border-[0.5px] border-line-strong rounded-[12px] bg-canvas text-[13px] font-normal cursor-pointer transition-[border-color,box-shadow,background] duration-120 hover:not-disabled:border-line-strong focus:outline-none focus:border-focal focus:shadow-[0_0_0_2px_var(--focus-halo)] disabled:opacity-60'

export const fieldTextarea =
  'w-full min-w-0 p-[9px_12px] text-ink border-[0.5px] border-line-strong rounded-[12px] bg-canvas text-[13px] leading-[1.55] transition-[border-color,box-shadow] duration-120 hover:not-disabled:border-line-strong focus:outline-none focus:border-focal focus:shadow-[0_0_0_2px_var(--focus-halo)] disabled:opacity-60'

export const subtleCard =
  'border-[0.5px] border-line-subtle rounded-[16px] bg-subtle transition-[border-color] duration-150'

export const surfaceCard =
  'border-[0.5px] border-line rounded-[16px] bg-canvas'

export const statusPill =
  'inline-flex items-center px-[7px] py-[1px] border-[0.5px] border-line rounded-full bg-subtle text-ink-secondary font-mono text-[11px] tabular-nums whitespace-nowrap'

export const badgeMono =
  'inline-flex items-center px-[6px] py-[1px] border-[0.5px] border-line-subtle rounded-[6px] bg-muted text-ink-secondary font-mono text-[11px] tabular-nums whitespace-nowrap'

export const popoverSurface =
  'border-0 rounded-[16px] bg-[var(--bg-glass)] backdrop-blur-[40px] backdrop-saturate-150 shadow-elev-md'

export const bannerError =
  'm-0 px-[12px] py-[9px] rounded-[12px] border-[0.5px] border-danger-border bg-danger-bg text-danger text-[12.5px] leading-[1.45]'

export const bannerStatus =
  'm-0 px-[12px] py-[9px] rounded-[12px] border-[0.5px] border-line bg-subtle text-ink-secondary text-[12.5px] leading-[1.45]'

export const backdropOverlay =
  'fixed inset-0 z-30 grid place-items-center p-5 bg-black/28 animate-[fade_.15s_ease_both]'
