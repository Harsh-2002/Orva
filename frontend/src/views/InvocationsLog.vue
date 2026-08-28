<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-foreground-strong tracking-tight">
          Invocations
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Function executions, requests, responses, and logs.
        </p>
      </div>
      <Button
        variant="secondary"
        @click="refresh"
      >
        <RefreshCw
          class="w-4 h-4"
          :class="{ 'animate-spin': loading }"
        />
        Refresh
      </Button>
    </div>

    <!-- Filter strip — single line, calm visual weight.
         Inspired by Grafana / Linear: search input is the anchor;
         filters appear inline as compact chips. Active filters render
         as removable pills next to the chips. -->
    <div class="flex items-center gap-2 flex-wrap">
      <div class="relative basis-full sm:basis-auto sm:flex-1 min-w-0 sm:min-w-[280px] max-w-full sm:max-w-[440px]">
        <Search class="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-foreground-muted pointer-events-none" />
        <input
          v-model="filters.q"
          placeholder="Search errors, container ids…"
          aria-label="Search invocations by error or container id"
          class="h-7 w-full bg-background border border-border rounded-md pl-8 pr-3 text-xs text-foreground placeholder-foreground-muted focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
          @input="onSearchInput"
        >
      </div>

      <FilterSelect
        :options="statusOptions"
        :model-value="filters.status"
        label="Status"
        @update:model-value="filters.status = $event; onFilterChange()"
      />
      <FilterSelect
        :options="rangeOptions"
        :model-value="filters.range"
        label="Range"
        @update:model-value="filters.range = $event; onFilterChange()"
      />

      <!-- Was a native <select>, which handed the phone its OS wheel while the
           two chips beside it opened a custom panel: three controls that look
           like one family and behaved like two. -->
      <FilterSelect
        :options="fnOptions"
        :model-value="filters.fnId"
        label="Function"
        class="max-w-[180px]"
        @update:model-value="filters.fnId = $event; onFilterChange()"
      />

      <button
        v-if="hasActiveFilter"
        class="touch-expand-xs text-xs text-foreground-muted hover:text-foreground-strong px-2 py-1.5 transition-colors"
        @click="clearFilters"
      >
        Clear
      </button>
    </div>

    <!-- All-failed fallback: every parallel fetch rejected. Distinct from
         the empty state — offers a Retry instead of trapping the user in a
         perpetual spinner. -->
    <div
      v-if="loadError && logs.length === 0"
      class="bg-background border border-border rounded-lg px-6 py-12 text-center"
    >
      <CircleAlert class="w-8 h-8 mx-auto text-danger-fg opacity-60" />
      <p class="mt-3 text-sm text-foreground">
        Could not load invocations.
      </p>
      <p class="mt-1 text-xs text-foreground-muted">
        {{ loadError }}
      </p>
      <Button
        variant="secondary"
        size="sm"
        class="mt-4"
        @click="refresh"
      >
        <RefreshCw class="w-3.5 h-3.5 mr-2" />
        Retry
      </Button>
    </div>

    <div
      v-else
      class="bg-background border border-border rounded-lg overflow-x-auto scrollable"
    >
      <!-- Mobile (<sm) stacked-row list. -->
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="log in logs"
          :key="log.id"
          class="px-4 py-3 transition-colors"
          :class="selected.has(log.id) ? 'bg-surface/30' : 'hover:bg-surface/50'"
        >
          <div class="flex items-start gap-1">
            <!-- The box itself stays 14px so the row keeps its density; the
                 label around it carries the touch target. A bare checkbox here
                 was a 13px-wide target with no clickable label beside it, since
                 the card body is the drawer trigger rather than this control's
                 label. -->
            <label
              class="touch-expand-iconbtn -ml-2 flex shrink-0 cursor-pointer items-start justify-center px-2 pt-0.5"
              :aria-label="`Select invocation ${log.id}`"
            >
              <input
                :checked="selected.has(log.id)"
                type="checkbox"
                class="mt-0.5 w-3.5 h-3.5 rounded border-border bg-background shrink-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
                @change="toggleOne(log.id)"
              >
            </label>
            <div class="min-w-0 flex-1">
              <!-- The card body is the drawer trigger. It has to be a real
                   button: the @click used to sit on the bare <li>, which put
                   the detail drawer out of reach of the keyboard entirely. -->
              <button
                type="button"
                class="touch-expand-sm w-full text-left cursor-pointer rounded-md focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                :aria-label="detailLabel(log)"
                @click="openDetail(log)"
              >
                <div class="flex items-center justify-between gap-2">
                  <span class="font-medium text-foreground-strong truncate">{{ getFnName(log.function_id) }}</span>
                  <StatusBadge :status="log.status" />
                </div>
                <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-foreground-muted">
                  <span>{{ formatTime(log.started_at) }}</span>
                  <span
                    v-if="log.duration_ms != null"
                    class="font-mono"
                  >{{ log.duration_ms }}ms</span>
                  <span
                    v-if="log.status_code != null"
                    class="font-mono"
                  >HTTP {{ log.status_code }}</span>
                  <span
                    v-if="log.cold_start"
                    class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] border bg-background font-mono text-info-fg border-info-ring"
                  >cold</span>
                </div>
              </button>
              <!-- The trace id was inert text on phones while the desktop cell
                   linked out. Same destination, same affordance, kept outside
                   the card button so it stays independently operable. -->
              <button
                v-if="log.trace_id"
                type="button"
                class="touch-expand-xs mt-1 inline-flex items-center gap-1 py-1 text-xs text-foreground-muted hover:text-foreground-strong font-mono underline-offset-2 hover:underline rounded-md focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                :title="log.trace_id"
                @click="openTrace(log.trace_id)"
              >
                <Network class="w-3 h-3" />
                trace {{ log.trace_id.substring(0, 11) }}
              </button>
            </div>
          </div>
        </li>
        <li
          v-if="logs.length === 0 && !loading"
          class="px-6 py-12 text-center text-sm text-foreground-muted"
        >
          {{ hasActiveFilter ? 'No matches.' : 'No invocations yet.' }}
        </li>
      </ul>

      <table class="hidden sm:table w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-6 py-3 w-8">
              <!-- The label is the touch target. A checkbox is a replaced
                   element: it renders no ::after, so the bounded hit
                   extension the other tiers use cannot reach it. -->
              <label class="touch-expand-iconbtn inline-flex items-center justify-center">
                <input
                  ref="selectAllRef"
                  type="checkbox"
                  :checked="allChecked"
                  aria-label="Select all invocations"
                  class="w-3.5 h-3.5 rounded border-border bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
                  @change="toggleAll"
                >
              </label>
            </th>
            <th class="px-6 py-3 font-medium">
              Time
            </th>
            <th class="px-6 py-3 font-medium">
              Function
            </th>
            <th class="px-6 py-3 font-medium">
              Status
            </th>
            <th class="px-6 py-3 font-medium hidden md:table-cell">
              Cold
            </th>
            <th class="px-6 py-3 font-medium hidden lg:table-cell">
              HTTP
            </th>
            <th class="px-6 py-3 font-medium hidden sm:table-cell">
              Duration
            </th>
            <th class="px-6 py-3 font-medium hidden lg:table-cell">
              Trace
            </th>
            <th class="px-6 py-3 font-medium text-right hidden xl:table-cell">
              ID
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="log in logs"
            :key="log.id"
            class="hover:bg-surface/50 transition-colors cursor-pointer"
            :class="{ 'bg-surface/30': selected.has(log.id) }"
            @click="openDetail(log)"
          >
            <td
              class="px-6 py-4 align-middle"
              @click.stop
            >
              <!-- The label is the touch target. A checkbox is a replaced
                   element: it renders no ::after, so the bounded hit
                   extension the other tiers use cannot reach it. -->
              <label class="touch-expand-iconbtn inline-flex items-center justify-center">
                <input
                  :checked="selected.has(log.id)"
                  type="checkbox"
                  aria-label="Select invocation"
                  class="w-3.5 h-3.5 rounded border-border bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
                  @change="toggleOne(log.id)"
                >
              </label>
            </td>
            <td class="px-6 py-4 text-foreground">
              <!-- Row clicks stay on the <tr> for the mouse, but the drawer
                   needs a real control to be reachable by keyboard. Rendered
                   bare, so the desktop cell looks exactly as it did. -->
              <button
                type="button"
                class="touch-expand-sm text-left rounded-md focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                :aria-label="detailLabel(log)"
                @click.stop="openDetail(log)"
              >
                {{ formatTime(log.started_at) }}
              </button>
              <!-- The Trace column only exists from lg up, so between sm and
                   lg the trace id had nowhere to render. Carry it here. -->
              <button
                v-if="log.trace_id"
                type="button"
                class="touch-expand-xs lg:hidden mt-1 flex items-center gap-1 text-xs text-foreground-muted hover:text-foreground-strong font-mono underline-offset-2 hover:underline rounded-md focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                :title="log.trace_id"
                @click.stop="openTrace(log.trace_id)"
              >
                <Network class="w-3 h-3" />
                {{ log.trace_id.substring(0, 11) }}
              </button>
            </td>
            <td class="px-6 py-4 font-medium text-foreground-strong">
              <button
                type="button"
                class="touch-expand-sm text-left hover:underline rounded-md focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                :aria-label="`Open function ${getFnName(log.function_id)}`"
                @click.stop="router.push('/functions/' + getFnName(log.function_id))"
              >
                {{ getFnName(log.function_id) }}
              </button>
            </td>
            <td class="px-6 py-4">
              <StatusBadge :status="log.status" />
            </td>
            <td class="px-6 py-4 hidden md:table-cell">
              <span
                v-if="log.cold_start"
                class="inline-flex items-center px-2 py-0.5 rounded text-xs border bg-background font-mono text-info-fg border-info-ring"
              >
                cold
              </span>
              <span
                v-else
                class="text-foreground-muted text-xs"
              >{{ EMPTY }}</span>
            </td>
            <td class="px-6 py-4 text-foreground-muted font-mono text-xs hidden lg:table-cell">
              {{ log.status_code ?? EMPTY }}
            </td>
            <td class="px-6 py-4 text-foreground-muted font-mono text-xs hidden sm:table-cell">
              {{ log.duration_ms != null ? log.duration_ms + 'ms' : EMPTY }}
            </td>
            <td class="px-6 py-4 hidden lg:table-cell">
              <button
                v-if="log.trace_id"
                class="touch-expand-xs text-foreground-muted hover:text-foreground-strong font-mono text-xs underline-offset-2 hover:underline inline-flex items-center gap-1"
                :title="log.trace_id"
                @click.stop="openTrace(log.trace_id)"
              >
                <Network class="w-3 h-3" />
                {{ log.trace_id.substring(0, 11) }}
              </button>
              <span
                v-else
                class="text-foreground-muted"
              >{{ EMPTY }}</span>
            </td>
            <td class="px-6 py-4 text-right text-foreground-muted font-mono text-xs hidden xl:table-cell">
              {{ log.id?.substring(0, 12) }}
            </td>
          </tr>
          <tr v-if="logs.length === 0 && !loading">
            <td
              colspan="9"
              class="px-6 py-8 text-center text-foreground-muted"
            >
              {{ hasActiveFilter ? 'No matches.' : 'No invocations yet.' }}
            </td>
          </tr>
        </tbody>
      </table>
      <div
        v-if="hasMore"
        class="flex justify-center border-t border-border py-3 bg-surface/30"
      >
        <button
          class="touch-expand-sm inline-flex h-8 items-center gap-1.5 rounded-md px-3 text-xs text-foreground-muted hover:bg-surface-hover hover:text-foreground-strong transition-colors"
          :disabled="loading"
          @click="loadMore"
        >
          <RefreshCw
            v-if="loading"
            class="w-3 h-3 animate-spin"
          />
          {{ loading ? 'Loading…' : `Load more (${total - logs.length} more)` }}
        </button>
      </div>
    </div>

    <!-- Floating bulk-action bar. The arbitrary bottom value adds
         env(safe-area-inset-bottom) so the pill clears the iOS home
         indicator in PWA / fullscreen mode; the inset is 0 on every other
         device, which puts the pill back on its 1 rem floor. -->
    <transition name="fade">
      <div
        v-if="selected.size"
        class="fixed bottom-[calc(1rem+env(safe-area-inset-bottom,0px))] left-1/2 -translate-x-1/2 z-30 flex items-center gap-3 bg-background border border-border shadow-lg rounded-full pl-4 pr-2 py-2"
      >
        <span class="text-xs text-foreground-strong">{{ selected.size }} selected</span>
        <span class="w-px h-4 bg-border" />
        <button
          class="touch-expand-xs text-xs text-foreground-muted hover:text-foreground-strong transition-colors px-2 py-1"
          @click="selected = new Set()"
        >
          Clear
        </button>
        <Button
          variant="danger"
          size="sm"
          class="!rounded-full px-4"
          :loading="bulkDeleting"
          @click="bulkDelete"
        >
          <Trash2 class="w-3.5 h-3.5" /> Delete {{ selected.size }}
        </Button>
      </div>
    </transition>

    <Drawer
      v-model="drawerOpen"
      :title="drawerTitle"
      width="640px"
    >
      <div
        v-if="detailLoading"
        class="p-6 text-sm text-foreground-muted"
      >
        Loading…
      </div>
      <div
        v-else-if="detailError"
        class="p-8 text-center"
      >
        <CircleAlert class="w-8 h-8 mx-auto text-danger-fg opacity-60" />
        <p class="mt-3 text-sm text-foreground">
          Could not load this invocation.
        </p>
        <p class="mt-1 text-xs text-foreground-muted">
          {{ detailError }}
        </p>
        <Button
          variant="secondary"
          size="sm"
          class="mt-4"
          @click="lastDetailLog && openDetail(lastDetailLog)"
        >
          <RefreshCw class="w-3.5 h-3.5 mr-2" />
          Retry
        </Button>
      </div>
      <div
        v-else-if="!drawerRow"
        class="p-6 text-sm text-foreground-muted"
      >
        No invocation selected.
      </div>
      <div
        v-else
        class="p-5 space-y-5"
      >
        <!-- Status badges row -->
        <div class="flex items-center gap-2 flex-wrap">
          <StatusBadge :status="drawerRow.status" />
          <span
            v-if="drawerRow.cold_start"
            class="inline-flex items-center px-2.5 py-1 rounded text-xs border bg-background font-mono text-info-fg border-info-ring"
          >
            cold start
          </span>
          <span
            v-if="drawerRow.status_code"
            class="inline-flex items-center px-2.5 py-1 rounded text-xs border bg-background font-mono text-foreground-muted"
          >
            HTTP {{ drawerRow.status_code }}
          </span>
          <!-- v0.4 A3 — small breadcrumb if this row is itself a replay. -->
          <span
            v-if="drawerRow.replay_of"
            class="inline-flex items-center gap-1 px-2.5 py-1 rounded text-xs border bg-background font-mono text-foreground-muted hover:text-foreground-strong cursor-pointer"
            title="Open the original execution"
            @click="openDetail({ id: drawerRow.replay_of, function_id: drawerRow.function_id, status: 'success', started_at: drawerRow.started_at })"
          >
            <RotateCcw class="w-3 h-3" />
            replay of {{ drawerRow.replay_of?.substring(0, 14) }}…
          </span>
        </div>

        <!-- Stat grid -->
        <div class="grid grid-cols-2 gap-3 text-sm">
          <Stat
            label="Duration"
            :value="drawerRow.duration_ms != null ? drawerRow.duration_ms + ' ms' : EMPTY"
          />
          <Stat
            label="Response size"
            :value="drawerRow.response_size != null ? formatBytes(drawerRow.response_size) : EMPTY"
          />
          <Stat
            label="Started"
            :value="formatTime(drawerRow.started_at)"
          />
          <Stat
            label="Finished"
            :value="drawerRow.finished_at ? formatTime(drawerRow.finished_at) : EMPTY"
          />
          <Stat
            label="Function"
            :value="getFnName(drawerRow.function_id)"
          />
          <Stat
            label="Execution ID"
            :value="drawerRow.id"
            mono
          />
        </div>

        <!-- Error message -->
        <div v-if="drawerRow.error_message">
          <h3 class="text-xs uppercase tracking-wider text-foreground-muted mb-2">
            Error
          </h3>
          <pre class="bg-danger-tint border border-danger-ring rounded p-3 text-xs text-danger-fg font-mono whitespace-pre-wrap break-words">{{ drawerRow.error_message }}</pre>
        </div>

        <!-- v0.4 A3: captured Request panel. Shown when capture is on
             AND the row has a captured envelope. Sensitive header values
             land here as the literal "[REDACTED]" string from the
             backend — never the original credential. -->
        <div v-if="requestData">
          <h3 class="text-xs uppercase tracking-wider text-foreground-muted mb-2">
            Request
          </h3>
          <div class="bg-surface border border-border rounded p-3 space-y-3">
            <div class="flex items-center gap-2 font-mono text-xs">
              <span class="px-2 py-0.5 rounded bg-background text-foreground-strong border border-border">{{ requestData.method }}</span>
              <span class="text-foreground-muted truncate">{{ requestData.path }}</span>
            </div>
            <div v-if="requestData.headers && Object.keys(requestData.headers).length">
              <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">
                Headers
              </div>
              <div class="bg-background border border-border rounded p-2 max-h-40 overflow-auto">
                <div
                  v-for="(value, name) in requestData.headers"
                  :key="name"
                  class="font-mono text-[11px] flex gap-2 py-0.5"
                >
                  <span class="text-foreground-muted shrink-0">{{ name }}:</span>
                  <span
                    class="text-foreground break-all"
                    :class="{ 'text-warning-fg': value === '[REDACTED]' }"
                  >{{ value }}</span>
                </div>
              </div>
            </div>
            <div v-if="requestData.body">
              <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">
                Body
              </div>
              <pre class="bg-background border border-border rounded p-2 text-xs text-foreground font-mono overflow-auto max-h-40 whitespace-pre-wrap break-words">{{ prettyBody(requestData.body) }}</pre>
            </div>
            <div
              v-if="requestData.truncated"
              class="text-[11px] text-warning-fg"
            >
              Body was truncated at the configured cap. Replay is disabled for this row.
            </div>
            <!-- v0.4 B3: Save as fixture. Round-trips the captured envelope
                 to the editor's Test pane with `prefill=` so the user can
                 review redacted headers before persisting. -->
            <div class="pt-1 flex justify-end">
              <button
                type="button"
                class="touch-expand-xs text-[11px] text-foreground-muted hover:text-foreground-strong px-2 py-1 rounded hover:bg-surface-hover transition-colors"
                title="Open the editor with this request prefilled"
                @click="saveAsFixture"
              >
                Save as fixture →
              </button>
            </div>
          </div>
        </div>

        <!-- Structured logs (orva.log.*) — emitted via the SDK and
             parsed server-side out of stderr. Empty when the handler
             didn't use the SDK's log helper. -->
        <div v-if="structuredLogs.length > 0">
          <h3 class="text-xs uppercase tracking-wider text-foreground-muted mb-2">
            Logs ({{ structuredLogs.length }})
          </h3>
          <div class="bg-surface border border-border rounded p-3 text-xs font-mono space-y-1 max-h-72 overflow-auto">
            <div
              v-for="entry in structuredLogs"
              :key="`exec-log-${entry.id}`"
              class="flex items-baseline gap-2"
            >
              <span class="text-foreground-muted text-[10px] tabular-nums">
                {{ formatLogTime(entry.ts) }}
              </span>
              <span
                class="text-[10px] uppercase"
                :class="logLevelClass(entry.level)"
              >
                {{ entry.level }}
              </span>
              <span class="text-foreground-strong truncate">{{ entry.message }}</span>
              <code
                v-if="entry.fields"
                class="text-[10px] text-foreground-muted truncate"
              >
                {{ entry.fields }}
              </code>
            </div>
          </div>
        </div>

        <!-- Stderr tail -->
        <div>
          <div class="flex items-center justify-between mb-2">
            <h3 class="text-xs uppercase tracking-wider text-foreground-muted">
              Stderr <span class="text-[10px] normal-case text-foreground-muted">(stdout is the response body, not stored)</span>
            </h3>
            <button
              v-if="stderrText"
              class="touch-expand-xs text-xs text-foreground-muted hover:text-foreground-strong"
              @click="copy(stderrText)"
            >
              {{ copied ? 'copied!' : 'copy' }}
            </button>
          </div>
          <pre
            class="bg-surface border border-border rounded p-3 text-xs text-foreground font-mono overflow-auto max-h-72 whitespace-pre-wrap break-words"
          >{{ stderrText || '(no stderr captured)' }}</pre>
        </div>

        <!-- Replay action — disabled when capture is unavailable or the
             body was truncated. Tooltip explains why.
             v0.4 B4: the AI "Suggest fix" button lives next to Replay
             so the failure-debugging path stays one drawer-click away.
             It only renders when the row failed (status>=500 or
             error_message), and stays disabled when stderr is empty
             since there's nothing for the model to chew on. -->
        <div class="pt-2 border-t border-border flex items-center gap-3">
          <Button
            variant="primary"
            :disabled="requestUnavailable || requestData?.truncated || replaying"
            :loading="replaying"
            :title="replayTooltip"
            @click="replay"
          >
            <Play class="w-4 h-4" />
            Replay
          </Button>
          <Button
            v-if="canSuggestFix"
            variant="secondary"
            :disabled="!stderrText || suggestingFix"
            :loading="suggestingFix"
            :title="suggestFixTooltip"
            @click="suggestFix"
          >
            <Sparkles class="w-4 h-4" />
            Suggest fix
          </Button>
          <span
            v-if="requestUnavailable"
            class="text-xs text-foreground-muted"
          >
            Request not captured for this invocation.
          </span>
        </div>
      </div>
    </Drawer>
  </div>
