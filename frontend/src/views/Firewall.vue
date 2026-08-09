<template>
  <div class="space-y-8">
    <!-- Header -->
    <header class="space-y-3">
      <div class="flex items-start justify-between gap-4 flex-wrap">
        <div class="max-w-2xl">
          <h1 class="text-xl font-semibold text-white tracking-tight">
            Egress controls
          </h1>
          <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
            Decide what your functions are allowed to talk to. Each switch
            below blocks one destination; turn one on and your functions
            can no longer reach it; turn it off and they can. Blocks compile
            into a sandbox egress policy that nsjail loads per sandbox —
            nothing on the host is reconfigured. DNS settings below control
            how your functions look hostnames up.
          </p>
        </div>
        <div class="flex items-center gap-2">
          <Button
            variant="secondary"
            size="sm"
            :loading="resolving"
            @click="forceResolve"
          >
            <RefreshCw class="w-4 h-4" /> Apply now
          </Button>
          <Button
            size="sm"
            @click="showCreate = true"
          >
            <Plus class="w-4 h-4" /> Block something
          </Button>
        </div>
      </div>

      <!-- Status strip — calm, mirrors the Docs/Logs aesthetic. "Stale" is kept
           apart from "not enforced" on purpose: a stale policy still has the
           last known-good generation loaded in every sandbox. -->
      <div
        class="flex items-start gap-3 text-xs px-3 py-2 rounded-md border"
        :class="statusBannerClass"
      >
        <component
          :is="statusIcon"
          class="w-4 h-4 shrink-0 mt-0.5"
        />
        <div class="flex-1 min-w-0 space-y-1">
          <p class="leading-snug">
            {{ statusBannerText }}
          </p>
          <!-- The daemon's own words, never paraphrased. -->
          <p
            v-if="status.last_error"
            class="font-mono text-[11px] leading-snug break-words opacity-90"
          >
            {{ status.last_error }}
          </p>
          <p
            v-if="statusRemedy"
            class="leading-snug opacity-80"
          >
            {{ statusRemedy }}
          </p>
        </div>
        <span
          v-if="status.enforced"
          class="text-foreground-muted hidden sm:inline shrink-0"
        >
          {{ enforcedRulesCount }} enforced · {{ disabledRulesCount }} off
        </span>
      </div>

      <!-- What is actually loaded into the sandboxes right now. -->
      <div class="policy-meta">
        <span class="policy-chip">
          <span class="policy-chip-k">Backend</span>
          <span class="policy-chip-v">{{ status.backend || 'nstun' }}</span>
        </span>
        <span class="policy-chip">
          <span class="policy-chip-k">Generation</span>
          <span class="policy-chip-v">{{ status.policy_generation || 'none' }}</span>
        </span>
        <span class="policy-chip">
          <span class="policy-chip-k">Compiled rules</span>
          <span class="policy-chip-v">{{ ruleCountsText }}</span>
        </span>
        <span
          v-if="controlPlaneText"
          class="policy-chip"
        >
          <span class="policy-chip-k">SDK carve-out</span>
          <span class="policy-chip-v">{{ controlPlaneText }}</span>
        </span>
        <span
          v-if="appliedAt"
          class="policy-chip"
        >
          <span class="policy-chip-k">Applied</span>
          <span class="policy-chip-v">{{ appliedAt }}</span>
        </span>
      </div>
    </header>

    <!-- DNS — resolvers + host overrides for sandboxed functions. -->
    <PanelSection
      title="DNS"
      :subtitle="dnsSubtitle"
    >
      <div class="dns-card">
        <!-- Resolvers row -->
        <div class="dns-row">
          <div class="dns-row-label">
            Upstream resolvers
          </div>
          <div class="dns-current">
            <div
              v-if="dns.servers.length"
              class="dns-chips"
            >
              <span
                v-for="(s, idx) in dns.servers"
                :key="s + idx"
                class="dns-chip"
              >
                <Globe2 class="w-3 h-3 opacity-60" />
                <span class="font-mono">{{ s }}</span>
                <button
                  class="dns-chip-x"
                  title="Remove"
                  @click="removeServer(idx)"
                >
                  ×
                </button>
              </span>
            </div>
            <div
              v-else
              class="dns-defaults"
            >
              <span class="text-foreground-muted text-xs">Defaults:</span>
              <span
                v-for="d in dns.defaults"
                :key="d"
                class="dns-chip muted"
              >
                <Globe2 class="w-3 h-3 opacity-60" />
                <span class="font-mono">{{ d }}</span>
              </span>
            </div>
          </div>
          <div class="dns-form">
            <input
              v-model="dnsAddInput"
              placeholder="1.1.1.1"
              class="dns-input"
              @keydown.enter="addServer"
            >
            <Button
              variant="secondary"
              size="sm"
              :disabled="!dnsAddInput.trim()"
              @click="addServer"
            >
              <Plus class="w-3.5 h-3.5" /> Add resolver
            </Button>
            <input
              v-model="dns.search"
              placeholder="search domain"
              class="dns-input narrow"
            >
          </div>
        </div>

        <!-- Custom records row -->
        <div class="dns-row">
          <div class="dns-row-label">
            Host overrides
            <span class="dns-row-meta">{{ dns.records.length }} record{{ dns.records.length === 1 ? '' : 's' }}</span>
          </div>
          <div
            v-if="dns.records.length"
            class="dns-records"
          >
            <div
              v-for="(rec, idx) in dns.records"
              :key="rec.host + idx"
              class="dns-record"
            >
              <span class="font-mono text-white text-xs flex-1 truncate">{{ rec.host }}</span>
              <span class="text-foreground-muted text-xs">→</span>
              <span class="font-mono text-foreground text-xs flex-1 truncate">{{ rec.ip }}</span>
              <button
                class="dns-chip-x"
                title="Remove"
                @click="removeRecord(idx)"
              >
                ×
              </button>
            </div>
          </div>
          <div
            v-else
            class="text-xs text-foreground-muted italic px-1"
          >
            No overrides. Anything resolves through the upstream resolvers above.
          </div>
          <div class="dns-form">
            <input
              v-model="recordHostInput"
              placeholder="api.internal"
              class="dns-input host"
              @keydown.enter="addRecord"
            >
            <span class="text-foreground-muted text-xs">→</span>
            <input
              v-model="recordIPInput"
              placeholder="10.0.5.10"
              class="dns-input"
              @keydown.enter="addRecord"
            >
            <Button
              variant="secondary"
              size="sm"
              :disabled="!(recordHostInput.trim() && recordIPInput.trim())"
              @click="addRecord"
            >
              <Plus class="w-3.5 h-3.5" /> Add record
            </Button>
          </div>
        </div>

        <!-- Save bar -->
        <div class="dns-savebar">
          <span class="dns-hint">
            Records win over upstream DNS. Anything in the override list bypasses
            resolution entirely. Existing warm workers keep their previous files;
            toggle the function's network off and on, or wait for idle TTL, to apply.
          </span>
          <button
            v-if="dns.servers.length || dns.search || dns.records.length"
            class="text-[11px] text-foreground-muted hover:text-white px-2 py-1 transition-colors"
            @click="resetDNS"
          >
            Reset
          </button>
          <Button
            size="sm"
            :loading="savingDNS"
            :disabled="!dnsDirty"
            @click="saveDNS"
          >
            Save
          </Button>
        </div>
      </div>
    </PanelSection>

    <!-- Unified blocklist — one section, filter tabs, friendly cards. -->
    <PanelSection
      title="Blocklist"
      :subtitle="blocklistSubtitle"
    >
      <!-- Stored but not in the compiled policy. Listed in full (not just as a
           count) because the previous UI implied these were blocking. -->
      <div
        v-if="unenforcedNotices.length"
        class="unenforced-note"
      >
        <AlertTriangle class="w-3.5 h-3.5 shrink-0 mt-0.5" />
        <div class="min-w-0">
          <p>
            {{ unenforcedNotices.length === 1
              ? 'One stored rule is not part of the compiled policy and blocks nothing:'
              : `${unenforcedNotices.length} stored rules are not part of the compiled policy and block nothing:` }}
          </p>
          <ul>
            <li
              v-for="u in unenforcedNotices"
              :key="u.id"
            >
              <code>{{ u.value }}</code> — {{ u.reason }}
            </li>
          </ul>
        </div>
      </div>

      <!-- Filter tabs -->
      <div class="rule-filterbar">
        <button
          v-for="tab in filterTabs"
          :key="tab.id"
          class="rule-filter"
          :class="{ active: filter === tab.id }"
          @click="filter = tab.id"
        >
          {{ tab.label }}
          <span class="rule-filter-count">{{ tab.count }}</span>
        </button>
      </div>

      <!-- Empty state — only shows when there are zero rules in the current filter. -->
      <div
        v-if="!visibleRules.length"
        class="empty-card"
      >
        <ShieldOff class="w-5 h-5 mb-2 text-foreground-muted/60" />
        <p class="text-sm text-white">
          {{ filter === 'yours' ? 'No custom blocks yet' : 'Nothing matches this filter' }}
        </p>
        <p class="text-xs text-foreground-muted mt-1 max-w-sm">
          {{ filter === 'yours'
            ? 'Block a specific IP, CIDR, or hostname to keep your functions out of internal infrastructure they shouldn\'t reach.'
            : 'Try a different filter, or add a custom block above.' }}
        </p>
        <Button
          v-if="filter === 'yours'"
          class="mt-4"
          size="sm"
          variant="secondary"
          @click="showCreate = true"
        >
          <Plus class="w-3.5 h-3.5" /> Block something
        </Button>
      </div>

      <div
        v-else
        class="rule-grid"
      >
        <RuleCard
          v-for="firewallRule in visibleRules"
          :key="firewallRule.id"
          :rule="firewallRule"
          :status="status"
          :unenforced-reason="unenforcedReason(firewallRule)"
          :busy="busyId === firewallRule.id"
          :readonly-edit="firewallRule.kind !== 'custom'"
          @toggle="toggle(firewallRule)"
          @delete="deleteRule(firewallRule)"
        />
      </div>
    </PanelSection>

    <!-- Add-rule modal -->
    <Modal
      v-model="showCreate"
      title="Block something"
      :icon="ShieldAlert"
      size="md"
    >
      <div class="space-y-4">
        <p class="text-xs text-foreground-muted leading-snug">
          Pick what you're blocking: a single IP, a network range, or a hostname.
          Once added, your functions can no longer reach it. Wildcard patterns
          like *.example.com are not supported — the policy matches addresses,
          not names.
        </p>
        <div>
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-2">
            What is it?
          </label>
          <div class="grid grid-cols-2 gap-2">
            <button
              v-for="opt in typeOptions"
              :key="opt.value"
              class="px-2 py-2 rounded border text-xs font-medium transition-colors flex flex-col items-center gap-1"
              :class="newRule.rule_type === opt.value
                ? 'bg-white text-black border-white'
                : 'bg-surface-hover text-foreground-muted border-border hover:border-foreground-muted'"
              @click="newRule.rule_type = opt.value"
            >
              <component
                :is="opt.icon"
                class="w-3.5 h-3.5"
              />
              {{ opt.label }}
            </button>
          </div>
          <p class="text-[10.5px] text-foreground-muted mt-1.5 leading-snug">
            {{ typeHint }}
          </p>
        </div>
        <Input
          v-model="newRule.value"
          :label="newRule.rule_type === 'hostname' ? 'Hostname' : 'IP or network'"
          :placeholder="placeholderForType"
        />
        <p
          v-if="wildcardAttempt"
          class="text-[11px] text-danger-fg leading-snug"
        >
          {{ WILDCARD_REASON }}
        </p>
        <Input
          v-model="newRule.label"
          label="Why? (optional)"
          placeholder="e.g. our staging Postgres"
        />
        <p class="text-[11px] text-foreground-muted leading-snug">
          Takes effect within seconds. Warm functions are recycled so the
          new block applies on the very next call.
        </p>
      </div>
      <template #footer>
        <Button
          variant="secondary"
          @click="showCreate = false"
        >
          Cancel
        </Button>
        <Button
          :loading="creating"
          :disabled="!newRule.value.trim() || wildcardAttempt"
          @click="submitCreate"
        >
          <Plus class="w-4 h-4" /> Block it
        </Button>
      </template>
    </Modal>
  </div>
