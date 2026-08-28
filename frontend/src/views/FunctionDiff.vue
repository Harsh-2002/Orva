<template>
  <div class="space-y-6">
    <!-- Page head — matches the standardised dashboard pattern: H1 over
         one-line subhead, max-w-prose, no icon. -->
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold text-foreground-strong tracking-tight text-balance">
          Compare versions
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Compare source and configuration for
          <router-link
            :to="`/functions/${fnName}`"
            class="text-foreground-strong underline"
          >
            {{ fnName }}
          </router-link>.
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <button
          class="text-xs text-foreground-muted hover:text-foreground-strong hover:bg-surface-hover rounded-md flex items-center justify-center gap-1.5 px-2 py-1.5 transition-colors min-w-[6rem] focus:outline-none focus-visible:ring-1 focus-visible:ring-focus-ring"
          title="Copy share link"
          aria-label="Copy share link"
          @click="copyShareLink"
        >
          <Copy class="w-3.5 h-3.5" />
          {{ copyState }}
        </button>
        <Button
          variant="secondary"
          @click="$router.push(`/functions/${fnName}/deployments`)"
        >
          <List class="w-4 h-4" />
          All deployments
        </Button>
      </div>
    </div>

    <!-- Version-pair toolbar — flat, no card. The border-b underline
         separates "what's being compared" from the diff body without
         turning the selectors into a fourth identical card. Inline
         labels sit above each select; the swap control lives between
         them. On mobile the row stacks, with swap rotating to vertical. -->
    <div class="pb-5 border-b border-border">
      <div class="grid gap-3 sm:gap-4 sm:grid-cols-[1fr_auto_1fr] items-end">
        <div class="space-y-1.5">
          <label
            for="diff-from"
            class="block text-xs font-medium uppercase tracking-label text-foreground-muted"
          >
            From
            <span class="ml-1 text-foreground-muted normal-case font-medium tracking-normal">(older)</span>
          </label>
          <FilterSelect
            :model-value="fromId"
            :options="fromOptions"
            label="Pick a version"
            trigger-id="diff-from"
            wide
            @update:model-value="updateRange({ from: $event })"
          />
        </div>
        <button
          :disabled="!fromId || !toId"
          class="justify-self-center sm:self-end mb-1 h-9 w-9 flex items-center justify-center rounded-md text-foreground-muted hover:text-foreground-strong hover:bg-surface-hover transition-colors disabled:opacity-30 disabled:hover:bg-transparent disabled:hover:text-foreground-muted"
          title="Swap from / to"
          aria-label="Swap from and to versions"
          @click="swap"
        >
          <ArrowLeftRight class="w-4 h-4 rotate-90 sm:rotate-0 motion-reduce:transition-none transition-transform" />
        </button>
        <div class="space-y-1.5">
          <label
            for="diff-to"
            class="block text-xs font-medium uppercase tracking-label text-foreground-muted"
          >
            To
            <span class="ml-1 text-foreground-muted normal-case font-medium tracking-normal">(newer)</span>
          </label>
          <FilterSelect
            :model-value="toId"
            :options="toOptions"
            label="Pick a version"
            trigger-id="diff-to"
            wide
            @update:model-value="updateRange({ to: $event })"
          />
        </div>
      </div>

      <!-- Action row: presets on the left, rollback CTA on the right.
           Operators read the diff to decide; this row turns the decision
           into one click. "vs Active" snaps the comparison to the most
           common case (previous distinct hash → active). "Roll back to
           From" pre-fills the same confirm flow used by Deployments.vue
           and Editor.vue. -->
      <div
        v-if="fn"
        class="mt-4 flex flex-wrap items-center gap-2 justify-end"
      >
        <button
          v-if="activeDeploymentId && toId !== activeDeploymentId"
          class="inline-flex items-center gap-1.5 text-xs text-foreground-muted hover:text-foreground-strong hover:bg-surface-hover rounded-md px-2.5 py-1.5 transition-colors"
          title="Compare previous version with currently active"
          @click="setToActive"
        >
          <Zap class="w-3.5 h-3.5" />
          vs active
        </button>
        <Button
          v-if="fromRow && !fromIsActive"
          variant="primary"
          size="sm"
          :loading="rollingBack"
          :disabled="rollingBack"
          @click="rollbackToFrom"
        >
          <RotateCcw class="w-3.5 h-3.5 mr-1.5" />
          Roll back to v{{ fromRow.version }}
        </Button>
      </div>
    </div>

    <!-- Empty / error states -->
    <p
      v-if="!fromId || !toId"
      class="text-sm text-foreground-muted py-12 text-center"
    >
      Pick two different deployments above to compare.
    </p>

    <p
      v-else-if="fromId === toId"
      class="text-sm text-foreground-muted py-12 text-center"
    >
      Same version on both sides. Pick a different deployment to see a diff.
    </p>

    <div
      v-else-if="errCode === 'VERSION_GCD'"
      class="bg-warning/10 border border-warning/30 rounded-lg p-5 space-y-3 text-sm"
    >
      <div class="text-warning font-medium">
        Source data for one or both versions has been garbage-collected.
      </div>
      <p class="text-warning/80 leading-body">
        The deployment row still exists, but the on-disk code tree was pruned by the version GC. Pick a version whose source is still archived:
      </p>
      <ul
        v-if="availableHashes.length"
        class="font-mono text-xs space-y-1"
      >
        <li
          v-for="h in availableHashes"
          :key="h"
        >
          <button
            v-if="findVersionByHash(h)"
            class="text-left text-warning hover:text-foreground-strong hover:bg-warning/15 rounded px-1.5 py-0.5 -mx-1.5 transition-colors"
            :title="`Use v${findVersionByHash(h).version} as the From side`"
            @click="useHashAsFrom(h)"
          >
            v{{ findVersionByHash(h).version }} · {{ h.slice(0, 12) }}… · {{ compactWhen(findVersionByHash(h).submitted_at) }}
          </button>
          <span
            v-else
            class="text-warning/90"
          >{{ h.slice(0, 12) }}…</span>
        </li>
      </ul>
      <p
        v-else
        class="text-warning/70 italic text-xs"
      >
        No surviving on-disk versions for this function. Redeploy the original source to compare against the current code.
      </p>
    </div>

    <div
      v-else-if="errCode === 'VERSION_NOT_FOUND'"
      class="bg-danger/10 border border-danger/30 rounded-lg p-5 text-sm text-danger"
    >
      One of the supplied deployment IDs doesn't exist. Pick from the dropdowns above, or go back to the
      <router-link
        :to="`/functions/${fnName}/deployments`"
        class="underline"
      >
        deployment history
      </router-link>.
    </div>

    <div
      v-else-if="errCode && errCode !== 'OK'"
      class="bg-danger/10 border border-danger/30 rounded-lg p-5 text-sm text-danger"
    >
      {{ errMessage || 'Failed to load diff.' }}
    </div>

    <!-- When the payload lands, the diff itself is the page's centre of
         gravity. Metadata is contextual; show it inline when there are
         no changes, and as a compact card otherwise. Code panel sits
         flush against the toolbar so the operator's eye lands there
         first. Manifest is only rendered when it has something to say. -->
    <template v-else-if="payload">
      <!-- No metadata changes — inline note, no card. Keeps the page's
           vertical mass on the actual code diff below. -->
      <p
        v-if="!metadataLines.length"
        class="text-xs text-foreground-muted -mt-2"
      >
        Settings and env are identical between these versions. Secrets aren't tracked per-version.
      </p>

      <!-- Metadata changes — compact card with semantic H2. -->
      <section
        v-else
        class="bg-background border border-border rounded-lg"
      >
        <header class="px-4 py-3 flex items-center justify-between gap-3">
          <h2 class="text-xs font-bold uppercase tracking-wider text-foreground-muted flex items-center gap-2">
            <Settings2 class="w-3.5 h-3.5" />
            Settings &amp; env
            <span class="text-[10px] px-1.5 py-0.5 rounded bg-warning/15 text-warning border border-warning/30 font-medium tracking-normal normal-case">
              {{ metadataLines.length }} change{{ metadataLines.length === 1 ? '' : 's' }}
            </span>
          </h2>
          <button
            class="touch-expand-iconbtn inline-flex items-center justify-center rounded text-foreground-muted hover:text-foreground-strong"
            :aria-label="metaOpen ? 'Collapse settings diff' : 'Expand settings diff'"
            @click="metaOpen = !metaOpen"
          >
            <ChevronDown
              class="w-4 h-4 transition-transform motion-reduce:transition-none"
              :class="{ 'rotate-180': metaOpen }"
            />
          </button>
        </header>
        <div
          v-if="metaOpen"
          class="px-4 pb-4 pt-3 border-t border-border/60"
        >
          <ul class="font-mono text-xs space-y-1">
            <li
              v-for="(line, i) in metadataLines"
              :key="i"
              :class="metaLineClass(line)"
            >
              {{ line }}
            </li>
          </ul>
          <p class="text-[11px] text-foreground-muted mt-3">
            Secrets aren't tracked per-version; they always reflect the current values.
          </p>
        </div>
      </section>

      <!-- Handler source — the page's centre of gravity. The H2 carries
           text-foreground-strong (vs the muted settings/manifest H2s) so the eye
           lands here first. The view-mode label is now a real button:
           toggling persists in localStorage and the merge view rebuilds. -->
      <section
        v-if="handlerFile"
        class="bg-background border border-border rounded-lg overflow-hidden"
      >
        <header class="px-4 py-2.5 flex items-center justify-between gap-3 bg-surface border-b border-border">
          <h2 class="text-xs font-bold uppercase tracking-wider text-foreground-strong flex items-center gap-2">
            <FileCode class="w-3.5 h-3.5 text-foreground-muted" />
            <span class="font-mono normal-case tracking-normal text-sm font-medium">{{ handlerFile.path }}</span>
            <span
              v-if="handlerFile.added"
              class="text-[10px] px-1.5 py-0.5 rounded bg-success/15 text-success border border-success/30 font-medium tracking-normal normal-case"
            >added</span>
            <span
              v-else-if="handlerFile.removed"
              class="text-[10px] px-1.5 py-0.5 rounded bg-danger/15 text-danger border border-danger/30 font-medium tracking-normal normal-case"
            >removed</span>
          </h2>
          <button
            class="text-[10px] font-medium uppercase tracking-label text-foreground-muted hover:text-foreground-strong hover:bg-surface-hover rounded px-2 py-1 transition-colors"
            :title="sideBySide ? 'Switch to unified inline view' : 'Switch to side-by-side view'"
            :aria-pressed="sideBySide"
            @click="toggleSideBySide"
          >
            {{ sideBySide ? 'side-by-side' : 'unified' }}
          </button>
        </header>
        <!-- Skeleton is a sibling, not a child of the mount node: CodeMirror
             appends its own DOM into codeMountRef and Vue must not be patching
             children in the same element. -->
        <div
          v-if="editorLoading"
          class="min-h-[240px] px-4 py-4 space-y-2"
          aria-busy="true"
          aria-label="Loading diff viewer"
        >
          <div class="h-3 w-1/3 rounded bg-surface-hover animate-pulse" />
          <div class="h-3 w-2/3 rounded bg-surface-hover animate-pulse" />
          <div class="h-3 w-1/2 rounded bg-surface-hover animate-pulse" />
          <div class="h-3 w-3/5 rounded bg-surface-hover animate-pulse" />
        </div>
        <div
          ref="codeMountRef"
          class="orva-merge"
        />
      </section>

      <!-- Manifest — only when it carries signal. Lighter chrome, real H2. -->
      <section
        v-if="manifestFile && (manifestFile.before !== manifestFile.after || manifestFile.added || manifestFile.removed)"
        class="bg-background border border-border rounded-lg overflow-hidden"
      >
        <header class="px-4 py-2.5 flex items-center justify-between gap-3 bg-surface border-b border-border">
          <h2 class="text-xs font-bold uppercase tracking-wider text-foreground-muted flex items-center gap-2">
            <Package class="w-3.5 h-3.5" />
            <span class="font-mono normal-case tracking-normal text-foreground-strong text-sm font-medium">{{ manifestFile.path }}</span>
            <span class="text-[10px] px-1.5 py-0.5 rounded bg-warning/15 text-warning border border-warning/30 font-medium tracking-normal normal-case">
              changed
            </span>
          </h2>
          <button
            class="touch-expand-iconbtn inline-flex items-center justify-center rounded text-foreground-muted hover:text-foreground-strong"
            :aria-label="manifestOpen ? 'Collapse manifest diff' : 'Expand manifest diff'"
            @click="manifestOpen = !manifestOpen"
          >
            <ChevronDown
              class="w-4 h-4 transition-transform motion-reduce:transition-none"
              :class="{ 'rotate-180': manifestOpen }"
            />
          </button>
        </header>
        <div
          v-if="manifestOpen && editorLoading"
          class="min-h-[160px] px-4 py-4 space-y-2"
          aria-busy="true"
          aria-label="Loading diff viewer"
        >
          <div class="h-3 w-1/2 rounded bg-surface-hover animate-pulse" />
          <div class="h-3 w-1/3 rounded bg-surface-hover animate-pulse" />
        </div>
        <div
          v-if="manifestOpen"
          ref="manifestMountRef"
          class="orva-merge"
        />
      </section>

      <p
        v-if="!handlerFile && !manifestFile"
        class="text-sm text-foreground-muted py-12 text-center"
      >
        No code changes between these versions.
      </p>
    </template>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Button from '@/components/common/Button.vue'