</template>

<script setup>
import { EMPTY } from '@/utils/format'
import { ref, computed, h, watch, onMounted, onUnmounted, onActivated, onDeactivated } from 'vue'
import { useRouter } from 'vue-router'
import FilterSelect from '@/components/common/FilterSelect.vue'
import { RefreshCw, Search, Trash2, Play, RotateCcw, Sparkles, Network, CircleAlert } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import Drawer from '@/components/common/Drawer.vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import { listInvocations, getInvocation, getInvocationLogs, getExecutionRequest, replayExecution, listFunctions, getFunctionSource, getFunction } from '@/api/endpoints'
import apiClient from '@/api/client'
import { copyText } from '@/utils/clipboard'
import { copyFixSuggestionToClipboard } from '@/utils/aiPrompts'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

const router = useRouter()
const PAGE_SIZE = 50
const logs = ref([])
const total = ref(0)
const loading = ref(false)
// Non-null when the initial parallel fetch fully failed — drives the
// all-failed fallback (error + Retry) instead of an endless spinner.
const loadError = ref('')
const bulkDeleting = ref(false)
const drawerOpen = ref(false)
const detailLoading = ref(false)
// Set when every parallel detail fetch (detail + logs + request) rejected —
// drives an error + Retry state inside the drawer instead of showing a blank
// body that looks like it's still loading.
const detailError = ref('')
const drawerRow = ref(null)
const selected = ref(new Set())  // bulk-select set of execution IDs
const stderrText = ref('')
// v0.6 SDK upgrade: structured log entries emitted from orva.log.*.
// Rendered above raw stderr so the operator sees the level-tagged stream
// first, then can fall back to free-form output below.
const structuredLogs = ref([])

