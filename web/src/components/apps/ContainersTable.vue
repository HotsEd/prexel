<template>
    <div class="containers">
        <header class="containers__head">
            <div>
                <h2 class="containers__title">{{ t('containersTable.title') }}</h2>
                <p class="containers__sub">
                    {{ containers.length === 0 ? t('containersTable.none') : t('containersTable.sub', { n: containers.length }) }}
                </p>
            </div>
            <Button
                v-if="!loading"
                size="small"
                text
                icon="pi pi-refresh"
                :aria-label="t('containersTable.refresh')"
                :title="t('containersTable.refresh')"
                @click="refresh"
            />
        </header>

        <!-- ─────────── Loading / empty ─────────── -->
        <div v-if="loading && containers.length === 0" class="containers__state muted">
            {{ t('containersTable.loading') }}
        </div>
        <div v-else-if="containers.length === 0" class="containers__state muted">
            {{ t('containersTable.emptyHint') }}
        </div>

        <!-- ─────────── Table ─────────── -->
        <!--
            Rows are clickable — navigate to the dedicated container
            detail page (/apps/:id/containers/:name) where logs,
            terminal, stats, env, mounts, and labels live together.
            Inline action buttons used to crowd this row; they're
            gone now because the destination page does all of that
            with full surface area.

            The one inline action we KEEP is "Domínio" — it's the
            row's relationship to the domains list, which is what
            the table is fundamentally about (per-service routing).

            Wrapper uses the system `pv-panel` class so the bg /
            border / radius come from the same tokens the apps
            table (AppsView) uses. Padding 0 lets the table fill
            the frame edge-to-edge.
        -->
        <div v-else class="pv-panel ct-wrap">
        <table class="ct">
            <thead>
                <tr>
                    <th>{{ t('containersTable.columns.service') }}</th>
                    <th>{{ t('containersTable.columns.image') }}</th>
                    <th>{{ t('containersTable.columns.state') }}</th>
                    <th>{{ t('containersTable.columns.ports') }}</th>
                    <th>{{ t('containersTable.columns.domain') }}</th>
                    <th class="ct__actions-col"></th>
                </tr>
            </thead>
            <tbody>
                <tr
                    v-for="row in containers"
                    :key="row.service || row.container_name"
                    :class="['ct__row', rowNavigationTarget(row) && 'ct__row--link']"
                    @click="onRowClick(row)"
                >
                    <td>
                        <div class="ct__service">
                            <strong>{{ row.service || row.container_name }}</strong>
                            <small v-if="row.container_name && row.container_name !== row.service" class="muted mono">
                                {{ row.container_name }}
                            </small>
                        </div>
                    </td>
                    <td class="mono ct__image">{{ row.image || '—' }}</td>
                    <td>
                        <!--
                            System StatusBadge — same pill style used
                            everywhere else (apps list, deployments,
                            servers). i18n keys for the container-
                            specific states live in `status.*`.
                        -->
                        <StatusBadge v-if="row.state" :status="row.state" />
                        <span v-else class="muted">—</span>
                    </td>
                    <td class="ct__ports">
                        <!--
                            Wrap chips in an inner div: applying
                            `display: flex` to a <td> breaks the
                            row's table-cell alignment (the flex cell
                            stops respecting `vertical-align: middle`
                            and the row inflates to its tallest cell).
                            Inner div = flex layout for the chips,
                            outer td stays a normal table cell.
                        -->
                        <div v-if="row.ports.length" class="ct__ports-list">
                            <code v-for="p in row.ports" :key="p" class="port">{{ p }}</code>
                        </div>
                        <span v-else class="muted">—</span>
                    </td>
                    <td class="ct__domains" @click.stop>
                        <!--
                            Per-service domain bindings live on the
                            domain row (domain.service + domain.port).
                            We render every domain currently routed
                            here. When `row.service` is empty (orphan
                            container) we just show "—" since we can't
                            match anything to it.

                            click.stop on the <td> stops the row-level
                            navigation handler so clicking a domain
                            link doesn't whisk the operator away from
                            their tab.
                        -->
                        <template v-if="row.service">
                            <div v-if="domainsForService(row.service).length" class="ct__domains-list">
                                <div v-for="d in domainsForService(row.service)" :key="d.id" class="ct__domain">
                                    <a :href="`https://${d.name}`" target="_blank" rel="noopener" class="mono">
                                        {{ d.name }}
                                        <i class="pi pi-external-link" aria-hidden="true" />
                                    </a>
                                    <Button
                                        text
                                        size="small"
                                        icon="pi pi-times"
                                        :aria-label="t('containersTable.removeDomainAria')"
                                        :title="t('containersTable.removeDomainTitle')"
                                        @click="confirmUnbind(d)"
                                    />
                                </div>
                            </div>
                            <span v-else class="muted">{{ t('containersTable.notExposed') }}</span>
                        </template>
                        <span v-else class="muted">—</span>
                    </td>
                    <td class="ct__actions" @click.stop>
                        <div class="ct__actions-row">
                            <Button
                                v-if="row.service"
                                size="small"
                                severity="secondary"
                                outlined
                                icon="pi pi-link"
                                :label="t('containersTable.domainAction')"
                                @click="openBindDialog(row)"
                            />
                            <i v-if="rowNavigationTarget(row)" class="pi pi-chevron-right ct__chevron" aria-hidden="true" />
                        </div>
                    </td>
                </tr>
            </tbody>
        </table>
        </div>

        <!-- ─────────── Bind domain dialog ─────────── -->
        <Dialog
            v-model:visible="bindOpen"
            modal
            :header="t('containersTable.bindDialog.header', { service: bindRow?.service ?? '' })"
            :style="{ width: '520px' }"
            :closable="!binding"
        >
            <div v-if="bindRow" class="bind-dialog">
                <p class="muted">
                    {{ t('containersTable.bindDialog.intro', { service: bindRow.service }) }}
                </p>

                <SField :label="t('containersTable.bindDialog.domainLabel')">
                    <DomainPicker
                        v-model="bindSelectedId"
                        :placeholder="bindableDomains.length ? t('containersTable.bindDialog.domainPlaceholder') : t('containersTable.bindDialog.domainNone')"
                        :disabled="bindableDomains.length === 0"
                    />
                </SField>

                <SField :label="t('containersTable.bindDialog.portLabel')" :hint="t('containersTable.bindDialog.portHint')">
                    <InputText
                        v-model="bindPortStr"
                        type="number"
                        min="1"
                        max="65535"
                        :placeholder="bindRow.ports[0]?.toString() ?? '80'"
                    />
                </SField>

                <p v-if="bindableDomains.length === 0" class="muted">
                    <RouterLink :to="`/domains/new?app=${appId}`" class="link">
                        {{ t('containersTable.bindDialog.registerNew') }}
                    </RouterLink>
                </p>
            </div>
            <template #footer>
                <Button text severity="secondary" :label="t('common.cancel')" :disabled="binding" @click="bindOpen = false" />
                <Button
                    :label="t('apps.detail.domains.link_dialog.submit')"
                    :loading="binding"
                    :disabled="!bindSelectedId || !bindPortValid"
                    @click="submitBind"
                />
            </template>
        </Dialog>
    </div>