import { ArrowLeftRight, Copy, Settings2, ChevronDown, FileCode, Package, List, RotateCcw, Zap } from '@lucide/vue'
import { compareDeployments, listDeployments, listFunctions, getDeployment, rollbackFunction } from '@/api/endpoints'
import { describeSnapshotDiff } from '@/utils/rollbackDiff'
import FilterSelect from '@/components/common/FilterSelect.vue'
import { copyText } from '@/utils/clipboard'
import { useConfirmStore } from '@/stores/confirm'

const route = useRoute()
const router = useRouter()

const fnName = computed(() => route.params.name)
const fromId = computed(() => route.query.from || '')
const toId = computed(() => route.query.to || '')

const fn = ref(null)
const versions = ref([])
const payload = ref(null)
const errCode = ref('')
const errMessage = ref('')
const availableHashes = ref([])
const copyState = ref('Copy link')

const metaOpen = ref(true)
const manifestOpen = ref(true)
const rollingBack = ref(false)
const confirmStore = useConfirmStore()

// Mode is a two-stage preference: viewport gives the default, but if
// the operator explicitly toggles, that choice wins and persists. Keeps
// the responsive collapse on phones while letting power users force
// their layout on a laptop.
const SIDE_BY_SIDE_STORAGE_KEY = 'orva:diff:sideBySide'
const viewportPrefers = ref(true)
const userOverride = ref(null) // null = follow viewport; true/false = explicit
const sideBySide = computed(() =>
  userOverride.value === null ? viewportPrefers.value : userOverride.value,
)
try {
  const stored = typeof window !== 'undefined' && window.localStorage?.getItem?.(SIDE_BY_SIDE_STORAGE_KEY)
  if (stored === 'true') userOverride.value = true
  else if (stored === 'false') userOverride.value = false
} catch { /* localStorage may be disabled in private mode */ }

