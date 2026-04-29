<template>
  <div class="space-y-8">
    <!-- Header -->
    <header class="space-y-3">
      <div class="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h1 class="text-xl font-semibold text-white tracking-tight">
            Firewall &amp; DNS
          </h1>
          <p class="text-sm text-foreground-muted mt-1">
            Outbound network policy for sandboxed functions. Toggle a rule
            off and the next invocation can reach the target; toggle back on,
            warm workers respawn within seconds.
          </p>
        </div>
        <div class="flex items-center gap-2">
          <Button
            variant="secondary"
            size="sm"
            :loading="resolving"
            @click="forceResolve"
          >
            <RefreshCw class="w-4 h-4" /> Force resolve
          </Button>
          <Button
            size="sm"
            @click="showCreate = true"
          >
            <Plus class="w-4 h-4" /> Add rule
          </Button>
        </div>
      </div>

      <!-- Status strip — single line, calm, mirrors the Docs/Logs aesthetic. -->
      <div
        class="flex items-center gap-3 text-xs px-3 py-2 rounded-md border"
        :class="statusBannerClass"
      >
        <component
          :is="statusIcon"
          class="w-4 h-4 shrink-0"
        />
        <span class="flex-1 min-w-0">{{ statusBannerText }}</span>
        <span
          v-if="status.nftables_available"
          class="text-foreground-muted hidden sm:inline shrink-0"
        >
          {{ status.ipv4?.length || 0 }} IPv4 + {{ status.ipv6?.length || 0 }} IPv6 entries enforced
        </span>
      </div>
    </header>

    <!-- DNS — resolvers + host overrides for sandboxed functions. -->
    <Section
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
              placeholder="Add resolver IP (1.1.1.1)…"
              class="dns-input"
              @keydown.enter="addServer"
            >
            <Button
              variant="secondary"
              size="sm"
              :disabled="!dnsAddInput.trim()"
              @click="addServer"
            >
              <Plus class="w-3.5 h-3.5" /> Add
            </Button>
            <input
              v-model="dns.search"
              placeholder="search domain (optional)"
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
              placeholder="hostname (api.internal)"
              class="dns-input"
              @keydown.enter="addRecord"
            >
            <span class="text-foreground-muted text-xs">→</span>
            <input
              v-model="recordIPInput"
              placeholder="IP (10.0.5.10)"
              class="dns-input narrow"
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
            Records win over upstream DNS — anything in the override list bypasses
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
    </Section>

    <!-- Default rules -->
    <Section
      title="Default rules"
      :subtitle="'Shipped on. Toggle off if you really need to. ' + countOf(defaultRules) + ' rule' + (countOf(defaultRules) === 1 ? '' : 's') + '.'"
    >
      <div class="rule-grid">
        <RuleCard
          v-for="rule in defaultRules"
          :key="rule.id"
          :rule="rule"
          :status="status"
          :busy="busyId === rule.id"
          :readonly-edit="true"
          @toggle="toggle(rule)"
        />
      </div>
    </Section>

    <!-- Suggested rules -->
    <Section
      title="Suggested rules"
      :subtitle="'Off by default. One click to add common protections. ' + countOf(suggestedRules) + ' available.'"
    >
      <div class="rule-grid">
        <RuleCard
          v-for="rule in suggestedRules"
          :key="rule.id"
          :rule="rule"
          :status="status"
          :busy="busyId === rule.id"
          :readonly-edit="true"
          @toggle="toggle(rule)"
        />
      </div>
    </Section>

    <!-- Custom rules -->
    <Section
      :title="'Custom rules'"
      :subtitle="customRules.length ? `${customRules.length} ${customRules.length === 1 ? 'rule' : 'rules'} you've added.` : 'Block specific IPs, CIDRs, or hostnames.'"
    >
      <div
        v-if="!customRules.length"
        class="empty-card"
      >
        <ShieldOff class="w-5 h-5 mb-2 text-foreground-muted/60" />
        <p class="text-sm text-white">
          No custom rules yet
        </p>
        <p class="text-xs text-foreground-muted mt-1 max-w-sm">
          Click <span class="text-white">Add rule</span> to block a specific IP, CIDR,
          hostname, or wildcard. Useful for blocking your own infrastructure.
        </p>
        <Button
          class="mt-4"
          size="sm"
          variant="secondary"
          @click="showCreate = true"
        >
          <Plus class="w-3.5 h-3.5" /> Add your first rule
        </Button>
      </div>
      <div
        v-else
        class="rule-grid"
      >
        <RuleCard
          v-for="rule in customRules"
          :key="rule.id"
          :rule="rule"
          :status="status"
          :busy="busyId === rule.id"
          :readonly-edit="false"
          @toggle="toggle(rule)"
          @delete="deleteRule(rule)"
        />
      </div>
    </Section>

    <!-- Add-rule modal -->
    <Modal
      v-model="showCreate"
      title="Add firewall rule"
      :icon="ShieldAlert"
      size="md"
    >
      <div class="space-y-4">
        <div>
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-2">
            Type
          </label>
          <div class="grid grid-cols-3 gap-2">
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
        </div>
        <Input
          v-model="newRule.value"
          label="Value"
          :placeholder="placeholderForType"
        />
        <Input
          v-model="newRule.label"
          label="Label (optional)"
          placeholder="e.g. internal monitoring"
        />
        <p class="text-[11px] text-foreground-muted leading-snug">
          Custom rules go into effect within seconds — the firewall re-resolves
          and re-applies nftables on save.
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
          :disabled="!newRule.value.trim()"
          @click="submitCreate"
        >
          <Plus class="w-4 h-4" /> Add rule
        </Button>
      </template>
    </Modal>
  </div>
