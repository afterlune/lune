export function cn(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(' ')
}

export const btnPrimary =
  'inline-flex items-center justify-center gap-1.5 min-h-[38px] px-4 text-accent-fg border border-accent rounded-[9px] bg-accent shadow-[inset_0_1px_0_rgb(255_255_255/.12),0_1px_2px_rgb(0_0_0/.08)] cursor-pointer text-[13.5px] font-medium tracking-[-0.01em] transition-[background,border-color,transform,opacity] duration-150 hover:not-disabled:bg-accent-hover hover:not-disabled:border-accent-hover active:not-disabled:scale-[0.985] disabled:opacity-40 disabled:cursor-not-allowed'

export const btnPrimaryCompact =
  'inline-flex items-center justify-center gap-1.5 min-h-[32px] px-3 text-accent-fg border border-accent rounded-[8px] bg-accent shadow-[inset_0_1px_0_rgb(255_255_255/.12),0_1px_2px_rgb(0_0_0/.08)] cursor-pointer text-[12.5px] font-medium tracking-[-0.01em] transition-[background,border-color,transform,opacity] duration-150 hover:not-disabled:bg-accent-hover hover:not-disabled:border-accent-hover active:not-disabled:scale-[0.985] disabled:opacity-40 disabled:cursor-not-allowed'

export const btnPrimaryDanger =
  'inline-flex items-center justify-center gap-1.5 min-h-[32px] px-3 text-white border border-danger rounded-[8px] bg-danger cursor-pointer text-[12.5px] font-medium transition-[opacity,transform] duration-150 hover:not-disabled:opacity-90 active:not-disabled:scale-[0.985] disabled:opacity-40 disabled:cursor-not-allowed'

export const btnSecondary =
  'inline-flex items-center justify-center gap-1.5 min-h-[32px] px-3 text-ink border border-line rounded-[8px] bg-canvas shadow-[0_1px_2px_light-dark(rgb(0_0_0/.035),rgb(0_0_0/.22))] cursor-pointer text-[12.5px] font-medium transition-[background,border-color,transform] duration-150 hover:not-disabled:border-line-strong hover:not-disabled:bg-hover active:not-disabled:scale-[0.985] disabled:opacity-40 disabled:cursor-not-allowed'

export const btnPlain =
  'inline-flex items-center justify-center gap-1.5 min-h-[28px] px-[9px] text-ink-secondary border border-transparent rounded-[7px] bg-transparent cursor-pointer text-[12.5px] font-medium transition-[background,color] duration-120 hover:not-disabled:text-ink hover:not-disabled:bg-hover disabled:opacity-40 disabled:cursor-not-allowed'

export const btnPlainDanger =
  'inline-flex items-center justify-center gap-1.5 min-h-[28px] px-[9px] text-ink-secondary border border-transparent rounded-[7px] bg-transparent cursor-pointer text-[12.5px] font-medium transition-[background,color] duration-120 hover:not-disabled:text-danger hover:not-disabled:bg-danger-bg disabled:opacity-40 disabled:cursor-not-allowed'

export const btnIcon =
  'inline-grid place-items-center w-[34px] h-[34px] text-ink-secondary border-0 rounded-[9px] bg-transparent cursor-pointer transition-[background,color] duration-120 hover:not-disabled:text-ink hover:not-disabled:bg-hover aria-[pressed=true]:text-ink aria-[pressed=true]:bg-hover aria-[expanded=true]:text-ink aria-[expanded=true]:bg-hover disabled:opacity-40 disabled:cursor-not-allowed'

export const btnSubtleIcon =
  'inline-grid place-items-center w-[26px] h-[26px] text-ink-secondary border-0 rounded-[7px] bg-transparent cursor-pointer transition-[background,color] duration-120 hover:not-disabled:text-ink hover:not-disabled:bg-hover disabled:opacity-40 disabled:cursor-not-allowed'

export const fieldInput =
  'w-full min-w-0 h-[36px] px-[11px] text-ink border border-line rounded-[9px] bg-canvas text-[13px] font-normal transition-[border-color,box-shadow,background] duration-150 hover:not-disabled:border-line-strong focus:outline-none focus:border-line-strong focus:shadow-[0_0_0_3px_var(--focus-halo)] disabled:opacity-60'

export const fieldSelect =
  'lune-select w-full min-w-0 h-[36px] pl-[11px] pr-[30px] text-ink border border-line rounded-[9px] bg-canvas text-[13px] font-normal cursor-pointer transition-[border-color,box-shadow,background] duration-150 hover:not-disabled:border-line-strong focus:outline-none focus:border-line-strong focus:shadow-[0_0_0_3px_var(--focus-halo)] disabled:opacity-60'

export const fieldTextarea =
  'w-full min-w-0 p-[9px_11px] text-ink border border-line rounded-[9px] bg-canvas text-[13px] leading-[1.55] transition-[border-color,box-shadow] duration-150 hover:not-disabled:border-line-strong focus:outline-none focus:border-line-strong focus:shadow-[0_0_0_3px_var(--focus-halo)] disabled:opacity-60'

export const subtleCard =
  'border border-line-subtle rounded-[11px] bg-subtle transition-[border-color] duration-150'

export const surfaceCard =
  'border border-line rounded-[11px] bg-canvas'

export const statusPill =
  'inline-flex items-center px-[7px] py-[1px] border border-line rounded-full bg-canvas text-ink-secondary font-mono text-[11px] whitespace-nowrap'

export const badgeMono =
  'inline-flex items-center px-[6px] py-[1px] border border-line-subtle rounded-[6px] bg-muted text-ink-secondary font-mono text-[11px] whitespace-nowrap'

export const popoverSurface =
  'border border-line rounded-[12px] bg-composer shadow-elev-md'

export const bannerError =
  'm-0 px-[12px] py-[9px] rounded-[9px] border border-danger-border bg-danger-bg text-danger text-[12.5px] leading-[1.45]'

export const bannerStatus =
  'm-0 px-[12px] py-[9px] rounded-[9px] border border-line bg-subtle text-ink-secondary text-[12.5px] leading-[1.45]'

export const backdropOverlay =
  'fixed inset-0 z-30 grid place-items-center p-5 bg-black/45 backdrop-blur-[4px] animate-[fade_.15s_ease_both]'
