<template>
    <div class="routing">
        <header class="routing__head">
            <div>
                <h3 class="routing__title">{{ t('routingTab.title') }}</h3>
                <p class="routing__sub">
                    {{ t('routingTab.sub') }}
                </p>
            </div>
            <Button
                :label="t('routingTab.bind')"
                icon="pi pi-link"
                size="small"
                @click="openBindDialog(null)"
            />
        </header>

        <!-- ─────────── Empty ─────────── -->
        <div v-if="linkedDomains.length === 0" class="routing__empty muted">
            {{ t('routingTab.emptyPrefix') }} <strong>{{ t('routingTab.emptyMid') }}</strong> {{ t('routingTab.emptySuffix') }}
            <span v-if="isCompose">{{ t('routingTab.emptyComposeSuffix') }}</span>
        </div>

        <!-- ─────────── Table ─────────── -->
        <table v-else class="rt">
            <thead>
                <tr>
                    <th>{{ t('routingTab.columns.domain') }}</th>
                    <th>{{ t('routingTab.columns.service') }}</th>
                    <th>{{ t('routingTab.columns.port') }}</th>
                    <th>{{ t('routingTab.columns.primary') }}</th>
                    <th>{{ t('routingTab.columns.ssl') }}</th>
                    <th>{{ t('routingTab.columns.https') }}</th>
                    <th class="rt__act"></th>
                </tr>
            </thead>
            <tbody>
                <tr v-for="d in linkedDomains" :key="d.id">
                    <td>
                        <a :href="`https://${d.name}`" target="_blank" rel="noopener" class="mono rt__name">
                            {{ d.name }}
                            <i class="pi pi-external-link" aria-hidden="true" />
                        </a>
                    </td>
                    <td>
                        <span v-if="d.service" class="pill pill--service mono">{{ d.service }}</span>
                        <span v-else class="muted">{{ t('routingTab.wholeApp') }}</span>
                    </td>
                    <td>
                        <code v-if="d.port" class="port">{{ d.port }}</code>
                        <span v-else class="muted">—</span>
                    </td>
                    <td>
                        <span v-if="d.is_primary" class="pill pill--primary">{{ t('routingTab.primaryTag') }}</span>
                        <Button
                            v-else
                            text
                            size="small"
                            :label="t('routingTab.setPrimary')"
                            :loading="primaryBusyId === d.id"
                            :disabled="!!primaryBusyId || !!unbindBusyId"
                            @click="setPrimary(d)"
                        />
                    </td>
                    <td>
                        <SslBadge :status="d.ssl_status" />
                    </td>
                    <td>
                        <div class="rt__https">
                            <span
                                v-if="d.force_https"
                                class="pill pill--https-on"
                            >
                                <i class="pi pi-lock" aria-hidden="true" /> {{ t('routingTab.httpsForced') }}
                            </span>
                            <span
                                v-else
                                class="pill pill--https-off"
                            >
                                <i class="pi pi-lock-open" aria-hidden="true" /> {{ t('routingTab.httpsHttpAndHttps') }}
                            </span>
                            <ToggleSwitch
                                :model-value="d.force_https"
                                :disabled="forceHttpsBusyId === d.id"
                                class="rt__https-toggle"
                                v-tooltip.top="t('routingTab.httpsTooltip')"
                                @update:model-value="(v: boolean) => toggleForceHttps(d, v)"
                            />
                        </div>
                    </td>
                    <td class="rt__act">
                        <Button
                            text
                            size="small"
                            icon="pi pi-pencil"
                            :aria-label="t('routingTab.editAria')"
                            :title="t('routingTab.editTitle')"
                            @click="openBindDialog(d)"
                        />
                        <Button
                            text
                            severity="danger"
                            size="small"
                            icon="pi pi-times"
                            :aria-label="t('routingTab.unbindAria')"
                            :title="t('routingTab.unbindTitle')"
                            :loading="unbindBusyId === d.id"
                            :disabled="!!primaryBusyId || !!unbindBusyId"
                            @click="confirmUnbind(d)"
                        />
                    </td>
                </tr>
            </tbody>
        </table>

        <!-- ─────────── Bind / edit dialog ─────────── -->
        <Dialog
            v-model:visible="bindOpen"
            modal
            :header="editing ? t('routingTab.bindDialog.editHeader', { name: editing.name }) : t('routingTab.bindDialog.newHeader')"
            :style="{ width: '540px' }"
            :closable="!binding"
        >
            <div class="bind">
                <SField v-if="!editing" :label="t('routingTab.bindDialog.domainLabel')" :error="bindSelectError ?? undefined">
                    <DomainPicker
                        v-model="bindSelectedId"
                        :placeholder="bindableDomains.length ? t('routingTab.bindDialog.domainPlaceholderHasFree') : t('routingTab.bindDialog.domainPlaceholderNoFree')"
                        :disabled="bindableDomains.length === 0"
                    />
                </SField>

                <SField v-if="isCompose" :label="t('routingTab.bindDialog.modeLabel')" :hint="t('routingTab.bindDialog.modeHint')">
                    <div class="bind__mode">
                        <label class="bind__radio">
                            <RadioButton v-model="bindMode" value="service" name="bind-mode" />
                            <span>
                                <strong>{{ t('routingTab.bindDialog.modeService') }}</strong>
                                <small>{{ t('routingTab.bindDialog.modeServiceHint') }}</small>
                            </span>
                        </label>
                        <label class="bind__radio">
                            <RadioButton v-model="bindMode" value="app" name="bind-mode" />
                            <span>
                                <strong>{{ t('routingTab.bindDialog.modeApp') }}</strong>
                                <small>{{ t('routingTab.bindDialog.modeAppHint') }}</small>
                            </span>
                        </label>
                    </div>
                </SField>

                <div v-if="bindMode === 'service'" class="bind__grid">
                    <SField :label="t('routingTab.bindDialog.serviceLabel')" :error="bindServiceError ?? undefined">
                        <Select
                            v-model="bindService"
                            :options="serviceOptions"
                            option-label="label"
                            option-value="value"
                            :placeholder="t('routingTab.bindDialog.servicePlaceholder')"
                            :filter="serviceOptions.length > 5"
                            editable
                        />
                    </SField>
                    <SField :label="t('routingTab.bindDialog.portLabel')" :hint="t('routingTab.bindDialog.portHint')">
                        <InputText
                            v-model="bindPortStr"
                            type="number"
                            min="1"
                            max="65535"
                            :placeholder="t('routingTab.bindDialog.portPlaceholder')"
                        />
                    </SField>
                </div>

                <label class="bind__primary">
                    <ToggleSwitch v-model="bindIsPrimary" />
                    <span>{{ t('routingTab.bindDialog.primaryToggle') }}</span>
                </label>

                <label class="bind__primary">
                    <Checkbox v-model="bindForceHttps" :binary="true" inputId="bind-force-https" />
                    <span>
                        <strong>{{ t('routingTab.bindDialog.forceHttps') }}</strong>
                        <small class="bind__hint">
                            {{ t('routingTab.bindDialog.forceHttpsHint') }}
                        </small>
                    </span>
                </label>

                <Message v-if="bindError" severity="error" :closable="false">{{ bindError }}</Message>
            </div>
            <template #footer>
                <Button text severity="secondary" :label="t('common.cancel')" :disabled="binding" @click="bindOpen = false" />
                <Button
                    :label="editing ? t('common.save') : t('apps.detail.domains.link_dialog.submit')"
                    :loading="binding"
                    :disabled="!canSaveBind"
                    @click="submitBind"
                />
            </template>
        </Dialog>

        <!-- ─────────── Unbind confirm ─────────── -->
        <Dialog
            v-model:visible="unbindOpen"
            modal
            :header="t('routingTab.unbindDialog.header')"
            :style="{ width: '440px' }"
            :closable="!unbindBusyId"
        >
            <p class="prose">
                <i18n-t keypath="routingTab.unbindDialog.body" scope="global">
                    <template #code><code>{{ unbindTarget?.name }}</code></template>
                    <template #link><RouterLink to="/domains" class="link">{{ t('routingTab.unbindDialog.domainsLink') }}</RouterLink></template>
                </i18n-t>
            </p>
            <template #footer>
                <Button text severity="secondary" :label="t('common.cancel')" :disabled="!!unbindBusyId" @click="unbindOpen = false" />
                <Button severity="danger" :label="t('apps.detail.domains.actions.unbind')" :loading="!!unbindBusyId" @click="doUnbind" />
            </template>
        </Dialog>
    </div>