const logLevelClass = (level) => {
  switch (level) {
    case 'error': return 'text-danger-fg'
    case 'warn':  return 'text-warning-fg'
    case 'debug': return 'text-foreground-muted'
    default:      return 'text-info-fg'
  }
}
const formatLogTime = (ts) => {
  if (!ts) return ''
  try {
    const d = new Date(ts)
    return d.toLocaleTimeString(undefined, { hour12: false }) + '.' +
           String(d.getMilliseconds()).padStart(3, '0')
  } catch {
    return ts
  }
}
const copied = ref(false)
const fnMap = ref({})
// v0.4 A3 — captured request envelope for the open drawer. null while
// loading or when capture was disabled / the row is too old. Drives the
// Request panel + the Replay button's enabled state.
const requestData = ref(null)
const requestUnavailable = ref(false)
const replaying = ref(false)
// v0.4 B4 — Suggest-fix affordance. We cache the source fetch for the
// lifetime of an open drawer so repeated clicks don't refetch the same
// blob; openDetail() resets this when a new row is loaded.
const suggestingFix = ref(false)
const cachedSource = ref(null)  // { source, language, runtime } | null
let pollTimer = null

const hasMore = computed(() => logs.value.length < total.value)
const allChecked = computed(() =>
  logs.value.length > 0 && logs.value.every((l) => selected.value.has(l.id)),
)
const someChecked = computed(() =>
  logs.value.some((l) => selected.value.has(l.id)),
)

