<template>
    <div class="logs-view">
        <RouterLink :to="`/apps/${id}`" class="back-link">
            <IconChevronLeft :size="14" />
            {{ t('logs.backToApp') }}
        </RouterLink>

        <div class="header">
            <h1 class="title">{{ t('logs.titleFor', { app: appName }) }}</h1>
            <!-- Hide the picker when there's nothing to pick: single-container
                 apps, no containers yet (pre-deploy), or while we're still
                 fetching the list. UX matches the pre-Compose behavior. -->
            <div v-if="selectableContainers.length > 1" class="container-picker">
                <label for="container-select">{{ t('logs.container') }}</label>
                <select
                    id="container-select"
                    v-model="selectedContainer"
                    class="select"
                >
                    <option
                        v-for="c in selectableContainers"
                        :key="c.container_name || c.service"
                        :value="c.container_name || ''"
                        :disabled="!c.container_name"
                    >
                        {{ c.service }}{{ c.container_name ? '' : ` (${t('logs.notStarted')})` }}
                    </option>
                </select>
            </div>
        </div>

        <div v-if="truncated" class="truncated">
            {{ t('logs.truncated', { n: MAX_LINES }) }}
        </div>
        <pre class="terminal">{{ output }}</pre>
    </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import IconChevronLeft from '@/components/icons/IconChevronLeft.vue'
import { useAppsStore } from '@/stores/apps'
import { useSSE } from '@/composables/useSSE'
import {
    fetchContainers,
    streamAppLogsURL,
    streamContainerLogsURL,
    type AppContainer,
} from '@/services/apps'

const { t } = useI18n()
const route = useRoute()
const appsStore = useAppsStore()
const id = computed(() => route.params.id as string)
const appName = ref('')

/*
 * Ring buffer (array of lines + cap). The previous implementation
 * used string concatenation (`output += line + '\n'`) which scales
 * O(n^2) and freezes the tab when a chatty container fills the
 * buffer. We keep the structured array and render via join(); when
 * the cap is hit we trim oldest lines and surface a banner so the
 * operator knows there's older content not on screen.
 */
const MAX_LINES = 10_000
const buffer = ref<string[]>([])
const truncated = ref(false)
const output = computed(() => {
    if (buffer.value.length === 0) return t('logs.waiting')
    return buffer.value.join('\n')
})

function pushLine(line: string) {
    buffer.value.push(line)
    if (buffer.value.length > MAX_LINES) {
        buffer.value.splice(0, buffer.value.length - MAX_LINES)
        truncated.value = true
    }
}

const containers = ref<AppContainer[]>([])
// Empty string = "use the legacy /apps/:id/logs endpoint" (single-container
// fallback when the app hasn't deployed yet, or when the /containers list
// failed to load). Concrete name = use the per-container endpoint.
const selectedContainer = ref<string>('')

// We only render rows that have a live container_name OR were synthesized
// from the Compose preview (so operators see "not started yet" entries
// instead of an empty dropdown). Preview-only rows are disabled in the
// <select> since there's no container to stream from.
const selectableContainers = computed(() => containers.value)

// SSE URL is reactive: switching the picker tears down the old stream
// and starts a new one automatically (useSSE watches the ref).
const sseURL = computed(() => {
    if (selectedContainer.value) {
        return streamContainerLogsURL(id.value, selectedContainer.value)
    }
    return streamAppLogsURL(id.value)
})

onMounted(async () => {
    try {
        const a = await appsStore.fetchOne(id.value)
        appName.value = a.name
    } catch { /* ignore */ }

    try {
        const list = await fetchContainers(id.value)
        containers.value = list
        // Default selection = first container with a live name. Falls
        // back to "" (legacy endpoint) when nothing is running yet.
        const firstLive = list.find((c) => !!c.container_name)
        if (firstLive?.container_name) {
            selectedContainer.value = firstLive.container_name
        }
    } catch {
        // No containers endpoint / no permission / no server: silently
        // fall back to legacy behavior (single-container assumption).
    }
})

const sse = useSSE<{ message?: string; line?: string }>(sseURL, {
    onEvent: (ev) => {
        const line = ev.data?.message ?? ev.data?.line ?? JSON.stringify(ev.data)
        pushLine(line)
    },
})

// useSSE reads the URL ref lazily on (re)connect but doesn't watch it.
// When the operator changes the picker we: clear the buffer (otherwise
// lines from the previous container get mixed in), close the in-flight
// stream, and reopen against the new URL.
watch(selectedContainer, () => {
    buffer.value = []
    truncated.value = false
    sse.close()
    sse.open()
})
</script>

<style scoped>
.logs-view { width: 100%; }
.back-link {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    color: var(--p-text-muted);
    text-decoration: none;
    margin-bottom: 16px;
}
.header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 16px;
    flex-wrap: wrap;
}
.title {
    font-family: var(--prexel-font-display);
    font-size: 22px;
    font-weight: 700;
    letter-spacing: -0.02em;
    margin: 0;
}
.container-picker {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    color: var(--p-text-muted);
}
.select {
    background: var(--p-surface, #fff);
    border: 1px solid var(--p-border, #e5e7eb);
    border-radius: 8px;
    padding: 6px 10px;
    font-size: 13px;
    color: var(--p-text, #111);
    min-width: 200px;
}
.truncated {
    margin: 0 0 8px;
    padding: 6px 10px;
    border-radius: 6px;
    background: color-mix(in srgb, var(--p-warning, #d97706), transparent 88%);
    color: var(--p-warning, #d97706);
    font-size: 11px;
    font-style: italic;
}
.terminal {
    background: #0a0a0f;
    color: #d4d4d8;
    border-radius: 12px;
    padding: 18px;
    font-family: ui-monospace, Menlo, monospace;
    font-size: 12px;
    line-height: 1.55;
    min-height: 500px;
    max-height: 70vh;
    overflow: auto;
    white-space: pre-wrap;
    word-break: break-all;
}
</style>