</template>

<script setup lang="ts">
/*
    RoutingTab — consolidated routing view for an app.

    Replaces the previous "Domínios" tab. Shows EVERY domain pointing
    at the app (with `service`+`port` for Compose, or app-level for
    single-container) in one table, with edit-in-dialog + remove +
    primary toggle.

    Decision matrix for the bind dialog:
      - app is single-container → `service` field hidden, PATCH omits
        service/port (Caddy keeps `prexel-<app>:<app.port>`)
      - app is Compose → radio between "service" (default) and "app"
        modes; service+port required in the first, omitted in the
        second
*/
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import RadioButton from 'primevue/radiobutton'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import SField from '@/components/settings/SField.vue'
import SslBadge from '@/components/SslBadge.vue'
import DomainPicker from '@/components/domains/DomainPicker.vue'
import { useAppsStore } from '@/stores/apps'
import { useDomainsStore } from '@/stores/domains'
import { useInstanceStore } from '@/stores/instance'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { App, AppContainer, Domain } from '@/types/api'

const props = defineProps<{
    app: App | null
}>()

const { t } = useI18n()
const appsStore = useAppsStore()
const domainsStore = useDomainsStore()
const instanceStore = useInstanceStore()

const isCompose = computed(() => props.app?.build_type === 'docker_compose')