const toggleSideBySide = () => {
  const next = !sideBySide.value
  userOverride.value = next
  try { window.localStorage?.setItem?.(SIDE_BY_SIDE_STORAGE_KEY, String(next)) } catch { /* ignore */ }
  rebuildViews()
}

const codeMountRef = ref(null)
const manifestMountRef = ref(null)
const editorLoading = ref(false)
let codeView = null
let manifestView = null

// CodeMirror — state + view + the merge addon + three language modes + oneDark —
// is by far the heaviest thing this route pulls, and Editor.vue already splits
// the same library out of its own chunk for the same reason. Statically importing
// it here put all of it on the critical path of a page an operator opens
// mid-incident, before the header, version selectors and rollback CTA could
// paint. It is fetched once, on the first mount that actually has a diff to draw.
let cm = null
let cmLoader = null
const loadCodeMirror = () => {
  if (cm) return Promise.resolve(cm)
  if (!cmLoader) {
    cmLoader = Promise.all([
      import('@codemirror/state'),
      import('codemirror'),
      import('@codemirror/merge'),
      import('@codemirror/lang-javascript'),
      import('@codemirror/lang-python'),
      import('@codemirror/lang-json'),
      import('@codemirror/theme-one-dark'),
    ]).then(([state, core, merge, js, py, jsonLang, dark]) => {
      cm = {
        EditorState: state.EditorState,
        EditorView: core.EditorView,
        basicSetup: core.basicSetup,
        MergeView: merge.MergeView,
        unifiedMergeView: merge.unifiedMergeView,
        javascript: js.javascript,
        python: py.python,
        json: jsonLang.json,
        oneDark: dark.oneDark,
        // EditorView only exists after the chunk lands, so the theme is
        // compiled here rather than at module scope.
        diffTheme: core.EditorView.theme(githubDiffThemeSpec),
      }
      return cm
    }).catch((err) => {
      // Let the next attempt re-fetch instead of resolving a broken cache.
      cmLoader = null
      throw err
    })
  }
  return cmLoader
}