// `indeterminate` is a DOM property with no matching HTML attribute, so it
// can't be set via a Vue binding reliably across re-renders. Drive it
// imperatively through a template ref whenever the partial-selection state
// changes.
const selectAllRef = ref(null)
watch(
  () => someChecked.value && !allChecked.value,
  (indeterminate) => {
    if (selectAllRef.value) selectAllRef.value.indeterminate = indeterminate
  },
  { immediate: true },
)
const toggleOne = (id) => {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id); else next.add(id)
  selected.value = next
}
const toggleAll = () => {
  if (allChecked.value) {
    selected.value = new Set()
  } else {
    const next = new Set(selected.value)
    logs.value.forEach((l) => next.add(l.id))
    selected.value = next
  }
}

const filters = ref({
  fnId:   '',
  status: '',
  range:  '',  // '' | '1h' | '24h' | '7d'
  q:      '',
})

// Statuses match the DB's stored values exactly (executions.status):
// "success" and "error" are what UpdateExecution writes. Anything else
// is reserved for future failure-typed statuses (timeout, oom, etc.).
const statusOptions = [
  { value: '',        label: 'All' },
  { value: 'success', label: 'Success' },
  { value: 'error',   label: 'Error' },
]

const rangeOptions = [
  { value: '',    label: 'All time' },
  { value: '1h',  label: '1h' },
  { value: '24h', label: '24h' },
  { value: '7d',  label: '7d' },
]