// All domains currently bound to this app — with or without service.
// Sorted: primary first, then alphabetical. Drives the table body.
const linkedDomains = computed<Domain[]>(() =>
    domainsStore.domains
        .filter((d) => d.app_id === props.app?.id)
        .slice()
        .sort((a, b) => {
            if (a.is_primary !== b.is_primary) return a.is_primary ? -1 : 1
            return a.name.localeCompare(b.name)
        }),
)

// Container list — used to populate the "Service" dropdown in the
// bind dialog. Best-effort: empty list still lets the operator
// type a service name manually (editable Select).
const containers = ref<AppContainer[]>([])
const serviceOptions = computed(() =>
    containers.value
        .filter((c) => !!c.service)
        .map((c) => ({ label: c.service, value: c.service })),
)

async function loadContainers() {
    if (!props.app) return
    containers.value = await appsStore.fetchContainers(props.app.id)
}

// ── Bind / edit dialog state ─────────────────────────────────────
const bindOpen = ref(false)
const editing = ref<Domain | null>(null)            // null → vincular novo; Domain → editar
const bindSelectedId = ref<string | null>(null)     // new-bind only
const bindMode = ref<'service' | 'app'>('service')
const bindService = ref<string>('')
const bindPortStr = ref('')
const bindIsPrimary = ref(false)
const bindForceHttps = ref(true)
const binding = ref(false)
const bindError = ref<string | null>(null)