// Match CodeMirror lang to the function's runtime (handler.js / handler.py).
// MergeView mounts two read-only sub-editors that share these extensions.
// A function, not a computed: the language modes only exist once the chunk has
// loaded, so this is read at mount time rather than tracked.
const langExtras = () => (fn.value?.runtime?.startsWith('python') ? [cm.python()] : [cm.javascript()])

const githubDiffThemeSpec = {
  // GitHub dark-adapted: soft red for deleted lines (left/side-A)
  '&.cm-merge-a .cm-changedLine': {
    backgroundColor: 'rgba(248, 81, 73, 0.15)',
  },
  // GitHub dark-adapted: soft green for inserted lines (right/side-B)
  '&.cm-merge-b .cm-changedLine': {
    backgroundColor: 'rgba(63, 185, 80, 0.15)',
  },
  // Changed tokens: visible background highlight instead of 2px underline.
  // The dashboard is always dark, and EditorView.theme() does NOT support
  // the &light/&dark markers (those are baseTheme-only — passing them here
  // throws "Unsupported selector: &light" and blanks the whole page). Use
  // plain &.cm-merge-{a,b} selectors matching the changedLine rules above.
  '&.cm-merge-a .cm-changedText': {
    background: 'none',
    backgroundColor: 'rgba(248, 81, 73, 0.35)',
    borderRadius: '2px',
  },
  '&.cm-merge-b .cm-changedText': {
    background: 'none',
    backgroundColor: 'rgba(63, 185, 80, 0.30)',
    borderRadius: '2px',
  },
  // Gutter markers: GitHub red/green
  '&.cm-merge-a .cm-changedLineGutter': { color: '#f85149' },
  '&.cm-merge-b .cm-changedLineGutter': { color: '#3fb950' },

  // Unified inline view (unifiedMergeView, the phone default) renders into
  // a single editor with NO cm-merge-a / cm-merge-b wrappers, so every rule
  // above is inert there. Deleted content arrives as a .cm-deletedChunk
  // widget; inserted / changed lines stay in the main doc. Mirror the same
  // GitHub palette onto those classes so the unified diff matches
  // side-by-side instead of falling back to the addon's stock colors.
  // (.cm-changedText / .cm-changedLine also exist side-by-side, but the
  //  two-class .cm-merge-* selectors above out-specify these single-class
  //  ones, so this block only takes effect in unified mode.)
  '.cm-deletedChunk': { backgroundColor: 'rgba(248, 81, 73, 0.15)' },
  '.cm-deletedChunk .cm-deletedText': {
    background: 'none',
    backgroundColor: 'rgba(248, 81, 73, 0.35)',
    borderRadius: '2px',
  },
  '.cm-deletedChunk .cm-changedLineGutter, .cm-deletedChunk .cm-deletedLineGutter': { color: '#f85149' },
  '.cm-insertedLine, .cm-changedLine, .cm-inlineChangedLine': {
    backgroundColor: 'rgba(63, 185, 80, 0.15)',
  },
  '.cm-changedLine .cm-changedText, .cm-inlineChangedLine .cm-changedText': {
    background: 'none',
    backgroundColor: 'rgba(63, 185, 80, 0.30)',
    borderRadius: '2px',
  },
  '.cm-changedLineGutter, .cm-inlineChangedLineGutter': { color: '#3fb950' },
}