</template>

<script setup>
import { computed, h, onMounted, onActivated, ref, defineComponent } from 'vue'
import {
  Plus, RefreshCw, ShieldAlert, ShieldOff, ShieldCheck,
  AlertTriangle, Network, Globe, Globe2, Asterisk, Hash, Trash2,
} from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import Modal from '@/components/common/Modal.vue'
import apiClient from '@/api/client'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

const rules = ref([])
const status = ref({ ipv4: [], ipv6: [], hostname_map: {}, nftables_available: true, last_error: '' })
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

const defaultRules   = computed(() => rules.value.filter((r) => r.kind === 'default'))
const suggestedRules = computed(() => rules.value.filter((r) => r.kind === 'suggested'))
const customRules    = computed(() => rules.value.filter((r) => r.kind === 'custom'))
const countOf = (list) => list.length

const typeOptions = [
  { value: 'cidr',     label: 'CIDR',     icon: Hash },
  { value: 'hostname', label: 'Host',     icon: Globe },
  { value: 'wildcard', label: 'Wildcard', icon: Asterisk },
]

const placeholderForType = computed(() => {
  switch (newRule.value.rule_type) {
    case 'hostname': return 'internal.corp.com'
    case 'wildcard': return '*.corp.com'
    default:         return '192.168.1.0/24'
  }
})

// Status banner branching — three states.
const statusBannerClass = computed(() => {
  if (status.value.last_error) return 'border-red-500/40 bg-red-500/10 text-red-200'
  if (!status.value.nftables_available) return 'border-amber-500/40 bg-amber-500/10 text-amber-200'
  return 'border-success/30 bg-success/5 text-foreground-muted'
})
const statusIcon = computed(() => {
  if (status.value.last_error) return AlertTriangle
  if (!status.value.nftables_available) return AlertTriangle
  return ShieldCheck
})
const statusBannerText = computed(() => {
  if (status.value.last_error) return status.value.last_error
  if (!status.value.nftables_available) {
    return 'nftables unavailable on this host — packet-level enforcement is disabled. Sandbox-level isolation still works.'
  }
  return 'Active. Rules apply to every function with outbound network enabled.'
})