// Same panel-domain exclusion as the rest of the UI — the panel's
// own URL is a binding target managed in Settings, not here.
const instanceHostname = computed<string | null>(() => {
    const raw = instanceStore.settings?.instance_url
    if (!raw) return null
    try { return new URL(raw).hostname.toLowerCase() } catch {
        return raw.replace(/^https?:\/\//, '').replace(/\/.*$/, '').toLowerCase() || null
    }
})
const bindableDomains = computed<Domain[]>(() =>
    domainsStore.domains.filter((d) => !d.app_id && d.name.toLowerCase() !== instanceHostname.value),
)

const bindPortValid = computed(() => {
    if (bindMode.value !== 'service') return true
    const n = Number(bindPortStr.value)
    return Number.isInteger(n) && n >= 1 && n <= 65535
})
const bindServiceError = computed<string | null>(() => {
    if (bindMode.value !== 'service') return null
    if (!bindService.value.trim()) return t('routingTab.errors.noService')
    return null
})
const bindSelectError = computed<string | null>(() => {
    if (editing.value) return null
    if (bindSelectedId.value) return null
    if (bindableDomains.value.length === 0) return t('routingTab.errors.noDomain')
    return null
})
const canSaveBind = computed(() => {
    if (binding.value) return false
    if (!editing.value && !bindSelectedId.value) return false
    if (bindMode.value === 'service') {
        if (!!bindServiceError.value) return false
        if (!bindPortValid.value) return false
    }
    return true
})

function openBindDialog(target: Domain | null) {
    editing.value = target
    bindError.value = null
    if (target) {
        bindSelectedId.value = target.id
        if (target.service) {
            bindMode.value = 'service'
            bindService.value = target.service
            bindPortStr.value = target.port?.toString() ?? ''
        } else {
            bindMode.value = isCompose.value ? 'service' : 'app'
            bindService.value = ''
            bindPortStr.value = ''
        }
        bindIsPrimary.value = target.is_primary
        bindForceHttps.value = target.force_https
    } else {
        bindSelectedId.value = null
        bindMode.value = isCompose.value ? 'service' : 'app'
        // Pre-fill with the first detected service / its first port —
        // most operators add a domain right after picking the service
        // from the Containers tab, so the dropdown defaulting saves
        // a couple of clicks.
        const first = containers.value.find((c) => !!c.service)
        bindService.value = first?.service ?? ''
        bindPortStr.value = first?.ports?.[0]?.toString() ?? ''
        bindIsPrimary.value = linkedDomains.value.length === 0
        bindForceHttps.value = true
    }
    // For single-container apps the choice is forced to "app". The
    // mode radio is hidden in the template (v-if="isCompose").
    if (!isCompose.value) bindMode.value = 'app'
    bindOpen.value = true
}

async function submitBind() {
    const targetId = editing.value?.id ?? bindSelectedId.value
    if (!targetId) return
    binding.value = true
    bindError.value = null
    try {
        const patch: Record<string, unknown> = {
            app_id: props.app!.id,
            is_primary: bindIsPrimary.value,
            force_https: bindForceHttps.value,
        }
        if (bindMode.value === 'service') {
            patch.service = bindService.value.trim()
            patch.port = Number(bindPortStr.value)
        } else {
            // App mode: wipe any previously-set service/port so the
            // Caddy upstream falls back to the app-level container.
            patch.clear_service = true
        }
        await domainsStore.update(targetId, patch)
        notify.success(editing.value ? t('routingTab.toast.routeUpdated') : t('routingTab.toast.linked'))
        bindOpen.value = false
    } catch (e) {
        bindError.value = apiErrorMessage(e)
    } finally {
        binding.value = false
    }
}

// ── Primary toggle (1-click action on the row) ──────────────────
const primaryBusyId = ref<string | null>(null)
async function setPrimary(d: Domain) {
    if (d.is_primary) return
    primaryBusyId.value = d.id
    try {
        await domainsStore.update(d.id, { is_primary: true })
        notify.success(t('routingTab.toast.primary'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        primaryBusyId.value = null
    }
}

// ── HTTPS redirect toggle (optimistic) ───────────────────────────
// Flips d.force_https in the store right away and reverts if the
// PATCH errors — keeps the UI snappy for what is effectively a
// 1-bit boolean. The store's `update()` writes the server response
// back over the local row on success, so we don't need a second
// fetch after the await.
const forceHttpsBusyId = ref<string | null>(null)
async function toggleForceHttps(d: Domain, next: boolean) {
    if (d.force_https === next) return
    const previous = d.force_https
    const idx = domainsStore.domains.findIndex((x) => x.id === d.id)
    if (idx >= 0) domainsStore.domains[idx] = { ...domainsStore.domains[idx], force_https: next }
    forceHttpsBusyId.value = d.id
    try {
        await domainsStore.update(d.id, { force_https: next })
        notify.success(next ? t('routingTab.toast.httpsOn') : t('routingTab.toast.httpsOff'))
    } catch (e) {
        // Revert the optimistic flip on error.
        const i = domainsStore.domains.findIndex((x) => x.id === d.id)
        if (i >= 0) domainsStore.domains[i] = { ...domainsStore.domains[i], force_https: previous }
        notify.error(apiErrorMessage(e))
    } finally {
        forceHttpsBusyId.value = null
    }
}

// ── Unbind ───────────────────────────────────────────────────────
const unbindOpen = ref(false)
const unbindTarget = ref<Domain | null>(null)
const unbindBusyId = ref<string | null>(null)
function confirmUnbind(d: Domain) {
    unbindTarget.value = d
    unbindOpen.value = true
}
async function doUnbind() {
    if (!unbindTarget.value) return
    unbindBusyId.value = unbindTarget.value.id
    try {
        await domainsStore.update(unbindTarget.value.id, { clear_app_id: true, clear_service: true })
        notify.success(t('routingTab.toast.unbound'))
        unbindOpen.value = false
        unbindTarget.value = null
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        unbindBusyId.value = null
    }
}

// ── Init ────────────────────────────────────────────────────────
onMounted(async () => {
    // domains + containers + (best-effort) instance settings for the
    // panel-domain exclusion in DomainPicker.
    await Promise.all([
        domainsStore.fetchAll().catch(() => null),
        loadContainers(),
        instanceStore.settings ? Promise.resolve(null) : instanceStore.load().catch(() => null),
    ])
})

watch(() => props.app?.id, () => { void loadContainers() })
</script>

<style scoped>
.routing { display: flex; flex-direction: column; gap: 16px; }
.routing__head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
}
.routing__title {
    margin: 0;
    font-size: 14px;
    font-weight: 700;
    color: var(--p-text);
    text-transform: uppercase;
    letter-spacing: 0.04em;
}
.routing__sub {
    margin: 4px 0 0;
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.5;
    max-width: 620px;
}
.routing__empty {
    padding: 24px 18px;
    text-align: center;
    border: 1px dashed var(--p-content-border);
    border-radius: 12px;
    font-size: 13px;
}

/* ── Table ── */
.rt {
    width: 100%;
    border-collapse: collapse;
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    overflow: hidden;
    background: var(--p-content-bg);
}
.rt thead th {
    text-align: left;
    padding: 10px 14px;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--p-text-muted);
    font-weight: 600;
    border-bottom: 1px solid var(--p-content-border);
}
.rt tbody td {
    padding: 10px 14px;
    border-bottom: 1px solid var(--p-divider);
    vertical-align: middle;
    font-size: 13px;
}
.rt tbody tr:last-child td { border-bottom: 0; }
.rt__name {
    color: var(--p-text);
    text-decoration: none;
    display: inline-flex;
    align-items: center;
    gap: 4px;
}
.rt__name:hover { color: var(--p-primary-500); }
.rt__name i { font-size: 10px; opacity: 0.6; }
.rt__act { white-space: nowrap; text-align: right; width: 1%; }

/* ── Pills ── */
.pill {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
}
.pill--service {
    background: color-mix(in srgb, var(--p-info, #3b82f6), transparent 85%);
    color: var(--p-info, #3b82f6);
}
.pill--primary {
    background: color-mix(in srgb, var(--p-warning, #d97706), transparent 80%);
    color: var(--p-warning, #d97706);
}
.pill--https-on {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    background: color-mix(in srgb, #10b981, transparent 82%);
    color: #10b981;
}
.pill--https-off {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    background: color-mix(in srgb, #f97316, transparent 82%);
    color: #f97316;
}
.pill--https-on i,
.pill--https-off i { font-size: 10px; }
.rt__https {
    display: inline-flex;
    align-items: center;
    gap: 8px;
}
.rt__https-toggle :deep(.p-toggleswitch),
.rt__https-toggle.p-toggleswitch {
    transform: scale(0.78);
    transform-origin: left center;
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

/* ── Bind dialog ── */
.bind { display: flex; flex-direction: column; gap: 14px; }
.bind__mode { display: flex; flex-direction: column; gap: 8px; }
.bind__radio {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 10px 12px;
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    cursor: pointer;
}
.bind__radio:hover { border-color: color-mix(in srgb, var(--p-primary-500), transparent 60%); }
.bind__radio span { display: flex; flex-direction: column; gap: 2px; }
.bind__radio strong { font-size: 13px; color: var(--p-text); }
.bind__radio small { font-size: 11px; color: var(--p-text-muted); line-height: 1.4; }
.bind__radio code {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 10px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    padding: 0 4px;
    border-radius: 3px;
}
.bind__grid {
    display: grid;
    grid-template-columns: 2fr 1fr;
    gap: 12px;
}
@media (max-width: 540px) {
    .bind__grid { grid-template-columns: 1fr; }
}
.bind__primary {
    display: inline-flex;
    align-items: flex-start;
    gap: 8px;
    color: var(--p-text);
    font-size: 13px;
}
.bind__primary span {
    display: flex;
    flex-direction: column;
    gap: 2px;
    line-height: 1.4;
}
.bind__hint {
    color: var(--p-text-muted);
    font-size: 11px;
    line-height: 1.4;
}

.muted { color: var(--p-text-muted); }
.mono { font-family: ui-monospace, Menlo, monospace; }
.prose { margin: 0 0 12px; color: var(--p-text); font-size: 14px; line-height: 1.5; }
.prose code {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 12px;
    padding: 1px 6px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 4px;
}
.link { color: var(--p-primary-500); text-decoration: none; }
.link:hover { text-decoration: underline; }
</style>
