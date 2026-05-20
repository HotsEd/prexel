<template>
    <!--
        DeployTerminal — fixed-height log surface for build logs.

        Renders an array of structured log lines (stream + text). For
        finished deploys the parent fetches the persisted log file as
        plain text and pre-parses it into the same shape; both modes
        thus share the SAME rendering path, giving us consistent
        stderr coloring, line numbers, and "jump to error" affordance.

        Toolbar:
          - Autoscroll toggle (auto-disables on user scroll up)
          - Wrap toggle (off = horizontal scrollbar for long lines)
          - Line numbers toggle
          - Jump-to-error (only when there's a stderr line; scrolls
            to the first one and highlights it briefly)
          - Copy + download

        Render: per-line `<div>` so we can address specific lines for
        scrollIntoView + temporary highlight. We deliberately don't
        use a single `<pre>` block — operators trade a tiny bit of
        memory for the ability to land directly on the error line in
        a 2000-line build log.
    -->
    <div :class="['deploy-terminal', wrap && 'is-wrap']">
        <div class="deploy-terminal__bar">
            <div class="deploy-terminal__counts">
                <span>{{ t('terminal.lineCount', lines.length, { named: { n: lines.length } }) }}</span>
                <span v-if="errCount > 0" class="deploy-terminal__err">
                    {{ t('terminal.stderrCount', { n: errCount }) }}
                </span>
            </div>
            <div class="deploy-terminal__actions">
                <button
                    v-if="firstErrorIdx >= 0"
                    type="button"
                    class="deploy-terminal__btn deploy-terminal__btn--alert"
                    :title="t('terminal.jumpToFirstStderr')"
                    @click="jumpToFirstError"
                >
                    <i class="pi pi-exclamation-triangle" />
                    <span>{{ t('terminal.jumpToError') }}</span>
                </button>
                <button
                    type="button"
                    :class="['deploy-terminal__btn', showLineNumbers && 'is-on']"
                    :title="t('terminal.showLineNumbers')"
                    @click="showLineNumbers = !showLineNumbers"
                >
                    <i class="pi pi-list" />
                    <span>#</span>
                </button>
                <button
                    type="button"
                    :class="['deploy-terminal__btn', autoscroll && 'is-on']"
                    :title="autoscroll ? t('terminal.autoscrollActive') : t('terminal.autoscrollPaused')"
                    @click="reArmAutoscroll"
                >
                    <i class="pi pi-arrow-down" />
                    <span>{{ autoscroll ? t('terminal.autoscroll') : t('terminal.reattach') }}</span>
                </button>
                <button
                    type="button"
                    :class="['deploy-terminal__btn', wrap && 'is-on']"
                    :title="t('terminal.wrapTitle')"
                    @click="wrap = !wrap"
                >
                    <i class="pi pi-arrows-h" />
                    <span>{{ t('terminal.wrap') }}</span>
                </button>
                <button
                    type="button"
                    class="deploy-terminal__btn"
                    :title="t('terminal.copyTitle')"
                    @click="copyAll"
                >
                    <i class="pi pi-copy" />
                </button>
                <button
                    type="button"
                    class="deploy-terminal__btn"
                    :title="t('terminal.downloadTitle')"
                    @click="download"
                >
                    <i class="pi pi-download" />
                </button>
            </div>
        </div>

        <div ref="bodyEl" class="deploy-terminal__body" @scroll="onScroll">
            <div v-if="lines.length === 0" class="deploy-terminal__empty">
                {{ emptyMessage || t('terminal.waitingLog') }}
            </div>
            <div v-else class="deploy-terminal__lines">
                <div
                    v-for="(l, i) in lines"
                    :key="i"
                    :ref="(el) => bindLineRef(i, el as HTMLDivElement | null)"
                    :class="[
                        'ln',
                        `ln--${l.stream}`,
                        i === highlightedIdx && 'ln--flash',
                    ]"
                >
                    <span v-if="showLineNumbers" class="ln__num">{{ i + 1 }}</span>
                    <span class="ln__text">
                        <template v-if="parsedLines[i]?.length">
                            <span
                                v-for="(seg, j) in parsedLines[i]"
                                :key="j"
                                :class="seg.classes"
                            >{{ seg.text }}</span>
                        </template>
                        <template v-else>{{ ' ' }}</template>
                    </span>
                </div>
            </div>
        </div>
    </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { notify } from '@/lib/notify'