</template>

<script setup>
/* eslint-disable vue/one-component-per-file -- Private render components share this page's state and styling. */
import { computed, h, onMounted, onActivated, ref, defineComponent } from 'vue'
import {
  Plus, RefreshCw, ShieldAlert, ShieldOff, ShieldCheck,
  AlertTriangle, Globe, Globe2, Asterisk, Hash, Trash2,
} from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import Modal from '@/components/common/Modal.vue'
import apiClient from '@/api/client'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

const rules = ref([])

// Mirrors firewall.Snapshot. Seeded with the "nothing compiled yet" shape so
// the page never renders an enforced-looking state before the first load, and
// so optional fields the server omits (last_error, unenforced_rules) always
// exist as empty values.
const emptyStatus = () => ({
  ipv4: [], ipv6: [], hostname_map: {}, last_error: '',
  backend: 'nstun', enforced: false, policy_generation: '',
  policy_rule_counts: { v4: 0, v6: 0, allow: 0, reject: 0 },
  policy_stale: false, last_compile_error: '', last_success_at: '',
  control_plane_allow: { addrs: [], port: 0 },
  unenforced_rules: [],
})
const status = ref(emptyStatus())
const busyId = ref(null)
const showCreate = ref(false)
const creating = ref(false)
const resolving = ref(false)
const newRule = ref({ rule_type: 'cidr', value: '', label: '' })