const baseExtensions = (extras) => [
  cm.basicSetup,
  ...extras,
  cm.oneDark,
  cm.diffTheme,
  cm.EditorState.readOnly.of(true),
  cm.EditorView.lineWrapping,
]

// Viewport-driven default: side-by-side on >=md, unified on phones
// (where two columns would crush each side to ~12ch). Re-evaluates on
// resize so an operator rotating a tablet doesn't get stuck in the
// wrong layout. If the operator has set userOverride explicitly, the
// sideBySide computed already ignores viewportPrefers, so resize is a
// no-op for them.
let mediaCleanup = null
const setupMediaQuery = () => {
  if (typeof window === 'undefined' || !window.matchMedia) return
  const mql = window.matchMedia('(min-width: 768px)')
  const apply = (e) => {
    const next = !!e.matches
    if (next === viewportPrefers.value) return
    viewportPrefers.value = next
    if (userOverride.value === null) rebuildViews()
  }
  apply(mql)
  // Older Safari only has addListener / removeListener.
  if (mql.addEventListener) {
    mql.addEventListener('change', apply)
    mediaCleanup = () => mql.removeEventListener('change', apply)
  } else {
    mql.addListener(apply)
    mediaCleanup = () => mql.removeListener(apply)
  }
}

// Callers must have awaited loadCodeMirror() before reaching this.
const mountMergeView = (mount, before, after, extras) => {
  if (!mount || !cm) return null
  if (sideBySide.value) {
    return new cm.MergeView({
      a: { doc: before, extensions: baseExtensions(extras) },
      b: { doc: after, extensions: baseExtensions(extras) },
      parent: mount,
      orientation: 'a-b',
      revertControls: false,
      highlightChanges: true,
      gutter: true,
    })
  }
  // Unified inline view for small screens. Single editor showing the "after"
  // doc with inline markers for what changed vs "before".
  return new cm.EditorView({
    parent: mount,
    state: cm.EditorState.create({
      doc: after,
      extensions: [
        ...baseExtensions(extras),
        cm.unifiedMergeView({ original: before, mergeControls: false }),
      ],
    }),
  })
}