import { parseAnsi, type AnsiSegment } from '@/utils/ansi'

const { t } = useI18n()

export interface DeployLogLine {
    stream: 'stdout' | 'stderr' | string
    line: string
    ts?: string
}

const props = withDefaults(defineProps<{
    /** Structured log lines. Always render from this; callers parse
        whatever shape they have (live SSE or static text dump) into
        DeployLogLine[] before passing in. */
    lines?: DeployLogLine[]
    /** Filename for the download button. */
    downloadName?: string
    /** Message shown when the line list is empty (still loading, no
        log yet, fresh deploy that hasn't emitted anything). */
    emptyMessage?: string
    /** When true, on first render with content present, jump to the
        first stderr line. Used by the parent for failed deploys so
        the operator's eye lands on the error immediately. */
    initialJumpToError?: boolean
}>(), {
    lines: () => [],
    downloadName: 'deploy.log',
    // Default empty stays as ''. The template falls back to a
    // localized "waiting" string via the {{ }} expression so we
    // don't bake a Portuguese literal into the prop default.
    emptyMessage: '',
    initialJumpToError: false,
})

const bodyEl = ref<HTMLDivElement | null>(null)
const lineEls = new Map<number, HTMLDivElement>()
const autoscroll = ref(true)
const wrap = ref(true)
const showLineNumbers = ref(false)
const highlightedIdx = ref<number | null>(null)

const errCount = computed(() => props.lines.filter((l) => l.stream === 'stderr').length)
const firstErrorIdx = computed(() => props.lines.findIndex((l) => l.stream === 'stderr'))

/*
   Cache of parsed ANSI segments per line. Docker build / npm /
   composer output uses ANSI colour escapes liberally; without
   parsing, the operator would see raw `\x1b[33m` codes mixed
   into the log. We parse lazily per render via computed — the
   work only happens once per (lines array identity, line index).
*/
const parsedLines = computed<AnsiSegment[][]>(() =>
    props.lines.map((l) => parseAnsi(l.line)),
)

/*
   When new lines arrive, scroll to bottom if autoscroll is armed.
   nextTick waits for the DOM update so we measure post-append heights.
*/
watch(() => props.lines.length, async () => {
    if (!autoscroll.value) return
    await nextTick()
    scrollToBottom()
})

onMounted(async () => {
    await nextTick()
    if (props.initialJumpToError && firstErrorIdx.value >= 0) {
        // Don't trigger autoscroll-to-bottom for failed deploys we
        // landed on with `initialJumpToError`. The operator wants
        // their attention on the error, not on the end-of-log noise.
        autoscroll.value = false
        jumpToFirstError()
    } else if (props.lines.length > 0) {
        scrollToBottom()
    }
})

function bindLineRef(idx: number, el: HTMLDivElement | null) {
    if (el) lineEls.set(idx, el)
    else lineEls.delete(idx)
}

function scrollToBottom() {
    const el = bodyEl.value
    if (!el) return
    el.scrollTop = el.scrollHeight
}

/*
   Disarm autoscroll the moment the operator scrolls up. Re-arm
   automatically if they scroll back to the bottom (within a small
   threshold so a few pixels of slop don't keep it paused).
*/
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

