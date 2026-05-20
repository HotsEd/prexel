<template>
    <!--
        AppLogsTab — aggregated log stream for every container of the
        app. For compose apps that means nginx + php + redis + … all
        merged into one chronological terminal. Operators almost
        always want this view (debugging an integration between
        services), not the per-container drill-down which already
        lives at /apps/:id/containers/:name → Logs tab.

        Layout:
          - Toolbar: container chips (toggle visibility per container),
            "Tudo / Limpar" actions, autoscroll + wrap toggles.
          - Terminal: stream of lines, each prefixed with the
            container name in a stable colour (hash → palette index).

        Streaming: one SSE connection per running container. The
        merged timeline is ordered by SSE arrival (good enough — the
        backend timestamps lines as they're produced, so out-of-order
        across containers within the same second is rare).
    -->
    <div class="app-logs">
        <header class="app-logs__head">
            <div>
                <h2 class="app-logs__title">{{ t('logs.title') }}</h2>
                <i18n-t keypath="logs.subFmt" tag="p" class="app-logs__sub">
                    <template #stderr><code>stderr</code></template>
                </i18n-t>
            </div>
            <div class="app-logs__head-actions">
                <Button
                    v-if="!loadingContainers"
                    size="small"
                    text
                    icon="pi pi-refresh"
                    :aria-label="t('logs.reloadContainers')"
                    :title="t('logs.reloadContainers')"
                    @click="reloadContainers"
                />
            </div>
        </header>

        <!-- ─────────── No running containers ─────────── -->
        <div v-if="loadingContainers && containers.length === 0" class="app-logs__state muted">
            {{ t('logs.loadingContainers') }}
        </div>
        <div v-else-if="runningContainers.length === 0" class="app-logs__state muted">
            {{ t('logs.noRunningContainers') }}
        </div>

        <!-- ─────────── Filter chips ─────────── -->
        <div v-else class="app-logs__filters">
            <button
                type="button"
                :class="['app-logs__chip', 'app-logs__chip--all', allVisible && 'is-active']"
                @click="toggleAll"
            >
                {{ allVisible ? t('logs.hideAll') : t('logs.showAll') }}
            </button>
            <button
                v-for="c in runningContainers"
                :key="c.container_name || c.service"
                type="button"
                :class="['app-logs__chip', visibleSet.has(keyOf(c)) && 'is-active']"
                :style="chipStyle(keyOf(c))"
                @click="toggleContainer(keyOf(c))"
            >
                <span class="app-logs__chip-dot" :style="dotStyle(keyOf(c))" />
                {{ c.service || c.container_name }}
            </button>

            <span class="app-logs__filler" />

            <button
                type="button"
                :class="['app-logs__toggle', autoscroll && 'is-on']"
                :title="autoscroll ? t('logs.autoscrollActive') : t('logs.autoscrollPaused')"
                @click="reArmAutoscroll"
            >
                <i class="pi pi-arrow-down" />
                {{ autoscroll ? t('logs.autoscroll') : t('logs.reattach') }}
            </button>
            <button
                type="button"
                :class="['app-logs__toggle', wrap && 'is-on']"
                :title="t('logs.wrapTitle')"
                @click="wrap = !wrap"
            >
                <i class="pi pi-arrows-h" />
                {{ t('logs.wrap') }}
            </button>
            <button
                type="button"
                class="app-logs__toggle"
                :title="t('logs.clearTitle')"
                @click="clearBuffer"
            >
                <i class="pi pi-trash" />
                {{ t('logs.clear') }}
            </button>
        </div>

        <!-- ─────────── Terminal ─────────── -->
        <div
            v-if="runningContainers.length > 0"
            ref="bodyEl"
            :class="['app-logs__body', wrap && 'is-wrap']"
            @scroll="onScroll"
        >
            <div v-if="truncated" class="app-logs__truncated">
                {{ t('logs.truncated', { n: MAX_LINES }) }}
            </div>
            <div v-if="visibleLines.length === 0" class="app-logs__empty">
                {{ allHidden ? t('logs.noneSelected') : t('logs.waiting') }}
            </div>
            <div v-else class="app-logs__lines">
                <div
                    v-for="(l, i) in visibleLines"
                    :key="`${l.container}-${l.seq}`"
                    :class="['app-logs__ln', l.stream === 'stderr' && 'is-stderr']"
                >
                    <span
                        class="app-logs__ln-tag"
                        :style="{ color: colorOf(l.container) }"
                    >
                        {{ l.container }}
                    </span>
                    <span class="app-logs__ln-text">
                        <template v-if="parseAnsiCache.get(l.seq)?.length">
                            <span
                                v-for="(seg, j) in parseAnsiCache.get(l.seq)"
                                :key="j"
                                :class="seg.classes"
                            >{{ seg.text }}</span>
                        </template>
                        <template v-else>{{ l.line || ' ' }}</template>
                    </span>
                </div>
                <!-- spacer at bottom so the last line never hides behind padding -->
                <div :key="i" />
            </div>
        </div>
    </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import {
    fetchContainers,
    streamContainerLogsURL,
    type AppContainer,
} from '@/services/apps'
import { useSSE } from '@/composables/useSSE'
import { parseAnsi, type AnsiSegment } from '@/utils/ansi'

