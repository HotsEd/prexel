<template>
    <div class="apps-view">
        <header class="page-header">
            <div>
                <h1 class="page-header__title">{{ t('apps.title') }}</h1>
                <p class="page-header__sub">{{ t('apps.listSubHeader') }}</p>
            </div>
            <Button :label="t('apps.newApp')" icon="pi pi-plus" @click="router.push('/apps/new')" />
        </header>

        <section class="deploy-overview" :aria-label="t('apps.overview.aria')">
            <div class="deploy-overview__card">
                <span class="deploy-overview__label">{{ t('apps.overview.production') }}</span>
                <strong>{{ runningCount }}</strong>
                <small>{{ t('apps.overview.productionHint') }}</small>
            </div>
            <div class="deploy-overview__card">
                <span class="deploy-overview__label">{{ t('apps.overview.activeBuilds') }}</span>
                <strong>{{ activeDeployCount }}</strong>
                <small>{{ t('apps.overview.activeBuildsHint') }}</small>
            </div>
            <div class="deploy-overview__card">
                <span class="deploy-overview__label">{{ t('apps.overview.attention') }}</span>
                <strong>{{ attentionCount }}</strong>
                <small>{{ t('apps.overview.attentionHint') }}</small>
            </div>
        </section>

        <div class="projects-toolbar">
            <div class="project-search">
                <i class="pi pi-search" />
                <input v-model="query" type="search" :placeholder="t('apps.toolbar.searchPlaceholder')" />
            </div>
            <MultiSelect
                v-model="tagsFilter"
                :options="availableTagNames"
                :placeholder="t('apps.toolbar.filterTags')"
                :max-selected-labels="3"
                display="chip"
                filter
                class="projects-toolbar__tags"
                :show-toggle-all="false"
            />
            <Button
                v-if="tagsFilter.length > 0"
                text
                size="small"
                :label="t('apps.toolbar.clear')"
                icon="pi pi-times"
                @click="clearTagsFilter"
            />
            <span class="projects-toolbar__count">{{ t('apps.toolbar.count', { n: filteredApps.length }) }}</span>
        </div>

        <div v-if="appsStore.loading && appsStore.apps.length === 0" class="state-row">
            {{ t('apps.loading') }}
        </div>

        <EmptyState
            v-else-if="appsStore.apps.length === 0"
            :title="t('apps.emptyTitle')"
            :body="t('apps.emptyBody')"
        >
            <template #icon><IconBox :size="24" /></template>
            <template #actions>
                <Button :label="t('apps.newApp')" icon="pi pi-plus" @click="router.push('/apps/new')" />
            </template>
        </EmptyState>

        <div v-else-if="filteredApps.length === 0" class="pv-panel state-row">
            {{ t('apps.noMatch', { q: query }) }}
        </div>

        <!--
            Apps list — tabular layout.

            The card grid used to live here; tables are the better
            fit for a list that operators scan for state, not a
            marketing-grade gallery. Row pattern matches the rest of
            the product (RoutingTab, SecretsTable in EnvEditor):
            hairline header on a tinted background, hover row tint,
            wrapped inside `pv-panel` for the framed-surface look.

            Click on the row navigates to the app detail. Action
            buttons stopPropagation so they don't double-fire.
        -->
        <div v-else class="pv-panel apps-table-wrap">
            <table class="apps-table">
                <thead>
                    <tr>
                        <th class="apps-table__name">{{ t('apps.columns.project') }}</th>
                        <th class="apps-table__status">{{ t('apps.columns.status') }}</th>
                        <th class="apps-table__tags">{{ t('apps.columns.tags') }}</th>
                        <th class="apps-table__origin">{{ t('apps.columns.origin') }}</th>
                        <th class="apps-table__branch">{{ t('apps.columns.branch') }}</th>
                        <th class="apps-table__server">{{ t('apps.columns.server') }}</th>
                        <th class="apps-table__deploy">{{ t('apps.columns.lastDeploy') }}</th>
                        <th class="apps-table__act"></th>
                    </tr>
                </thead>
                <tbody>
                    <tr
                        v-for="app in filteredApps"
                        :key="app.id"
                        class="apps-row"
                        @click="goTo(app.id)"
                    >
                        <td class="apps-table__name">
                            <div class="apps-row__identity">
                                <div class="apps-row__glyph">{{ app.name.slice(0, 1).toUpperCase() }}</div>
                                <div class="apps-row__identity-text">
                                    <div class="apps-row__name">{{ app.name }}</div>
                                    <div class="apps-row__url">
                                        {{ primaryDomain(app.id) ?? t('apps.row.noDomain') }}
                                    </div>
                                </div>
                            </div>
                        </td>
                        <td class="apps-table__status">
                            <StatusBadge :status="app.status" />
                        </td>
                        <td class="apps-table__tags" @click.stop>
                            <div v-if="app.tags && app.tags.length > 0" class="apps-row__tags">
                                <button
                                    v-for="tag in visibleTags(app.tags)"
                                    :key="tag"
                                    type="button"
                                    class="tag-chip"
                                    :style="tagChipStyle(tag)"
                                    :title="t('apps.row.filterByTag', { tag })"
                                    @click="addTagFilter(tag)"
                                >
                                    {{ tag }}
                                </button>
                                <span v-if="app.tags.length > MAX_VISIBLE_TAGS" class="tag-chip tag-chip--more">
                                    +{{ app.tags.length - MAX_VISIBLE_TAGS }}
                                </span>
                            </div>
                            <span v-else class="apps-row__muted">—</span>
                        </td>
                        <td class="apps-table__origin">
                            <span class="apps-row__origin">
                                {{ app.repo_url ? shortRepo(app.repo_url) : dockerOrigin(app) }}
                            </span>
                        </td>
                        <td class="apps-table__branch">
                            <span class="branch-pill">{{ app.branch || 'main' }}</span>
                        </td>
                        <td class="apps-table__server">
                            <span class="apps-row__server">{{ serverName(app.server_id) }}</span>
                        </td>
                        <td class="apps-table__deploy">
                            <span :class="['apps-row__deploy', !app.last_deployed_at && 'apps-row__muted']">
                                {{ app.last_deployed_at ? formatRelative(app.last_deployed_at) : t('apps.row.never') }}
                            </span>
                        </td>
                        <td class="apps-table__act" @click.stop>
                            <div class="apps-row__actions">
                                <Button
                                    :label="t('apps.deploy')"
                                    size="small"
                                    severity="primary"
                                    @click="deploy(app.id)"
                                />
                                <Button
                                    text
                                    size="small"
                                    icon="pi pi-ellipsis-v"
                                    :aria-label="t('apps.logs')"
                                    @click="showMenu($event, app.id)"
                                />
                            </div>
                        </td>
                    </tr>
                </tbody>
            </table>
        </div>

        <Menu ref="menuRef" :model="menuItems" :popup="true" />

        <!--
            App creation moved to /apps/new (full page) — the dialog
            was cramped for the source picker + repo picker flow.
            AppsView keeps only the destructive-remove dialog below.
        -->

        <Dialog v-model:visible="removeOpen" modal :header="t('apps.removeDialog.header')" :style="{ width: '440px' }">
            <div class="danger-dialog">
                <div class="danger-dialog__icon"><i class="pi pi-exclamation-triangle" /></div>
                <div>
                    <strong>{{ t('apps.removeDialog.warningTitle') }}</strong>
                    <p>{{ t('apps.removeDialog.warningBody') }}</p>
                </div>
            </div>
            <template #footer>
                <Button :label="t('common.cancel')" text severity="secondary" @click="removeOpen = false" />
                <Button :label="t('apps.remove')" severity="danger" :loading="removing" @click="removeAppConfirmed" />
            </template>
        </Dialog>
    </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Menu from 'primevue/menu'
