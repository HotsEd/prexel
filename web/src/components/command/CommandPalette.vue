<template>
    <Dialog
        v-model:visible="show"
        modal
        :showHeader="false"
        :closeOnEscape="true"
        :pt="dialogPt"
        @show="onShow"
    >
        <div class="cmdp">
            <header class="cmdp__head">
                <IconSearch :size="16" :stroke-width="1.75" class="cmdp__head-icon" />
                <input
                    ref="inputRef"
                    v-model="query"
                    type="text"
                    class="cmdp__input"
                    :placeholder="t('commandPalette.searchPlaceholder')"
                    @keydown.down.prevent="moveActive(1)"
                    @keydown.up.prevent="moveActive(-1)"
                    @keydown.enter.prevent="selectActive"
                    @keydown.esc="close"
                />
                <kbd class="cmdp__kbd">esc</kbd>
            </header>

            <div class="cmdp__body">
                <template v-if="flatItems.length === 0">
                    <div class="cmdp__empty">
                        <div class="cmdp__empty-title">{{ t('commandPalette.emptyTitle') }}</div>
                        <div class="cmdp__empty-hint">{{ t('commandPalette.emptyHint') }}</div>
                    </div>
                </template>
                <template v-else>
                    <section v-for="g in groups" :key="g.key" v-show="g.items.length" class="cmdp__group">
                        <div class="cmdp__group-label">{{ g.label }}</div>
                        <button
                            v-for="item in g.items"
                            :key="item.id"
                            type="button"
                            :class="['cmdp__item', activeId === item.id && 'is-active']"
                            @mouseenter="activeId = item.id"
                            @click="select(item)"
                        >
                            <component :is="item.icon" :size="16" :stroke-width="1.8" class="cmdp__item-icon" />
                            <div class="cmdp__item-main">
                                <div class="cmdp__item-title">{{ item.title }}</div>
                            </div>
                        </button>
                    </section>
                </template>
            </div>

            <footer class="cmdp__foot">
                <span><kbd>↑</kbd><kbd>↓</kbd> {{ t('commandPalette.hint.navigate') }}</span>
                <span><kbd>↵</kbd> {{ t('commandPalette.hint.select') }}</span>
                <span><kbd>esc</kbd> {{ t('commandPalette.hint.close') }}</span>
            </footer>
        </div>
    </Dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch, type Component } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Dialog from 'primevue/dialog'
import IconSearch from '@/components/icons/IconSearch.vue'
import IconBox from '@/components/icons/IconBox.vue'
import IconServer from '@/components/icons/IconServer.vue'
import IconGlobe from '@/components/icons/IconGlobe.vue'
import IconSettings from '@/components/icons/IconSettings.vue'
import IconPlus from '@/components/icons/IconPlus.vue'
import { useCommandPalette } from '@/composables/useCommandPalette'

interface CommandItem {
    id: string
    icon: Component
    title: string
    action: () => void
}

interface CommandGroup {
    key: 'actions' | 'pages'
    label: string
    items: CommandItem[]
}

const { t } = useI18n()
const router = useRouter()
const palette = useCommandPalette()

const show = computed({
    get: () => palette.isOpen.value,
    set: (v) => { palette.isOpen.value = v },
})

const dialogPt = {
    root: { style: 'border-radius: 14px; overflow: hidden; max-width: 640px; width: 92vw; margin-top: -25vh;' },
    content: { style: 'padding: 0;' },
}

const query = ref('')
const inputRef = ref<HTMLInputElement | null>(null)
const activeId = ref<string | null>(null)

const PAGES = [
    { key: 'apps', icon: IconBox, route: '/apps' },
    { key: 'servers', icon: IconServer, route: '/servers' },
    { key: 'domains', icon: IconGlobe, route: '/domains' },
    { key: 'settings', icon: IconSettings, route: '/settings' },
]
const ACTIONS = [
    { key: 'newApp', icon: IconPlus, route: '/apps?new=1' },
    { key: 'newServer', icon: IconPlus, route: '/servers?new=1' },
]