const { t } = useI18n()

const props = defineProps<{
    appId: string
}>()

// ── Containers + SSE management ───────────────────────────────────
const containers = shallowRef<AppContainer[]>([])
const loadingContainers = ref(true)
const visibleSet = ref<Set<string>>(new Set())

const runningContainers = computed(() =>
    containers.value.filter((c) => c.state === 'running' && c.container_name),
)

const allVisible = computed(() => {
    if (runningContainers.value.length === 0) return false
    return runningContainers.value.every((c) => visibleSet.value.has(keyOf(c)))
})
const allHidden = computed(() => visibleSet.value.size === 0 && runningContainers.value.length > 0)

function keyOf(c: AppContainer): string {
    return c.container_name || c.service
}

async function reloadContainers() {
    loadingContainers.value = true
    try {
        containers.value = await fetchContainers(props.appId)
    } finally {
        loadingContainers.value = false
    }
    // First load: subscribe to every running container by default so
    // the operator sees output without having to opt in. Subsequent
    // refreshes preserve the existing filter choices.
    if (visibleSet.value.size === 0) {
        const next = new Set<string>()
        for (const c of runningContainers.value) next.add(keyOf(c))
        visibleSet.value = next
    }
    syncStreams()
}

// ── Lines buffer ───────────────────────────────────────────────────
//
// Each line tagged with its source container + a monotonically
// increasing `seq` so Vue's :key stays stable across re-renders and
// the parsed-ANSI cache can be addressed by seq.
interface MergedLine {
    seq: number
    container: string
    stream: string
    line: string
}
// Hard cap on retained lines. The ring buffer trims the oldest
// entries when this is exceeded — keeps DOM size predictable on a
// chatty container without dropping the most recent (i.e. most
// useful) lines. Truncation surfaces an inline banner so the
// operator knows older lines exist but aren't on screen.
const MAX_LINES = 10_000
let nextSeq = 0
const lines = shallowRef<MergedLine[]>([])
const truncated = ref(false)

// Per-line ANSI parse cache. Indexed by seq; entries are dropped
// when lines roll off the MAX_LINES buffer.
const parseAnsiCache = shallowRef<Map<number, AnsiSegment[]>>(new Map())

const visibleLines = computed(() =>
    lines.value.filter((l) => visibleSet.value.has(l.container)),
)

function pushLine(container: string, stream: string, line: string) {
    const seq = nextSeq++
    const m = { seq, container, stream, line }
    // ANSI parse upfront, cached by seq.
    const cache = new Map(parseAnsiCache.value)
    cache.set(seq, parseAnsi(line))
    // Ring-buffer trim with cache cleanup. We push then slice (instead
    // of pre-allocating a fixed-size array) because the common case
    // is well under MAX_LINES and the slice cost is amortised.
    let next = [...lines.value, m]
    if (next.length > MAX_LINES) {
        const drop = next.length - MAX_LINES
        const dropped = next.slice(0, drop)
        next = next.slice(drop)
        for (const d of dropped) cache.delete(d.seq)
        truncated.value = true
    }
    lines.value = next
    parseAnsiCache.value = cache
}

function clearBuffer() {
    lines.value = []
    parseAnsiCache.value = new Map()
    truncated.value = false
}

// ── SSE plumbing ──────────────────────────────────────────────────
//
// One SSE handle per container, keyed by container name. We open
// streams lazily as visibility flips on; closing a chip tears the
// stream down to stop wasted bandwidth on a long-running shell.
const streams = new Map<string, () => void>()

function syncStreams() {
    // Open streams for visible containers.
    for (const c of runningContainers.value) {
        const k = keyOf(c)
        if (visibleSet.value.has(k) && !streams.has(k)) {
            openStream(c)
        }
    }
    // Close streams that became hidden or whose container is no
    // longer in the running set (e.g. stopped between refreshes).
    const live = new Set(runningContainers.value.map(keyOf))
    for (const [k, stop] of streams) {
        if (!visibleSet.value.has(k) || !live.has(k)) {
            stop()
            streams.delete(k)
        }
    }
}