import MultiSelect from 'primevue/multiselect'
import { useAppsStore } from '@/stores/apps'
import { useServersStore } from '@/stores/servers'
import { useDomainsStore } from '@/stores/domains'
import { tagsService } from '@/services/tags'
import type { Tag } from '@/types/api'
import StatusBadge from '@/components/StatusBadge.vue'
import EmptyState from '@/components/EmptyState.vue'
import IconBox from '@/components/icons/IconBox.vue'
import { formatRelative } from '@/utils/format'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const appsStore = useAppsStore()
const serversStore = useServersStore()
const domainsStore = useDomainsStore()

// ── Tag filter state ────────────────────────────────────────────
// Caps the per-card chip count so wide tag sets don't push the
// status badge off the card; the overflow renders as "+N".
const MAX_VISIBLE_TAGS = 4
const tagsCatalog = ref<Tag[]>([])
// Seed from the URL once on mount; subsequent edits sync back so
// the back/forward buttons + refresh preserve the filter.
const tagsFilter = ref<string[]>(parseTagsQuery(route.query.tags))

// Local UI state — list filter + remove-confirm dialog. The whole
// creation flow lives in NewAppView (/apps/new) since the dialog was
// too cramped for the source-picker + repo-picker journey.
const removeOpen = ref(false)
const removing = ref(false)
const query = ref('')