// DNS settings — operator-managed resolvers and host overrides for
// sandboxed functions with outbound network access.
const dns = ref({ servers: [], search: '', records: [], defaults: [] })
const savedDNS = ref({ servers: [], search: '', records: [] })  // last persisted; for dirty check
const dnsAddInput = ref('')
const recordHostInput = ref('')
const recordIPInput = ref('')
const savingDNS = ref(false)

const dnsDirty = computed(() => {
  const a = JSON.stringify({
    s: dns.value.servers || [],
    q: dns.value.search || '',
    r: (dns.value.records || []).map(r => `${r.host}=${r.ip}`).sort(),
  })
  const b = JSON.stringify({
    s: savedDNS.value.servers || [],
    q: savedDNS.value.search || '',
    r: (savedDNS.value.records || []).map(r => `${r.host}=${r.ip}`).sort(),
  })
  return a !== b
})

const dnsSubtitle = computed(() => {
  const parts = []
  parts.push(dns.value.servers.length
    ? `${dns.value.servers.length} resolver${dns.value.servers.length === 1 ? '' : 's'}`
    : `defaults (${(dns.value.defaults || []).join(', ') || 'none'})`)
  if (dns.value.records.length) parts.push(`${dns.value.records.length} override${dns.value.records.length === 1 ? '' : 's'}`)
  return parts.join(' · ')
})

const loadDNS = async () => {
  try {
    const res = await apiClient.get('/firewall/dns')
    dns.value = {
      servers: res.data.servers || [],
      search:  res.data.search  || '',
      records: res.data.records || [],
      defaults: res.data.defaults || [],
    }
    savedDNS.value = {
      servers: [...dns.value.servers],
      search: dns.value.search,
      records: dns.value.records.map(r => ({ ...r })),
    }
  } catch (e) {
    console.error('loadDNS failed', e)
  }
}

const addServer = () => {
  const v = dnsAddInput.value.trim()
  if (!v) return
  // Light client-side validation; server enforces strictly.
  // Accept v4 a.b.c.d or v6 (contains ':').
  const looksValid = /^[0-9.]+$/.test(v) || v.includes(':')
  if (!looksValid) {
    confirmStore.notify({ title: 'Invalid IP', message: `"${v}" doesn't look like an IPv4 or IPv6 address.` })
    return
  }
  if (dns.value.servers.includes(v)) {
    dnsAddInput.value = ''
    return
  }
  dns.value.servers = [...dns.value.servers, v]
  dnsAddInput.value = ''
}
const removeServer = (idx) => {
  dns.value.servers = dns.value.servers.filter((_, i) => i !== idx)
}
const addRecord = () => {
  const host = recordHostInput.value.trim()
  const ip = recordIPInput.value.trim()
  if (!host || !ip) return
  const looksHost = /^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$/.test(host)
  const looksIP = /^[0-9.]+$/.test(ip) || ip.includes(':')
  if (!looksHost) {
    confirmStore.notify({ title: 'Invalid hostname', message: `"${host}" is not a valid hostname.` })
    return
  }
  if (!looksIP) {
    confirmStore.notify({ title: 'Invalid IP', message: `"${ip}" is not a literal IPv4 or IPv6 address.` })
    return
  }
  if ((dns.value.records || []).some(r => r.host === host)) {
    confirmStore.notify({ title: 'Duplicate host', message: `"${host}" already has an override.` })
    return
  }
  dns.value.records = [...(dns.value.records || []), { host, ip }]
  recordHostInput.value = ''
  recordIPInput.value = ''
}
const removeRecord = (idx) => {
  dns.value.records = dns.value.records.filter((_, i) => i !== idx)
}
const resetDNS = () => {
  dns.value.servers = []
  dns.value.search = ''
  dns.value.records = []
}
const saveDNS = async () => {
  savingDNS.value = true
  try {
    const res = await apiClient.put('/firewall/dns', {
      servers: dns.value.servers,
      search: dns.value.search || '',
      records: dns.value.records || [],
    })
    dns.value = {
      servers: res.data.servers || [],
      search:  res.data.search  || '',
      records: res.data.records || [],
      defaults: res.data.defaults || dns.value.defaults,
    }
    savedDNS.value = {
      servers: [...dns.value.servers],
      search: dns.value.search,
      records: dns.value.records.map(r => ({ ...r })),
    }
  } catch (e) {
    confirmStore.notify({
      title: 'Save failed',
      message: e.response?.data?.error?.message || e.message,
      danger: true,
    })
  } finally {
    savingDNS.value = false
  }
}