function openStream(c: AppContainer) {
    const name = c.container_name
    if (!name) return
    const label = c.service || name
    const sse = useSSE<{ stream?: string; line?: string }>(
        streamContainerLogsURL(props.appId, name, 200),
        {
            onEvent(ev) {
                const d = ev.data
                if (!d || typeof d !== 'object') return
                const line = String(d.line ?? '')
                if (!line) return
                const stream = String(d.stream ?? 'stdout')
                pushLine(label, stream, line)
            },
        },
    )
    streams.set(keyOf(c), sse.close)
}

function toggleContainer(k: string) {
    const next = new Set(visibleSet.value)
    if (next.has(k)) next.delete(k)
    else next.add(k)
    visibleSet.value = next
    syncStreams()
}

function toggleAll() {
    if (allVisible.value) {
        visibleSet.value = new Set()
    } else {
        visibleSet.value = new Set(runningContainers.value.map(keyOf))
    }
    syncStreams()
}

// ── Per-container color palette ───────────────────────────────────
//
// 8 distinct hues from the same xterm palette the ANSI parser uses.
// Stable hash → palette index so the same container always gets
// the same colour across reloads.
const PALETTE = [
    '#23d18b', // green
    '#3b8eea', // blue
    '#e5e510', // yellow
    '#d670d6', // magenta
    '#29b8db', // cyan
    '#f14c4c', // red
    '#f5f543', // yellow-bright
    '#23d18b', // green again as wrap
]
function hash(s: string): number {
    let h = 0
    for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0
    return Math.abs(h)
}
function colorOf(container: string): string {
    return PALETTE[hash(container) % PALETTE.length] ?? '#23d18b'
}
function chipStyle(container: string): Record<string, string> {
    const c = colorOf(container)
    return visibleSet.value.has(container)
        ? {
              borderColor: `color-mix(in srgb, ${c}, transparent 60%)`,
              background: `color-mix(in srgb, ${c}, transparent 88%)`,
              color: c,
          }
        : {}
}
function dotStyle(container: string): Record<string, string> {
    return { background: colorOf(container) }
}

// ── Autoscroll + wrap ─────────────────────────────────────────────
const bodyEl = ref<HTMLDivElement | null>(null)
const autoscroll = ref(true)
const wrap = ref(true)

watch(() => visibleLines.value.length, async () => {
    if (!autoscroll.value) return
    await nextTick()
    scrollToBottom()
})

function scrollToBottom() {
    const el = bodyEl.value
    if (!el) return
    el.scrollTop = el.scrollHeight
}
function onScroll() {
    const el = bodyEl.value
    if (!el) return
    const distance = el.scrollHeight - (el.scrollTop + el.clientHeight)
    autoscroll.value = distance < 24
}
function reArmAutoscroll() {
    autoscroll.value = true
    scrollToBottom()
}

// ── Lifecycle ─────────────────────────────────────────────────────
onMounted(reloadContainers)

onBeforeUnmount(() => {
    for (const stop of streams.values()) stop()
    streams.clear()
})

// Re-init when the app changes (route change between apps reuses
// this component when nested under the same view).
watch(() => props.appId, () => { void reloadContainers() })
</script>

<style scoped>
.app-logs {
    display: flex;
    flex-direction: column;
    gap: 14px;
}

.app-logs__head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
}
.app-logs__title {
    margin: 0;
    font-size: 16px;
    font-weight: 700;
    color: var(--p-text);
}
.app-logs__sub {
    margin: 4px 0 0;
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.5;
    max-width: 720px;
}
.app-logs__sub code {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
    padding: 0 4px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 3px;
}
.app-logs__state {
    padding: 24px 0;
    font-size: 13px;
}

/* ─────────── Filter chips ─────────── */
.app-logs__filters {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    padding: 10px 12px;
    border: 1px solid var(--p-content-border);
    background: var(--p-content-bg);
    border-radius: 10px;
}
.app-logs__chip {
    appearance: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 10px;
    border: 1px solid var(--p-content-border);
    background: transparent;
    color: var(--p-text-muted);
    border-radius: 999px;
    font: inherit;
    font-size: 12px;
    cursor: pointer;
    transition: background 100ms ease, color 100ms ease, border-color 100ms ease;
}
.app-logs__chip:hover { color: var(--p-text); }
.app-logs__chip-dot {
    width: 7px;
    height: 7px;
    border-radius: 999px;
    flex-shrink: 0;
    opacity: 0.4;
    transition: opacity 100ms ease;
}
.app-logs__chip.is-active .app-logs__chip-dot { opacity: 1; }
.app-logs__chip--all {
    border-color: var(--p-content-border);
}
.app-logs__chip--all.is-active {
    background: color-mix(in srgb, var(--p-primary-500), transparent 88%);
    color: var(--p-primary-500);
    border-color: color-mix(in srgb, var(--p-primary-500), transparent 60%);
}

