import { useEffect, useRef } from 'react'
import type { Theme } from '../hooks/useTheme'
import { usePrefersReducedMotion } from '../hooks/usePrefersReducedMotion'

interface Props {
  theme: Theme
  /** Dim the field for the role gateway behind the cards. */
  dim?: boolean
  /** Accelerate and bloom toward the throat for the landing transition. */
  rushing?: boolean
}

interface Lane {
  v: number
  u: Float32Array
  speed: Float32Array
  alpha: number
}

interface Packet {
  lane: number
  u: number
  speed: number
}

const BASE_T = 0.045

// GatewayCanvas draws the request gateway: many lanes of dots that pinch into a
// narrow throat and fan back out. Raw Canvas 2D, one RAF loop, no libraries.
export function GatewayCanvas({ theme, dim = false, rushing = false }: Props) {
  const ref = useRef<HTMLCanvasElement>(null)
  const reduced = usePrefersReducedMotion()
  const rushingRef = useRef(rushing)
  rushingRef.current = rushing

  useEffect(() => {
    const canvas = ref.current
    const host = canvas?.parentElement
    if (!canvas || !host) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const styles = getComputedStyle(document.documentElement)
    const particle = styles.getPropertyValue('--particle').trim() || '255,255,255'
    const canvasBg = styles.getPropertyValue('--canvas-bg').trim() || '#000'
    const brand = styles.getPropertyValue('--brand-3').trim() || '#38bdf8'
    const dark = theme === 'dark'

    let width = 1
    let height = 1
    let lanes: Lane[] = []
    let packets: Packet[] = []
    let raf = 0
    let inView = true
    let last = 0
    let breathing = 0
    let quality = 1
    let slowMs = 0
    let avgFrame = 16
    let pointerX = 0
    let pointerY = 0
    let targetX = 0
    let targetY = 0

    const rand = (a: number, b: number) => a + Math.random() * (b - a)

    const build = () => {
      const area = width * height
      const cores = navigator.hardwareConcurrency || 4
      const base = 3200 + Math.min(1800, area / 900) + cores * 40
      const mobile = window.innerWidth < 720 ? 0.45 : 1
      const count = Math.max(700, Math.round(base * quality * mobile * (dim ? 0.6 : 1)))
      const laneCount = Math.max(24, Math.min(88, Math.round(count / 58)))
      const per = Math.max(18, Math.round(count / laneCount))
      lanes = Array.from({ length: laneCount }, (_, i) => ({
        v: ((i + 0.5) / laneCount) * 2 - 1,
        u: Float32Array.from({ length: per }, () => Math.random()),
        speed: Float32Array.from({ length: per }, () => rand(0.1, 0.2)),
        alpha: rand(0.3, 0.62),
      }))
      packets = Array.from({ length: Math.max(8, Math.round(30 * quality * mobile)) }, () => ({
        lane: Math.floor(Math.random() * lanes.length),
        u: Math.random(),
        speed: rand(0.11, 0.19),
      }))
    }

    const resize = () => {
      const rect = host.getBoundingClientRect()
      width = Math.max(1, rect.width)
      height = Math.max(1, rect.height)
      const dpr = Math.min(2, window.devicePixelRatio || 1)
      canvas.width = Math.round(width * dpr)
      canvas.height = Math.round(height * dpr)
      canvas.style.width = `${width}px`
      canvas.style.height = `${height}px`
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      build()
      if (reduced) render(0)
    }

    const point = (v: number, u: number, t: number) => {
      const halfW = width * 0.56
      const halfH = height * 0.42
      return {
        x: width / 2 + pointerX + u * halfW,
        y: height / 2 + pointerY + v * halfH * Math.sqrt(t * t + (1 - t * t) * u * u),
      }
    }

    const render = (dt: number) => {
      const t = BASE_T * (1 + 0.15 * Math.sin(breathing))
      const boost = rushingRef.current ? 2.4 : 1
      pointerX += (targetX - pointerX) * 0.05
      pointerY += (targetY - pointerY) * 0.05
      breathing += dt * 0.5

      ctx.globalCompositeOperation = 'source-over'
      ctx.fillStyle = canvasBg
      ctx.fillRect(0, 0, width, height)

      // A single controlled bloom at the throat.
      const gx = width / 2 + pointerX
      const gy = height / 2 + pointerY
      const glowR = Math.max(140, Math.min(width, height) * 0.34)
      const glow = ctx.createRadialGradient(gx, gy, 0, gx, gy, glowR)
      glow.addColorStop(0, hexA(brand, dark ? 0.14 * (rushingRef.current ? 1.8 : 1) : 0.07))
      glow.addColorStop(0.45, hexA(brand, dark ? 0.04 : 0.03))
      glow.addColorStop(1, hexA(brand, 0))
      ctx.fillStyle = glow
      ctx.fillRect(0, 0, width, height)

      ctx.globalCompositeOperation = dark ? 'lighter' : 'source-over'
      const dotSize = dark ? 1 : 1.25

      for (const lane of lanes) {
        for (let i = 0; i < lane.u.length; i++) {
          const uu = lane.u[i] * 2 - 1
          const laneSpeed = lane.speed[i] * (1 + 6 * Math.exp(-(uu * uu) / 0.03))
          let p = lane.u[i] + laneSpeed * dt * boost
          if (p >= 1) p -= 1
          lane.u[i] = p
          const u = p * 2 - 1
          const near = Math.exp(-(u * u) / 0.06)
          const alpha = (0.08 + near * 0.72) * lane.alpha * (dim ? 0.55 : 1)
          const { x, y } = point(lane.v, u, t)
          ctx.fillStyle = `rgba(${particle},${alpha.toFixed(3)})`
          ctx.fillRect(x, y, dotSize, dotSize)
        }
      }

      // Larger packets with a short trail: individual requests.
      for (const pkt of packets) {
        pkt.u += pkt.speed * dt * boost
        if (pkt.u >= 1) {
          pkt.u -= 1
          pkt.lane = Math.floor(Math.random() * lanes.length)
        }
        const lane = lanes[pkt.lane]
        if (!lane) continue
        for (let j = 4; j >= 0; j--) {
          const pu = (pkt.u - j * 0.008) * 2 - 1
          const { x, y } = point(lane.v, pu, t)
          const a = (0.9 - j * 0.17) * (dim ? 0.5 : 1)
          ctx.fillStyle = `rgba(${particle},${Math.max(0, a).toFixed(3)})`
          const s = j === 0 ? 2.4 : 1.5
          ctx.fillRect(x - s / 2, y - s / 2, s, s)
        }
      }
      ctx.globalCompositeOperation = 'source-over'
    }

    const loop = (now: number) => {
      const dt = Math.min(0.05, (now - last) / 1000)
      const frameMs = now - last
      last = now
      if (frameMs > 0 && frameMs < 1000) {
        avgFrame = avgFrame * 0.92 + frameMs * 0.08
        if (avgFrame > 24) slowMs += frameMs
        else slowMs = Math.max(0, slowMs - frameMs * 0.5)
        if (slowMs > 1000 && quality > 0.35) {
          quality = Math.max(0.35, quality - 0.2)
          slowMs = 0
          build()
        }
      }
      render(dt)
      raf = requestAnimationFrame(loop)
    }

    const start = () => {
      if (raf || reduced) return
      last = performance.now()
      raf = requestAnimationFrame(loop)
    }
    const stop = () => {
      if (raf) cancelAnimationFrame(raf)
      raf = 0
    }

    const onPointer = (e: PointerEvent) => {
      if (e.pointerType === 'touch') return
      const rect = host.getBoundingClientRect()
      targetX = ((e.clientX - rect.left) / rect.width - 0.5) * width * 0.05
      targetY = ((e.clientY - rect.top) / rect.height - 0.5) * height * 0.05
    }
    const onLeave = () => {
      targetX = 0
      targetY = 0
    }
    const onVisibility = () => {
      if (document.hidden) stop()
      else if (inView) start()
    }

    const ro = new ResizeObserver(resize)
    ro.observe(host)
    const io = new IntersectionObserver(
      (entries) => {
        inView = entries[0]?.isIntersecting ?? true
        if (inView && !document.hidden) start()
        else stop()
      },
      { threshold: 0.01 },
    )
    io.observe(canvas)

    host.addEventListener('pointermove', onPointer, { passive: true })
    host.addEventListener('pointerleave', onLeave)
    document.addEventListener('visibilitychange', onVisibility)

    resize()
    if (reduced) render(0)
    start()

    return () => {
      stop()
      ro.disconnect()
      io.disconnect()
      host.removeEventListener('pointermove', onPointer)
      host.removeEventListener('pointerleave', onLeave)
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [theme, dim, reduced])

  return <canvas ref={ref} className="gateway-canvas" aria-hidden="true" />
}

// hexA turns "#rrggbb" or "rgb(...)" tokens into an rgba() color.
function hexA(color: string, alpha: number): string {
  const c = color.trim()
  if (c.startsWith('#')) {
    const hex = c.slice(1)
    const full = hex.length === 3 ? hex.split('').map((h) => h + h).join('') : hex
    const n = Number.parseInt(full, 16)
    if (Number.isFinite(n)) {
      const r = (n >> 16) & 255
      const g = (n >> 8) & 255
      const b = n & 255
      return `rgba(${r},${g},${b},${alpha})`
    }
  }
  return `rgba(99,102,241,${alpha})`
}