const customRules    = computed(() => rules.value.filter((r) => r.kind === 'custom'))

// Rules the compiler deliberately left out of the policy, keyed by rule id.
const unenforcedById = computed(() => {
  const m = new Map()
  for (const u of status.value.unenforced_rules || []) m.set(u.id, u.reason)
  return m
})

// A packet carries an address, not a name, so a wildcard can never be a rule.
// Stored wildcard rows stay in the table untouched; the API refuses new ones.
const WILDCARD_REASON = 'Wildcard patterns are not enforceable: the egress policy matches IPs and CIDRs, not DNS names. Block a CIDR or an exact hostname instead.'

// The daemon only compiles ENABLED rows, so a disabled wildcard never shows up
// in unenforced_rules — the rule_type check is what keeps those rows honest.
const unenforcedReason = (rule) =>
  unenforcedById.value.get(rule.id)
  || (rule.rule_type === 'wildcard' ? WILDCARD_REASON : '')

const isUnenforced = (rule) => unenforcedReason(rule) !== ''

// Every rule the operator must not mistake for an active block, in one list so
// the section can state them outright rather than only tinting a card.
const unenforcedNotices = computed(() => {
  const byId = new Map()
  for (const u of status.value.unenforced_rules || []) {
    byId.set(u.id, { id: u.id, value: u.value, reason: u.reason })
  }
  for (const r of rules.value) {
    if (r.rule_type === 'wildcard' && !byId.has(r.id)) {
      byId.set(r.id, { id: r.id, value: r.value, reason: WILDCARD_REASON })
    }
  }
  return [...byId.values()]
})

// Plain-language explanations for shipped (default + suggested) rules. The
// table values are stable identifiers in the migration so we can key on
// them. Each entry gives a human title and a one-sentence "why this matters"
// so an operator who isn't a network engineer can decide whether to leave
// it on or turn it off without reading the CIDR. Custom rules use the
// operator's own label; if no friendly name exists for a shipped rule we
// fall back to the rule's stored label.
const friendlyMap = {
  '169.254.0.0/16':    { name: 'Cloud metadata service',  why: 'Blocks the special address AWS, Azure, and GCP use to expose VM credentials and instance settings. Leaving this open is a common credential-leak path.' },
  'fd00:ec2::254/128': { name: 'Cloud metadata (IPv6)',   why: 'Same as above, but the IPv6 path GCP uses. Recommended on.' },
  '10.0.0.0/8':        { name: 'Private network, 10.x', why: 'Standard internal-network range. Turn on if your functions should not reach internal services on your LAN.' },
  '172.16.0.0/12':     { name: 'Private network, 172.16.x', why: 'Another internal-network range (often used by Docker default bridge). Turn on for stricter isolation.' },
  '192.168.0.0/16':    { name: 'Private network, 192.168.x', why: 'Common home/office network range. Turn on if functions should not reach your local LAN.' },
  '100.64.0.0/10':     { name: 'CGNAT / Tailscale',       why: 'Used by Tailscale and large ISPs. Turn on to keep functions out of your tailnet.' },
}

const friendlyTitle = (rule) => {
  if (rule.kind === 'custom') return rule.label || rule.value
  return friendlyMap[rule.value]?.name || rule.label || rule.value
}
const friendlyWhy = (rule) => {
  if (rule.kind === 'custom') return ''
  return friendlyMap[rule.value]?.why || ''
}

// Filter state — default to "Enforced" so the operator's first scan is "what's
// actually blocking my functions." All / Enforced / Off / Not enforced / Yours.
const filter = ref('enforced')

const filterTabs = computed(() => {
  const tabs = [
    { id: 'all',      label: 'All',      count: rules.value.length },
    { id: 'enforced', label: 'Enforced', count: enforcedRulesCount.value },
    { id: 'off',      label: 'Off',      count: disabledRulesCount.value },
  ]
  // Only surfaced when something is actually excluded — a permanent 0 tab would
  // just be noise on a healthy instance.
  if (unenforcedRules.value.length) {
    tabs.push({ id: 'unenforced', label: 'Not enforced', count: unenforcedRules.value.length })
  }
  tabs.push({ id: 'yours', label: 'Yours', count: customRules.value.length })
  return tabs
})

const visibleRules = computed(() => {
  // Within whichever filter, sort: enforceable first, then enabled, then
  // default → suggested → custom, then by friendly name. Stable order across
  // renders keeps toggles from jumping.
  const kindRank = { default: 0, suggested: 1, custom: 2 }
  const list = rules.value.filter((r) => {
    if (filter.value === 'enforced')   return r.enabled && !isUnenforced(r)
    if (filter.value === 'off')        return !r.enabled
    if (filter.value === 'unenforced') return isUnenforced(r)
    if (filter.value === 'yours')      return r.kind === 'custom'
    return true
  })
  return [...list].sort((a, b) => {
    const au = isUnenforced(a)
    const bu = isUnenforced(b)
    if (au !== bu) return au ? 1 : -1
    if (a.enabled !== b.enabled) return a.enabled ? -1 : 1
    if (kindRank[a.kind] !== kindRank[b.kind]) return kindRank[a.kind] - kindRank[b.kind]
    return friendlyTitle(a).localeCompare(friendlyTitle(b))
  })
})

