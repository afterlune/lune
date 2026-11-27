import { useCallback, useEffect, useRef, type MouseEvent, type ReactNode } from 'react'

const CONTROLS =
  'button, a, input, textarea, select, label, [contenteditable], [role="button"], [role="dialog"], [role="menu"], [role="listbox"], .composer, .suggestion-row, .connect-card'

const CODE_PHRASES = [
  'core.run()',
  'delegate_team()',
  'runtime.subscribe()',
  'session.rewind()',
  'ctx.tools',
  'MCP.connect()',
  'agent.plan()',
  'await turn.wait()',
  'stream.flush()',
  'model.list()',
  'const state = idle',
  '// Lune Runtime',
] as const

const DURATION_MS = 5400
const INTERVAL_MS = 14000
const FRAME_INTERVAL_MS = 1000 / 24
const CELL_COLS = 152
const CELL_ROWS = 86

interface AmbientHandle {
  play: () => void
  dispose: () => void
}

function isBlankClick(event: MouseEvent<HTMLDivElement>): boolean {
  const target = event.target as Element | null
  if (!target) return false
  const frame = event.currentTarget
  const selection = window.getSelection()
  return (
    event.button === 0 &&
    frame.contains(target) &&
    target.closest(CONTROLS) === null &&
    !(selection !== null && !selection.isCollapsed && frame.contains(selection.anchorNode))
  )
}

function smoothstep(edge0: number, edge1: number, x: number): number {
  const t = Math.max(0, Math.min(1, (x - edge0) / (edge1 - edge0)))
  return t * t * (3 - 2 * t)
}

/**
 * Sample per-cell intensity (0..1) for a breaching crescent/wave silhouette rising
 * gracefully from a faint water line and subsiding into expanding surface ripples.
 */
function sampleLunarTideMask(nx: number, ny: number, progress: number): number {
  const waterY = 0.66
  const horizonDist = Math.abs(ny - waterY)

  const arch = Math.sin(Math.min(1, progress / 0.88) * Math.PI)
  const breach = Math.pow(Math.max(0, arch), 0.82)

  const cx = 0.36 + progress * 0.28
  const cy = waterY + 0.14 - breach * 0.45
  const tilt = -0.58 + progress * 1.18
  const cosT = Math.cos(tilt)
  const sinT = Math.sin(tilt)
  const dx = (nx - cx) * 1.18
  const dy = (ny - cy) * 1.48
  const rx = dx * cosT + dy * sinT
  const ry = -dx * sinT + dy * cosT

  const outerR = 0.175
  const innerR = 0.144
  const outerDist = Math.hypot(rx, ry)
  const innerDist = Math.hypot(rx - 0.056, ry + 0.048)
  const outerAlpha = 1 - smoothstep(outerR - 0.025, outerR + 0.012, outerDist)
  const innerCut = smoothstep(innerR - 0.022, innerR + 0.015, innerDist)
  let crescent = outerAlpha * innerCut

  if (ny > waterY) {
    const submergeFade = 1 - smoothstep(waterY, waterY + 0.12, ny)
    const mirrorWave = Math.sin(nx * 56 - progress * 14) * 0.35 + 0.65
    crescent *= submergeFade * 0.34 * mirrorWave
  }

  let trail = 0
  if (breach > 0.15) {
    for (let i = 0; i < 6; i++) {
      const lag = (i + 1) * 0.052
      const pTrail = Math.max(0, progress - lag)
      const aTrail = Math.pow(Math.max(0, Math.sin(Math.min(1, pTrail / 0.88) * Math.PI)), 0.85)
      const tx = 0.36 + pTrail * 0.28 + Math.sin(i * 2.7) * 0.032
      const ty = waterY + 0.12 - aTrail * 0.43 + (i % 2 === 0 ? 0.022 : -0.015)
      if (ty <= waterY + 0.02) {
        const td = Math.hypot((nx - tx) * 1.2, (ny - ty) * 1.5)
        trail = Math.max(trail, (1 - smoothstep(0.008, 0.032, td)) * (0.48 - i * 0.05))
      }
    }
  }

  const entrySplash = Math.exp(-Math.pow((progress - 0.16) / 0.16, 2))
  const exitSplash = Math.exp(-Math.pow((progress - 0.78) / 0.18, 2))
  const splashStrength = Math.max(entrySplash, exitSplash * 0.85, breach * 0.38)
  const radialX = Math.abs(nx - (progress < 0.5 ? 0.41 : 0.57))
  const waveRadius = (progress < 0.5 ? progress * 0.54 : (progress - 0.45) * 0.62) + 0.03
  const ring = Math.exp(-Math.pow((radialX - waveRadius) / 0.045, 2))
  const secondRing = Math.exp(-Math.pow((radialX - waveRadius * 0.58) / 0.035, 2)) * 0.65
  const band = Math.exp(-Math.pow(horizonDist / 0.024, 2))
  const ripple = band * splashStrength * (0.28 + 0.72 * Math.max(ring, secondRing))

  return Math.max(crescent, trail, ripple * 0.85)
}