function jumpToFirstError() {
    const idx = firstErrorIdx.value
    if (idx < 0) return
    const el = lineEls.get(idx)
    if (!el) return
    el.scrollIntoView({ block: 'center', behavior: 'smooth' })
    // Brief flash so the eye lands on the line even when several
    // stderr lines are clumped together at the bottom of a long log.
    highlightedIdx.value = idx
    setTimeout(() => {
        if (highlightedIdx.value === idx) highlightedIdx.value = null
    }, 1600)
}

// Exposed to parents — DeploymentDetailView calls this from the
// "Ver no log" button inside its error banner so the operator can
// jump to the failure line in the terminal without re-scrolling.
defineExpose({ jumpToFirstError })

async function copyAll() {
    try {
        await navigator.clipboard.writeText(buildText())
        notify.success(t('terminal.copied'))
    } catch {
        notify.error(t('terminal.copyFailed'))
    }
}

function download() {
    const blob = new Blob([buildText()], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = props.downloadName
    a.click()
    URL.revokeObjectURL(url)
}

function buildText(): string {
    return props.lines.map((l) => l.line).join('\n')
}
</script>

<style scoped>
.deploy-terminal {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    background: color-mix(in srgb, var(--p-surface-900, #050912), transparent 0%);
    overflow: hidden;
    min-height: 360px;
    /* Default cap. Parent can override with a CSS rule for full-bleed. */
    max-height: 60vh;
}

.deploy-terminal__bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 8px 12px;
    border-bottom: 1px solid var(--p-content-border);
    background: color-mix(in srgb, var(--p-text-muted), transparent 94%);
    font-size: 12px;
}
.deploy-terminal__counts {
    display: inline-flex;
    align-items: center;
    gap: 12px;
    color: var(--p-text-muted);
    font-variant-numeric: tabular-nums;
}
.deploy-terminal__err {
    color: var(--p-red-400, #f87171);
    font-weight: 600;
}
.deploy-terminal__actions {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;
}
.deploy-terminal__btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 5px 9px;
    border: 1px solid transparent;
    background: transparent;
    color: var(--p-text-muted);
    border-radius: 6px;
    font: inherit;
    font-size: 11px;
    cursor: pointer;
    transition: background 100ms ease, color 100ms ease, border-color 100ms ease;
}
.deploy-terminal__btn:hover {
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    color: var(--p-text);
}
.deploy-terminal__btn.is-on {
    background: color-mix(in srgb, var(--p-primary-500), transparent 88%);
    color: var(--p-primary-500);
    border-color: color-mix(in srgb, var(--p-primary-500), transparent 70%);
}
/* Variant: alert. Drives the "Ir para o erro" button so it stands
   out among the chrome toggles. */
.deploy-terminal__btn--alert {
    background: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 88%);
    color: var(--p-red-400, #f87171);
    border-color: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 60%);
}
.deploy-terminal__btn--alert:hover {
    background: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 78%);
    color: var(--p-red-300, #fca5a5);
}

.deploy-terminal__body {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding: 12px 14px;
    background: color-mix(in srgb, var(--p-surface-900, #04070c), transparent 0%);
}
.deploy-terminal__lines {
    font-family: ui-monospace, "JetBrains Mono", Menlo, monospace;
    font-size: 12px;
    line-height: 1.55;
    /* Match the legacy <pre> rendering. */
    white-space: pre;
    color: var(--p-text);
}
.deploy-terminal.is-wrap .deploy-terminal__lines {
    white-space: pre-wrap;
    word-break: break-word;
}

.ln {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    padding: 0 2px;
    border-radius: 3px;
    transition: background 200ms ease;
}
.ln__num {
    flex-shrink: 0;
    min-width: 36px;
    color: var(--p-text-muted);
    opacity: 0.65;
    text-align: right;
    user-select: none;
    font-variant-numeric: tabular-nums;
}
.ln__text {
    /* Allow wrapping when the parent enables it. */
    flex: 1;
    min-width: 0;
}
.ln--stderr .ln__text {
    color: var(--p-red-400, #f87171);
}
.ln--stderr .ln__num {
    color: var(--p-red-400, #f87171);
    opacity: 0.85;
}
/* Brief flash when we jump to an error so the operator can locate
   it among clumped stderr lines. */
.ln--flash {
    background: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 75%);
    animation: deploy-terminal-flash 1.6s ease-out;
}
@keyframes deploy-terminal-flash {
    0%   { background: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 55%); }
    100% { background: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 92%); }
}

.deploy-terminal__empty {
    color: var(--p-text-muted);
    font-style: italic;
    padding: 8px 0;
}

/* ─────────── ANSI colours ───────────────────────────────────────
   Driven by `utils/ansi.ts`. Class names mirror the SGR semantics —
   `ansi-fg-red`, `ansi-bg-yellow`, `ansi-bold`, etc. Hex values come
   from the standard xterm palette so the colours read the way the
   operator expects when comparing to their local terminal.

   `!important` on stderr override: the per-line `.ln--stderr` rule
   above paints every span red. When a build log mixes ANSI colours
   into stderr, we let ANSI win because the upstream tool chose
   that colour deliberately (e.g. composer painting WARNING amber).
*/
.ln--stderr .ln__text :where(.ansi-fg-black,.ansi-fg-red,.ansi-fg-green,.ansi-fg-yellow,.ansi-fg-blue,.ansi-fg-magenta,.ansi-fg-cyan,.ansi-fg-white,.ansi-fg-black-bright,.ansi-fg-red-bright,.ansi-fg-green-bright,.ansi-fg-yellow-bright,.ansi-fg-blue-bright,.ansi-fg-magenta-bright,.ansi-fg-cyan-bright,.ansi-fg-white-bright) {
    color: inherit;
}

.ansi-fg-black           { color: #1e1e1e; }
.ansi-fg-red             { color: #cd3131; }
.ansi-fg-green           { color: #0dbc79; }
.ansi-fg-yellow          { color: #e5e510; }
.ansi-fg-blue            { color: #2472c8; }
.ansi-fg-magenta         { color: #bc3fbc; }
.ansi-fg-cyan            { color: #11a8cd; }
.ansi-fg-white           { color: #e5e5e5; }
.ansi-fg-black-bright    { color: #666666; }
.ansi-fg-red-bright      { color: #f14c4c; }
.ansi-fg-green-bright    { color: #23d18b; }
.ansi-fg-yellow-bright   { color: #f5f543; }
.ansi-fg-blue-bright     { color: #3b8eea; }
.ansi-fg-magenta-bright  { color: #d670d6; }
.ansi-fg-cyan-bright     { color: #29b8db; }
.ansi-fg-white-bright    { color: #ffffff; }

.ansi-bg-black           { background-color: #1e1e1e; }
.ansi-bg-red             { background-color: #cd3131; }
.ansi-bg-green           { background-color: #0dbc79; }
.ansi-bg-yellow          { background-color: #e5e510; }
.ansi-bg-blue            { background-color: #2472c8; }
.ansi-bg-magenta         { background-color: #bc3fbc; }
.ansi-bg-cyan            { background-color: #11a8cd; }
.ansi-bg-white           { background-color: #e5e5e5; }
.ansi-bg-black-bright    { background-color: #666666; }
.ansi-bg-red-bright      { background-color: #f14c4c; }
.ansi-bg-green-bright    { background-color: #23d18b; }
.ansi-bg-yellow-bright   { background-color: #f5f543; }
.ansi-bg-blue-bright     { background-color: #3b8eea; }
.ansi-bg-magenta-bright  { background-color: #d670d6; }
.ansi-bg-cyan-bright     { background-color: #29b8db; }
.ansi-bg-white-bright    { background-color: #ffffff; }

.ansi-bold      { font-weight: 700; }
.ansi-dim       { opacity: 0.65; }
.ansi-italic    { font-style: italic; }
.ansi-underline { text-decoration: underline; }
</style>