</template>

<script setup lang="ts">
/*
    ContainersTable — the AppDetail "Containers" tab body.

    Folds two data sources into one row-per-service table:
      - live containers from Docker (label `prexel.app_id=<id>`)
      - Compose YAML services (preview, even pre-deploy)

    Backend's GET /apps/:id/containers does the merge; this component
    just renders + lets the operator bind a domain per service. The
    bind dialog issues PATCH /domains/:id with `service` + `port`,
    which the backend rewires Caddy for immediately.

    Logs and Terminal buttons emit events; the parent (AppDetailView)
    owns the modal/route for those since they have lifecycle of
    their own (xterm session, SSE stream).
*/
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import SField from '@/components/settings/SField.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import DomainPicker from '@/components/domains/DomainPicker.vue'
import { useAppsStore } from '@/stores/apps'
import { useDomainsStore } from '@/stores/domains'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { AppContainer, Domain } from '@/types/api'

const props = defineProps<{
    appId: string
}>()

const { t } = useI18n()
const router = useRouter()

/*
   Row → container-detail navigation target. We prefer the live
   container_name (real Docker name); fall back to the service name
   for preview rows so the backend can synthesise a "preview" detail
   from the Compose YAML. Rows without either are not clickable —
   they're orphan-row placeholders the table renders defensively.
*/
function rowNavigationTarget(row: AppContainer): string | null {
    return row.container_name || row.service || null
}

function onRowClick(row: AppContainer) {
    const name = rowNavigationTarget(row)
    if (!name) return
    void router.push(`/apps/${props.appId}/containers/${encodeURIComponent(name)}`)
}

const appsStore = useAppsStore()
const domainsStore = useDomainsStore()

const containers = ref<AppContainer[]>([])
const loading = ref(true)

async function refresh() {
    loading.value = true
    try {
        containers.value = await appsStore.fetchContainers(props.appId)
        // Refetch domains too — bindings could have changed
        // server-side (background reconcile, another tab, etc.).
        await domainsStore.fetchAll()
    } finally {
        loading.value = false
    }
}

onMounted(refresh)

// Re-fetch when the appId changes (single component instance is
// reused when navigating between apps via the side rail).
watch(() => props.appId, () => { void refresh() })

// ─── Domain binding ──────────────────────────────────────────────
// `domain.service` matches `container.service`. Single-container
// apps store domain.service = null and we don't render those here
// (the AppDetail Domínios tab handles them generically).
function domainsForService(service: string): Domain[] {
    return domainsStore.domains.filter(
        (d) => d.app_id === props.appId && d.service === service,
    )
}

// Free domains = registered but not yet bound to any app. Picking
// one and binding it via the dialog moves it to this app+service.
const bindableDomains = computed<Domain[]>(() =>
    domainsStore.domains.filter((d) => !d.app_id),
)