.app-logs__filler { flex: 1; }
.app-logs__toggle {
    appearance: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 9px;
    border: 1px solid transparent;
    background: transparent;
    color: var(--p-text-muted);
    border-radius: 6px;
    font: inherit;
    font-size: 11px;
    cursor: pointer;
    transition: background 100ms ease, color 100ms ease, border-color 100ms ease;
}
.app-logs__toggle:hover {
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    color: var(--p-text);
}
.app-logs__toggle.is-on {
    background: color-mix(in srgb, var(--p-primary-500), transparent 88%);
    color: var(--p-primary-500);
    border-color: color-mix(in srgb, var(--p-primary-500), transparent 70%);
}

/* ─────────── Terminal body ─────────── */
.app-logs__body {
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    background: color-mix(in srgb, var(--p-surface-900, #04070c), transparent 0%);
    height: 65vh;
    overflow: auto;
    padding: 12px 14px;
}
.app-logs__lines {
    font-family: ui-monospace, "JetBrains Mono", Menlo, monospace;
    font-size: 12px;
    line-height: 1.55;
    color: var(--p-text);
    white-space: pre;
}
.app-logs.is-wrap .app-logs__lines,
.app-logs__body.is-wrap .app-logs__lines {
    white-space: pre-wrap;
    word-break: break-word;
}
.app-logs__ln {
    display: flex;
    gap: 12px;
    align-items: flex-start;
    padding: 0 2px;
}
.app-logs__ln-tag {
    flex-shrink: 0;
    min-width: 110px;
    max-width: 180px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: 700;
    user-select: none;
    /* color set inline per container */
}
.app-logs__ln-text {
    flex: 1;
    min-width: 0;
}
.app-logs__ln.is-stderr .app-logs__ln-text {
    color: var(--p-red-400, #f87171);
}
.app-logs__empty {
    color: var(--p-text-muted);
    font-style: italic;
    padding: 8px 0;
}
.app-logs__truncated {
    margin: 0 0 8px;
    padding: 6px 10px;
    border-radius: 6px;
    background: color-mix(in srgb, var(--p-warning, #d97706), transparent 88%);
    color: var(--p-warning, #d97706);
    font-size: 11px;
    font-style: italic;
}

/* ANSI palette classes used by parseAnsi. Re-declared here (scoped)
   so the ANSI colours survive the scope barrier. Same hex as the
   DeployTerminal. */
.app-logs :deep(.ansi-fg-black)           { color: #1e1e1e; }
.app-logs :deep(.ansi-fg-red)             { color: #cd3131; }
.app-logs :deep(.ansi-fg-green)           { color: #0dbc79; }
.app-logs :deep(.ansi-fg-yellow)          { color: #e5e510; }
.app-logs :deep(.ansi-fg-blue)            { color: #2472c8; }
.app-logs :deep(.ansi-fg-magenta)         { color: #bc3fbc; }
.app-logs :deep(.ansi-fg-cyan)            { color: #11a8cd; }
.app-logs :deep(.ansi-fg-white)           { color: #e5e5e5; }
.app-logs :deep(.ansi-fg-black-bright)    { color: #666666; }
.app-logs :deep(.ansi-fg-red-bright)      { color: #f14c4c; }
.app-logs :deep(.ansi-fg-green-bright)    { color: #23d18b; }
.app-logs :deep(.ansi-fg-yellow-bright)   { color: #f5f543; }
.app-logs :deep(.ansi-fg-blue-bright)     { color: #3b8eea; }
.app-logs :deep(.ansi-fg-magenta-bright)  { color: #d670d6; }
.app-logs :deep(.ansi-fg-cyan-bright)     { color: #29b8db; }
.app-logs :deep(.ansi-fg-white-bright)    { color: #ffffff; }
.app-logs :deep(.ansi-bold)      { font-weight: 700; }
.app-logs :deep(.ansi-dim)       { opacity: 0.65; }
.app-logs :deep(.ansi-italic)    { font-style: italic; }
.app-logs :deep(.ansi-underline) { text-decoration: underline; }
</style>
