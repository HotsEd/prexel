<template>
    <!--
        ContainerDetailView — dedicated page for ONE container.

        Layout follows the system's detail-page convention used by
        DomainDetailView / GitSourceDetailView:
          - back-link
          - .detail-header (kicker + big title + sub)
          - .status-row with the state pill + meta
          - SCard sections for every framed block
          - PrimeVue Tabs for the runtime panes (Logs / Terminal /
            Stats) so each only mounts when active and stops its
            subscriptions/timers on tab change

        Preview rows (Compose YAML services with no live container yet)
        hide the runtime tabs — those endpoints would 404 against
        Docker. Operator still gets the declared image / ports / env
        from the YAML.
    -->
    <div class="container-detail">
        <!--
            Back link to the app's Containers tab. We use the object
            form (name + params + query) instead of a stringified
            path so Vue Router resolves it via the named route
            registry — that side-steps an edge case where a query-
            string embedded in the `to` string gets eaten by the
            history backend and the tab param never arrives, leaving
            the operator on the Overview tab.
        -->
        <RouterLink
            :to="{ name: 'apps.detail', params: { id: appId }, query: { tab: 'containers' } }"
            class="back-link"
        >
            <IconChevronLeft :size="14" />
            <span>{{ t('containers.backToList') }}</span>
        </RouterLink>

        <!-- Loading / 404. Mirrors the "single full-page state"
             pattern used by DomainDetailView so the header doesn't
             render half-empty mid-load. -->
        <div v-if="loading && !detail" class="muted-state">{{ t('containers.loading') }}</div>
        <div v-else-if="!detail && loadError" class="muted-state">{{ loadError }}</div>

        <template v-else-if="detail">
            <!-- ─────────── Header ─────────── -->
            <header class="detail-header">
                <div class="detail-header__kicker">{{ t('containers.kicker') }}</div>
                <h1 class="detail-header__title">{{ detail.service || detail.container_name }}</h1>
                <p class="detail-header__sub mono">{{ detail.container_name }}</p>
            </header>

            <div class="status-row">
                <!-- StatusBadge keeps the pill style identical to the
                     rest of the system (apps, deploys, servers). The
                     `preview` flag from the backend already arrives as
                     `state: 'preview'`, so the same component handles
                     it without a separate badge. -->
                <StatusBadge v-if="detail.state" :status="detail.state" />
                <span v-if="detail.status" class="status-row__hint">{{ detail.status }}</span>
                <span v-if="detail.exit_code !== 0 && detail.state === 'exited'" class="status-row__exit">
                    {{ t('containers.exitCode', { code: detail.exit_code }) }}
                </span>
            </div>

            <!-- ─────────── Visão geral ─────────── -->
            <SCard :title="t('containers.overview.title')" :sub="t('containers.overview.sub')">
                <div class="grid-meta">
                    <div>
                        <div class="key">{{ t('containers.fields.image') }}</div>
                        <div class="val mono" :title="detail.image">{{ detail.image || '—' }}</div>
                    </div>
                    <div>
                        <div class="key">{{ t('containers.fields.ports') }}</div>
                        <div class="val">
                            <template v-if="detail.ports.length">
                                <code v-for="p in detail.ports" :key="p" class="port">{{ p }}</code>
                            </template>
                            <span v-else class="muted">—</span>
                        </div>
                    </div>
                    <div>
                        <div class="key">{{ t('containers.fields.ip') }}</div>
                        <div class="val mono">{{ detail.ip_address || '—' }}</div>
                    </div>
                    <div>
                        <div class="key">{{ t('containers.fields.network') }}</div>
                        <div class="val mono">{{ detail.networks.length ? detail.networks.join(', ') : '—' }}</div>
                    </div>
                    <div>
                        <div class="key">{{ t('containers.fields.restartPolicy') }}</div>
                        <div class="val">{{ detail.restart_policy || '—' }}</div>
                    </div>
                    <div>
                        <div class="key">{{ t('containers.fields.created') }}</div>
                        <div class="val">{{ detail.created_at ? formatRelative(detail.created_at) : '—' }}</div>
                    </div>
                    <div>
                        <div class="key">{{ t('containers.fields.started') }}</div>
                        <div class="val">{{ detail.started_at ? formatRelative(detail.started_at) : '—' }}</div>
                    </div>
                    <div v-if="detail.finished_at">
                        <div class="key">{{ t('containers.fields.finished') }}</div>
                        <div class="val">{{ formatRelative(detail.finished_at) }}</div>
                    </div>
                    <!--
                        Inline CPU + Memória — when the container is
                        running, surface the latest sample alongside
                        the rest of the metadata so the operator sees
                        live load without having to switch tabs. The
                        Stats tab is still where the polling lives
                        (per-tab lazy attach) — these two cells just
                        mirror its most recent value.
                    -->
                    <div v-if="canRuntime">
                        <div class="key">{{ t('containers.fields.cpu') }}</div>
                        <div class="val">
                            <template v-if="stats">{{ stats.cpu_percent.toFixed(1) }}%</template>
                            <span v-else class="muted">…</span>
                        </div>
                    </div>
                    <div v-if="canRuntime">
                        <div class="key">{{ t('containers.fields.memory') }}</div>
                        <div class="val">
                            <template v-if="stats">
                                {{ formatBytes(stats.memory_used_bytes) }}<span
                                    v-if="stats.memory_limit_bytes > 0" class="muted"
                                > / {{ formatBytes(stats.memory_limit_bytes) }}</span>
                            </template>
                            <span v-else class="muted">…</span>
                        </div>
                    </div>
                </div>
            </SCard>

            <!--
                Tabs — hand-rolled with plain buttons + v-show panels.
                We tried PrimeVue's `<Tabs>` / `<TabList>` / `<Tab>` and
                it threw silently in this exact composition (wrapped in
                a card with conditionally-rendered Tab children),
                leaving the entire subtree invisible. A flat button
                row + per-panel `v-show` works everywhere and lets us
                style the strip to match the rest of the system.

                Order: operational stuff first (Logs / Terminal /
                Stats / Domínios), then static config. Default tab is
                `logs` when the container is runnable.
            -->
            <div class="container-detail__tabs pv-panel">
                <div class="ct-tabs__list" role="tablist">
                    <button
                        v-for="tab in visibleTabs"
                        :key="tab.value"
                        type="button"
                        role="tab"
                        :class="['ct-tabs__btn', activeTab === tab.value && 'is-active']"
                        :aria-selected="activeTab === tab.value"
                        @click="setTab(tab.value)"
                    >
                        {{ tab.label }}
                    </button>
                </div>

                <div class="ct-tabs__panels">
                        <!-- Static blocks: env / mounts / cmd / labels live as separate
                             tabs (matches Coolify's right-side panes) so an empty env
                             list doesn't push the more interesting stuff off-screen. -->
                        <div v-show="activeTab === 'environment'" class="ct-tab-panel">
                            <div v-if="envEntries.length === 0" class="muted block-empty">
                                {{ t('containers.empty.env') }}
                            </div>
                            <table v-else class="kv-table">
                                <tbody>
                                    <tr v-for="[k, v] in envEntries" :key="k">
                                        <td class="kv-table__k mono">{{ k }}</td>
                                        <td class="kv-table__v mono">{{ v }}</td>
                                    </tr>
                                </tbody>
                            </table>
                        </div>

                        <div v-show="activeTab === 'mounts'" class="ct-tab-panel">
                            <!--
                                All `detail.*` accesses below use optional
                                chaining because `v-show` keeps the panel
                                content in the DOM (and Vue evaluates the
                                bindings) even when the tab is hidden. The
                                outer `v-else-if="detail"` block prevents
                                the panels from being created in the first
                                place when detail is null, but during HMR
                                reloads and route changes the reactive
                                graph can briefly see a null detail — the
                                `?.` keeps the render loop alive.
                            -->
                            <div v-if="(detail?.mounts?.length ?? 0) === 0" class="muted block-empty">
                                {{ t('containers.empty.mounts') }}
                            </div>
                            <table v-else class="kv-table">
                                <thead>
                                    <tr>
                                        <th>{{ t('containers.mounts.type') }}</th>
                                        <th>{{ t('containers.mounts.source') }}</th>
                                        <th>{{ t('containers.mounts.destination') }}</th>
                                        <th>{{ t('containers.mounts.ro') }}</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    <tr v-for="m in (detail?.mounts ?? [])" :key="`${m.type}-${m.destination}`">
                                        <td>
                                            <span :class="['pill', m.type === 'bind' ? 'pill--bind' : 'pill--volume']">
                                                {{ m.type }}
                                            </span>
                                        </td>
                                        <td class="mono">{{ m.source }}</td>
                                        <td class="mono">{{ m.destination }}</td>
                                        <td>
                                            <i v-if="m.read_only" class="pi pi-lock" :title="t('containers.mounts.readOnly')" />
                                            <span v-else class="muted">—</span>
                                        </td>
                                    </tr>
                                </tbody>
                            </table>
                        </div>

                        <div v-show="activeTab === 'command'" class="ct-tab-panel">
                            <pre v-if="detail?.entrypoint?.length" class="block-pre">ENTRYPOINT: {{ (detail.entrypoint ?? []).join(' ') }}</pre>
                            <pre v-if="detail?.cmd?.length" class="block-pre">CMD: {{ (detail.cmd ?? []).join(' ') }}</pre>
                            <div
                                v-if="!detail?.entrypoint?.length && !detail?.cmd?.length"
                                class="muted block-empty"
                            >
                                {{ t('containers.empty.command') }}
                            </div>
                        </div>

                        <div v-show="activeTab === 'labels'" class="ct-tab-panel">
                            <div v-if="labelEntries.length === 0" class="muted block-empty">
                                {{ t('containers.empty.labels') }}
                            </div>
                            <table v-else class="kv-table">
                                <tbody>
                                    <tr v-for="[k, v] in labelEntries" :key="k">
                                        <td class="kv-table__k mono">{{ k }}</td>
                                        <td class="kv-table__v mono">{{ v }}</td>
                                    </tr>
                                </tbody>
                            </table>
                        </div>

                        <!-- Domínios — bind/unbind domains for this
                             container's service. Only available when
                             the row has a service name (orphan
                             containers don't have a routing target).
                             Same backend call as the per-service
                             dialog in the ContainersTable.  -->
                        <div v-show="activeTab === 'domains'" v-if="canBindDomain" class="ct-tab-panel">
                            <div class="domains-pane">
                                <i18n-t
                                    v-if="boundDomains.length === 0"
                                    keypath="containers.domains.emptyFmt"
                                    tag="p"
                                    class="muted block-empty"
                                >
                                    <template #service><strong class="mono">{{ detail.service }}</strong></template>
                                </i18n-t>
                                <table v-else class="kv-table domains-table">
                                    <thead>
                                        <tr>
                                            <th>{{ t('containers.domains.domain') }}</th>
                                            <th>{{ t('containers.domains.port') }}</th>
                                            <th>{{ t('containers.domains.ssl') }}</th>
                                            <th></th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        <tr v-for="d in boundDomains" :key="d.id">
                                            <td>
                                                <a :href="`https://${d.name}`" target="_blank" rel="noopener" class="domain-link mono">
                                                    {{ d.name }}
                                                    <i class="pi pi-external-link" aria-hidden="true" />
                                                </a>
                                            </td>
                                            <td class="mono">{{ d.port ?? '—' }}</td>
                                            <td>
                                                <span :class="['pill', `pill--ssl-${d.ssl_status}`]">
                                                    {{ d.ssl_status }}
                                                </span>
                                            </td>
                                            <td class="domains-table__act">
                                                <Button
                                                    text
                                                    size="small"
                                                    icon="pi pi-times"
                                                    severity="danger"
                                                    :aria-label="t('containers.domains.unbindAria', { name: d.name })"
                                                    :title="t('containers.domains.unbindTitle')"
                                                    @click="unbindDomain(d)"
                                                />
                                            </td>
                                        </tr>
                                    </tbody>
                                </table>

                                <div class="domains-pane__cta">
                                    <Button
                                        size="small"
                                        severity="secondary"
                                        outlined
                                        icon="pi pi-link"
                                        :label="t('containers.domains.bind')"
                                        @click="openBindDialog"
                                    />
                                </div>
                            </div>
                        </div>

                        <!-- Runtime panes — lazy: SSE/xterm/poll only attach
                             when the tab is active (see watch on activeTab). -->
                        <div v-show="activeTab === 'logs'" v-if="canRuntime" class="ct-tab-panel">
                            <pre ref="logEl" class="block-terminal">{{ logsOutput || t('logs.waiting') }}</pre>
                        </div>

                        <div v-show="activeTab === 'terminal'" v-if="canRuntime" class="ct-tab-panel">
                            <div class="block-terminal-host">
                                <ContainerTerminal
                                    v-if="activeTab === 'terminal' && containerName && appId"
                                    :app-id="appId"
                                    :container-name="containerName"
                                    class="block-terminal-host__inner"
                                    @closed="onTerminalClosed"
                                />
                            </div>
                        </div>

                        <div v-show="activeTab === 'stats'" v-if="canRuntime" class="ct-tab-panel">
                            <div class="stats-pane">
                                <div class="stat-card">
                                    <div class="stat-card__label">{{ t('containers.fields.cpu') }}</div>
                                    <div class="stat-card__value">
                                        <template v-if="stats">
                                            {{ stats.cpu_percent.toFixed(1) }}<span class="stat-card__unit">%</span>
                                        </template>
                                        <template v-else>—</template>
                                    </div>
                                    <div class="stat-card__bar">
                                        <div class="stat-card__bar-fill" :style="{ width: `${Math.min(100, stats?.cpu_percent ?? 0)}%` }" />
                                    </div>
                                </div>
                                <div class="stat-card">
                                    <div class="stat-card__label">{{ t('containers.fields.memory') }}</div>
                                    <div class="stat-card__value">
                                        <template v-if="stats">
                                            {{ formatBytes(stats.memory_used_bytes) }}
                                            <span v-if="stats.memory_limit_bytes > 0" class="stat-card__unit">
                                                / {{ formatBytes(stats.memory_limit_bytes) }}
                                            </span>
                                        </template>
                                        <template v-else>—</template>
                                    </div>
                                    <div class="stat-card__bar">
                                        <div class="stat-card__bar-fill" :style="{ width: `${memPercent}%` }" />
                                    </div>
                                </div>
                                <p v-if="statsError" class="stats-pane__err">{{ statsError }}</p>
                                <p class="stats-pane__hint muted">
                                    {{ t('containers.stats.updateHint', { when: stats ? formatRelative(stats.sampled_at) : '—' }) }}
                                </p>
                            </div>
                        </div>
                </div>
            </div>
        </template>

        <!-- Bind-domain dialog. Same flow as the dialog in
             ContainersTable: pick a free domain, set the in-container
             port, PATCH /domains/:id with {app_id, service, port} so
             Caddy starts routing the host. -->
        <Dialog
            v-model:visible="bindOpen"
            modal
            :header="bindHeader"
            :style="{ width: '520px' }"
            :closable="!binding"
        >
            <div v-if="detail" class="bind-dialog">
                <i18n-t keypath="containers.bind.introFmt" tag="p" class="muted">
                    <template #service>
                        <strong class="mono">{{ detail.service || detail.container_name }}</strong>
                    </template>
                </i18n-t>
                <SField :label="t('containers.bind.domainLabel')">
                    <DomainPicker
                        v-model="bindSelectedId"
                        :placeholder="bindableDomains.length ? t('containers.bind.domainPlaceholder') : t('containers.bind.domainNoneFree')"
                        :disabled="bindableDomains.length === 0"
                    />
                </SField>
                <SField :label="t('containers.bind.portLabel')" :hint="t('containers.bind.portHint')">
                    <InputText
                        v-model="bindPortStr"
                        type="number"
                        min="1"
                        max="65535"
                        :placeholder="(detail.ports[0] ?? 80).toString()"
                    />
                </SField>
                <p v-if="bindableDomains.length === 0" class="muted">
                    <RouterLink :to="`/domains/new?app=${appId}`" class="link">
                        {{ t('containers.bind.registerNew') }}
                    </RouterLink>
                </p>
            </div>
            <template #footer>
                <Button text severity="secondary" :label="t('common.cancel')" :disabled="binding" @click="bindOpen = false" />
                <Button
                    :label="t('containers.bind.submit')"
                    :loading="binding"
                    :disabled="!bindSelectedId || !bindPortValid"
                    @click="submitBind"
                />
            </template>
        </Dialog>
    </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import IconChevronLeft from '@/components/icons/IconChevronLeft.vue'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import DomainPicker from '@/components/domains/DomainPicker.vue'
import ContainerTerminal from '@/components/apps/ContainerTerminal.vue'
import {
    fetchContainerDetail,
    fetchContainerStats,
    streamContainerLogsURL,
} from '@/services/apps'
import { useSSE } from '@/composables/useSSE'
import { useDomainsStore } from '@/stores/domains'
import { formatRelative } from '@/utils/format'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { AppStats, ContainerDetail, Domain } from '@/types/api'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const appId = computed(() => String(route.params.id ?? ''))
const containerName = computed(() => String(route.params.name ?? ''))

const detail = ref<ContainerDetail | null>(null)
const loading = ref(true)
const loadError = ref<string | null>(null)

const stats = ref<AppStats | null>(null)
const statsError = ref<string | null>(null)
let statsTimer: ReturnType<typeof setInterval> | null = null

const logsOutput = ref('')
const logEl = ref<HTMLPreElement | null>(null)
let stopLogsSSE: (() => void) | null = null

// Tabs ─────────────────────────────────────────────────────────────
// Order matters: the operator landing here typically wants the live
// state (logs, terminal, stats, domains). Static config (env, mounts,
// labels, command) lives at the end. The default tab is `logs` when
// the container is running — that's the first thing you want to see.
const RUNTIME_TABS = ['logs', 'terminal', 'stats'] as const
const DOMAIN_TAB = 'domains' as const
const STATIC_TABS = ['environment', 'mounts', 'command', 'labels'] as const
type TabValue = typeof STATIC_TABS[number] | typeof RUNTIME_TABS[number] | typeof DOMAIN_TAB
const ALL_TABS = [...RUNTIME_TABS, DOMAIN_TAB, ...STATIC_TABS] as readonly string[]

const defaultTab = computed<TabValue>(() => (canRuntime.value ? 'logs' : 'environment'))

const activeTab = computed<TabValue>(() => {
    const raw = String(route.query.tab ?? defaultTab.value)
    return (ALL_TABS.includes(raw) ? raw : defaultTab.value) as TabValue
})
function setTab(v: string | number | undefined) {
    const next = String(v ?? defaultTab.value)
    if (next === activeTab.value) return
    void router.replace({
        query: {
            ...route.query,
            tab: next === defaultTab.value ? undefined : next,
        },
    })
}

// Derived ──────────────────────────────────────────────────────────
const isPreview = computed(() => detail.value?.preview === true)
const canRuntime = computed(() => !!detail.value && !isPreview.value && detail.value.state === 'running')

// Domain binding is only meaningful when the container reports a
// service name — orphan containers (no prexel.service label) have
// no routing target.
const canBindDomain = computed(() => !!detail.value && !isPreview.value && !!detail.value.service)

// Visible tabs in display order. Built reactively so the strip
// shows/hides tabs as canRuntime / canBindDomain flip during the
// container's lifecycle (e.g. a container that crashed loses
// Logs/Terminal/Stats automatically).
interface TabSpec { value: TabValue; label: string }
const visibleTabs = computed<TabSpec[]>(() => {
    const out: TabSpec[] = []
    if (canRuntime.value) {
        out.push({ value: 'logs', label: t('containers.tabs.logs') })
        out.push({ value: 'terminal', label: t('containers.tabs.terminal') })
        out.push({ value: 'stats', label: t('containers.tabs.stats') })
    }
    if (canBindDomain.value) {
        out.push({ value: 'domains', label: t('containers.tabs.domains') })
    }
    out.push({ value: 'environment', label: t('containers.tabs.environment') })
    out.push({ value: 'mounts', label: t('containers.tabs.mounts') })
    out.push({ value: 'command', label: t('containers.tabs.command') })
    out.push({ value: 'labels', label: t('containers.tabs.labels') })
    return out
})

const envEntries = computed(() =>
    Object.entries(detail.value?.env ?? {}).sort(([a], [b]) => a.localeCompare(b)),
)
const labelEntries = computed(() =>
    Object.entries(detail.value?.labels ?? {}).sort(([a], [b]) => a.localeCompare(b)),
)

const memPercent = computed(() => {
    if (!stats.value || stats.value.memory_limit_bytes === 0) return 0
    return Math.min(100, (stats.value.memory_used_bytes / stats.value.memory_limit_bytes) * 100)
})

// Domain binding ───────────────────────────────────────────────────
const domainsStore = useDomainsStore()
const bindOpen = ref(false)
const bindSelectedId = ref<string | null>(null)
const bindPortStr = ref('')
const binding = ref(false)

// All domains routed to (app, service). Re-derived reactively from
// the domains store so flipping bindings refreshes without a manual
// fetch round-trip.
const boundDomains = computed<Domain[]>(() => {
    if (!detail.value?.service || !appId.value) return []
    return domainsStore.domains.filter(
        (d) => d.app_id === appId.value && d.service === detail.value!.service,
    )
})

// Free domains = registered but not yet bound to any app. Same rule
// the ContainersTable uses for its dialog.
const bindableDomains = computed<Domain[]>(() =>
    domainsStore.domains.filter((d) => !d.app_id),
)

const bindPortValid = computed(() => {
    const n = Number(bindPortStr.value)
    return Number.isInteger(n) && n >= 1 && n <= 65535
})

const bindHeader = computed(() =>
    t('containers.bind.header', { service: detail.value?.service ?? '' }),
)

function openBindDialog() {
    if (!detail.value) return
    bindSelectedId.value = null
    bindPortStr.value = (detail.value.ports[0] ?? 80).toString()
    bindOpen.value = true
}

async function submitBind() {
    if (!detail.value?.service || !bindSelectedId.value || !bindPortValid.value) return
    binding.value = true
    try {
        await domainsStore.update(bindSelectedId.value, {
            app_id: appId.value,
            service: detail.value.service,
            port: Number(bindPortStr.value),
        })
        notify.success(t('containers.toast.linked'))
        bindOpen.value = false
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        binding.value = false
    }
}

async function unbindDomain(d: Domain) {
    try {
        await domainsStore.update(d.id, { clear_service: true })
        notify.success(t('containers.toast.unbound'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

// Lifecycle ────────────────────────────────────────────────────────
onMounted(async () => {
    await load()
    // Domains feed the bind dialog AND the Domínios tab table.
    // Best-effort: if it fails the tab still renders (just empty).
    void domainsStore.fetchAll().catch(() => null)
    // Page Visibility — pause polling when the tab is in the
    // background. Saves CPU/network on a long-lived container page
    // an operator left open in another window. We re-evaluate on
    // visibilitychange and let the same gate (canRuntime + active
    // tab + visible) decide whether the timer should run.
    document.addEventListener('visibilitychange', onVisibilityChange)
})

onBeforeUnmount(() => {
    stopStatsPolling()
    stopLogsSSE?.()
    document.removeEventListener('visibilitychange', onVisibilityChange)
})

function onVisibilityChange() {
    syncStatsPolling()
}

// Re-load on route change (operator navigating between containers).
watch(
    () => `${appId.value}:${containerName.value}`,
    async () => {
        stopStatsPolling()
        stopLogsSSE?.()
        stopLogsSSE = null
        logsOutput.value = ''
        stats.value = null
        statsError.value = null
        await load()
    },
)

// Stats polling — lazy + visibility-aware.
//
// Conditions for polling to run:
//   1. The container is runnable (`canRuntime`).
//   2. The operator is looking at the Stats tab.
//   3. The browser tab itself is visible (Page Visibility API).
//
// We do ONE sample on `canRuntime` flipping true so the inline
// CPU/Memory cells in "Visão geral" have something to show before
// the operator clicks into Stats. The recurring interval, however,
// only runs when all three conditions hold — which avoids burning
// CPU on a background tab someone left open.
watch(
    canRuntime,
    (runnable) => {
        if (runnable) {
            void pollStats()
        } else {
            stopStatsPolling()
        }
        syncStatsPolling()
    },
    { immediate: true },
)

watch(() => activeTab.value, () => syncStatsPolling())

function syncStatsPolling() {
    const shouldPoll =
        canRuntime.value &&
        activeTab.value === 'stats' &&
        typeof document !== 'undefined' &&
        document.visibilityState !== 'hidden'
    if (shouldPoll && !statsTimer) {
        void pollStats()
        statsTimer = setInterval(() => void pollStats(), 5000)
    } else if (!shouldPoll) {
        stopStatsPolling()
    }
}

// Logs SSE: lazy — only attach when the Logs tab is open. The
// stream can be megabytes per minute on a chatty container, so
// keeping it off until the operator asks for it is intentional.
watch(
    () => activeTab.value,
    (tab) => {
        if (tab === 'logs' && !stopLogsSSE && containerName.value && appId.value && canRuntime.value) {
            attachLogsSSE()
        }
    },
    { immediate: true },
)

async function load() {
    loading.value = true
    loadError.value = null
    try {
        detail.value = await fetchContainerDetail(appId.value, containerName.value)
    } catch (e) {
        loadError.value = apiErrorMessage(e)
        notify.error(loadError.value)
    } finally {
        loading.value = false
    }
}

// Logs ─────────────────────────────────────────────────────────────
function attachLogsSSE() {
    const sse = useSSE<{ line?: string; stream?: string }>(
        streamContainerLogsURL(appId.value, containerName.value),
        {
            onEvent(ev) {
                const d = ev.data
                if (!d || typeof d !== 'object') return
                const line = String(d.line ?? '')
                if (!line) return
                logsOutput.value += line + '\n'
                requestAnimationFrame(() => {
                    if (logEl.value) {
                        logEl.value.scrollTop = logEl.value.scrollHeight
                    }
                })
            },
        },
    )
    stopLogsSSE = sse.close
}

// Stats ────────────────────────────────────────────────────────────
async function pollStats() {
    if (!canRuntime.value) return
    try {
        stats.value = await fetchContainerStats(appId.value, containerName.value)
        statsError.value = null
    } catch (e) {
        statsError.value = apiErrorMessage(e)
    }
}
function stopStatsPolling() {
    if (statsTimer) {
        clearInterval(statsTimer)
        statsTimer = null
    }
}

function onTerminalClosed() {
    // No-op — the terminal component owns its WS lifecycle.
}

// Formatters ───────────────────────────────────────────────────────
function formatBytes(n: number): string {
    if (!Number.isFinite(n) || n <= 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    let i = 0
    let v = n
    while (v >= 1024 && i < units.length - 1) {
        v /= 1024
        i++
    }
    return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`
}
</script>

<style scoped>
/* ─────────── Page wrapper ─────────── */
.container-detail {
    max-width: 1280px;
    margin: 0 auto;
    padding: 24px 28px 32px;
}

.back-link {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--p-text-muted);
    font-size: 12px;
    text-decoration: none;
    margin-bottom: 16px;
    transition: color 120ms;
}
.back-link:hover { color: var(--p-text); }

.muted-state {
    color: var(--p-text-muted);
    font-size: 13px;
    padding: 24px 0;
}

/* ─────────── Header (matches DomainDetailView convention) ─────────── */
.detail-header { margin-bottom: 18px; }
.detail-header__kicker {
    margin-bottom: 4px;
    color: var(--p-primary-500);
    font-size: 11px;
    font-weight: 750;
    letter-spacing: .06em;
    text-transform: uppercase;
}
.detail-header__title {
    margin: 0;
    color: var(--p-text);
    font-size: 28px;
    font-weight: 750;
    letter-spacing: 0;
    word-break: break-all;
}
.detail-header__sub {
    max-width: 760px;
    margin: 6px 0 0;
    color: var(--p-text-muted);
    font-size: 13px;
    word-break: break-all;
}

/* ─────────── Status row (state pill + meta inline) ─────────── */
.status-row {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
    margin-bottom: 18px;
}
.status-row__hint {
    font-size: 12px;
    color: var(--p-text-muted);
}
.status-row__exit {
    font-size: 11px;
    font-family: ui-monospace, Menlo, monospace;
    color: var(--p-red-400, #f87171);
    background: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 88%);
    padding: 2px 8px;
    border-radius: 999px;
}

/* ─────────── Grid meta (Visão geral) ─────────── */
.grid-meta {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 18px 24px;
}
@media (min-width: 1020px) {
    .grid-meta { grid-template-columns: repeat(4, minmax(0, 1fr)); }
}
.key {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--p-text-muted);
    font-weight: 600;
    margin-bottom: 4px;
}
.val { font-size: 14px; color: var(--p-text); word-break: break-all; }

/* ─────────── Tabs panel — hand-rolled ─────────── */
/*
   Plain button strip + v-show panels. We dropped PrimeVue's `Tabs`
   here because in this exact composition (Tabs inside a wrapper
   panel with v-if'd Tab children) the component failed to render
   silently in production — the whole subtree was just missing.
   Hand-rolling the chrome is more code but completely predictable.
*/
.container-detail__tabs {
    padding: 0;
    margin-bottom: 16px;
    overflow: hidden;
}
.ct-tabs__list {
    display: flex;
    flex-wrap: wrap;
    align-items: stretch;
    border-bottom: 1px solid var(--p-content-border);
    background: color-mix(in srgb, var(--p-text-muted), transparent 96%);
}
.ct-tabs__btn {
    appearance: none;
    background: transparent;
    border: 0;
    border-bottom: 2px solid transparent;
    color: var(--p-text-muted);
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    padding: 12px 18px;
    cursor: pointer;
    transition: color 120ms ease, border-color 120ms ease, background 120ms ease;
    /* Pull the bottom border on top of the strip border below so
       the active tab visually "owns" its slot. */
    margin-bottom: -1px;
}
.ct-tabs__btn:hover {
    color: var(--p-text);
    background: color-mix(in srgb, var(--p-text-muted), transparent 94%);
}
.ct-tabs__btn.is-active {
    color: var(--p-primary-500);
    border-bottom-color: var(--p-primary-500);
    background: transparent;
}
.ct-tabs__panels {
    padding: 20px 24px 24px;
}
.ct-tab-panel {
    /* All panels live in DOM via v-show; this is the only positioning
       they need. Per-panel content (terminals, tables, etc.) brings
       its own styles. */
}

/* ─────────── KV tables (env / labels / mounts) ─────────── */
.kv-table {
    width: 100%;
    border-collapse: collapse;
    background: color-mix(in srgb, var(--p-surface-900, #0b0e14), transparent 40%);
    border: 1px solid var(--p-content-border);
    border-radius: 8px;
    overflow: hidden;
    font-size: 13px;
}
.kv-table thead th {
    text-align: left;
    padding: 9px 12px;
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--p-text-muted);
    background: color-mix(in srgb, var(--p-text-muted), transparent 92%);
    border-bottom: 1px solid var(--p-content-border);
}
.kv-table tbody td {
    padding: 9px 12px;
    color: var(--p-text);
    border-bottom: 1px solid color-mix(in srgb, var(--p-content-border), transparent 60%);
    vertical-align: top;
    word-break: break-all;
}
.kv-table tbody tr:last-child td { border-bottom: 0; }
.kv-table__k {
    width: 30%;
    color: var(--p-text-muted);
    white-space: nowrap;
}
.kv-table__v { color: var(--p-text); }

.block-empty {
    padding: 16px 4px;
    font-style: italic;
    font-size: 13px;
}

.block-pre {
    margin: 8px 0;
    padding: 12px 14px;
    background: color-mix(in srgb, var(--p-surface-900, #04070c), transparent 0%);
    border: 1px solid var(--p-content-border);
    border-radius: 8px;
    font-family: ui-monospace, "JetBrains Mono", Menlo, monospace;
    font-size: 12px;
    color: var(--p-text);
    white-space: pre-wrap;
    word-break: break-word;
}

/* ─────────── Runtime panes ─────────── */
.block-terminal {
    margin: 0;
    padding: 14px 16px;
    border: 1px solid var(--p-content-border);
    border-radius: 8px;
    background: color-mix(in srgb, var(--p-surface-900, #04070c), transparent 0%);
    color: var(--p-text);
    font-family: ui-monospace, "JetBrains Mono", Menlo, monospace;
    font-size: 12px;
    line-height: 1.55;
    height: 60vh;
    overflow: auto;
    white-space: pre-wrap;
    word-break: break-word;
}

.block-terminal-host {
    border: 1px solid var(--p-content-border);
    border-radius: 8px;
    overflow: hidden;
    background: #000;
    height: 60vh;
}
.block-terminal-host__inner { width: 100%; height: 100%; }

/* ─────────── Stats cards ─────────── */
.stats-pane {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: 16px;
}
.stat-card {
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    padding: 16px 18px;
    background: color-mix(in srgb, var(--p-content-bg), transparent 4%);
}
.stat-card__label {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--p-text-muted);
    font-weight: 650;
}
.stat-card__value {
    margin-top: 6px;
    font-size: 22px;
    font-weight: 700;
    color: var(--p-text);
    font-variant-numeric: tabular-nums;
    line-height: 1.1;
}
.stat-card__unit {
    font-size: 12px;
    font-weight: 500;
    color: var(--p-text-muted);
    margin-left: 4px;
}
.stat-card__bar {
    margin-top: 12px;
    height: 6px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    overflow: hidden;
}
.stat-card__bar-fill {
    height: 100%;
    background: linear-gradient(90deg, var(--p-primary-500), color-mix(in srgb, var(--p-primary-500), white 20%));
    transition: width 600ms ease-out;
}
.stats-pane__err {
    grid-column: 1 / -1;
    margin: 0;
    color: var(--p-red-400, #f87171);
    font-size: 12px;
}
.stats-pane__hint {
    grid-column: 1 / -1;
    margin: 0;
    font-size: 12px;
}

/* ─────────── Domains tab ─────────── */
.domains-pane {
    display: flex;
    flex-direction: column;
    gap: 14px;
}
.domains-table__act { width: 1%; text-align: right; }
.domain-link {
    color: var(--p-text);
    text-decoration: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
}
.domain-link:hover { color: var(--p-primary-500); }
.domain-link i { font-size: 10px; opacity: 0.6; }
.domains-pane__cta {
    display: flex;
    justify-content: flex-end;
}

/* SSL status pills inside the Domínios tab. The colors mirror what
   the rest of the product uses for the same states. */
.pill--ssl-active {
    background: color-mix(in srgb, var(--p-success, #10b981), transparent 85%);
    color: var(--p-success, #10b981);
}
.pill--ssl-issuing,
.pill--ssl-pending {
    background: color-mix(in srgb, var(--p-warning, #d97706), transparent 85%);
    color: var(--p-warning, #d97706);
}
.pill--ssl-failed,
.pill--ssl-expired {
    background: color-mix(in srgb, var(--p-danger, #dc2626), transparent 85%);
    color: var(--p-danger, #dc2626);
}
.pill--ssl-none {
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    color: var(--p-text-muted);
}

/* ─────────── Bind dialog (reused from ContainersTable pattern) ─────────── */
.bind-dialog {
    display: flex;
    flex-direction: column;
    gap: 14px;
}
.link {
    color: var(--p-primary-500);
    text-decoration: none;
    font-weight: 600;
}
.link:hover { text-decoration: underline; }

/* ─────────── Pills (mount type only — state uses StatusBadge) ─────────── */
.pill {
    display: inline-block;
    padding: 1px 8px;
    border-radius: 999px;
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
}
.pill--bind {
    background: color-mix(in srgb, var(--p-warning, #d97706), transparent 80%);
    color: var(--p-warning, #d97706);
}
.pill--volume {
    background: color-mix(in srgb, var(--p-primary-500), transparent 80%);
    color: var(--p-primary-500);
}

.port {
    display: inline-block;
    padding: 1px 7px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 4px;
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
    color: var(--p-text);
    margin-right: 4px;
}

.mono { font-family: ui-monospace, "JetBrains Mono", Menlo, monospace; }
.muted { color: var(--p-text-muted); }
</style>
