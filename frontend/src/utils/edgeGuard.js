// Stop horizontal scrollers rubber-banding past their ends.
//
// `overscroll-behavior-x: none` is the CSS answer and it is in style.css, but
// it does not land on every engine: on iOS the filter strips could still be
// dragged well past their last chip and sprung back, which reads as a loose
// component floating over the page rather than a scroller with ends. The strip
// is not moving -- the browser is painting an elastic overscroll the stylesheet
// asked it not to.
//
// Chromium clamps either way, so this is invisible to a headless run. The
// browser suite drives a real touch sequence and asserts the event is
// cancelled, which is the part that IS observable everywhere.
//
// The rule: while a gesture is predominantly horizontal AND the scroller is
// already at the end it is being pushed toward, cancel the move. Vertical
// scrolling and every in-range horizontal scroll are untouched, so the page
// still scrolls normally when a drag starts on a strip.

const SCROLLERS = '.swipe-x, .scrollable'

// Below this the gesture has not declared a direction yet; cancelling on the
// first pixel would swallow a vertical scroll that merely started with a wobble.
const AXIS_THRESHOLD = 6

/** The nearest ancestor that actually scrolls horizontally. */
function horizontalScroller(node) {
  for (let el = node; el && el !== document.body; el = el.parentElement) {
    // Duck-typed rather than `instanceof Element`: a text node has no matches.
    if (typeof el.matches !== 'function') continue
    if (!el.matches(SCROLLERS)) continue
    if (el.scrollWidth - el.clientWidth > 1) return el
  }
  return null
}

export function installEdgeGuard(target = document) {
  let startX = 0
  let startY = 0
  let scroller = null
  let locked = null // 'x' | 'y' | null, once the gesture declares itself

  const onStart = (e) => {
    if (e.touches.length !== 1) { scroller = null; return }
    const t = e.touches[0]
    startX = t.clientX
    startY = t.clientY
    locked = null
    scroller = horizontalScroller(e.target)
  }

  const onMove = (e) => {
    if (!scroller || e.touches.length !== 1) return
    const t = e.touches[0]
    const dx = t.clientX - startX
    const dy = t.clientY - startY

    if (locked === null) {
      if (Math.abs(dx) < AXIS_THRESHOLD && Math.abs(dy) < AXIS_THRESHOLD) return
      locked = Math.abs(dx) > Math.abs(dy) ? 'x' : 'y'
    }
    if (locked !== 'x') return

    const max = scroller.scrollWidth - scroller.clientWidth
    const atStart = scroller.scrollLeft <= 0
    const atEnd = scroller.scrollLeft >= max - 1

    // dx > 0 is a drag to the right, which scrolls content toward the start.
    if ((atStart && dx > 0) || (atEnd && dx < 0)) {
      if (e.cancelable) e.preventDefault()
    }
  }

  const onEnd = () => { scroller = null; locked = null }

  // passive:false is the whole point -- a passive listener cannot preventDefault.
  target.addEventListener('touchstart', onStart, { passive: true })
  target.addEventListener('touchmove', onMove, { passive: false })
  target.addEventListener('touchend', onEnd, { passive: true })
  target.addEventListener('touchcancel', onEnd, { passive: true })

  return () => {
    target.removeEventListener('touchstart', onStart)
    target.removeEventListener('touchmove', onMove)
    target.removeEventListener('touchend', onEnd)
    target.removeEventListener('touchcancel', onEnd)
  }
}
