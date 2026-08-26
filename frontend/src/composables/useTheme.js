import { ref, computed, onMounted, onUnmounted } from 'vue'

/**
 * Theme preference, tri-state.
 *
 * `null` follows the OS, 'day' / 'night' override it. That is the same shape as
 * the one persisted UI preference this codebase already had before theming
 * existed (`orva:diff:sideBySide`, FunctionDiff.vue), and the same namespace.
 *
 * The attribute written to <html> is always CONCRETE — 'day' or 'night', never
 * 'system' — so CSS only ever matches one of two states and never has to
 * re-implement the media query. `data-theme-pref` carries the tri-state
 * separately, for the control to reflect.
 *
 * The first resolution does NOT happen here. It happens in an inline script in
 * index.html, before the stylesheet, because this module arrives with the app
 * bundle and a module script is deferred by definition: resolving here would
 * paint night first and correct itself, which is a black flash on every load
 * for anyone who chose day.
 */

const KEY = 'orva:theme'

// Module scope, not per-component: several places read this (the Settings
// control, anything that needs the resolved value) and they must agree.
const pref = ref(read())
const systemIsDay = ref(matchesDay())

function read() {
  try {
    const v = window.localStorage?.getItem?.(KEY)
    return v === 'day' || v === 'night' ? v : null
  } catch {
    return null // private mode, or storage disabled
  }
}

function matchesDay() {
  return typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-color-scheme: light)').matches
}

/** The concrete theme in force right now. */
const resolved = computed(() => pref.value ?? (systemIsDay.value ? 'day' : 'night'))

const PAGE = { day: '#F8F7F4', night: '#0E0D09' }

/**
 * Keep the browser's own chrome in step.
 *
 * While following the OS we hand the decision back with a media-scoped pair, so
 * it stays correct even if the OS flips before any script runs. An explicit
 * choice has to be a single unscoped tag: a scoped light tag loses to a
 * system-dark phone, which is exactly the case an override exists for.
 */
function syncThemeColor() {
  const head = document.head
  head.querySelectorAll('meta[name="theme-color"]').forEach((m) => m.remove())
  const add = (content, media) => {
    const m = document.createElement('meta')
    m.name = 'theme-color'
    m.content = content
    if (media) m.media = media
    head.appendChild(m)
  }
  if (pref.value === null) {
    add(PAGE.day, '(prefers-color-scheme: light)')
    add(PAGE.night, '(prefers-color-scheme: dark)')
  } else {
    add(PAGE[resolved.value])
  }
}

function apply() {
  const el = document.documentElement
  el.dataset.themePref = pref.value ?? 'system'
  el.dataset.theme = resolved.value
  syncThemeColor()
}

/**
 * @param {'day'|'night'|null} next - null returns to following the OS.
 */
function setTheme(next) {
  pref.value = next === 'day' || next === 'night' ? next : null
  try {
    if (pref.value === null) window.localStorage?.removeItem?.(KEY)
    else window.localStorage?.setItem?.(KEY, pref.value)
  } catch {
    // Storage unavailable. The choice still applies for this page; it just
    // will not survive a reload, which is better than throwing.
  }
  apply()
}

export function useTheme() {
  let mq = null
  const onSystemChange = (e) => {
    systemIsDay.value = e.matches
    if (pref.value === null) apply() // a System user follows the OS live
  }

  onMounted(() => {
    if (typeof window.matchMedia !== 'function') return
    mq = window.matchMedia('(prefers-color-scheme: light)')
    systemIsDay.value = mq.matches
    mq.addEventListener('change', onSystemChange)
    apply() // reconcile with whatever the pre-paint script decided
  })

  onUnmounted(() => mq?.removeEventListener('change', onSystemChange))

  return { pref, resolved, setTheme }
}