// "Enforced" is enabled AND in the compiled policy — an enabled wildcard is
// neither, and counting it as active is the lie this page used to tell.
const enforcedRulesCount = computed(() => rules.value.filter((r) => r.enabled && !isUnenforced(r)).length)
const disabledRulesCount = computed(() => rules.value.filter((r) => !r.enabled).length)
const unenforcedRules = computed(() => rules.value.filter((r) => isUnenforced(r)))

const blocklistSubtitle = computed(() => {
  if (!rules.value.length) return 'Nothing in the blocklist yet.'
  const on = enforcedRulesCount.value
  const off = disabledRulesCount.value
  const parts = [
    // "enforced" would be a lie while no policy is loaded: the same rules are
    // only staged until one compiles.
    status.value.enforced
      ? `${on} block${on === 1 ? '' : 's'} enforced`
      : `${on} block${on === 1 ? '' : 's'} staged, none enforced`,
    `${off} available to turn on`,
    `${customRules.value.length} you added`,
  ]
  if (unenforcedRules.value.length) parts.push(`${unenforcedRules.value.length} not enforceable`)
  return parts.join(' · ')
})

const typeOptions = [
  { value: 'cidr',     label: 'IP / Range', icon: Hash },
  { value: 'hostname', label: 'Hostname',   icon: Globe },
]

const placeholderForType = computed(() =>
  newRule.value.rule_type === 'hostname' ? 'api.internal.corp' : '192.168.1.0/24')

const typeHint = computed(() => {
  if (newRule.value.rule_type === 'hostname') {
    return 'A specific website or service name. We resolve it to IPs and block those, re-resolving on every refresh.'
  }
  return 'A single IP (e.g. 1.2.3.4) or a CIDR range (e.g. 10.0.0.0/8) to block all addresses inside it.'
})

// The API rejects wildcards outright, so explain it here rather than let the
// operator earn a 400.
const wildcardAttempt = computed(() => newRule.value.value.includes('*'))

// Policy state — four cases, kept apart because each asks for different action.
// Order matters: stale means a good policy IS still loaded, so it must be
// tested before the generic error case.
const policyState = computed(() => {
  if (status.value.enforced && status.value.policy_stale) return 'stale'
  if (!status.value.enforced) return 'unenforced'
  if (status.value.last_error) return 'degraded'
  return 'ok'
})

const statusBannerClass = computed(() => {
  switch (policyState.value) {
    case 'unenforced': return 'border-danger-ring bg-danger-tint text-danger-fg'
    case 'stale':
    case 'degraded':   return 'border-warning-ring bg-warning-tint text-warning-fg'
    default:           return 'border-success-ring bg-success-tint text-foreground-muted'
  }
})
const statusIcon = computed(() => (policyState.value === 'ok' ? ShieldCheck : AlertTriangle))
const statusBannerText = computed(() => {
  switch (policyState.value) {
    case 'unenforced':
      return 'No egress policy is compiled, so nothing below is in force. Orva fails closed: functions with outbound network enabled cannot start until a policy compiles.'
    case 'stale':
      return `Enforcing the last known-good policy (generation ${status.value.policy_generation}). The newest recompile failed, so recent changes are not live yet.`
    case 'degraded':
      return 'The policy is in force, but the last refresh reported a problem.'
    default:
      return `Enforced per sandbox by nsjail NSTUN, generation ${status.value.policy_generation}. Applies to every function with outbound network enabled.`
  }
})
// Nothing to install: enforcement rides along with nsjail. The two real failure
// modes are the policy not compiling and nsjail having no tun device to attach
// the sandbox network to.
const statusRemedy = computed(() => {
  switch (policyState.value) {
    case 'unenforced':
      return 'Hit Apply now to recompile. There is nothing to install: if invocations fail with a sandbox error instead, nsjail needs /dev/net/tun (Docker: --device /dev/net/tun).'
    case 'stale':
    case 'degraded':
      return 'Fix the reported rule or resolver, then hit Apply now to recompile.'
    default:
      return ''
  }
})

const ruleCountsText = computed(() => {
  const c = status.value.policy_rule_counts || {}
  return `${c.v4 ?? 0} v4 · ${c.v6 ?? 0} v6 · ${c.reject ?? 0} reject · ${c.allow ?? 0} allow`
})

const controlPlaneText = computed(() => {
  const cp = status.value.control_plane_allow || {}
  const addrs = cp.addrs || []
  if (!addrs.length) return ''
  return `${addrs.join(', ')}${cp.port ? `:${cp.port}` : ''}`
})

const appliedAt = computed(() => {
  const raw = status.value.last_success_at
  if (!raw) return ''
  const d = new Date(raw)
  return Number.isNaN(d.getTime()) ? raw : d.toLocaleTimeString()
})

const load = async () => {
  const res = await apiClient.get('/firewall/rules')
  rules.value = res.data.rules || []
  status.value = { ...emptyStatus(), ...(res.data.status || {}) }
}

const toggle = async (rule) => {
  busyId.value = rule.id
  try {
    await apiClient.put(`/firewall/rules/${rule.id}`, { enabled: !rule.enabled })
    await load()
  } catch (e) {
    confirmStore.notify({
      title: 'Toggle failed',
      message: e.response?.data?.error?.message || e.message,
      danger: true,
    })
  } finally {
    busyId.value = null
  }
}