const destroyManifestView = () => {
  manifestView?.destroy?.()
  manifestView = null
}

const destroyViews = () => {
  codeView?.destroy?.()
  destroyManifestView()
  codeView = null
}

// ensureCodeMirror fetches the chunk if needed, driving the skeleton, and
// reports whether the caller may go on to mount.
const ensureCodeMirror = async () => {
  if (cm) return true
  editorLoading.value = true
  try {
    await loadCodeMirror()
    return true
  } catch {
    if (!errCode.value) {
      errCode.value = 'EDITOR_UNAVAILABLE'
      errMessage.value = 'Could not load the diff viewer. Reload the page to try again.'
    }
    return false
  } finally {
    editorLoading.value = false
  }
}

// mountManifestView (re)mounts only the manifest panel. Split out from
// rebuildViews so collapsing/expanding the manifest doesn't tear down and
// rebuild the handler diff above it (which would flicker and lose its
// scroll position). Caller is responsible for the preceding nextTick so
// the v-if="manifestOpen" mount node exists.
const mountManifestView = async () => {
  const manifest = manifestFile.value
  if (!manifest || !manifestOpen.value) return
  if (manifest.before === manifest.after && !manifest.added && !manifest.removed) return
  if (!(await ensureCodeMirror())) return
  // The panel can have been collapsed, or the view already mounted by a
  // concurrent rebuild, while the chunk was in flight.
  if (!manifestOpen.value || manifestView) return
  manifestView = mountMergeView(manifestMountRef.value, manifest.before || '', manifest.after || '', [cm.json()])
}

// Every rebuild takes a sequence number: the chunk fetch is a real await, so a
// version switch or a layout toggle can land mid-flight, and the superseded run
// must not mount a second set of editors into the same node.
let mountSeq = 0
const rebuildViews = async () => {
  const seq = ++mountSeq
  await nextTick()
  destroyViews()
  if (!payload.value) return
  const handler = handlerFile.value
  const manifest = manifestFile.value
  const needsManifest = !!manifest && manifestOpen.value
    && (manifest.before !== manifest.after || manifest.added || manifest.removed)
  // Nothing to draw yet: don't pay for the chunk. Expanding a collapsed
  // manifest later goes through the manifestOpen watcher, which loads it then.
  if (!handler && !needsManifest) return
  if (!(await ensureCodeMirror())) return
  if (seq !== mountSeq) return
  if (handler) {
    codeView = mountMergeView(codeMountRef.value, handler.before || '', handler.after || '', langExtras())
  }
  await mountManifestView()
}

const reload = async () => {
  errCode.value = ''
  errMessage.value = ''
  payload.value = null
  destroyViews()
  if (!fn.value || !fromId.value || !toId.value || fromId.value === toId.value) return
  try {
    const res = await compareDeployments(fn.value.id, fromId.value, toId.value, 'json')
    payload.value = res.data
    await rebuildViews()
  } catch (e) {
    const body = e.response?.data?.error
    errCode.value = body?.code || 'INTERNAL'
    errMessage.value = body?.message || e.message || 'Failed to load diff.'
    if (errCode.value === 'VERSION_GCD') {
      availableHashes.value = body?.details?.available_hashes || []
    }
  }
}

const handlerFile = computed(() => payload.value?.files?.find((f) => f.kind === 'handler'))
const manifestFile = computed(() => payload.value?.files?.find((f) => f.kind === 'manifest'))

const metadataLines = computed(() => {
  const a = payload.value?.from?.snapshot
  const b = payload.value?.to?.snapshot
  if (!a || !b) return []
  return describeSnapshotDiff(a, b)
})

const metaLineClass = (line) => {
  if (line.startsWith('+')) return 'text-success'
  if (line.startsWith('-')) return 'text-danger'
  return 'text-warning'
}

// versionOptions: succeeded deployments newest-first. `isActive` carries
// the "this is what's currently serving" flag so the operator never
// has to cross-reference the deployments page to know which side of the
// diff is live. Comparing against the function's current code_hash is
// what the existing Versions modal already does (Editor.vue:1904).
const versionOptions = computed(() => {
  const activeHash = fn.value?.code_hash || ''
  return versions.value
    .filter((d) => d.status === 'succeeded' && d.code_hash)
    .map((d) => ({
      id: d.id,
      version: d.version,
      shortHash: (d.code_hash || '').slice(0, 12),
      submittedAt: d.submitted_at,
      isActive: !!activeHash && d.code_hash === activeHash,
    }))
})