const menuRef = ref<InstanceType<typeof Menu> | null>(null)
const menuTargetId = ref<string | null>(null)
const pendingRemoveId = ref<string | null>(null)
const menuItems = ref([
    { label: t('apps.logs'), icon: 'pi pi-list', command: () => menuTargetId.value && router.push(`/apps/${menuTargetId.value}/logs`) },
    { label: t('apps.rollback'), icon: 'pi pi-undo', command: () => menuTargetId.value && rollback(menuTargetId.value) },
    { separator: true },
    { label: t('apps.remove'), icon: 'pi pi-trash', command: () => menuTargetId.value && remove(menuTargetId.value) },
])

onMounted(async () => {
    // List view only needs apps + servers (for the per-card meta) +
    // domains (to surface the primary FQDN). Tags feed the filter
    // dropdown — best-effort: a 404 (older backend) leaves the
    // dropdown empty but still functional thanks to the
    // `availableTagNames` fallback to the union of app tags.
    try {
        await Promise.all([
            appsStore.fetchAll(),
            serversStore.fetchAll(),
            domainsStore.fetchAll(),
            tagsService.list().then((tags) => { tagsCatalog.value = tags }).catch(() => null),
        ])
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
})

const filteredApps = computed(() => {
    const q = query.value.trim().toLowerCase()
    const activeTags = tagsFilter.value
    return appsStore.apps.filter((app) => {
        if (q) {
            const hit = [
                app.name,
                app.repo_url ?? '',
                app.branch,
                app.status,
                primaryDomain(app.id) ?? '',
            ].some((value) => value.toLowerCase().includes(q))
            if (!hit) return false
        }
        if (activeTags.length > 0) {
            const appTags = app.tags ?? []
            // AND semantics: an app matches only if it carries every
            // selected tag (matches the verbal "filter by these tags").
            if (!activeTags.every((t) => appTags.includes(t))) return false
        }
        return true
    })
})

// Source for the MultiSelect: prefer the global dictionary so that
// tags with zero apps still appear. Fall back to the union of tags
// present on apps when the dictionary endpoint isn't available.
const availableTagNames = computed<string[]>(() => {
    if (tagsCatalog.value.length > 0) {
        return tagsCatalog.value.map((t) => t.name).sort((a, b) => a.localeCompare(b))
    }
    const seen = new Set<string>()
    for (const app of appsStore.apps) {
        for (const tag of app.tags ?? []) seen.add(tag)
    }
    return Array.from(seen).sort((a, b) => a.localeCompare(b))
})

function visibleTags(tags: string[]): string[] {
    return tags.slice(0, MAX_VISIBLE_TAGS)
}

// Color lookup: the global dictionary is the source of truth for
// per-tag color. If a tag is absent (filter falling back to the
// app union) or color is null, we render with a neutral default
// via CSS — that's why this can safely return null.
const tagColorByName = computed<Record<string, string | null>>(() =>
    Object.fromEntries(tagsCatalog.value.map((t) => [t.name, t.color ?? null])),
)

function tagChipStyle(tag: string): Record<string, string> {
    const color = tagColorByName.value[tag]
    if (!color) return {}
    // The same color drives both background (very transparent) and
    // foreground so the chip stays readable in both light and dark
    // themes without us hardcoding a palette.
    return {
        '--tag-color': color,
        background: `color-mix(in srgb, ${color}, transparent 82%)`,
        color: color,
        borderColor: `color-mix(in srgb, ${color}, transparent 60%)`,
    }
}

function addTagFilter(tag: string) {
    if (tagsFilter.value.includes(tag)) return
    tagsFilter.value = [...tagsFilter.value, tag]
}

function clearTagsFilter() {
    tagsFilter.value = []
}

function parseTagsQuery(raw: unknown): string[] {
    // Tags arrive as either ?tags=a,b or ?tags=a&tags=b — Vue
    // Router normalises the second form to string[]. We always
    // collapse to a deduped string[].
    if (Array.isArray(raw)) return Array.from(new Set(raw.filter((v): v is string => !!v)))
    if (typeof raw === 'string' && raw.length > 0) {
        return Array.from(new Set(raw.split(',').map((s) => s.trim()).filter(Boolean)))
    }
    return []
}

// Mirror the filter selection into the URL query so refresh + the
// browser back button preserve it. `router.replace` keeps history
// flat — filter tweaks shouldn't add new entries to "back".
watch(tagsFilter, (next) => {
    const current = parseTagsQuery(route.query.tags)
    const same = current.length === next.length && current.every((t, i) => t === next[i])
    if (same) return
    const query = { ...route.query }
    if (next.length === 0) delete query.tags
    else query.tags = next.join(',')
    void router.replace({ query })
})

const runningCount = computed(() => appsStore.apps.filter((app) => app.status === 'running').length)
const activeDeployCount = computed(() => appsStore.apps.filter((app) => app.status === 'building' || app.status === 'deploying').length)
const attentionCount = computed(() => appsStore.apps.filter((app) => app.status === 'error' || app.status === 'unreachable').length)

function goTo(id: string) { router.push(`/apps/${id}`) }

function shortRepo(url: string): string {
    return url.replace(/^https?:\/\//, '').replace(/\.git$/, '')
}

function primaryDomain(appId: string): string | null {
    return domainsStore.domains.find((domain) => domain.app_id === appId && domain.is_primary)?.name
        ?? domainsStore.domains.find((domain) => domain.app_id === appId)?.name
        ?? null
}

function serverName(serverId?: string | null): string {
    if (!serverId) return t('apps.row.noServer')
    return serversStore.servers.find((server) => server.id === serverId)?.name ?? serverId.slice(0, 8)
}

function dockerOrigin(app: { build_type: string; image_name?: string | null; image_tag?: string | null }): string {
    if (app.build_type === 'docker_image') return `${app.image_name ?? 'docker'}:${app.image_tag ?? 'latest'}`
    if (app.build_type === 'docker_compose') return t('apps.row.dockerCompose')
    return t('apps.row.dockerfile')
}

async function deploy(id: string) {
    try {
        const dep = await appsStore.deploy(id)
        notify.success(t('apps.deployStarted'))
        // Take the operator straight into the deployment-detail page —
        // they clicked "Deploy", they want to watch it happen, not stay
        // on a static dashboard.
        await router.push(`/apps/${id}/deployments/${dep.id}?watch=1`)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

async function rollback(id: string) {
    try {
        const dep = await appsStore.rollback(id)
        notify.success(t('apps.rollbackStarted'))
        await router.push(`/apps/${id}/deployments/${dep.id}?watch=1`)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

function remove(id: string) {
    pendingRemoveId.value = id
    removeOpen.value = true
}

async function removeAppConfirmed() {
    if (!pendingRemoveId.value) return
    removing.value = true
    try {
        await appsStore.remove(pendingRemoveId.value)
        removeOpen.value = false
        pendingRemoveId.value = null
        notify.success(t('apps.appRemoved'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        removing.value = false
    }
}

function showMenu(event: MouseEvent, id: string) {
    menuTargetId.value = id
    menuRef.value?.toggle(event)
}

// Creation flow lives in /apps/new (NewAppView.vue). Anything related
// to drafting an app — source picker, repo picker, build-type
// inference, submit payload assembly — moved there with the modal.
</script>

<style scoped>
.apps-view { width: 100%; }
.state-row { padding: 32px; color: var(--p-text-muted); }
.dialog-body { display: flex; flex-direction: column; gap: 12px; padding-top: 8px; }
:deep(.p-select), :deep(.p-inputtext) { width: 100%; }
.deploy-overview {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px;
    margin-bottom: 16px;
}
.deploy-overview__card {
    border: 1px solid var(--p-content-border);
    background: color-mix(in srgb, var(--p-content-bg), transparent 4%);
    border-radius: 12px;
    padding: 14px 16px;
}
.deploy-overview__label,
.app-card__label {
    display: block;
    color: var(--p-text-muted);
    font-size: 11px;
    font-weight: 650;
    letter-spacing: 0.04em;
    text-transform: uppercase;
}
.deploy-overview__card strong {
    display: block;
    margin-top: 6px;
    font-size: 26px;
    line-height: 1;
    color: var(--p-text);
}
.deploy-overview__card small {
    display: block;
    margin-top: 6px;
    color: var(--p-text-muted);
}
.projects-toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 16px;
    flex-wrap: wrap;
}
.projects-toolbar__tags {
    min-width: 220px;
    max-width: 360px;
}
.project-search {
    flex: 1;
    min-width: 240px;
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 44px;
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    background: var(--p-content-bg);
    padding: 0 12px;
    color: var(--p-text-muted);
}
.project-search input {
    flex: 1;
    border: 0;
    outline: 0;
    background: transparent;
    color: var(--p-text);
    font: inherit;
}
.project-search input::placeholder { color: var(--p-text-muted); }
.projects-toolbar__count {
    color: var(--p-text-muted);
    font-size: 12px;
    white-space: nowrap;
}
/* ── Apps table ──────────────────────────────────────────────────
   Wraps in `pv-panel` for the framed-surface look the rest of the
   product uses. The table itself has hairline borders between rows
   only — no vertical column borders — and a tinted header so it
   reads as separate from the data without losing the calm-flat
   tone. */
.apps-table-wrap {
    padding: 0;
    overflow-x: auto;
}
.apps-table {
    width: 100%;
    border-collapse: collapse;
    /* Ensure cells render edge-to-edge inside the rounded panel. */
    border-radius: inherit;
}
.apps-table thead th {
    text-align: left;
    padding: 11px 14px;
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--p-text-muted);
    background: color-mix(in srgb, var(--p-text-muted), transparent 92%);
    border-bottom: 1px solid var(--p-content-border);
    white-space: nowrap;
}
.apps-table tbody td {
    padding: 12px 14px;
    font-size: 13px;
    color: var(--p-text);
    border-bottom: 1px solid color-mix(in srgb, var(--p-content-border), transparent 40%);
    vertical-align: middle;
}
.apps-table tbody tr:last-child td { border-bottom: none; }
.apps-row { cursor: pointer; transition: background 120ms ease; }
.apps-row:hover td {
    background: color-mix(in srgb, var(--p-primary-500), transparent 94%);
}

/* Column sizing — most columns hug content; project + tags get the
   stretch so they take the remaining width as the viewport grows. */
.apps-table__name   { width: 28%; min-width: 220px; }
.apps-table__status { width: 110px; }
.apps-table__tags   { width: 18%; min-width: 140px; }
.apps-table__origin { width: 18%; min-width: 160px; }
.apps-table__branch { width: 100px; }
.apps-table__server { width: 130px; }
.apps-table__deploy { width: 130px; white-space: nowrap; }
.apps-table__act    { width: 130px; text-align: right; }

/* Identity cell — glyph + name + primary domain stacked. */
.apps-row__identity {
    display: flex;
    align-items: center;
    gap: 12px;
    min-width: 0;
}
.apps-row__identity-text { min-width: 0; }
.apps-row__glyph {
    width: 32px;
    height: 32px;
    border-radius: 8px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: linear-gradient(135deg, rgba(16, 185, 129, 0.18), rgba(56, 189, 248, 0.16));
    color: var(--p-text);
    font-weight: 800;
    font-size: 13px;
    border: 1px solid rgba(148, 163, 184, 0.18);
    flex-shrink: 0;
}
.apps-row__name {
    font-weight: 600;
    color: var(--p-text);
    line-height: 1.2;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.apps-row__url {
    margin-top: 2px;
    color: var(--p-text-muted);
    font-size: 12px;
    font-family: ui-monospace, Menlo, monospace;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.apps-row__origin,
.apps-row__server,
.apps-row__deploy {
    color: var(--p-text);
    font-size: 13px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.apps-row__muted { color: var(--p-text-muted); }

.apps-row__tags {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
}

.apps-row__actions {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    justify-content: flex-end;
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
.tag-chip {
    display: inline-flex;
    align-items: center;
    border: 1px solid var(--p-content-border);
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    color: var(--p-text);
    border-radius: 999px;
    padding: 2px 9px;
    font-size: 11px;
    font-weight: 600;
    line-height: 1.4;
    cursor: pointer;
    transition: filter 0.12s ease, transform 0.12s ease;
    font-family: inherit;
}
.tag-chip:hover { filter: brightness(1.1); transform: translateY(-1px); }
.tag-chip--more {
    cursor: default;
    color: var(--p-text-muted);
    background: transparent;
}
.tag-chip--more:hover { filter: none; transform: none; }
/*
    Everything from `.create-app*` to `.repo-picker*` used to live here
    and styled the in-modal creation flow. Moved to NewAppView (full
    page at /apps/new) — kept here is only what AppsView still needs:
    the destructive-remove dialog (`.danger-dialog*`) and the list grid.
*/
.danger-dialog {
    display: flex;
    gap: 12px;
    color: var(--p-text);
}
.danger-dialog__icon {
    display: grid;
    place-items: center;
    flex: 0 0 38px;
    width: 38px;
    height: 38px;
    border-radius: 10px;
    background: color-mix(in srgb, var(--p-red-500), transparent 88%);
    color: var(--p-red-400);
}
.danger-dialog strong {
    display: block;
    margin-bottom: 4px;
}
.danger-dialog p {
    margin: 0;
    color: var(--p-text-muted);
}
/* Footer divider for the remove-confirm dialog header. */
:deep(.p-dialog-footer) {
    border-top: 1px solid var(--p-divider);
    padding: 14px 24px;
}
@media (max-width: 780px) {
    .deploy-overview { grid-template-columns: 1fr; }
    .projects-toolbar { align-items: stretch; flex-direction: column; }
    /* On narrow viewports the table scrolls horizontally inside the
       wrapper — better than reflowing to a card layout, which would
       defeat the whole point of switching to a table. */
    .apps-table { min-width: 920px; }
}
</style>