const deleteRule = async (rule) => {
  const ok = await confirmStore.ask({
    title: 'Delete custom rule?',
    message: `"${rule.value}" will be removed from the blocklist.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  try {
    await apiClient.delete(`/firewall/rules/${rule.id}`)
    await load()
  } catch (e) {
    confirmStore.notify({
      title: 'Delete failed',
      message: e.response?.data?.error?.message || e.message,
      danger: true,
    })
  }
}

const submitCreate = async () => {
  if (!newRule.value.value.trim()) return
  creating.value = true
  try {
    await apiClient.post('/firewall/rules', {
      rule_type: newRule.value.rule_type,
      value: newRule.value.value.trim(),
      label: newRule.value.label.trim(),
    })
    showCreate.value = false
    newRule.value = { rule_type: 'cidr', value: '', label: '' }
    await load()
  } catch (e) {
    confirmStore.notify({
      title: 'Failed to add rule',
      message: e.response?.data?.error?.message || e.message,
      danger: true,
    })
  } finally {
    creating.value = false
  }
}

const forceResolve = async () => {
  resolving.value = true
  try {
    const res = await apiClient.post('/firewall/resolve')
    if (res.data.error) {
      confirmStore.notify({ title: 'Resolve had errors', message: res.data.error, danger: true })
    }
    await load()
  } catch (e) {
    confirmStore.notify({
      title: 'Resolve failed',
      message: e.response?.data?.error?.message || e.message,
      danger: true,
    })
  } finally {
    resolving.value = false
  }
}

const loadAll = async () => { await Promise.all([load(), loadDNS()]) }
onMounted(loadAll)
onActivated(loadAll)

// ── PanelSection component (matches Docs page aesthetic) ─────────────
// Named PanelSection, not Section: a bare "Section" collides with the
// reserved HTML <section> element name (vue/no-reserved-component-names).
const PanelSection = defineComponent({
  name: 'PanelSection',
  props: {
    title: { type: String, default: '' },
    subtitle: { type: String, default: '' },
  },
  setup(p, { slots }) {
    return () =>
      h('section', { class: 'space-y-3' }, [
        h('div', null, [
          h('h2', { class: 'text-sm font-semibold text-white tracking-tight' }, p.title),
          p.subtitle ? h('p', { class: 'text-xs text-foreground-muted mt-0.5' }, p.subtitle) : null,
        ]),
        h('div', null, slots.default?.()),
      ])
  },
})

// ── RuleCard — friendly, plain-language rule cell ────────────────────
// Top row: friendly name + a small "kind" pill (Recommended / Optional /
// Yours) + the toggle.
// Body: one-sentence explanation of what this protects (operators who
// aren't networking experts shouldn't have to decode 169.254.0.0/16).
// Footer: the actual rule value in mono so the technical user can still
// see what's enforced.
//
// The card has four visual states, and only one of them may look active:
// enforced (in the compiled policy), off (operator turned it off), unenforced
// (stored but excluded from the policy), and pending (enabled, but no policy is
// in force at all). The last two are never green.
const KIND_PILLS = {
  default:   { label: 'Recommended', cls: 'kind-recommended' },
  suggested: { label: 'Optional',    cls: 'kind-optional' },
  custom:    { label: 'Yours',       cls: 'kind-yours' },
}

const RuleCard = defineComponent({
  name: 'RuleCard',
  props: {
    rule:             { type: Object,  required: true },
    status:           { type: Object,  required: true },
    unenforcedReason: { type: String,  default: '' },
    busy:             { type: Boolean, default: false },
    readonlyEdit:     { type: Boolean, default: false },
  },
  emits: ['toggle', 'delete'],
  setup(p, { emit }) {
    const TypeIcon = computed(() => {
      switch (p.rule.rule_type) {
        case 'hostname': return Globe
        case 'wildcard': return Asterisk
        default:         return Hash
      }
    })
    const resolved = computed(() => {
      if (p.rule.rule_type === 'cidr') return [p.rule.value]
      const ips = p.status.hostname_map?.[p.rule.value] || []
      return ips
    })
    const title = computed(() => friendlyTitle(p.rule))
    const why = computed(() => friendlyWhy(p.rule))
    const pill = computed(() => KIND_PILLS[p.rule.kind] || KIND_PILLS.custom)

    const state = computed(() => {
      if (p.unenforcedReason) return 'unenforced'
      if (!p.rule.enabled) return 'off'
      return p.status.enforced ? 'on' : 'pending'
    })
    // Turning an unenforceable rule ON is refused by the API, so the switch
    // stays dead rather than inviting a 400. Turning one off is still allowed
    // (that is how the operator retires a stored wildcard).
    const toggleBlocked = computed(() => state.value === 'unenforced' && !p.rule.enabled)
    const toggleTitle = computed(() => {
      if (toggleBlocked.value) return 'Cannot be enforced — see the reason on this card'
      return p.rule.enabled ? 'Click to allow' : 'Click to block'
    })

    return () =>
      h('div', {
        class: ['rule-card', `is-${state.value}`],
      }, [
        // Top row: title + pill(s) + toggle
        h('div', { class: 'rule-card-row' }, [
          h('div', { class: 'rule-card-titlewrap' }, [
            h('div', { class: 'rule-card-title' }, title.value),
            h('div', { class: 'rule-card-pills' }, [
              h('span', { class: ['rule-kind-pill', pill.value.cls] }, pill.value.label),
              state.value === 'unenforced'
                ? h('span', { class: 'rule-kind-pill kind-inert' }, 'Not enforced')
                : null,
              state.value === 'pending'
                ? h('span', { class: 'rule-kind-pill kind-inert' }, 'Not in force')
                : null,
            ]),
          ]),
          h('button', {
            class: [
              'rule-toggle',
              // Only a genuinely enforced rule gets the green "on" treatment;
              // an unenforceable or not-yet-in-force rule shows its stored
              // position in an inert colour so it cannot read as active.
              state.value === 'on' ? 'on'
                : state.value === 'off' ? 'off'
                  : ['inert', p.rule.enabled ? 'is-set' : ''],
              p.busy ? 'busy' : '',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background',
            ],
            disabled: p.busy || toggleBlocked.value,
            title: toggleTitle.value,
            onClick: () => emit('toggle'),
          }, [
            h('span', { class: 'rule-toggle-knob' }),
          ]),
        ]),

        // Why this rule is not in force — the operator's most important line
        // when it applies, so it sits above the "why it matters" blurb.
        p.unenforcedReason
          ? h('p', { class: 'rule-card-warn' }, p.unenforcedReason)
          : null,

        // Why this matters (default + suggested rules only).
        why.value
          ? h('p', { class: 'rule-card-why' }, why.value)
          : null,

        // Technical footer: type icon + the literal value, plus resolved IPs
        // when relevant. Muted so it doesn't compete with the title.
        h('div', { class: 'rule-card-foot' }, [
          h(TypeIcon.value, { class: 'rule-card-type-icon' }),
          h('code', { class: 'rule-card-value' }, p.rule.value),
          resolved.value.length && p.rule.rule_type !== 'cidr'
            ? h('span', { class: 'rule-card-resolved' },
                `→ ${resolved.value.slice(0, 2).join(', ')}${resolved.value.length > 2 ? ` +${resolved.value.length - 2}` : ''}`)
            : null,
        ]),

        // Delete (custom only)
        !p.readonlyEdit
          ? h('button', {
              class: 'rule-card-delete',
              title: 'Remove this block',
              onClick: () => emit('delete'),
            }, [h(Trash2, { class: 'w-3.5 h-3.5' })])
          : null,
      ])
  },
})
</script>

<style>
/* Not scoped: RuleCard is rendered via defineComponent inside this SFC,
   and Vue's data-v- attribute doesn't reach those nodes. All class
   names are firewall-prefixed (.rule-*, .resolved-chip, .empty-card)
   so collision risk is nil. */
/* Filter tabs above the rule grid. Pill-row pattern; the active tab gets
   a white border so it reads as "selected" without screaming. */
.rule-filterbar {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem;
  margin-bottom: 0.85rem;
}
.rule-filter {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  padding: 0.3rem 0.7rem;
  border-radius: 999px;
  border: 1px solid var(--color-border);
  background: var(--color-surface);
  color: var(--color-foreground-muted);
  font-size: 12px;
  cursor: pointer;
  transition: color 150ms ease, border-color 150ms ease, background-color 150ms ease;
}
.rule-filter:hover {
  color: white;
  border-color: var(--color-foreground-muted);
}
.rule-filter.active {
  color: white;
  border-color: white;
  background: rgba(255, 255, 255, 0.04);
}
.rule-filter-count {
  font-family: var(--font-mono);
  font-size: 10.5px;
  padding: 0.05rem 0.4rem;
  border-radius: 999px;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  color: var(--color-foreground-muted);
}

/* Compiled-policy facts under the status strip. Chips rather than a table:
   the operator scans them, and a table would out-weigh the rule grid. */
.policy-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem;
}
.policy-chip {
  display: inline-flex;
  align-items: baseline;
  gap: 0.4rem;
  max-width: 100%;
  padding: 0.2rem 0.55rem;
  border-radius: 6px;
  border: 1px solid var(--color-border);
  background: var(--color-surface);
  font-size: 11px;
}
.policy-chip-k {
  font-size: 9.5px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--color-foreground-muted);
  white-space: nowrap;
}
.policy-chip-v {
  font-family: var(--font-mono);
  color: white;
  overflow-wrap: anywhere;
}

/* Stored-but-not-enforced callout above the rule grid. Danger tone, not
   warning: a rule the operator believes is blocking and isn't is a
   correctness problem, not a caution. */
.unenforced-note {
  display: flex;
  gap: 0.55rem;
  margin-bottom: 0.85rem;
  padding: 0.6rem 0.75rem;
  border-radius: 8px;
  border: 1px solid var(--color-danger-ring);
  background: var(--color-danger-tint);
  color: var(--color-danger-fg);
  font-size: 11.5px;
  line-height: 1.55;
}
.unenforced-note ul {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  margin: 0.35rem 0 0;
  padding: 0;
  list-style: none;
}
.unenforced-note code {
  font-family: var(--font-mono);
  color: white;
}

.rule-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 0.75rem;
}

.rule-card {
  position: relative;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 10px;
  padding: 0.95rem 1rem 0.85rem;
  display: flex;
  flex-direction: column;
  gap: 0.55rem;
  transition: border-color 150ms ease, background-color 150ms ease;
}
.rule-card.is-on {
  border-color: rgba(34, 197, 94, 0.35);
  background: linear-gradient(180deg, rgba(34, 197, 94, 0.04) 0%, var(--color-surface) 60%);
}
.rule-card.is-off {
  opacity: 0.78;
}
.rule-card.is-off:hover {
  opacity: 1;
  border-color: var(--color-foreground-muted);
}
/* Never green: the rule is stored but excluded from the compiled policy. */
.rule-card.is-unenforced {
  border-style: dashed;
  border-color: var(--color-danger-ring);
}
/* Enabled, but no policy is in force yet — it will apply once one compiles. */
.rule-card.is-pending {
  border-style: dashed;
  border-color: var(--color-warning-ring);
}

.rule-card-row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 0.75rem;
}
.rule-card-titlewrap {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  flex: 1;
  min-width: 0;
}
.rule-card-title {
  font-size: 13.5px;
  font-weight: 600;
  color: white;
  line-height: 1.3;
}

/* Kind pills — same shape, different tone per origin. */
.rule-kind-pill {
  display: inline-flex;
  align-self: flex-start;
  padding: 0.1rem 0.5rem;
  border-radius: 999px;
  font-size: 9.5px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  border: 1px solid;
}
/* Recommended / optional / yours kind chips. Routed through the
   semantic status tokens so a future palette change is a four-token
   edit, not a six-site rewrite. recommended → success (green), optional
   → info (blue), yours → warning (amber). */
.kind-recommended { color: var(--color-success-fg); border-color: var(--color-success-ring); background: var(--color-success-tint); }
.kind-optional    { color: var(--color-info-fg);    border-color: var(--color-info-ring);    background: var(--color-info-tint); }
.kind-yours       { color: var(--color-warning-fg); border-color: var(--color-warning-ring); background: var(--color-warning-tint); }
/* "Not enforced" / "Not in force" — danger tone so it out-reads the kind pill
   sitting beside it. */
.kind-inert       { color: var(--color-danger-fg);  border-color: var(--color-danger-ring);  background: var(--color-danger-tint); }

/* Kind pill + state pill share a wrapping row so a narrow card stacks them
   instead of overflowing. */
.rule-card-pills {
  display: flex;
  flex-wrap: wrap;
  gap: 0.3rem;
}

/* Pill toggle — distinct on/off colour states so the meaning is obvious. */
.rule-toggle {
  flex-shrink: 0;
  width: 36px;
  height: 20px;
  border-radius: 999px;
  border: 1px solid var(--color-border);
  background: var(--color-background);
  position: relative;
  display: inline-flex;
  align-items: center;
  cursor: pointer;
  transition: background-color 150ms ease, border-color 150ms ease;
}
.rule-toggle.on {
  background: rgba(34, 197, 94, 0.4);
  border-color: rgba(34, 197, 94, 0.55);
}
/* Stored position of a rule that is not actually in force. Same knob travel as
   .on so the operator still sees which way the switch is set, without the
   green that would claim the block is live. */
.rule-toggle.inert {
  background: var(--color-background);
  border-color: var(--color-danger-ring);
}
.rule-toggle.busy {
  opacity: 0.5;
  cursor: not-allowed;
}
.rule-toggle:disabled {
  cursor: not-allowed;
}
.rule-toggle-knob {
  display: block;
  width: 14px;
  height: 14px;
  border-radius: 999px;
  background: var(--color-foreground-muted);
  margin-left: 2px;
  transition: transform 150ms ease, background-color 150ms ease;
}
.rule-toggle.on .rule-toggle-knob {
  background: white;
  transform: translateX(16px);
}
.rule-toggle.inert .rule-toggle-knob {
  background: var(--color-danger-fg);
}
.rule-toggle.inert.is-set .rule-toggle-knob {
  transform: translateX(16px);
}

.rule-card-why {
  font-size: 12px;
  color: var(--color-foreground-muted);
  line-height: 1.55;
  margin: 0;
}
.rule-card-warn {
  font-size: 11.5px;
  color: var(--color-danger-fg);
  line-height: 1.55;
  margin: 0;
}

.rule-card-foot {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding-top: 0.5rem;
  border-top: 1px dashed var(--color-border);
  flex-wrap: wrap;
}
.rule-card-type-icon {
  width: 12px;
  height: 12px;
  color: var(--color-foreground-muted);
  flex-shrink: 0;
}
.rule-card-value {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--color-foreground-muted);
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 0.1rem 0.4rem;
  word-break: break-all;
}
.rule-card-resolved {
  font-family: var(--font-mono);
  font-size: 10.5px;
  color: var(--color-foreground-muted);
  opacity: 0.7;
}

.rule-card-delete {
  position: absolute;
  top: 0.55rem;
  right: 3.6rem;
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 5px;
  border: 1px solid transparent;
  background: transparent;
  color: var(--color-foreground-muted);
  cursor: pointer;
  opacity: 0;
  transition: color 150ms ease, background-color 150ms ease, opacity 150ms ease;
}
.rule-card:hover .rule-card-delete {
  opacity: 1;
}
.rule-card-delete:hover {
  color: var(--color-danger-fg);
  background: var(--color-background);
}

/* Empty-state card for "no custom rules". */
.empty-card {
  border: 1px dashed var(--color-border);
  border-radius: 10px;
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  background: var(--color-surface);
}

/* DNS resolver section */
.dns-card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 10px;
  padding: 0.85rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.dns-current {
  min-height: 28px;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.4rem;
}
.dns-chips, .dns-defaults {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.4rem;
}
.dns-chip {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.55rem;
  border-radius: 999px;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  font-size: 11.5px;
  color: white;
}
.dns-chip.muted {
  color: var(--color-foreground-muted);
  border-color: var(--color-border);
  background: rgba(255, 255, 255, 0.02);
}
.dns-chip-x {
  margin-left: 0.15rem;
  width: 16px;
  height: 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  border: 0;
  background: transparent;
  color: var(--color-foreground-muted);
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
  transition: color 150ms ease, background-color 150ms ease;
}
.dns-chip-x:hover {
  color: var(--color-danger-fg);
  background: var(--color-danger-tint);
}

.dns-form {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}
/* DNS inputs are sized to the values they accept rather than stretching
   to fill the row. IPv4 maxes at 15 chars, IPv6 at 39 (rare); hostnames
   typical 12–24. Using ch + a flex: 0 0 auto basis keeps the width
   honest at any container size, and the row wraps when the viewport
   shrinks. */
.dns-input {
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 0.35rem 0.65rem;
  font-size: 12px;
  font-family: var(--font-mono);
  color: var(--color-foreground);
  flex: 0 0 auto;
  width: 18ch;        /* default: IP-sized */
}
.dns-input.host {
  width: 26ch;        /* hostname slot — slightly wider */
}
.dns-input.narrow {
  width: 14ch;        /* search domain etc. */
}
.dns-input::placeholder {
  color: var(--color-foreground-muted);
  opacity: 0.7;
}
.dns-input:focus {
  outline: none;
  border-color: white;
}
.dns-hint {
  font-size: 11.5px;
  color: var(--color-foreground-muted);
  line-height: 1.5;
  margin: 0;
}

.dns-row {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 0.5rem 0;
}
.dns-row + .dns-row {
  border-top: 1px solid var(--color-border);
  padding-top: 0.85rem;
}
.dns-row-label {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--color-foreground-muted);
  font-weight: 600;
  display: flex;
  align-items: baseline;
  gap: 0.5rem;
}
.dns-row-meta {
  font-size: 10.5px;
  text-transform: none;
  letter-spacing: normal;
  font-weight: 400;
  opacity: 0.75;
}

.dns-records {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 0.4rem;
}
.dns-record {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.35rem 0.55rem;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  background: var(--color-background);
}

.dns-savebar {
  display: flex;
  align-items: center;
  gap: 0.65rem;
  padding-top: 0.65rem;
  border-top: 1px solid var(--color-border);
  flex-wrap: wrap;
}
.dns-savebar .dns-hint {
  flex: 1;
  min-width: 240px;
}
</style>