// Compact local datetime: "5/15 6:43 PM" instead of locale's full
// "5/15/2026, 6:43:38 PM". Keeps the dropdown labels readable on
// narrow viewports without losing the at-a-glance ordering signal.
const compactWhen = (iso) => {
  if (!iso) return ''
  const d = new Date(iso)
  const date = d.toLocaleDateString(undefined, { month: 'numeric', day: 'numeric' })
  const time = d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })
  return `${date} ${time}`
}

const fromOptions = computed(() => [
  { value: '', label: 'Pick a version', disabled: true },
  ...versionOptions.value.map((v) => ({ value: v.id, label: versionLabel(v), disabled: v.id === toId.value })),
])
const toOptions = computed(() => [
  { value: '', label: 'Pick a version', disabled: true },
  ...versionOptions.value.map((v) => ({ value: v.id, label: versionLabel(v), disabled: v.id === fromId.value })),
])
const versionLabel = (v) =>
  `v${v.version} · ${v.shortHash} · ${compactWhen(v.submittedAt)}${v.isActive ? ' · active' : ''}`

const updateRange = ({ from, to }) => {
  router.replace({
    query: {
      from: from ?? fromId.value,
      to: to ?? toId.value,
    },
  })
}

const swap = () => {
  if (!fromId.value || !toId.value) return
  router.replace({ query: { from: toId.value, to: fromId.value } })
}

const copyShareLink = async () => {
  const ok = await copyText(window.location.href)
  copyState.value = ok ? 'Copied!' : 'Copy failed'
  setTimeout(() => { copyState.value = 'Copy link' }, 1500)
}

// activeDeploymentId: the deployment id whose code_hash matches the
// function's currently-served code_hash. Used by the "vs active" preset
// and to gate the "Roll back to From" button (rolling back to what's
// already serving would be a no-op the server rejects).
const activeDeploymentId = computed(() => {
  const activeHash = fn.value?.code_hash
  if (!activeHash) return null
  const row = versions.value.find((d) => d.status === 'succeeded' && d.code_hash === activeHash)
  return row?.id || null
})

const fromRow = computed(() => versions.value.find((d) => d.id === fromId.value) || null)
const fromIsActive = computed(() =>
  !!(fromRow.value && fn.value?.code_hash && fromRow.value.code_hash === fn.value.code_hash),
)

// vs active: shortcut that sets to=active and from=most recent earlier
// row with a different code_hash. Mirrors the CLI's `orva diff` default
// behaviour so the same operator instinct works in both places.
const setToActive = () => {
  if (!activeDeploymentId.value || !fn.value) return
  const activeHash = fn.value.code_hash
  const distinct = versions.value
    .filter((d) => d.status === 'succeeded' && d.code_hash && d.code_hash !== activeHash)
  const prev = distinct[0] // versions are already newest-first per ListDeploymentsForFunction
  updateRange({ from: prev?.id || fromId.value, to: activeDeploymentId.value })
}

// findVersionByHash powers the clickable GCD recovery list. Some hashes
// in available_hashes may not correspond to any deployment row (e.g.
// rows reaped from the DB while the on-disk tree survived); those stay
// non-clickable.
const findVersionByHash = (hash) => versions.value.find((d) => d.code_hash === hash) || null

const useHashAsFrom = (hash) => {
  const row = findVersionByHash(hash)
  if (!row) return
  // If "to" is the GCD'd side, swap it for active so the recovery
  // actually produces a valid diff. Otherwise just move "from".
  const toRow = versions.value.find((d) => d.id === toId.value)
  if (toRow && availableHashes.value.length && !availableHashes.value.includes(toRow.code_hash)) {
    updateRange({ from: row.id, to: activeDeploymentId.value || toId.value })
  } else {
    updateRange({ from: row.id, to: toId.value })
  }
}

// refreshFunctionState re-pulls the function record and deployment
// history so the active marker and dropdowns reflect the new state
// after a rollback. Called by rollbackToFrom on success.
const refreshFunctionState = async () => {
  try {
    const listRes = await listFunctions()
    const found = (listRes.data?.functions || []).find((f) => f.name === fnName.value)
    if (found) fn.value = found
    const list = await listDeployments(fn.value.id, 100)
    versions.value = list.data.deployments || list.data || []
  } catch { /* best-effort refresh */ }
}