const hasActiveFilter = computed(() =>
  !!(filters.value.fnId || filters.value.status || filters.value.range || filters.value.q),
)

const clearFilters = () => {
  filters.value = { fnId: '', status: '', range: '', q: '' }
  fetchLogs()
}

let searchDebounce = null
const onSearchInput = () => {
  if (searchDebounce) clearTimeout(searchDebounce)
  searchDebounce = setTimeout(() => fetchLogs(), 300)
}

const onFilterChange = () => fetchLogs()

const sinceFromRange = (range) => {
  if (!range) return ''
  const ms = { '1h': 3600e3, '24h': 86400e3, '7d': 7 * 86400e3 }[range]
  if (!ms) return ''
  return new Date(Date.now() - ms).toISOString()
}

const drawerTitle = computed(() =>
  drawerRow.value ? `Invocation · ${drawerRow.value.id?.substring(0, 14)}` : 'Invocation'
)

const replayTooltip = computed(() => {
  if (requestUnavailable.value) return 'request not captured'
  if (requestData.value?.truncated) return 'body was truncated; replay would be inaccurate'
  return 'Re-run this exact request against the current code'
})

// v0.4 B4 — show the Suggest-fix button only on rows we know failed.
// Status >= 500 covers handler exceptions + sandbox-level kills; an
// explicit error_message catches the rare cases where the handler
// returned <500 but the platform still recorded an error (e.g. timeout
// returns 504 but the row is also tagged with status='error').
const canSuggestFix = computed(() => {
  const r = drawerRow.value
  if (!r) return false
  if (typeof r.status_code === 'number' && r.status_code >= 500) return true
  return !!r.error_message
})