// ─── Bind dialog ─────────────────────────────────────────────────
const bindOpen = ref(false)
const bindRow = ref<AppContainer | null>(null)
const bindSelectedId = ref<string | null>(null)
const bindPortStr = ref('')
const binding = ref(false)

const bindPortValid = computed(() => {
    const n = Number(bindPortStr.value)
    return Number.isInteger(n) && n >= 1 && n <= 65535
})

function openBindDialog(row: AppContainer) {
    bindRow.value = row
    bindSelectedId.value = null
    // Pre-fill with the row's first declared port — most common case.
    bindPortStr.value = row.ports[0]?.toString() ?? ''
    bindOpen.value = true
}

async function submitBind() {
    if (!bindRow.value || !bindSelectedId.value || !bindPortValid.value) return
    binding.value = true
    try {
        await domainsStore.update(bindSelectedId.value, {
            app_id: props.appId,
            service: bindRow.value.service,
            port: Number(bindPortStr.value),
        })
        notify.success(t('containersTable.toast.linked'))
        bindOpen.value = false
        await refresh()
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        binding.value = false
    }
}

// "Remover" = wipe the per-service route but keep the domain on
// the app (operator can re-bind later). The domain stays in /domains
// listed against this app; it just stops routing to a specific
// service. To fully detach, the operator goes to /domains/:id.
async function confirmUnbind(domain: Domain) {
    try {
        await domainsStore.update(domain.id, { clear_service: true })
        notify.success(t('containersTable.toast.unbound'))
        await refresh()
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}
</script>

<style scoped>
.containers__head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 14px;
}
.containers__title {
    margin: 0;
    font-size: 16px;
    font-weight: 700;
    color: var(--p-text);
}
.containers__sub {
    margin: 4px 0 0;
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.5;
}
.containers__state { padding: 24px 0; font-size: 13px; }

/* ── Table — frame comes from the parent .pv-panel wrapper, same
   pattern used by .apps-table in AppsView. The <table> itself is
   transparent + collapsed so the panel's bg + radius show through.
   Header tint, hairline row dividers and hover tint are identical
   to apps-table values for cross-screen consistency. */
.ct-wrap {
    padding: 0;
    overflow: hidden;
}
.ct {
    width: 100%;
    border-collapse: collapse;
    border-radius: inherit;
}
.ct thead th {
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
.ct tbody td {
    padding: 12px 14px;
    color: var(--p-text);
    border-bottom: 1px solid color-mix(in srgb, var(--p-content-border), transparent 40%);
    vertical-align: middle;
    font-size: 13px;
}
.ct tbody tr:last-child td { border-bottom: 0; }

.ct__service strong {
    display: block;
    color: var(--p-text);
    font-weight: 600;
}
.ct__service small {
    display: block;
    margin-top: 2px;
    font-size: 11px;
}
.ct__image { color: var(--p-text); word-break: break-all; }

/*
   Layout helpers live on INNER divs, never on the <td> itself —
   applying `display: flex` / `flex-direction: column` to a table
   cell breaks `vertical-align: middle` and inflates the row to
   the cell's intrinsic content height (the action button was
   landing below the rest of the row that way).
*/
.ct__ports-list {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
}
.port {
    display: inline-block;
    padding: 1px 6px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 4px;
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
    color: var(--p-text);
}
.ct__domains-list {
    display: flex;
    flex-direction: column;
    gap: 4px;
}
.ct__domain {
    display: inline-flex;
    align-items: center;
    gap: 4px;
}
.ct__domain a {
    color: var(--p-text);
    text-decoration: none;
    font-size: 12px;
    display: inline-flex;
    align-items: center;
    gap: 4px;
}
.ct__domain a:hover { color: var(--p-primary-500); }
.ct__domain a i { font-size: 10px; opacity: 0.6; }

.ct__actions { text-align: right; white-space: nowrap; }
.ct__actions-row {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    justify-content: flex-end;
}
.ct__actions-col { width: 1%; }
.ct__chevron {
    color: var(--p-text-muted);
    font-size: 11px;
    opacity: 0;
    transition: opacity 120ms ease, transform 120ms ease;
}

/* Clickable rows — open the container detail page on row click.
   Subtle hover-tint matches the patterns in AppsView and the
   deployments-history list. The chevron only fades in on hover so
   it doesn't add noise to the default state. */
.ct__row--link { cursor: pointer; }
.ct__row--link:hover td {
    background: color-mix(in srgb, var(--p-primary-500), transparent 94%);
}
.ct__row--link:hover .ct__chevron {
    opacity: 1;
    transform: translateX(2px);
}

/* ── Bind dialog ── */
.bind-dialog { display: flex; flex-direction: column; gap: 14px; }
.muted { color: var(--p-text-muted); font-size: 12px; }
.mono { font-family: ui-monospace, Menlo, monospace; }
.link { color: var(--p-primary-500); text-decoration: none; font-weight: 600; }
.link:hover { text-decoration: underline; }
</style>
