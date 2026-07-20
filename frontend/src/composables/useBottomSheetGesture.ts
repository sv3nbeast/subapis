import { onBeforeUnmount, type Ref } from 'vue'

interface BottomSheetGestureOptions {
  enabled: () => boolean
  panelRef: Ref<HTMLElement | null>
  scrimRef: Ref<HTMLElement | null>
  onDismiss: () => void
}

interface PointerSample {
  y: number
  time: number
}

export function rubberbandOffset(overshoot: number, dimension: number, constant = 0.28): number {
  return (overshoot * dimension * constant) / (dimension + constant * Math.abs(overshoot))
}

export function projectMomentum(initialVelocity: number, decelerationRate = 0.99): number {
  return (initialVelocity / 1000) * decelerationRate / (1 - decelerationRate)
}

function currentTranslateY(element: HTMLElement): number {
  const transform = getComputedStyle(element).transform
  if (!transform || transform === 'none') return 0
  const values = transform.match(/matrix(?:3d)?\((.+)\)/)?.[1]?.split(',').map(Number)
  if (!values) return 0
  return values.length === 16 ? values[13] || 0 : values[5] || 0
}

export function useBottomSheetGesture(options: BottomSheetGestureOptions): {
  beginSheetDrag: (event: PointerEvent) => void
  moveSheetDrag: (event: PointerEvent) => void
  endSheetDrag: (event: PointerEvent) => void
  cancelSheetDrag: () => void
} {
  let pointerId: number | null = null
  let startY = 0
  let offsetY = 0
  let samples: PointerSample[] = []
  let animationFrame: number | null = null

  const updatePresentation = (offset: number) => {
    const panel = options.panelRef.value
    if (!panel) return
    panel.style.transform = `translate3d(0, ${offset}px, 0)`
    const progress = Math.max(0, Math.min(1, 1 - offset / Math.max(panel.offsetHeight, 1)))
    options.scrimRef.value?.style.setProperty('opacity', String(progress))
  }

  const resetPresentation = () => {
    const panel = options.panelRef.value
    if (panel) {
      panel.classList.remove('is-dragging')
      panel.style.removeProperty('transform')
      panel.style.removeProperty('transition')
    }
    options.scrimRef.value?.style.removeProperty('opacity')
    pointerId = null
    offsetY = 0
    samples = []
  }

  const cancelSheetDrag = () => {
    if (animationFrame !== null) cancelAnimationFrame(animationFrame)
    animationFrame = null
    resetPresentation()
  }

  const animateTo = (target: number, initialVelocity: number, onSettled: () => void) => {
    const panel = options.panelRef.value
    if (!panel) return onSettled()

    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      updatePresentation(target)
      onSettled()
      return
    }

    let position = offsetY
    let velocity = initialVelocity
    let previousTime = performance.now()
    const stiffness = 360
    const damping = target === 0 ? 38 : 30

    const tick = (time: number) => {
      const delta = Math.min((time - previousTime) / 1000, 0.032)
      previousTime = time
      const acceleration = -stiffness * (position - target) - damping * velocity
      velocity += acceleration * delta
      position += velocity * delta
      offsetY = position
      updatePresentation(position)

      if (Math.abs(velocity) < 5 && Math.abs(position - target) < 1) {
        offsetY = target
        updatePresentation(target)
        animationFrame = null
        onSettled()
        return
      }
      animationFrame = requestAnimationFrame(tick)
    }

    animationFrame = requestAnimationFrame(tick)
  }

  const beginSheetDrag = (event: PointerEvent) => {
    if (!options.enabled() || pointerId !== null) return
    const panel = options.panelRef.value
    const handle = event.currentTarget as HTMLElement
    if (!panel) return

    if (animationFrame !== null) cancelAnimationFrame(animationFrame)
    animationFrame = null
    pointerId = event.pointerId
    offsetY = currentTranslateY(panel)
    startY = event.clientY - offsetY
    samples = [{ y: event.clientY, time: performance.now() }]
    panel.style.transition = 'none'
    panel.classList.add('is-dragging')
    handle.setPointerCapture?.(event.pointerId)
  }

  const moveSheetDrag = (event: PointerEvent) => {
    if (event.pointerId !== pointerId) return
    const panel = options.panelRef.value
    if (!panel) return

    const rawOffset = event.clientY - startY
    offsetY = rawOffset < 0 ? rubberbandOffset(rawOffset, panel.offsetHeight) : rawOffset
    updatePresentation(offsetY)
    const now = performance.now()
    samples.push({ y: event.clientY, time: now })
    samples = samples.filter(sample => now - sample.time <= 90)
  }

  const endSheetDrag = (event: PointerEvent) => {
    if (event.pointerId !== pointerId) return
    const panel = options.panelRef.value
    if (!panel) return resetPresentation()

    const first = samples[0]
    const last = samples[samples.length - 1]
    const elapsed = first && last ? last.time - first.time : 0
    const velocity = elapsed > 0 && first && last ? ((last.y - first.y) / elapsed) * 1000 : 0
    const projectedOffset = offsetY + projectMomentum(velocity)
    const shouldDismiss = projectedOffset > panel.offsetHeight * 0.34 || velocity > 850

    pointerId = null
    panel.classList.remove('is-dragging')
    animateTo(shouldDismiss ? panel.offsetHeight + 24 : 0, velocity, () => {
      if (shouldDismiss) {
        samples = []
        offsetY = 0
        options.onDismiss()
        return
      }
      resetPresentation()
    })
  }

  onBeforeUnmount(cancelSheetDrag)

  return { beginSheetDrag, moveSheetDrag, endSheetDrag, cancelSheetDrag }
}