const load = async () => {
  const res = await apiClient.get('/firewall/rules')
  rules.value = res.data.rules || []
  status.value = res.data.status || { ipv4: [], ipv6: [] }
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

// ── Section component (matches Docs page aesthetic) ──────────────────
const Section = defineComponent({
  name: 'Section',
  props: { title: String, subtitle: String },
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

// ── RuleCard — replaces the table row ────────────────────────────────
// One card per rule: type icon top-left, value prominent in mono mid,
// label below, resolved IPs as muted chips, toggle on the right.
const RuleCard = defineComponent({
  name: 'RuleCard',
  props: {
    rule:         { type: Object,  required: true },
    status:       { type: Object,  required: true },
    busy:         { type: Boolean, default: false },
    readonlyEdit: { type: Boolean, default: false },
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
    return () =>
      h('div', {
        class: ['rule-card', p.rule.enabled ? 'is-on' : 'is-off'],
      }, [
        h('div', { class: 'rule-card-head' }, [
          h('div', { class: 'rule-card-icon' }, [
            h(TypeIcon.value, { class: 'w-3.5 h-3.5' }),
          ]),
          h('button', {
            class: ['rule-toggle', p.rule.enabled ? 'on' : 'off', p.busy ? 'busy' : ''],
            disabled: p.busy,
            title: p.rule.enabled ? 'Disable' : 'Enable',
            onClick: () => emit('toggle'),
          }, [
            h('span', { class: 'rule-toggle-knob' }),
          ]),
        ]),
        h('div', { class: 'rule-card-value' }, p.rule.value),
        p.rule.label
          ? h('div', { class: 'rule-card-label' }, p.rule.label)
          : h('div', { class: 'rule-card-label muted' }, p.rule.rule_type),
        resolved.value.length
          ? h('div', { class: 'rule-card-resolved' },
              resolved.value.slice(0, 3).map((ip) =>
                h('span', { class: 'resolved-chip' }, ip)
              ).concat(
                resolved.value.length > 3
                  ? [h('span', { class: 'resolved-chip muted' }, `+${resolved.value.length - 3}`)]
                  : []
              )
            )
          : (p.rule.rule_type !== 'cidr' && p.rule.enabled)
            ? h('div', { class: 'rule-card-resolved' }, h('span', { class: 'resolved-chip muted' }, 'resolving…'))
            : null,
        !p.readonlyEdit
          ? h('div', { class: 'rule-card-actions' }, [
              h('button', {
                class: 'rule-action',
                title: 'Delete rule',
                onClick: () => emit('delete'),
              }, [h(Trash2, { class: 'w-3.5 h-3.5' })]),
            ])
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
.rule-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 0.75rem;
}

.rule-card {
  position: relative;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 10px;
  padding: 0.85rem 0.95rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  transition: border-color 150ms ease, opacity 150ms ease, transform 150ms ease;
}
.rule-card.is-off {
  opacity: 0.55;
}
.rule-card.is-off:hover {
  opacity: 0.85;
}
.rule-card:hover {
  border-color: var(--color-foreground-muted);
}

.rule-card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.rule-card-icon {
  width: 26px;
  height: 26px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  color: var(--color-foreground-muted);
}
.rule-card.is-on .rule-card-icon {
  color: var(--color-success);
  border-color: rgba(34, 197, 94, 0.3);
  background: rgba(34, 197, 94, 0.08);
}

/* Pill toggle. Uses currentColor on the bg so on/off states feel
   distinct without a hard color swap. */
.rule-toggle {
  width: 32px;
  height: 18px;
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
  background: rgba(34, 197, 94, 0.3);
  border-color: rgba(34, 197, 94, 0.5);
}
.rule-toggle.busy {
  opacity: 0.5;
  cursor: not-allowed;
}
.rule-toggle-knob {
  display: block;
  width: 12px;
  height: 12px;
  border-radius: 999px;
  background: var(--color-foreground-muted);
  margin-left: 2px;
  transition: transform 150ms ease, background-color 150ms ease;
}
.rule-toggle.on .rule-toggle-knob {
  background: var(--color-success);
  transform: translateX(14px);
}

.rule-card-value {
  font-family: var(--font-mono);
  font-size: 13px;
  color: white;
  word-break: break-all;
  line-height: 1.35;
}
.rule-card-label {
  font-size: 11px;
  color: var(--color-foreground);
  line-height: 1.35;
}
.rule-card-label.muted {
  color: var(--color-foreground-muted);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  font-size: 10px;
}

.rule-card-resolved {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem;
  margin-top: 0.1rem;
}
.resolved-chip {
  font-family: var(--font-mono);
  font-size: 10.5px;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 0.1rem 0.4rem;
  color: var(--color-foreground-muted);
  word-break: break-all;
}
.resolved-chip.muted {
  opacity: 0.6;
}

.rule-card-actions {
  position: absolute;
  top: 0.6rem;
  right: 3rem;
  display: flex;
  gap: 0.25rem;
  opacity: 0;
  transition: opacity 150ms ease;
}
.rule-card:hover .rule-card-actions {
  opacity: 1;
}
.rule-action {
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
  transition: color 150ms ease, background-color 150ms ease;
}
.rule-action:hover {
  color: #f87171;
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
  color: #f87171;
  background: rgba(248, 113, 113, 0.12);
}

.dns-form {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.dns-input {
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 0.35rem 0.65rem;
  font-size: 12px;
  font-family: var(--font-mono);
  color: var(--color-foreground);
  min-width: 220px;
  flex: 1;
}
.dns-input.narrow {
  flex: 0 0 auto;
  min-width: 160px;
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