const suggestFixTooltip = computed(() => {
  if (!stderrText.value) return 'no stderr to debug from'
  return 'Build a paste-ready debug prompt with source + request + stderr'
})

// Vue resolves to the runtime-only build here, so a `template:` string
// compiles to nothing: every Stat in the drawer's grid rendered empty.
// Same markup, expressed as a render function.
const Stat = {
  props: { label: String, value: [String, Number], mono: Boolean },
  setup(p) {
    return () =>
      h('div', { class: 'bg-surface border border-border rounded p-3' }, [
        h('div', { class: 'text-[10px] uppercase tracking-wider text-foreground-muted mb-1' }, p.label),
        h('div', { class: ['text-sm text-foreground-strong', p.mono && 'font-mono text-xs'] },
          p.value == null ? '' : String(p.value)),
      ])
  },
}

// Compact filter chip used in the action bar. Shows the label until
// a value is picked; once active, becomes a pill with the value + an x.
// Click anywhere to open the menu, click an option to apply, click the x
// to clear. Calmer than a row of always-visible buttons.
// fnMap is { id: name } for the table's own lookups; the picker wants a list.
const fnOptions = computed(() => [
  { value: '', label: 'All functions' },
  ...Object.entries(fnMap.value || {}).map(([id, name]) => ({ value: id, label: name })),
])
const formatTime = (ts) => (ts ? new Date(ts).toLocaleString() : EMPTY)

// Accessible name for the row trigger: the visible text is just a timestamp,
// which says nothing about which invocation the drawer would open.
const detailLabel = (log) =>
  `Open invocation details for ${getFnName(log.function_id)} at ${formatTime(log.started_at)}`

const openTrace = (traceId) => router.push('/traces/' + traceId)

