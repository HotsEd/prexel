<template>
    <!--
        DeploymentDetailView — full-page view of a single deployment.

        Two operating modes, picked at mount based on the deployment's
        `status`:

          1. Terminal mode (success | failed). One-shot GET of the
             persisted build log file, render as plain text. No live
             subscription — the deploy is done, nothing new is coming.

          2. Live mode (pending | building | deploying). Subscribe to
             SSE `/apps/{appId}/events`, filter by `deployment_id ===
             depId`. New `deploy.log` events append to the terminal;
             `deploy.success` / `deploy.failed` switch us out of live
             mode and re-fetch the deployment row to see the final
             timing.

        For live mode we ALSO seed with the persisted log prefix (if
        any) before subscribing — covers the case where the operator
        opened this page mid-deploy and would otherwise see only
        events newer than the page load.
    -->
    <div class="deployment-detail">
        <!-- Object form (named route + query) — the string form
             `/apps/X?tab=deployments` can silently lose the query
             on history backends; the explicit object always lands
             on the right tab. Same treatment used in
             ContainerDetailView. -->
        <RouterLink
            :to="{ name: 'apps.detail', params: { id: appId }, query: { tab: 'deployments' } }"
            class="back-link"
        >
            <IconChevronLeft :size="14" />
            {{ t('deployments.backToList') }}
        </RouterLink>

        <header class="deployment-detail__head">
            <div class="deployment-detail__title-row">
                <h1 class="deployment-detail__title">
                    {{ headerTitle }}
                </h1>
                <StatusBadge v-if="deployment" :status="deployment.status" />
            </div>
            <dl v-if="deployment" class="deployment-detail__meta">
                <div>
                    <dt>{{ t('deployments.fields.commit') }}</dt>
                    <dd class="mono">{{ deployment.commit_sha?.slice(0, 7) ?? '—' }}</dd>
                </div>
                <div>
                    <dt>{{ t('deployments.fields.branch') }}</dt>
                    <dd><span class="branch-pill">{{ deployment.branch || 'main' }}</span></dd>
                </div>
                <div>
                    <dt>{{ t('deployments.fields.app') }}</dt>
                    <dd>
                        <RouterLink v-if="appId" :to="`/apps/${appId}`" class="link">
                            {{ appName || appId.slice(0, 8) }}
                        </RouterLink>
                    </dd>
                </div>
                <div>
                    <dt>{{ t('deployments.fields.started') }}</dt>
                    <dd>{{ deployment.started_at ? formatRelative(deployment.started_at) : '—' }}</dd>
                </div>
                <div>
                    <dt>{{ t('deployments.fields.duration') }}</dt>
                    <dd>{{ durationLabel }}</dd>
                </div>
                <div v-if="deployment.image_tag">
                    <dt>{{ t('deployments.fields.image') }}</dt>
                    <dd class="mono ellipsis" :title="deployment.image_tag">
                        {{ deployment.image_tag }}
                    </dd>
                </div>
                <div v-if="deployment.rollback_of">
                    <dt>{{ t('deployments.fields.rollbackOf') }}</dt>
                    <dd class="mono">{{ deployment.rollback_of.slice(0, 8) }}</dd>
                </div>
            </dl>
        </header>

        <section v-if="deployment" class="deployment-detail__pipeline pv-panel">
            <DeployPipeline :status="deployment.status" :active-hint="activeHint" />
        </section>

        <!--
            Prominent error panel — only when the deploy failed AND
            we have an error string to show. Uses the multi-line
            `deployment.error_message` from the DB (wrapped error
            chain from failDeploy) when available; falls back to
            whatever the SSE `deploy.failed` event carried.

            Renders the WHOLE message verbatim, including the wrap
            chain (e.g. "build: build: compose `build:` directives
            not supported yet ..."), so the operator gets the same
            information they'd see in the terminal at the failure
            point without having to scroll.
        -->
        <section v-if="errorPanel" class="deployment-detail__error pv-panel">
            <div class="deployment-detail__error-head">
                <i class="pi pi-exclamation-triangle" aria-hidden="true" />
                <h2>{{ t('deployments.failed.title') }}</h2>
                <button
                    v-if="hasTerminalErrorLine"
                    type="button"
                    class="deployment-detail__error-link"
                    @click="terminalRef?.jumpToFirstError()"
                >
                    {{ t('deployments.failed.viewInLog') }}
                    <i class="pi pi-arrow-down" />
                </button>
            </div>
            <!--
                The `errorPanel` value originates either from the DB
                (`deployment.error_message`) or from the SSE
                `deploy.failed` payload. We render it via
                text interpolation only ({{ … }}) — never v-html — so
                even a hostile log line can't introduce HTML/script
                into the page. Same rule applies to `activeHint` (set
                from SSE log lines), which is also rendered as plain
                text inside DeployPipeline.
            -->
            <pre class="deployment-detail__error-body">{{ errorPanel }}</pre>
        </section>

        <section class="deployment-detail__actions">
            <div class="deployment-detail__actions-left">
                <span v-if="isLive" class="live-pill">
                    <span class="live-pill__dot" />
                    {{ t('deployments.followingLive') }}
                </span>
            </div>
            <div class="deployment-detail__actions-right">
                <Button
                    v-if="deployment?.status === 'failed'"
                    :label="t('apps.retry')"
                    icon="pi pi-refresh"
                    severity="secondary"
                    outlined
                    :loading="actionBusy"
                    @click="reDeploy"
                />
                <Button
                    v-if="deployment?.status === 'success'"
                    :label="t('deployments.rollbackToThis')"
                    icon="pi pi-undo"
                    severity="secondary"
                    outlined
                    :loading="actionBusy"
                    @click="rollbackToThis"
                />
            </div>
        </section>

        <section class="deployment-detail__terminal-wrap">
            <DeployTerminal
                v-if="loaded"
                ref="terminalRef"
                :lines="terminalLines"
                :download-name="`deploy-${depId.slice(0, 8)}.log`"
                :empty-message="emptyMsg"
                :initial-jump-to-error="deployment?.status === 'failed'"
            />
            <div v-else class="state-row">{{ t('deployments.loading') }}</div>
        </section>
    </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'

import IconChevronLeft from '@/components/icons/IconChevronLeft.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import DeployPipeline from '@/components/apps/DeployPipeline.vue'
import DeployTerminal, { type DeployLogLine } from '@/components/apps/DeployTerminal.vue'

import { useAppsStore } from '@/stores/apps'
import {
    fetchDeployment,
    fetchDeploymentLogs,
    streamAppEventsURL,
} from '@/services/apps'
import { useSSE } from '@/composables/useSSE'
import { formatRelative } from '@/utils/format'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { Deployment } from '@/types/api'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const appsStore = useAppsStore()

const depId = computed(() => String(route.params.depId ?? ''))
const appId = computed(() => String(route.params.id ?? ''))

const deployment = ref<Deployment | null>(null)
const appName = ref('')
const loaded = ref(false)

// Single source of lines for the terminal. Both modes (live SSE
// and one-shot text dump for finished deploys) parse into this
// array, so the terminal renders with the same per-line coloring,
// line numbers, and jump-to-error affordance regardless of source.
const terminalLines = ref<DeployLogLine[]>([])
// Live-only — emitted by SSE during deploy.failed so we have the
// engine's wrapped cause without parsing the log. Falls back to
// `deployment.error_message` (persisted in the DB by failDeploy)
// for deploys that already finished.
const liveErrorMessage = ref<string | null>(null)
// activeHint surfaces a tail-of-the-log hint under the active pipeline
// chip — gives the operator some signal of WHAT specifically is happening
// (e.g. "pulling layer 4/12") without having to read the terminal.
const activeHint = ref<string | null>(null)
const actionBusy = ref(false)

const terminalRef = ref<InstanceType<typeof DeployTerminal> | null>(null)
let stopSse: (() => void) | null = null

// ── Derived display ────────────────────────────────────────────────
const isLive = computed(() => {
    const s = deployment.value?.status
    return s === 'pending' || s === 'building' || s === 'deploying'
})

/*
   The "what to show in the failure banner" computed.
   Priority:
     1. The persisted DB column (`deployment.error_message`) — always
        carries the full wrapped error chain.
     2. The live SSE event payload (`liveErrorMessage`) — for the case
        where the operator was watching when the failure happened and
        we haven't re-fetched the row yet.
     3. The last stderr line in the log as a heuristic — last resort
        for older rows that pre-date the error_message column.
   Null = banner hidden.
*/
const errorPanel = computed<string | null>(() => {
    if (deployment.value?.status !== 'failed') return null
    if (deployment.value.error_message && deployment.value.error_message.trim() !== '') {
        return deployment.value.error_message
    }
    if (liveErrorMessage.value && liveErrorMessage.value.trim() !== '') {
        return liveErrorMessage.value
    }
    return summariseError(terminalLines.value)
})

const hasTerminalErrorLine = computed(() =>
    terminalLines.value.some((l) => l.stream === 'stderr'),
)

const headerTitle = computed(() => {
    if (!deployment.value) return t('deployments.header.deploy')
    const msg = deployment.value.commit_msg
    if (msg && msg.trim()) {
        return msg.length > 80 ? msg.slice(0, 80) + '…' : msg
    }
    return t('deployments.header.deployId', { id: deployment.value.id.slice(0, 8) })
})

const durationLabel = computed(() => {
    const d = deployment.value
    if (!d?.started_at) return '—'
    const end = d.finished_at ?? Math.floor(Date.now() / 1000)
    return formatSecondsDuration(end - d.started_at)
})

const emptyMsg = computed(() => {
    if (!deployment.value) return t('deployments.empty.loading')
    if (deployment.value.status === 'pending') return t('deployments.empty.pending')
    return t('deployments.empty.noLog')
})

// ── Lifecycle ──────────────────────────────────────────────────────
onMounted(async () => {
    try {
        await loadDeployment()
        await loadAppName()
        await primeOrAttach()
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        loaded.value = true
    }
})

onBeforeUnmount(() => {
    stopSse?.()
})

// If the route changes to a different deployment (e.g. navigating
// from one history row to another), tear down + reload.
watch(depId, async (next, prev) => {
    if (next === prev) return
    stopSse?.()
    terminalLines.value = []
    liveErrorMessage.value = null
    loaded.value = false
    activeHint.value = null
    try {
        await loadDeployment()
        await primeOrAttach()
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        loaded.value = true
    }
})

// ── Loading ────────────────────────────────────────────────────────
async function loadDeployment() {
    deployment.value = await fetchDeployment(depId.value)
}

async function loadAppName() {
    // Best-effort: pull from the store cache if present, otherwise
    // fetch. We only need this for the header — failure is silent.
    const cached = appsStore.apps.find((a) => a.id === appId.value)
    if (cached) {
        appName.value = cached.name
        return
    }
    try {
        const app = await appsStore.fetchOne(appId.value)
        appName.value = app?.name ?? ''
    } catch {
        // Don't block on this — header just shows the id.
    }
}

/*
   Seed the terminal with whatever the persisted log file holds.
   For live deploys we also attach an SSE subscription that appends
   new lines on top. Either way the terminal renders from a single
   `terminalLines` array so per-line coloring + line numbers +
   jump-to-error work uniformly.
*/
async function primeOrAttach() {
    if (!deployment.value) return
    try {
        const text = await fetchDeploymentLogs(depId.value)
        terminalLines.value = parseStructuredLog(text)
    } catch {
        // Empty start — for fresh live deploys the log file hasn't
        // been written yet; SSE will populate as lines arrive.
        terminalLines.value = []
    }
    if (isLive.value) {
        attachSse()
    }
}

function attachSse() {
    const sse = useSSE<{ topic: string; type: string; payload: Record<string, unknown>; ts: string }>(
        streamAppEventsURL(appId.value),
        {
            onEvent(ev) {
                const data = ev.data
                if (!data || typeof data !== 'object') return
                const payload = data.payload as Record<string, unknown> | undefined
                if (!payload || payload.deployment_id !== depId.value) return

                switch (data.type) {
                    case 'deploy.log': {
                        const line = String(payload.line ?? '')
                        const stream = String(payload.stream ?? 'stdout')
                        const ts = typeof payload.ts === 'string' ? payload.ts : undefined
                        terminalLines.value = [...terminalLines.value, { stream, line, ts }]
                        // Refresh the pipeline hint with the latest
                        // log line (truncated). Gives the operator
                        // something to glance at without reading the
                        // full terminal.
                        activeHint.value = line.length > 60 ? line.slice(0, 60) + '…' : line
                        break
                    }
                    case 'deploy.started':
                        // Status will move to "building" on its own
                        // via app.status_changed.
                        break
                    case 'app.status_changed': {
                        const next = String(payload.status ?? '')
                        if (deployment.value && (next === 'building' || next === 'deploying')) {
                            deployment.value = { ...deployment.value, status: next }
                        }
                        break
                    }
                    case 'deploy.success':
                        void onTerminalEvent('success')
                        break
                    case 'deploy.failed': {
                        const err = typeof payload.error === 'string' ? payload.error : null
                        liveErrorMessage.value = err
                        void onTerminalEvent('failed')
                        break
                    }
                }
            },
        },
    )
    stopSse = sse.close
}

async function onTerminalEvent(_finalStatus: 'success' | 'failed') {
    // Tear down the live subscription, then re-fetch the deployment
    // row to pick up `finished_at` + final `image_tag`. We don't try
    // to switch render mode (lines → text) — the operator can refresh
    // if they want the consolidated <pre> form.
    stopSse?.()
    stopSse = null
    try {
        await loadDeployment()
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

// ── Actions ────────────────────────────────────────────────────────
async function rollbackToThis() {
    if (!deployment.value) return
    actionBusy.value = true
    try {
        const newDep = await appsStore.rollback(appId.value, deployment.value.id)
        notify.success(t('deployments.toast.rollbackStarted'))
        // Navigate the operator to the new (in-flight) deploy so they
        // can watch the swap happen — exactly the same pattern as
        // firing a regular deploy.
        await router.push(`/apps/${appId.value}/deployments/${newDep.id}?watch=1`)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        actionBusy.value = false
    }
}

async function reDeploy() {
    actionBusy.value = true
    try {
        const newDep = await appsStore.deploy(appId.value)
        notify.success(t('deployments.toast.deployStarted'))
        await router.push(`/apps/${appId.value}/deployments/${newDep.id}?watch=1`)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        actionBusy.value = false
    }
}

// ── Helpers ────────────────────────────────────────────────────────

/*
   Parse the engine's persisted log format back into structured
   lines. Format (see build/engine.go writeLog):
     "{rfc3339} [{stream}] {text}\n"
   Anything that doesn't match falls back to stdout — we don't want
   to drop lines just because the prefix is missing.
*/
const LOG_RE = /^(\S+)\s+\[(stdout|stderr)\]\s+(.*)$/
function parseStructuredLog(text: string): DeployLogLine[] {
    if (!text) return []
    const out: DeployLogLine[] = []
    for (const raw of text.split(/\r?\n/)) {
        if (!raw) continue
        const m = LOG_RE.exec(raw)
        if (m) {
            out.push({ ts: m[1], stream: m[2] as DeployLogLine['stream'], line: m[3] ?? '' })
        } else {
            out.push({ stream: 'stdout', line: raw })
        }
    }
    return out
}

/*
   Heuristic fallback for the error banner — only used when neither
   `deployment.error_message` (DB column from failDeploy) nor the
   live SSE `error` payload is available. Picks the last stderr
   line in the parsed terminal lines; falls back to the last line of
   any kind. Capped at 200 chars so a misbehaving log can't crash
   the layout.
*/
function summariseError(lines: DeployLogLine[]): string | null {
    if (lines.length === 0) return null
    let pick: DeployLogLine | null = null
    for (let i = lines.length - 1; i >= 0; i--) {
        const candidate = lines[i]
        if (!candidate) continue
        if (candidate.stream === 'stderr') { pick = candidate; break }
        if (!pick) pick = candidate
    }
    if (!pick) return null
    const text = pick.line.trim()
    if (!text) return null
    return text.length > 200 ? text.slice(0, 200) + '…' : text
}

/*
   Backend timestamps are Unix-seconds. Format as `Ns`, `Nm Ss`, or
   `Nh Mm` depending on magnitude. Used for the duration field in
   the header.
*/
function formatSecondsDuration(seconds: number): string {
    if (!Number.isFinite(seconds) || seconds < 0) return '—'
    if (seconds < 60) return `${Math.max(1, Math.round(seconds))}s`
    const m = Math.floor(seconds / 60)
    if (m < 60) return `${m}m ${Math.round(seconds % 60)}s`
    const h = Math.floor(m / 60)
    return `${h}h ${m % 60}m`
}
</script>

<style scoped>
.deployment-detail {
    max-width: 1280px;
    margin: 0 auto;
    padding: 24px 28px 32px;
    display: flex;
    flex-direction: column;
    gap: 18px;
}

.back-link {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--p-text-muted);
    font-size: 12px;
    text-decoration: none;
    width: max-content;
}
.back-link:hover { color: var(--p-text); }

.deployment-detail__head {
    display: flex;
    flex-direction: column;
    gap: 14px;
}
.deployment-detail__title-row {
    display: flex;
    align-items: center;
    gap: 14px;
    flex-wrap: wrap;
}
.deployment-detail__title {
    margin: 0;
    font-size: 22px;
    font-weight: 700;
    color: var(--p-text);
    line-height: 1.25;
    letter-spacing: -0.01em;
}

.deployment-detail__meta {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: 12px 24px;
    margin: 0;
    padding: 0;
}
.deployment-detail__meta > div { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.deployment-detail__meta dt {
    font-size: 11px;
    color: var(--p-text-muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-weight: 650;
}
.deployment-detail__meta dd {
    margin: 0;
    font-size: 13px;
    color: var(--p-text);
}
.mono { font-family: ui-monospace, Menlo, monospace; }
.ellipsis {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 100%;
}

.deployment-detail__pipeline {
    padding: 16px 18px;
}

.deployment-detail__actions {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    min-height: 38px;
    flex-wrap: wrap;
}
.deployment-detail__actions-left,
.deployment-detail__actions-right {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
}

.live-pill {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    padding: 4px 10px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--p-primary-500), transparent 86%);
    color: var(--p-primary-500);
    font-size: 12px;
    font-weight: 600;
    border: 1px solid color-mix(in srgb, var(--p-primary-500), transparent 60%);
}
.live-pill__dot {
    width: 7px;
    height: 7px;
    border-radius: 999px;
    background: var(--p-primary-500);
    box-shadow: 0 0 0 0 color-mix(in srgb, var(--p-primary-500), transparent 50%);
    animation: live-pulse 1.6s ease-in-out infinite;
}
@keyframes live-pulse {
    0%,100% { box-shadow: 0 0 0 0 color-mix(in srgb, var(--p-primary-500), transparent 50%); }
    50%     { box-shadow: 0 0 0 6px color-mix(in srgb, var(--p-primary-500), transparent 90%); }
}

/* Prominent error panel — sits between the pipeline and the
   action bar when status=failed. Renders the full wrapped error
   chain in a mono <pre>, with a "view in log" jump button that
   scrolls the terminal to the first stderr line. */
.deployment-detail__error {
    padding: 16px 18px;
    border-color: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 55%);
    background: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 92%);
}
.deployment-detail__error-head {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 10px;
}
.deployment-detail__error-head h2 {
    margin: 0;
    font-size: 13px;
    font-weight: 700;
    color: var(--p-red-400, #f87171);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    flex: 1;
}
.deployment-detail__error-head > i {
    color: var(--p-red-400, #f87171);
}
.deployment-detail__error-link {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    background: transparent;
    border: 1px solid color-mix(in srgb, var(--p-red-500, #dc2626), transparent 55%);
    color: var(--p-red-400, #f87171);
    border-radius: 999px;
    padding: 3px 10px;
    font-size: 11px;
    cursor: pointer;
    font-family: inherit;
    transition: background 120ms ease, color 120ms ease;
}
.deployment-detail__error-link:hover {
    background: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 80%);
    color: var(--p-red-300, #fca5a5);
}
.deployment-detail__error-body {
    margin: 0;
    color: var(--p-text);
    font-family: ui-monospace, "JetBrains Mono", Menlo, monospace;
    font-size: 12px;
    line-height: 1.55;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 240px;
    overflow: auto;
}

.branch-pill {
    display: inline-flex;
    align-items: center;
    border: 1px solid var(--p-content-border);
    border-radius: 999px;
    padding: 2px 8px;
    color: var(--p-text-subtle);
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
}

.link {
    color: var(--p-primary-500);
    text-decoration: none;
}
.link:hover { text-decoration: underline; }

.state-row { padding: 32px; color: var(--p-text-muted); text-align: center; }

@media (prefers-reduced-motion: reduce) {
    .live-pill__dot { animation: none; }
}
</style>