function attachHomeAmbient(canvas: HTMLCanvasElement): AmbientHandle {
  const ctx = canvas.getContext('2d')
  if (!ctx) return { play: () => {}, dispose: () => {} }

  const lettering = document.createElement('canvas')
  const mask = document.createElement('canvas')
  const ink = lettering.getContext('2d')
  const matte = mask.getContext('2d')
  mask.width = CELL_COLS
  mask.height = CELL_ROWS
  const alphaImage = matte ? matte.createImageData(CELL_COLS, CELL_ROWS) : null

  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
  const systemDark = window.matchMedia('(prefers-color-scheme: dark)')
  let disposed = false
  let visible = true
  let rafId = 0
  let layoutRafId = 0
  let startedAt: number | null = null
  let lastPaintAt = -Infinity
  let mode: 'dots' | 'code' = 'dots'
  let width = 0
  let height = 0
  let sceneW = 0
  let sceneH = 0
  let sceneLeft = 0
  let sceneTop = 0

  const canAnimate = () => !reducedMotion.matches && !disposed && !document.hidden && visible && width > 0 && height > 0

  const rebuildCodeLayer = () => {
    if (!ink || sceneW <= 0 || sceneH <= 0) return
    const dpr = Math.min(window.devicePixelRatio || 1, 2)
    lettering.width = Math.ceil(sceneW * dpr)
    lettering.height = Math.ceil(sceneH * dpr)
    ink.setTransform(dpr, 0, 0, dpr, 0, 0)
    const computed = getComputedStyle(canvas)
    const fontFamily = computed.fontFamily || 'ui-monospace, monospace'
    const fontSize = Math.max(7, Math.min(10, sceneW / 95))
    ink.clearRect(0, 0, sceneW, sceneH)
    ink.fillStyle = computed.color || '#52525b'
    ink.font = `500 ${fontSize}px ${fontFamily}`
    ink.textBaseline = 'middle'

    let seed = 0x9e3779b9
    const nextRand = () => {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0
      return seed / 0x1_0000_0000
    }
    for (let y = fontSize; y < sceneH; y += fontSize * 1.35) {
      for (let x = -nextRand() * fontSize * 8; x < sceneW; ) {
        const phrase = CODE_PHRASES[Math.floor(nextRand() * CODE_PHRASES.length)]
        const w = ink.measureText(phrase).width
        ink.globalAlpha = 0.58 + nextRand() * 0.42
        ink.fillText(phrase, x, y)
        x += w + fontSize * (0.45 + nextRand() * 0.7)
      }
    }
    ink.globalAlpha = 1
  }

  const paint = (now: number) => {
    ctx.clearRect(0, 0, width, height)
    if (startedAt === null) return
    const progress = (now - startedAt) / DURATION_MS
    if (progress >= 1) {
      startedAt = null
      return
    }
    const envelope = Math.min(1, progress * 8, (1 - progress) * 5.5)

    if (mode === 'code' && ink && matte && alphaImage) {
      const data = alphaImage.data
      for (let row = 0; row < CELL_ROWS; row++) {
        const ny = (row + 0.5) / CELL_ROWS
        for (let col = 0; col < CELL_COLS; col++) {
          const nx = (col + 0.5) / CELL_COLS
          const intensity = sampleLunarTideMask(nx, ny, progress)
          const idx = (row * CELL_COLS + col) * 4
          data[idx] = 0
          data[idx + 1] = 0
          data[idx + 2] = 0
          data[idx + 3] = Math.round(Math.min(1, intensity * 1.18) * 255)
        }
      }
      matte.putImageData(alphaImage, 0, 0)
      ctx.save()
      ctx.globalAlpha = 0.85 * envelope
      ctx.drawImage(lettering, sceneLeft, sceneTop, sceneW, sceneH)
      ctx.globalCompositeOperation = 'destination-in'
      ctx.drawImage(mask, sceneLeft, sceneTop, sceneW, sceneH)
      ctx.restore()
      return
    }

    const cellW = sceneW / CELL_COLS
    const cellH = sceneH / CELL_ROWS
    const maxRadius = Math.min(cellW, cellH) * 0.44
    ctx.fillStyle = getComputedStyle(canvas).color || '#52525b'
    for (let row = 0; row < CELL_ROWS; row++) {
      const ny = (row + 0.5) / CELL_ROWS
      for (let col = 0; col < CELL_COLS; col++) {
        const nx = (col + 0.5) / CELL_COLS
        const intensity = sampleLunarTideMask(nx, ny, progress)
        if (intensity < 0.03) continue
        const radius = maxRadius * (0.28 + 0.72 * intensity)
        ctx.globalAlpha = intensity * 0.8 * envelope
        ctx.beginPath()
        ctx.arc(sceneLeft + col * cellW + cellW * 0.5, sceneTop + row * cellH + cellH * 0.5, radius, 0, Math.PI * 2)
        ctx.fill()
      }
    }
    ctx.globalAlpha = 1
  }

  const stop = () => {
    if (rafId) {
      window.cancelAnimationFrame(rafId)
      rafId = 0
    }
    startedAt = null
    ctx.clearRect(0, 0, width, height)
  }

  const tick = (now: number) => {
    rafId = 0
    if (!canAnimate()) {
      stop()
      return
    }
    if (now - lastPaintAt >= FRAME_INTERVAL_MS) {
      paint(now)
      lastPaintAt = now
    }
    if (startedAt !== null) {
      rafId = window.requestAnimationFrame(tick)
    }
  }

  const resume = () => {
    if (rafId) {
      window.cancelAnimationFrame(rafId)
      rafId = 0
    }
    if (!canAnimate()) {
      stop()
      return
    }
    if (startedAt !== null) {
      paint(performance.now())
      lastPaintAt = performance.now()
      if (startedAt !== null) rafId = window.requestAnimationFrame(tick)
    }
  }

  const resize = () => {
    width = canvas.clientWidth
    height = canvas.clientHeight
    const dpr = Math.min(window.devicePixelRatio || 1, 2)
    canvas.width = Math.round(width * dpr)
    canvas.height = Math.round(height * dpr)
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    sceneW = Math.min(Math.max(width * 0.88, 560), height * 1.45, 980)
    sceneH = sceneW * (CELL_ROWS / CELL_COLS)
    sceneLeft = (width - sceneW) / 2
    sceneTop = height * 0.44 - sceneH / 2
    rebuildCodeLayer()
    resume()
  }

  const scheduleResize = () => {
    if (disposed || layoutRafId) return
    layoutRafId = window.requestAnimationFrame(() => {
      layoutRafId = 0
      resize()
    })
  }

  const play = () => {
    if (!canAnimate() || startedAt !== null) return
    mode = mode === 'dots' ? 'code' : 'dots'
    if (mode === 'code') rebuildCodeLayer()
    startedAt = performance.now()
    lastPaintAt = -Infinity
    resume()
  }

  const resizeObserver = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(scheduleResize) : null
  const intersectionObserver = typeof IntersectionObserver !== 'undefined'
    ? new IntersectionObserver(([entry]) => {
        visible = entry?.isIntersecting ?? false
        resume()
      })
    : null
  const themeObserver = typeof MutationObserver !== 'undefined' ? new MutationObserver(scheduleResize) : null

  resizeObserver?.observe(canvas)
  intersectionObserver?.observe(canvas)
  themeObserver?.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  document.addEventListener('visibilitychange', resume)
  reducedMotion.addEventListener('change', resume)
  systemDark.addEventListener('change', scheduleResize)
  resize()

  const initialTimer = window.setTimeout(() => {
    play()
  }, 360)
  const intervalTimer = window.setInterval(() => {
    play()
  }, INTERVAL_MS)

  return {
    play,
    dispose: () => {
      disposed = true
      window.clearTimeout(initialTimer)
      window.clearInterval(intervalTimer)
      stop()
      if (layoutRafId) {
        window.cancelAnimationFrame(layoutRafId)
        layoutRafId = 0
      }
      lettering.width = lettering.height = mask.width = mask.height = 0
      resizeObserver?.disconnect()
      intersectionObserver?.disconnect()
      themeObserver?.disconnect()
      document.removeEventListener('visibilitychange', resume)
      reducedMotion.removeEventListener('change', resume)
      systemDark.removeEventListener('change', scheduleResize)
    },
  }
}

export function HomeAmbientStage({
  active,
  className,
  children,
}: {
  active: boolean
  className?: string
  children: ReactNode
}) {
  const handleRef = useRef<AmbientHandle | null>(null)
  const canvasRef = useCallback((node: HTMLCanvasElement | null) => {
    handleRef.current?.dispose()
    handleRef.current = node ? attachHomeAmbient(node) : null
  }, [])

  useEffect(() => () => {
    handleRef.current?.dispose()
    handleRef.current = null
  }, [])

  return (
    <div
      className={className}
      onClick={(event) => {
        if (active && isBlankClick(event)) {
          handleRef.current?.play()
        }
      }}
    >
      {active && (
        <canvas
          ref={canvasRef}
          className="home-ambient-canvas pointer-events-none absolute inset-0 z-0 block h-full w-full font-mono text-ink-secondary grayscale opacity-[0.17]"
          aria-hidden="true"
        />
      )}
      {children}
    </div>
  )
}