function matches(text: string, q: string): boolean {
    return text.toLowerCase().includes(q.toLowerCase())
}

const groups = computed<CommandGroup[]>(() => {
    const q = query.value.trim()
    const actionItems: CommandItem[] = ACTIONS
        .filter((a) => !q || matches(t(`commandPalette.actions.${a.key}`), q))
        .map((a) => ({
            id: `action:${a.key}`,
            icon: a.icon,
            title: t(`commandPalette.actions.${a.key}`),
            action: () => router.push(a.route),
        }))
    const pageItems: CommandItem[] = PAGES
        .filter((p) => !q || matches(t(`nav.${p.key}`), q))
        .map((p) => ({
            id: `page:${p.key}`,
            icon: p.icon,
            title: t(`nav.${p.key}`),
            action: () => router.push(p.route),
        }))

    return [
        { key: 'actions', label: t('commandPalette.groups.actions'), items: actionItems },
        { key: 'pages', label: t('commandPalette.groups.pages'), items: pageItems },
    ]
})

const flatItems = computed<CommandItem[]>(() => groups.value.flatMap((g) => g.items))

watch(flatItems, (items) => {
    if (!items.length) { activeId.value = null; return }
    if (!activeId.value || !items.some((i) => i.id === activeId.value)) {
        activeId.value = items[0]!.id
    }
})

function moveActive(delta: number) {
    const items = flatItems.value
    if (!items.length) return
    const idx = items.findIndex((i) => i.id === activeId.value)
    const next = (idx + delta + items.length) % items.length
    activeId.value = items[next]!.id
}

function selectActive() {
    const item = flatItems.value.find((i) => i.id === activeId.value)
    if (item) select(item)
}

function select(item: CommandItem) {
    item.action()
    close()
}

function close() {
    show.value = false
}

function onShow() {
    query.value = ''
    nextTick(() => inputRef.value?.focus())
}
</script>

<style scoped>
.cmdp {
    display: flex;
    flex-direction: column;
    max-height: 70vh;
}
.cmdp__head {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--p-divider);
}
.cmdp__head-icon { color: var(--p-text-muted); }
.cmdp__input {
    flex: 1;
    border: 0;
    outline: 0;
    background: transparent;
    font: inherit;
    font-size: 15px;
    color: var(--p-text);
}
.cmdp__input::placeholder { color: var(--p-text-muted); }
.cmdp__kbd,
.cmdp__foot kbd {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
    color: var(--p-text-muted);
    background: var(--p-hover);
    padding: 2px 6px;
    border-radius: 4px;
    border: 1px solid var(--p-content-border);
}
.cmdp__body { overflow-y: auto; padding: 8px 0; flex: 1; min-height: 200px; }
.cmdp__group { padding: 4px 0; }
.cmdp__group-label {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--p-text-muted);
    padding: 6px 16px;
}
.cmdp__item {
    display: flex;
    align-items: center;
    gap: 12px;
    width: 100%;
    padding: 8px 16px;
    border: 0;
    background: transparent;
    cursor: pointer;
    font-family: inherit;
    text-align: left;
    color: var(--p-text);
}
.cmdp__item.is-active { background: var(--p-hover); }
.cmdp__item-icon { color: var(--p-text-muted); flex-shrink: 0; }
.cmdp__item.is-active .cmdp__item-icon { color: var(--p-primary-500); }
.cmdp__item-main { flex: 1; min-width: 0; }
.cmdp__item-title {
    font-size: 13px;
    font-weight: 500;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}
.cmdp__empty { padding: 36px 16px; text-align: center; color: var(--p-text-muted); }
.cmdp__empty-title { font-size: 14px; font-weight: 600; color: var(--p-text); margin-bottom: 4px; }
.cmdp__empty-hint { font-size: 12px; line-height: 1.5; }
.cmdp__foot {
    display: flex;
    gap: 18px;
    padding: 10px 16px;
    border-top: 1px solid var(--p-divider);
    font-size: 11px;
    color: var(--p-text-muted);
    flex-wrap: wrap;
}
.cmdp__foot span { display: inline-flex; align-items: center; gap: 6px; }
</style>
