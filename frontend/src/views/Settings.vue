<template>
  <div class="space-y-8">
    <div>
      <h1 class="text-xl font-semibold text-foreground-strong tracking-tight">
        Settings
      </h1>
      <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
        Instance, access, storage, backups, and AI.
      </p>
    </div>

    <!-- Build info card. The first thing an operator needs when
         troubleshooting: "what release am I actually running?"
         Sourced from /api/v1/system/health (one-shot at page mount;
         these values don't change while the binary is running). -->
    <details class="group border-b border-border pb-6">
      <summary class="touch-expand-sm flex cursor-pointer list-none items-center gap-2 text-sm font-semibold text-foreground-strong focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary [&::-webkit-details-marker]:hidden">
        <ChevronRight
          class="w-3.5 h-3.5 shrink-0 transition-transform group-open:rotate-90 text-foreground-muted"
          aria-hidden="true"
        />
        Build info
        <span class="ml-auto text-xs font-normal text-foreground-muted group-open:hidden">{{ buildInfo?.version || EMPTY }}</span>
      </summary>
      <dl class="mt-4 grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 text-xs">
        <dt class="text-foreground-muted">
          Version
        </dt>
        <dd class="font-mono text-foreground-strong">
          {{ buildInfo?.version || EMPTY }}
        </dd>

        <dt class="text-foreground-muted">
          Commit
        </dt>
        <dd class="font-mono text-foreground-strong">
          {{ buildInfo?.commit && buildInfo.commit !== 'unknown' ? buildInfo.commit : 'dev build' }}
        </dd>

        <dt class="text-foreground-muted">
          Built
        </dt>
        <dd class="font-mono text-foreground-strong">
          {{ formatBuildTime(buildInfo?.buildTime) }}
        </dd>

        <dt class="text-foreground-muted">
          Image
        </dt>
        <dd class="font-mono text-foreground-strong flex items-center gap-2 min-w-0">
          <span class="truncate">{{ buildInfo?.image || EMPTY }}</span>
          <!-- p-1 around a 13px icon lands at 21x21 on a phone. touch-expand-*
               grows the real box on coarse pointers only; a button centres its
               own content, so the glyph stays put. -->
          <button
            v-if="buildInfo?.image"
            class="p-1 rounded-md hover:bg-surface text-foreground-muted hover:text-foreground-strong transition-colors shrink-0 touch-expand-iconbtn"
            title="Copy image reference"
            aria-label="Copy image reference"
            @click="copyImage"
          >
            <Copy class="w-3.5 h-3.5" />
          </button>
          <span
            v-if="imageCopied"
            class="text-xs text-primary-hover shrink-0"
          >copied</span>
        </dd>
      </dl>
    </details>

    <!-- Appearance card. Theme is the operator's choice and follows the OS by
         default. Placed first among the configurable cards because it is the
         one setting that changes every other screen. -->
    <section class="space-y-4 border-b border-border pb-8">
      <div>
        <h2 class="text-sm font-semibold text-foreground-strong tracking-tight flex items-center gap-2">
          <Sun class="w-4 h-4 text-foreground-muted" />
          Appearance
        </h2>
        <p class="text-xs text-foreground-muted leading-snug mt-1.5 max-w-prose">
          Follows your system by default. The code editor stays dark in both themes.
        </p>
      </div>

      <fieldset>
        <legend class="sr-only">
          Theme
        </legend>
        <div
          class="inline-flex rounded-md border border-border bg-background p-1 gap-1"
          role="radiogroup"
          aria-label="Theme"
        >
          <button
            v-for="opt in THEME_OPTIONS"
            :key="String(opt.value)"
            type="button"
            role="radio"
            :aria-checked="themePref === opt.value"
            :tabindex="themePref === opt.value ? 0 : -1"
            class="touch-expand-sm inline-flex items-center gap-1.5 rounded-md px-3 h-8 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus-ring"
            :class="themePref === opt.value
              ? 'bg-surface-hover text-foreground-strong'
              : 'text-foreground-muted hover:text-foreground hover:bg-surface-hover'"
            @click="setTheme(opt.value)"
            @keydown="onThemeKey($event, opt.value)"
          >
            <component
              :is="opt.icon"
              class="w-3.5 h-3.5 shrink-0"
            />
            {{ opt.label }}
          </button>
        </div>
      </fieldset>
    </section>

    <!-- AI assistant card. Centralized configuration for the in-product AI
         chat: BYO providers + encrypted keys + base URL, and the assistant's
         defaults (approval policy, tool steps). Provider/model selection is
         shared with Chat; reasoning remains a conversation-time control.
         The id anchors the chat's "configure" deep-link (#ai). -->
    <section
      id="ai"
      class="scroll-mt-6 space-y-4 border-b border-border pb-8"
    >
      <div>
        <h2 class="text-sm font-semibold text-foreground-strong tracking-tight flex items-center gap-2">
          <MessagesSquare class="w-4 h-4 text-foreground-muted" />
          AI assistant
        </h2>
      </div>
      <AISettingsPanel />
    </section>

    <!-- Storage card. Shows orva.db / functions tree / WAL sizes plus
         a "Compact" affordance that runs SQLite VACUUM via the
         admin-gated POST /api/v1/system/vacuum endpoint. -->
    <section class="space-y-4 border-b border-border pb-8">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h2 class="text-sm font-semibold text-foreground-strong tracking-tight flex items-center gap-2">
            <HardDrive class="w-4 h-4 text-foreground-muted" />
            Storage
          </h2>
          <p class="text-xs text-foreground-muted mt-1 max-w-prose">
            Data directory usage and database maintenance.
          </p>
        </div>
        <Button
          variant="secondary"
          :loading="vacuuming"
          :disabled="!storage || vacuuming"
          @click="askVacuum"
        >
          <Wand2 class="w-4 h-4" />
          Compact database
        </Button>
      </div>

      <!-- Skeleton while we wait for the first response. -->
      <div
        v-if="!storage && !storageError"
        class="text-xs text-foreground-muted italic"
      >
        Loading storage stats…
      </div>

      <!-- Every outcome panel on this page is conditionally rendered, so the
           insertion itself is what announces: role="alert" for failures,
           role="status" for successes. Without them a screen-reader operator
           submits a form here and hears nothing at all (WCAG 4.1.3). -->
      <div
        v-if="storageError"
        class="rounded-md border border-danger-ring bg-danger-tint p-3 text-xs text-danger-fg"
        role="alert"
      >
        <div class="font-semibold mb-1">
          Failed to load storage stats
        </div>
        <div class="font-mono break-all">
          {{ storageError }}
        </div>
      </div>

      <div
        v-if="storage"
        class="space-y-3"
      >
        <!-- Stacked bar — proportions of total. -->
        <div class="h-2 w-full rounded-full overflow-hidden bg-border/60 flex">
          <div
            v-if="dbPct > 0"
            class="bg-info h-full"
            :style="{ width: dbPct + '%' }"
            :title="`orva.db: ${formatBytes(storage.db_bytes)}`"
          />
          <div
            v-if="walPct > 0"
            class="bg-warning h-full"
            :style="{ width: walPct + '%' }"
            :title="`WAL: ${formatBytes(storage.wal_bytes)}`"
          />
          <div
            v-if="fnPct > 0"
            class="bg-success h-full"
            :style="{ width: fnPct + '%' }"
            :title="`functions/: ${formatBytes(storage.functions_bytes)}`"
          />
        </div>

        <!-- Numeric breakdown. -->
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1.5 text-xs">
          <div class="flex items-center justify-between">
            <span class="flex items-center gap-2 text-foreground-muted">
              <span class="w-2 h-2 rounded-sm bg-info" />
              orva.db
            </span>
            <span class="font-mono text-foreground-strong">{{ formatBytes(storage.db_bytes) }}</span>
          </div>
          <div class="flex items-center justify-between">
            <span class="flex items-center gap-2 text-foreground-muted">
              <span class="w-2 h-2 rounded-sm bg-success" />
              functions/
            </span>
            <span class="font-mono text-foreground-strong">{{ formatBytes(storage.functions_bytes) }}</span>
          </div>
          <div
            v-if="storage.wal_bytes > 0"
            class="flex items-center justify-between"
          >
            <span class="flex items-center gap-2 text-foreground-muted">
              <span class="w-2 h-2 rounded-sm bg-warning" />
              orva.db-wal
            </span>
            <span class="font-mono text-foreground-strong">{{ formatBytes(storage.wal_bytes) }}</span>
          </div>
          <div class="flex items-center justify-between">
            <span class="text-foreground-muted">total</span>
            <span class="font-mono text-foreground-strong font-semibold">{{ formatBytes(storage.total_bytes) }}</span>
          </div>
        </div>

        <!-- VACUUM hint — only shown when there's something to reclaim. -->
        <div
          v-if="reclaimableBytes > 0"
          class="text-xs text-foreground-muted pt-1 border-t border-border"
        >
          {{ formatBytes(reclaimableBytes) }} reclaimable
          ({{ storage.db_free_pages }} free SQLite pages)
        </div>
      </div>

      <!-- Last-vacuum result. Sticks until next vacuum or page reload. -->
      <div
        v-if="lastVacuum"
        class="rounded-md border border-success-ring bg-success-tint p-3 text-xs text-success-fg"
        role="status"
      >
        Compacted in {{ lastVacuum.duration_ms }} ms and freed
        <span class="font-mono">{{ formatBytes(lastVacuum.freed_bytes) }}</span>
        ({{ formatBytes(lastVacuum.before_bytes) }} →
        {{ formatBytes(lastVacuum.after_bytes) }}).
      </div>
      <div
        v-if="vacuumError"
        class="rounded-md border border-danger-ring bg-danger-tint p-3 text-xs text-danger-fg"
        role="alert"
      >
        <div class="font-semibold mb-1">
          Compact failed
        </div>
        <div class="font-mono break-all">
          {{ vacuumError }}
        </div>
      </div>
    </section>

    <!-- Account card — change password + logout. -->
    <section class="space-y-4 border-b border-border pb-8">
      <div>
        <h2 class="text-sm font-semibold text-foreground-strong tracking-tight flex items-center gap-2">
          <KeyRound class="w-4 h-4 text-foreground-muted" />
          Account
        </h2>
        <p class="text-xs text-foreground-muted mt-1">
          Update your password or end your session.
        </p>
      </div>

      <form
        class="space-y-3 pt-2"
        @submit.prevent="handleChangePassword"
      >
        <h3 class="text-sm font-medium text-foreground">
          Change password
        </h3>
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <div class="flex flex-col gap-1">
            <label
              for="settings-current-password"
              class="text-xs text-foreground-muted"
            >Current password</label>
            <input
              id="settings-current-password"
              v-model="pwForm.current"
              type="password"
              autocomplete="current-password"
              aria-describedby="pw-error"
              class="h-10 bg-surface border border-border rounded-md px-3 text-sm text-foreground-strong placeholder:text-foreground-muted focus:outline-none focus:ring-1 focus:ring-primary"
              placeholder="••••••••"
            >
          </div>
          <div class="flex flex-col gap-1">
            <label
              for="settings-new-password"
              class="text-xs text-foreground-muted"
            >New password</label>
            <input
              id="settings-new-password"
              v-model="pwForm.next"
              type="password"
              autocomplete="new-password"
              aria-describedby="pw-error"
              class="h-10 bg-surface border border-border rounded-md px-3 text-sm text-foreground-strong placeholder:text-foreground-muted focus:outline-none focus:ring-1 focus:ring-primary"
              placeholder="••••••••"
            >
          </div>
          <div class="flex flex-col gap-1">
            <label
              for="settings-confirm-password"
              class="text-xs text-foreground-muted"
            >Confirm new password</label>
            <input
              id="settings-confirm-password"
              v-model="pwForm.confirm"
              type="password"
              autocomplete="new-password"
              aria-describedby="pw-error"
              class="h-10 bg-surface border border-border rounded-md px-3 text-sm text-foreground-strong placeholder:text-foreground-muted focus:outline-none focus:ring-1 focus:ring-primary"
              placeholder="••••••••"
            >
          </div>
        </div>

        <div
          v-if="pwError"
          id="pw-error"
          class="rounded-md border border-danger-ring bg-danger-tint p-2.5 text-xs text-danger-fg"
          role="alert"
        >
          {{ pwError }}
        </div>
        <!-- A successful submit clears the three fields and leaves focus on the
             button, so aria-describedby on the inputs never gets read: the
             success panel has to announce itself. -->
        <div
          v-if="pwSuccess"
          class="rounded-md border border-success-ring bg-success-tint p-2.5 text-xs text-success-fg"
          role="status"
        >
          Password updated successfully.
        </div>

        <Button
          type="submit"
          variant="primary"
          :loading="pwLoading"
          :disabled="pwLoading"
        >
          <KeyRound class="w-4 h-4" />
          Update password
        </Button>
      </form>

      <div class="pt-2 border-t border-border">
        <Button
          variant="danger"
          @click="handleLogout"
        >
          <LogOut class="w-4 h-4" />
          Log out
        </Button>
      </div>
    </section>

    <!-- Connected applications card — OAuth grants from claude.ai
         web, ChatGPT web, etc. Each row maps to one active
         oauth_access_tokens row. Revoke flips revoked_at; the next
         /mcp call from that connector returns 401. -->
    <section class="space-y-4 border-b border-border pb-8">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h2 class="text-sm font-semibold text-foreground-strong tracking-tight flex items-center gap-2">
            <Plug class="w-4 h-4 text-foreground-muted" />
            Connected applications
          </h2>
          <p class="text-xs text-foreground-muted mt-1 max-w-prose">
            OAuth clients with access to this instance. Add connectors from
            <RouterLink
              to="/docs#mcp"
              class="text-link hover:underline"
            >
              Docs
            </RouterLink>
          </p>
        </div>
        <span
          v-if="connectedApps.length > 0"
          class="text-xs text-foreground-muted self-center"
        >
          {{ connectedApps.length }} active
        </span>
      </div>

      <div
        v-if="connectedAppsError"
        class="rounded-md border border-danger-ring bg-danger-tint p-3 text-xs text-danger-fg"
        role="alert"
      >
        <div class="font-semibold mb-1">
          Failed to load connected apps
        </div>
        <div class="font-mono break-all">
          {{ connectedAppsError }}
        </div>
      </div>

      <div
        v-else-if="connectedAppsLoading"
        class="text-xs text-foreground-muted italic"
      >
        Loading…
      </div>

      <div
        v-else-if="connectedApps.length === 0"
        class="py-3"
      >
        <p class="text-xs text-foreground-muted">
          No connected applications.
        </p>
      </div>

      <!-- No negative margin here: the section has no horizontal padding of its
           own (the page inset comes from Layout's p-page), so -mx-5 had nothing
           to cancel and pushed the row rules 19px past the section's own
           border-b on both sides — clipped, not scrolled, at phone widths. -->
      <ul
        v-else
        class="divide-y divide-border"
      >
        <li
          v-for="app in connectedApps"
          :key="app.id"
          class="py-3 flex items-start gap-3"
        >
          <component
            :is="iconForClient(app.client_name).icon"
            class="w-5 h-5 mt-0.5 shrink-0"
            :class="iconForClient(app.client_name).accent"
          />
          <div class="flex-1 min-w-0">
            <div class="text-sm font-medium text-foreground-strong truncate">
              {{ app.client_name }}
            </div>
            <div class="text-xs text-foreground-muted mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5">
              <span>Authorized {{ formatRelative(app.issued_at) }}</span>
              <span v-if="app.last_used_at">
                · Last used {{ formatRelative(app.last_used_at) }}
              </span>
              <span
                v-else
                class="italic opacity-70"
              >· Never used</span>
              <span v-if="app.refresh_expires_at">
                · Re-consent {{ formatRelative(app.refresh_expires_at) }}
              </span>
            </div>
            <div class="flex flex-wrap gap-1 mt-2">
              <span
                v-for="s in scopeList(app.scope)"
                :key="s"
                class="text-xs px-1.5 py-0.5 rounded font-mono"
                :class="scopeBadgeClass(s)"
              >
                {{ s }}
              </span>
            </div>
          </div>
          <div class="flex items-center gap-3 shrink-0 self-center">
            <button
              type="button"
              class="text-xs text-foreground-muted hover:text-danger-fg transition-colors flex items-center gap-1 touch-expand-xs"
              :disabled="busyApp === app.id || busyApp === app.client_id"
              @click="revokeApp(app)"
            >
              <Trash2 class="w-3.5 h-3.5" />
              Revoke
            </button>
            <button
              type="button"
              class="text-xs text-foreground-muted hover:text-danger-fg transition-colors flex items-center gap-1 touch-expand-xs"
              :disabled="busyApp === app.id || busyApp === app.client_id"
              @click="removeApp(app)"
            >
              <Ban class="w-3.5 h-3.5" />
              Remove
            </button>
          </div>
        </li>
      </ul>
    </section>

    <!-- Active sessions card — operator's own browser logins. The
         calling session is flagged `current` and shows no Revoke
         button (use the Logout button in the Account card instead). -->
    <section class="space-y-4 border-b border-border pb-8">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h2 class="text-sm font-semibold text-foreground-strong tracking-tight flex items-center gap-2">
            <Monitor class="w-4 h-4 text-foreground-muted" />
            Active sessions
          </h2>
          <p class="text-xs text-foreground-muted mt-1 max-w-prose">
            Browsers signed in to this instance.
          </p>
        </div>
        <span
          v-if="sessions.length > 0"
          class="text-xs text-foreground-muted self-center"
        >
          {{ sessions.length }} active
        </span>
      </div>

      <div
        v-if="sessionsError"
        class="rounded-md border border-danger-ring bg-danger-tint p-3 text-xs text-danger-fg"
        role="alert"
      >
        <div class="font-semibold mb-1">
          Failed to load sessions
        </div>
        <div class="font-mono break-all">
          {{ sessionsError }}
        </div>
      </div>

      <ul
        v-else
        class="divide-y divide-border"
      >
        <li
          v-for="s in sessions"
          :key="s.prefix"
          class="py-3 flex items-start gap-3"
        >
          <Monitor
            class="w-5 h-5 mt-0.5 shrink-0"
            :class="s.current ? 'text-success-fg' : 'text-foreground-muted'"
          />
          <div class="flex-1 min-w-0">
            <div class="text-sm font-medium text-foreground-strong flex items-center gap-2 flex-wrap">
              <span v-if="s.current">This session</span>
              <span
                v-else
                class="font-mono text-xs"
              >{{ maskPrefix(s.prefix) }}</span>
              <span
                v-if="s.current"
                class="text-xs px-1.5 py-0.5 rounded bg-success-tint text-success-fg font-medium"
              >
                current
              </span>
            </div>
            <div class="text-xs text-foreground-muted mt-0.5">
              Signed in {{ formatRelative(s.created_at) }}
              · expires {{ formatRelative(s.expires_at) }}
            </div>
          </div>
          <button
            v-if="!s.current"
            type="button"
            class="text-xs text-foreground-muted hover:text-danger-fg transition-colors flex items-center gap-1 shrink-0 self-center touch-expand-xs"
            :disabled="revokingPrefix === s.prefix"
            @click="revokeOtherSession(s)"
          >
            <Trash2 class="w-3.5 h-3.5" />
            Revoke
          </button>
        </li>
      </ul>
    </section>

    <!-- Backup / Restore card. -->
    <section class="space-y-4">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h2 class="text-sm font-semibold text-foreground-strong tracking-tight flex items-center gap-2">
            <DatabaseBackup class="w-4 h-4 text-foreground-muted" />
            Backup &amp; Restore
          </h2>
          <p class="text-xs text-foreground-muted mt-1 max-w-prose">
            Download or restore a complete instance snapshot.
          </p>
          <p class="text-xs text-warning-fg mt-2 max-w-prose">
            Backups contain secret keys. Store them securely.
          </p>
        </div>
      </div>

      <div class="flex flex-col sm:flex-row gap-3 pt-2 border-t border-border">
        <Button
          variant="primary"
          @click="downloadBackup"
        >
          <Download class="w-4 h-4" />
          Download backup
        </Button>
        <Button
          variant="secondary"
          :loading="restoring"
          @click="pickRestoreFile"
        >
          <Upload class="w-4 h-4" />
          Restore from backup
        </Button>
        <input
          ref="fileInput"
          type="file"
          accept=".tar.gz,.tgz,application/gzip"
          class="hidden"
          @change="onFileSelected"
        >
      </div>

      <!-- Restore status panel — surfaces backend errors verbatim because
           the operator needs to know exactly what broke. -->
      <div
        v-if="restoreError"
        class="rounded-md border border-danger-ring bg-danger-tint p-3 text-xs text-danger-fg"
        role="alert"
      >
        <div class="font-semibold mb-1">
          Restore failed
        </div>
        <div class="font-mono break-all">
          {{ restoreError }}
        </div>
      </div>
      <div
        v-if="restoreOk"
        class="rounded-md border border-success-ring bg-success-tint p-3 text-xs text-success-fg"
        role="status"
      >
        Restore complete. The server is restarting to load the new data. Reload in
        a few seconds.
        <button
          class="underline ml-1 touch-expand-xs"
          @click="reload"
        >
          Reload now
        </button>
      </div>
    </section>
  </div>
</template>

<script setup>
defineOptions({ name: 'SettingsView' })

import { EMPTY } from '@/utils/format'
import { ref, computed, onMounted } from 'vue'
import { useRouter, RouterLink } from 'vue-router'
import {
  Download,
  Upload,
  DatabaseBackup,
  HardDrive,
  Wand2,
  KeyRound,
  LogOut,
  Plug,
  Monitor,
  Trash2,
  Ban,
  Copy,
  MessagesSquare,
  Sun,
  Moon,
  ChevronRight,
} from '@lucide/vue'
import AISettingsPanel from '@/components/ai/AISettingsPanel.vue'
import Button from '@/components/common/Button.vue'
import { useConfirmStore } from '@/stores/confirm'
import { useAuthStore } from '@/stores/auth'
import { useSystemStore } from '@/stores/system'
import { copyText } from '@/utils/clipboard'
import {
  uploadRestore,
  getStorage,
  runVacuum,
  listConnectedApps,
  revokeConnectedApp,
  removeOAuthApplication,
  listSessions,
  revokeSession,
} from '@/api/endpoints'
import { formatRelative } from '@/utils/time'
import { iconForClient } from '@/utils/connectorIcons'
import { useTheme } from '@/composables/useTheme'

const confirmStore = useConfirmStore()
const auth = useAuthStore()
const router = useRouter()
const systemStore = useSystemStore()

// Theme. `null` follows the OS; 'day' / 'night' override it. Monitor is already
// imported for the sessions card, so the System option reuses that mark rather
// than introducing a second glyph for "your computer".
const { pref: themePref, setTheme } = useTheme()
const THEME_OPTIONS = [
  { value: null, label: 'System', icon: Monitor },
  { value: 'day', label: 'Day', icon: Sun },
  { value: 'night', label: 'Night', icon: Moon },
]

// Arrow keys move between radios, which is what a radiogroup owes a keyboard
// user: Tab enters the group once and lands on the checked option.
function onThemeKey(event, value) {
  const keys = ['ArrowRight', 'ArrowDown', 'ArrowLeft', 'ArrowUp']
  if (!keys.includes(event.key)) return
  event.preventDefault()
  const i = THEME_OPTIONS.findIndex((o) => o.value === value)
  const step = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : -1
  const next = THEME_OPTIONS[(i + step + THEME_OPTIONS.length) % THEME_OPTIONS.length]
  setTheme(next.value)
  const group = event.currentTarget.parentElement
  group?.children[THEME_OPTIONS.indexOf(next)]?.focus()
}

// Build info card — sourced from /api/v1/system/health via the system
// store's seed(). The store may or may not have run by the time this
// view mounts (e.g. when Settings is the first-loaded route), so the
// computed gracefully renders "—" until the snapshot arrives.
const buildInfo = computed(() => systemStore.buildInfo)
const imageCopied = ref(false)
const copyImage = async () => {
  if (!buildInfo.value?.image) return
  if (await copyText(buildInfo.value.image)) {
    imageCopied.value = true
    setTimeout(() => { imageCopied.value = false }, 1500)
  }
}
const formatBuildTime = (ts) => {
  if (!ts || ts === 'unknown') return EMPTY
  try {
    const d = new Date(ts)
    return d.toISOString().replace('T', ' ').replace(/\.\d+Z$/, ' UTC')
  } catch {
    return ts
  }
}

// Account card state.
const pwForm = ref({ current: '', next: '', confirm: '' })
const pwLoading = ref(false)
const pwError = ref('')
const pwSuccess = ref(false)

const handleChangePassword = async () => {
  pwError.value = ''
  pwSuccess.value = false
  if (!pwForm.value.current || !pwForm.value.next || !pwForm.value.confirm) {
    pwError.value = 'All three fields are required.'
    return
  }
  if (pwForm.value.next.length < 8) {
    pwError.value = 'New password must be at least 8 characters.'
    return
  }
  if (pwForm.value.next !== pwForm.value.confirm) {
    pwError.value = 'New password and confirmation do not match.'
    return
  }
  pwLoading.value = true
  try {
    await auth.changePassword(pwForm.value.current, pwForm.value.next)
    pwSuccess.value = true
    pwForm.value = { current: '', next: '', confirm: '' }
  } catch (err) {
    pwError.value = err?.response?.data?.error?.message || 'Failed to update password.'
  } finally {
    pwLoading.value = false
  }
}

const handleLogout = async () => {
  await auth.logout()
  router.push('/login')
}

const fileInput = ref(null)
const restoring = ref(false)
const restoreError = ref('')
const restoreOk = ref(false)

// Storage card state.
const storage = ref(null)
const storageError = ref('')
const vacuuming = ref(false)
const vacuumError = ref('')
const lastVacuum = ref(null)

// Bar segments — clamp so nothing renders as 0px-but-visible.
const dbPct = computed(() =>
  storage.value && storage.value.total_bytes > 0
    ? Math.max(0.5, (storage.value.db_bytes / storage.value.total_bytes) * 100)
    : 0,
)
const walPct = computed(() =>
  storage.value && storage.value.total_bytes > 0 && storage.value.wal_bytes > 0
    ? Math.max(0.5, (storage.value.wal_bytes / storage.value.total_bytes) * 100)
    : 0,
)
const fnPct = computed(() =>
  storage.value && storage.value.total_bytes > 0
    ? Math.max(0.5, (storage.value.functions_bytes / storage.value.total_bytes) * 100)
    : 0,
)

// Upper bound on what VACUUM could reclaim — every page on the
// freelist is dead weight, so freelist_count × page_size is the
// optimistic estimate. The actual freed bytes after VACUUM is
// usually slightly higher (page-level fragmentation gets repacked).
const reclaimableBytes = computed(() => {
  if (!storage.value) return 0
  return (storage.value.db_free_pages || 0) * (storage.value.db_page_size || 0)
})

const fetchStorage = async () => {
  try {
    storageError.value = ''
    const res = await getStorage()
    storage.value = res.data
  } catch (err) {
    storageError.value = err?.response?.data?.error?.message || err?.message || 'unknown error'
  }
}

const askVacuum = async () => {
  const ok = await confirmStore.ask({
    title: 'Compact database?',
    message:
      'VACUUM rewrites orva.db to drop the freelist and shrink the file. ' +
      'It holds an exclusive lock for the duration; every other writer (deploys, ' +
      'invocations recording executions, KV puts, job enqueues) blocks until ' +
      'it returns. Typical runtime is sub-second, but a heavily-loaded instance ' +
      'can stall for several seconds.',
    confirmLabel: 'Compact',
    danger: false,
  })
  if (!ok) return

  vacuuming.value = true
  vacuumError.value = ''
  lastVacuum.value = null
  try {
    const res = await runVacuum()
    lastVacuum.value = res.data
    // Refresh the storage card so the operator sees the new sizes
    // immediately instead of having to reload the page.
    await fetchStorage()
  } catch (err) {
    vacuumError.value = err?.response?.data?.error?.message || err?.message || 'vacuum failed'
  } finally {
    vacuuming.value = false
  }
}

const formatBytes = (n) => {
  if (n == null || isNaN(n)) return EMPTY
  const k = 1024
  if (n < k) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n
  let i = -1
  while (v >= k && i < units.length - 1) {
    v /= k
    i++
  }
  return `${v.toFixed(2)} ${units[i]}`
}

onMounted(fetchStorage)
// Deep-link from the chat ("configure providers") lands on the AI card.
onMounted(() => {
  if (window.location.hash === '#ai') {
    window.requestAnimationFrame(() => {
      document.getElementById('ai')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    })
  }
})

// ── Connected applications ──────────────────────────────────────────

const connectedApps = ref([])
const connectedAppsLoading = ref(false)
const connectedAppsError = ref('')
// One busy marker for both controls on a row. Keying each on its own field
// meant neither disabled the other, so Revoke and Remove could race and the
// loser surfaced "no such connected app".
const busyApp = ref('')

const fetchConnectedApps = async () => {
  connectedAppsLoading.value = true
  connectedAppsError.value = ''
  try {
    const res = await listConnectedApps()
    connectedApps.value = res.data.apps || []
  } catch (err) {
    connectedAppsError.value = err?.response?.data?.error?.message || err?.message || 'unknown error'
  } finally {
    connectedAppsLoading.value = false
  }
}

const revokeApp = async (app) => {
  const ok = await confirmStore.ask({
    title: `Revoke ${app.client_name}?`,
    message:
      `${app.client_name} will lose access immediately. Any in-flight ` +
      'request will fail with 401. The connector can be re-authorized ' +
      'at any time from the originating app.',
    confirmLabel: 'Revoke',
    danger: true,
  })
  if (!ok) return
  busyApp.value = app.id
  try {
    await revokeConnectedApp(app.id)
    await fetchConnectedApps()
  } catch (err) {
    connectedAppsError.value = err?.response?.data?.error?.message || err?.message || 'failed to revoke'
  } finally {
    busyApp.value = ''
  }
}

// Revoke ends one grant; Remove retires the application. The distinction is
// load-bearing because /oauth/register is open dynamic client registration —
// an application whose grant you revoke can request another one, so revoking
// alone is not "make this stop".
const removeApp = async (app) => {
  const ok = await confirmStore.ask({
    title: `Remove ${app.client_name}?`,
    message:
      `${app.client_name} loses access immediately, for this whole instance, ` +
      'and cannot connect again without you approving it on the consent ' +
      'screen. Revoking instead ends this connection but lets the app ' +
      'reconnect on its own.',
    confirmLabel: 'Remove',
    danger: true,
  })
  if (!ok) return
  busyApp.value = app.client_id
  try {
    await removeOAuthApplication(app.client_id)
    await fetchConnectedApps()
  } catch (err) {
    connectedAppsError.value = err?.response?.data?.error?.message || err?.message || 'failed to remove application'
  } finally {
    busyApp.value = ''
  }
}

// scope → list. Always parse fresh from the row; the API returns
// space-separated per RFC 6749 §3.3.
const scopeList = (s) => (s || '').split(/\s+/).filter(Boolean)

// Semantic classes per scope. OIDC scopes remain neutral.
const scopeBadgeClass = (s) => {
  switch (s) {
    case 'admin':
      return 'bg-danger-tint text-danger-fg'
    case 'write':
      return 'bg-warning-tint text-warning-fg'
    case 'invoke':
      return 'bg-info-tint text-info-fg'
    case 'read':
      return 'bg-foreground-muted/15 text-foreground-muted'
    default:
      return 'bg-foreground-muted/10 text-foreground-muted'
  }
}

// ── Active sessions ─────────────────────────────────────────────────

const sessions = ref([])
const sessionsError = ref('')
const revokingPrefix = ref('')

const fetchSessions = async () => {
  sessionsError.value = ''
  try {
    const res = await listSessions()
    sessions.value = res.data.sessions || []
  } catch (err) {
    sessionsError.value = err?.response?.data?.error?.message || err?.message || 'unknown error'
  }
}

const revokeOtherSession = async (s) => {
  const ok = await confirmStore.ask({
    title: 'Revoke this session?',
    message:
      'The browser using this session will be logged out on its next ' +
      'request. Use this if you suspect a device was lost or to clean ' +
      'up old logins.',
    confirmLabel: 'Revoke',
    danger: true,
  })
  if (!ok) return
  revokingPrefix.value = s.prefix
  try {
    await revokeSession(s.prefix)
    await fetchSessions()
  } catch (err) {
    sessionsError.value = err?.response?.data?.error?.message || err?.message || 'failed to revoke'
  } finally {
    revokingPrefix.value = ''
  }
}

// Show a few characters of the prefix so the operator can disambiguate
// rows without exposing the full token. "o••••••••42a3" pattern: first
// + last 4, dots in between.
const maskPrefix = (p) => {
  if (!p || p.length < 8) return p
  return p.slice(0, 1) + '••••••••' + p.slice(-4)
}

onMounted(fetchConnectedApps)
onMounted(fetchSessions)

// downloadBackup just hands the URL to the browser. The session cookie
// is sent automatically (same-origin), the server replies with
// Content-Disposition: attachment; filename=…, and the browser writes
// the file to the user's downloads dir without our UI having to buffer
// the whole tarball in memory.
const downloadBackup = () => {
  // Adding a cache-buster + same-tab navigation triggers a download
  // for application/gzip with a Content-Disposition header without
  // navigating away from this view. We could also use a hidden <a>;
  // window.location.assign keeps the implementation tiny.
  window.location.assign('/api/v1/backup?ts=' + Date.now())
}

const pickRestoreFile = () => {
  restoreError.value = ''
  restoreOk.value = false
  fileInput.value?.click()
}

const onFileSelected = async (e) => {
  const file = e.target.files?.[0]
  // Reset the input so picking the same file twice in a row still
  // fires `change`.
  e.target.value = ''
  if (!file) return

  const ok = await confirmStore.ask({
    title: 'Restore from backup?',
    message:
      `This will replace the live database and function code with the contents of "${file.name}". ` +
      'The current orva.db is moved aside as orva.db.before-restore-<timestamp> in case rollback is needed. ' +
      'You will need to reload after restore completes.',
    confirmLabel: 'Restore',
    danger: true,
  })
  if (!ok) return

  restoring.value = true
  restoreError.value = ''
  restoreOk.value = false
  try {
    await uploadRestore(file)
    restoreOk.value = true
  } catch (err) {
    restoreError.value = err?.response?.data?.error?.message || err?.message || 'Restore failed'
  } finally {
    restoring.value = false
  }
}

const reload = () => {
  window.location.reload()
}
</script>