// rollbackToFrom mirrors the rollback dialog used in Deployments.vue
// and Editor.vue: confirm with a snapshot-diff preview, then POST to
// /rollback. After success, refresh state and update the To dropdown
// to point at the newly-active deployment so the page rebalances
// without an extra navigation.
const rollbackToFrom = async () => {
  if (rollingBack.value || !fn.value || !fromRow.value || fromIsActive.value) return
  const target = fromRow.value
  const shortHash = (target.code_hash || '').slice(0, 12)
  let message = `Code hash ${shortHash}. The current version stays in history.`
  try {
    const depRes = await getDeployment(target.id)
    const snap = depRes?.data?.snapshot
    if (snap) {
      const lines = describeSnapshotDiff(fn.value, snap)
      if (lines.length) {
        message = `Rolling back to v${target.version} (code ${shortHash}) will also change:\n\n${lines.join('\n')}\n\nSecrets keep their current values; they aren't part of the rollback.`
      } else {
        message = `Rolling back to v${target.version} (code ${shortHash}). Settings and env are already identical, so only the code changes.`
      }
    }
  } catch { /* fall through to default message */ }

  const ok = await confirmStore.ask({
    title: `Restore v${target.version}?`,
    message,
    confirmLabel: 'Rollback',
  })
  if (!ok) return

  rollingBack.value = true
  try {
    const res = await rollbackFunction(fn.value.id, { deployment_id: target.id })
    const newDepId = res?.data?.id || res?.data?.deployment_id
    await refreshFunctionState()
    // Re-aim the page at the new state: From stays where it is so the
    // operator can still see what they restored; To moves to the new
    // active deployment row.
    if (newDepId) {
      updateRange({ from: fromId.value, to: newDepId })
    }
    confirmStore.notify({
      title: 'Rollback complete',
      message: `v${target.version} (code ${shortHash}) is now serving.`,
    })
  } catch (err) {
    const code = err.response?.data?.error?.code || ''
    const msg = err.response?.data?.error?.message || err.message || 'Rollback failed'
    if (code === 'VERSION_GCD') {
      confirmStore.notify({
        title: 'Version unavailable',
        message: `This version has been garbage-collected and can no longer be restored.\n\n${msg}`,
        danger: true,
      })
    } else {
      confirmStore.notify({ title: 'Rollback failed', message: msg, danger: true })
    }
  } finally {
    rollingBack.value = false
  }
}

onMounted(async () => {
  setupMediaQuery()
  // GET /api/v1/functions/{id} doesn't resolve names — list and filter
  // the way Editor.vue / Deployments.vue do. The diff endpoint itself
  // is fine with names (it uses the shared resolveFnID helper) but we
  // need the runtime + canonical id for downstream calls.
  try {
    const listRes = await listFunctions()
    const found = (listRes.data?.functions || []).find((f) => f.name === fnName.value)
    if (!found) throw new Error('not found')
    fn.value = found
  } catch (e) {
    errCode.value = e.response?.data?.error?.code || 'FUNCTION_NOT_FOUND'
    errMessage.value = e.response?.data?.error?.message || 'Function not found.'
    return
  }
  try {
    const list = await listDeployments(fn.value.id, 100)
    versions.value = list.data.deployments || list.data || []
  } catch {
    versions.value = []
  }
  await reload()
})

// Manifest panel toggles independently of the handler diff: only its own
// view is mounted/destroyed, so the code panel above keeps its DOM and
// scroll position.
watch(manifestOpen, async (open) => {
  await nextTick()
  destroyManifestView()
  if (open) await mountManifestView()
})

watch([fromId, toId], reload)
watch(() => fn.value?.runtime, () => { rebuildViews() })

onUnmounted(() => {
  destroyViews()
  mediaCleanup?.()
})
</script>

<style>
/* Match the existing CodeEditor styling so the merge view feels native. */
.orva-merge .cm-editor { height: auto; min-height: 240px; }
.orva-merge .cm-scroller {
  font-family: 'JetBrains Mono', monospace;
  line-height: 1.6;
  /* DESIGN.md "Hidden scrollbars by intent" — nested code regions feel
     calmer without a 6px track in every column. Wheel/touch/keys still
     scroll, the gutter just isn't drawn. */
  scrollbar-width: none;
}
.orva-merge .cm-scroller::-webkit-scrollbar { display: none; }
.orva-merge .cm-content { padding: 16px 0; }
.orva-merge .cm-line { padding: 0 16px; }
/* Deliberately not a theme token: this is @codemirror/theme-one-dark's own
   background, and the MergeView gutter sits flush against the two oneDark
   sub-editors. Pointing it at a palette token would seam the moment the
   palette moves away from the vendored editor theme. */
.orva-merge .cm-mergeView { background: #282c34; }
</style>