const formatBytes = (n) => {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

const getFnName = (id) => fnMap.value[id] || id?.slice(0, 12) || '?'

const loadFnMap = async () => {
  try {
    const res = await listFunctions()
    ;(res.data.functions || []).forEach((f) => (fnMap.value[f.id] = f.name))
  } catch {}
}

const buildParams = (offset) => {
  const p = { limit: PAGE_SIZE, offset }
  if (filters.value.fnId)   p.function_id = filters.value.fnId
  if (filters.value.status) p.status = filters.value.status
  if (filters.value.range)  p.since = sinceFromRange(filters.value.range)
  if (filters.value.q)      p.q = filters.value.q
  return p
}

// `silent` is for the live-tail poll: it must not drive the Refresh spinner
// or disable Load more every five seconds.
const fetchLogs = async ({ silent = false } = {}) => {
  if (!silent) loading.value = true
  try {
    const res = await listInvocations(buildParams(0))
    logs.value = res.data.executions || []
    total.value = res.data.total ?? logs.value.length
    loadError.value = ''
  } catch (e) {
    console.error('Failed to fetch logs:', e)
    // Surface the failure so the all-failed fallback (error + Retry) can
    // render in place of an endless spinner when we have no rows to show.
    loadError.value = e?.response?.data?.error?.message || e.message || 'Request failed'
  } finally {
    if (!silent) loading.value = false
  }
}

// Live tail. The interval used to call fetchLogs, which assigns page 0
// straight into `logs` — every row the operator had appended with Load more
// vanished on the next tick. Once they have paged past the first page, merge
// the fresh page over what we hold instead: rows pushed out of page 0 by
// newer arrivals keep their place directly below it, so the list stays
// newest-first. Rows deleted from another session linger until the next
// explicit refresh, which is much the cheaper of the two wrong answers.
const pollTick = async () => {
  if (loading.value) return  // a manual refresh or Load more is mid-flight
  if (logs.value.length <= PAGE_SIZE) {
    await fetchLogs({ silent: true })
    return
  }
  try {
    const res = await listInvocations(buildParams(0))
    const fresh = res.data.executions || []
    const freshIds = new Set(fresh.map((l) => l.id))
    logs.value = [...fresh, ...logs.value.filter((l) => !freshIds.has(l.id))]
    total.value = res.data.total ?? logs.value.length
    loadError.value = ''
  } catch (e) {
    // A dropped tick is not worth an error state while rows are on screen.
    console.error('Failed to poll logs:', e)
  }
}

const loadMore = async () => {
  loading.value = true
  try {
    const res = await listInvocations(buildParams(logs.value.length))
    logs.value = [...logs.value, ...(res.data.executions || [])]
    total.value = res.data.total ?? logs.value.length
  } catch (e) {
    console.error('Failed to load more:', e)
  } finally {
    loading.value = false
  }
}

const refresh = () => fetchLogs()

const bulkDelete = async () => {
  const n = selected.value.size
  const ok = await confirmStore.ask({
    title: `Delete ${n} ${n === 1 ? 'invocation' : 'invocations'}?`,
    message: 'Removes the rows + their stderr logs. This is irreversible.',
    confirmLabel: `Delete ${n}`,
    danger: true,
  })
  if (!ok) return
  bulkDeleting.value = true
  const ids = [...selected.value]
  try {
    const res = await apiClient.post('/executions/bulk-delete', { ids })
    selected.value = new Set()
    await fetchLogs()
    if (res.data.failed) {
      confirmStore.notify({
        title: 'Some deletes failed',
        message: `${res.data.failed} of ${ids.length} could not be deleted.`,
        danger: true,
      })
    }
  } catch (e) {
    confirmStore.notify({
      title: 'Bulk delete failed',
      message: e.response?.data?.error?.message || e.message,
      danger: true,
    })
  } finally {
    bulkDeleting.value = false
  }
}

// Remembers the row passed to the open drawer so the in-drawer Retry can
// re-run the same fetch without the caller plumbing the argument back in.
const lastDetailLog = ref(null)

const openDetail = async (log) => {
  lastDetailLog.value = log
  drawerRow.value = log
  drawerOpen.value = true
  detailLoading.value = true
  detailError.value = ''
  stderrText.value = ''
  copied.value = false
  requestData.value = null
  requestUnavailable.value = false
  cachedSource.value = null
  try {
    const [detailRes, logsRes, reqRes] = await Promise.allSettled([
      getInvocation(log.id),
      getInvocationLogs(log.id),
      getExecutionRequest(log.id),
    ])
    // A rejected request envelope is expected (404 when capture is off), so
    // it doesn't count toward "everything failed". If both detail AND logs
    // rejected, we have nothing meaningful to render — surface a retry.
    if (detailRes.status === 'rejected' && logsRes.status === 'rejected') {
      const err = detailRes.reason
      detailError.value =
        err?.response?.data?.error?.message || err?.message || 'Failed to load invocation detail'
    }
    if (detailRes.status === 'fulfilled') {
      // Server returns the full Execution row — overlay over the row data.
      drawerRow.value = { ...log, ...detailRes.value.data }
    }
    if (logsRes.status === 'fulfilled') {
      stderrText.value = logsRes.value.data.stderr || ''
      structuredLogs.value = logsRes.value.data.log_entries || []
    }
    if (reqRes.status === 'fulfilled') {
      requestData.value = reqRes.value.data
    } else {
      // 404 means capture was disabled when this row ran, or the feature
      // is off at the platform level. Either way: no Replay for this row.
      requestUnavailable.value = true
    }
  } finally {
    detailLoading.value = false
  }
}

// deepParse strips a layer of JSON-string wrapping, recursively. Copied
// from KVStore.vue — the Request panel's body display benefits from the
// same "show the actual shape, not an escaped JSON string" treatment.
const deepParse = (v, depth = 3) => {
  if (depth <= 0 || typeof v !== 'string') return v
  const s = v.trim()
  if (!s || !'{["tfn0123456789-'.includes(s[0])) return v
  try {
    const parsed = JSON.parse(s)
    return deepParse(parsed, depth - 1)
  } catch {
    return v
  }
}

const prettyBody = (raw) => {
  if (!raw) return ''
  const u = deepParse(raw)
  if (typeof u === 'string') return u
  try {
    return JSON.stringify(u, null, 2)
  } catch {
    return String(raw)
  }
}

// v0.4 B3: pack the captured request envelope into a base64-encoded
// query param and route to the editor. The editor's onMounted hook
// pulls `prefill` off the URL, decodes it, and lands the user on the
// Test pane with method/path/headers/body already filled in. We pass
// through the redacted headers as-is — the editor lets the user prune
// them before saving.
const saveAsFixture = () => {
  if (!drawerRow.value || !requestData.value) return
  const fnName = getFnName(drawerRow.value.function_id)
  if (!fnName || fnName === drawerRow.value.function_id?.slice(0, 12)) {
    confirmStore.notify({
      title: 'Function not loaded',
      message: 'Could not resolve the function name. Try opening the function from the dashboard first, then revisit this drawer.',
      danger: true,
    })
    return
  }
  const env = {
    method: requestData.value.method,
    path: requestData.value.path,
    headers: requestData.value.headers || {},
    body: requestData.value.body || '',
  }
  // base64(JSON) keeps the URL short and survives nested quotes / newlines
  // in the body without each layer needing its own URL-encoding pass.
  const encoded = btoa(unescape(encodeURIComponent(JSON.stringify(env))))
  router.push({
    path: `/functions/${fnName}`,
    query: { prefill: encoded },
  })
  drawerOpen.value = false
}

const replay = async () => {
  if (!drawerRow.value || replaying.value || requestUnavailable.value) return
  replaying.value = true
  try {
    const res = await replayExecution(drawerRow.value.id)
    // The backend stamps the new execution id on the response header.
    // Some axios + responseType=text combinations lower-case the keys —
    // try both for safety.
    const newId =
      res.headers?.['x-orva-execution-id'] ||
      res.headers?.['X-Orva-Execution-ID']
    if (newId) {
      // Open a fresh drawer pointed at the new row. We don't have the
      // full Execution row yet — fetch the list refresh in parallel so
      // the table reflects the new entry too.
      fetchLogs()
      await openDetail({ id: newId, function_id: drawerRow.value.function_id, status: 'running', started_at: new Date().toISOString() })
    } else {
      fetchLogs()
    }
  } catch (e) {
    confirmStore.notify({
      title: 'Replay failed',
      message: e?.response?.data?.error?.message || e.message,
      danger: true,
    })
  } finally {
    replaying.value = false
  }
}

const copy = async (text) => {
  if (await copyText(text)) {
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  }
}

// v0.4 B4 — assemble a debug prompt from {source, request, stderr,
// error} and write it to the clipboard. NOTHING is sent to the network
// from this handler beyond the GET /functions/<id>/source call (and
// even that we cache for the drawer lifetime). Operator pastes the
// result into ChatGPT / Claude / Cursor / Copilot manually.
const suggestFix = async () => {
  if (!drawerRow.value || suggestingFix.value) return
  if (!stderrText.value) return  // disabled state already enforces this
  suggestingFix.value = true
  try {
    // Resolve runtime + source. cachedSource lives until the drawer
    // closes or the user opens a different row.
    if (!cachedSource.value) {
      // The /source endpoint returns {code, filename, runtime}. Fall
      // back to a function GET if the runtime field is missing on the
      // older shape — we want a clean "Python 3.14"-style label in the
      // prompt's <context> section.
      try {
        const res = await getFunctionSource(drawerRow.value.function_id)
        cachedSource.value = {
          source: res.data?.code || res.data?.source || '',
          runtime: res.data?.runtime || '',
        }
        if (!cachedSource.value.runtime) {
          try {
            const fnRes = await getFunction(drawerRow.value.function_id)
            cachedSource.value.runtime = fnRes.data?.runtime || ''
          } catch { /* non-fatal — prompt still works without runtime */ }
        }
      } catch (e) {
        confirmStore.notify({
          title: 'Could not load function source',
          message: e?.response?.data?.error?.message || e.message ||
            'Source fetch failed. The function may have been deleted.',
          danger: true,
        })
        return
      }
    }
    const ok = await copyFixSuggestionToClipboard({
      source:         cachedSource.value.source,
      runtime:        cachedSource.value.runtime,
      stderr:         stderrText.value,
      requestPreview: requestData.value
        ? {
            method:  requestData.value.method,
            path:    requestData.value.path,
            headers: requestData.value.headers || {},
            body:    requestData.value.body || '',
          }
        : null,
      errorMessage: drawerRow.value.error_message || '',
      statusCode:   drawerRow.value.status_code || '',
    })
    if (ok) {
      confirmStore.notify({
        title: 'Prompt copied',
        message: 'Paste into ChatGPT, Claude, or your AI tool of choice.',
      })
    } else {
      confirmStore.notify({
        title: 'Copy failed',
        message: 'Could not write to the clipboard. Try again, or copy the stderr by hand.',
        danger: true,
      })
    }
  } finally {
    suggestingFix.value = false
  }
}

// keep-alive lifecycle: pause polling when the view is offscreen and
// resume when the user comes back. This avoids running a 5s timer for
// every cached view, and refreshes once on re-activation so they see
// new rows immediately.
// startPolling is idempotent — the pollTimer guard prevents a second
// interval if onMounted and onActivated both fire on the initial mount.
const startPolling = () => {
  if (pollTimer) return
  pollTimer = setInterval(pollTick, 5000)
}
const stopPolling = () => {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
}

// onMounted runs once even with keep-alive — initial load + start polling.
onMounted(async () => {
  await loadFnMap()
  await fetchLogs()
  startPolling()
})
onActivated(() => { fetchLogs(); startPolling() })
onDeactivated(stopPolling)
onUnmounted(stopPolling)
</script>
