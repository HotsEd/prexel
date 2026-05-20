<template>
    <div class="domain-detail">
        <RouterLink to="/domains" class="back-link">
            <IconChevronLeft :size="14" />
            <span>{{ t('domains.detail.back') }}</span>
        </RouterLink>

        <!-- ─────────── Loading / 404 ─────────── -->
        <div v-if="loading && !domain" class="muted-state">{{ t('common.loading') }}</div>

        <EmptyState
            v-else-if="!domain"
            :title="t('domains.detail.not_found.title')"
            :body="t('domains.detail.not_found.body')"
        >
            <template #icon><IconNetwork :size="24" /></template>
            <template #actions>
                <Button
                    :label="t('domains.detail.not_found.back')"
                    icon="pi pi-arrow-left"
                    @click="router.push('/domains')"
                />
            </template>
        </EmptyState>

        <template v-else>
            <!-- ─────────── Header ─────────── -->
            <header class="detail-header">
                <div class="detail-header__kicker">{{ t('domains.detail.kicker') }}</div>
                <h1 class="detail-header__title mono">{{ domain.name }}</h1>
                <p class="detail-header__sub">{{ subtitle }}</p>
            </header>

            <!-- ─────────── Visão geral ─────────── -->
            <SCard :title="t('domains.detail.overview.title')">
                <div class="status-row">
                    <SslBadge :status="domain.ssl_status" />
                    <span :class="['p-tag', domain.dns_verified ? 'p-tag-success' : 'p-tag-warn']">
                        <span class="p-tag-dot" />
                        {{ domain.dns_verified ? t('domains.dns_verified') : t('domains.dns_pending') }}
                    </span>
                    <span
                        v-if="domain.covered_by_wildcard"
                        class="pill pill--info"
                        :title="t('domains.wildcard_covered_hint')"
                    >
                        {{ t('domains.wildcard_covered') }}
                    </span>
                    <span v-if="domain.is_primary" class="pill pill--success">
                        {{ t('domains.primary') }}
                    </span>
                </div>

                <div class="grid-meta">
                    <div>
                        <div class="key">{{ t('domains.detail.overview.fields.hostname') }}</div>
                        <div class="val mono">{{ domain.name }}</div>
                    </div>
                    <div>
                        <div class="key">{{ t('domains.detail.overview.fields.zone') }}</div>
                        <div class="val mono">{{ zone?.apex ?? '—' }}</div>
                    </div>
                    <div>
                        <div class="key">{{ t('domains.detail.overview.fields.created') }}</div>
                        <div class="val">{{ formatDateTime(domain.created_at) }}</div>
                    </div>
                    <div>
                        <div class="key">{{ t('domains.detail.overview.fields.dns_last') }}</div>
                        <div class="val">{{ domain.dns_last_check ? formatRelative(domain.dns_last_check) : '—' }}</div>
                    </div>
                    <div>
                        <div class="key">{{ t('domains.detail.overview.fields.ssl_last') }}</div>
                        <div class="val">{{ sslLastLabel }}</div>
                    </div>
                    <div>
                        <div class="key">{{ t('domains.detail.overview.fields.ssl_expires') }}</div>
                        <div class="val">{{ domain.ssl_expires_at ? formatDateTime(domain.ssl_expires_at) : '—' }}</div>
                    </div>
                </div>
            </SCard>

            <!-- ─────────── Vinculação (readonly) ─────────── -->
            <!--
                Three distinct states the operator must be able to tell apart:
                  1. Bound to an app  → routes to that app's container
                  2. Bound to the panel (instance_url == this hostname) →
                     the panel is a "target" just like an app; show it as
                     such, never as a default fallback
                  3. NOT bound to anything → the row exists in the DB but
                     no traffic destination is configured; needs the
                     operator to pick a target
                Editing happens elsewhere (Apps → Domínios for app binding,
                Settings → Instância for panel binding).
            -->
            <SCard :title="t('domains.detail.linkage.title')" :sub="t('domains.detail.linkage.sub_readonly')">
                <!-- State 1: bound to an app -->
                <div v-if="linkedApp" class="linked-target">
                    <UiAvatar :name="linkedApp.name" size="md" />
                    <div class="linked-target__body">
                        <RouterLink :to="`/apps/${linkedApp.id}`" class="linked-target__name">
                            {{ linkedApp.name }}
                            <i class="pi pi-arrow-up-right" aria-hidden="true" />
                        </RouterLink>
                        <span class="linked-target__kind">{{ t('domains.detail.linkage.kind_app') }}</span>
                        <span v-if="linkedApp.description" class="muted">{{ linkedApp.description }}</span>
                        <span v-if="domain?.is_primary" class="primary-tag">
                            {{ t('domains.detail.linkage.primary_tag') }}
                        </span>
                    </div>
                </div>

                <!-- State 2: bound to the panel (instance) -->
                <div v-else-if="isInstanceDomain" class="linked-target linked-target--instance">
                    <div class="linked-target__icon">
                        <IconNetwork :size="20" />
                    </div>
                    <div class="linked-target__body">
                        <RouterLink to="/settings/instance" class="linked-target__name">
                            {{ t('domains.detail.linkage.kind_instance_name') }}
                            <i class="pi pi-arrow-up-right" aria-hidden="true" />
                        </RouterLink>
                        <span class="linked-target__kind">{{ t('domains.detail.linkage.kind_instance') }}</span>
                        <span class="muted">{{ t('domains.detail.linkage.kind_instance_desc') }}</span>
                    </div>
                </div>

                <!-- State 3: not bound to anything -->
                <Message v-else severity="warn" :closable="false" class="banner-unbound">
                    {{ t('domains.detail.linkage.unbound_banner') }}
                </Message>

                <p class="muted hint">{{ t('domains.detail.linkage.edit_elsewhere_hint') }}</p>

                <div class="actions-row">
                    <RouterLink
                        v-if="linkedApp"
                        :to="`/apps/${linkedApp.id}?tab=domains`"
                        class="p-button p-button-text"
                    >
                        <i class="pi pi-arrow-up-right" aria-hidden="true" />
                        <span>{{ t('domains.detail.linkage.go_to_app_btn') }}</span>
                    </RouterLink>
                    <RouterLink
                        v-else-if="isInstanceDomain"
                        to="/settings/instance"
                        class="p-button p-button-text"
                    >
                        <i class="pi pi-arrow-up-right" aria-hidden="true" />
                        <span>{{ t('domains.detail.linkage.go_to_instance_btn') }}</span>
                    </RouterLink>
                </div>
            </SCard>

            <!-- ─────────── Verificação DNS ─────────── -->
            <SCard :title="t('domains.detail.verify.title')" :sub="t('domains.detail.verify.sub')">
                <div class="verify-bar">
                    <Button
                        :label="t('domains.detail.verify.verify_btn')"
                        icon="pi pi-refresh"
                        size="small"
                        :loading="verifying"
                        :disabled="!zone"
                        @click="runVerify"
                    />
                    <span v-if="lastAutoCheck" class="muted small">
                        {{ t('domains.detail.verify.last_auto', { when: formatRelative(lastAutoCheck) }) }}
                    </span>
                </div>

                <!--
                    Cards live here permanently — `currentResult` is
                    populated from persisted backend state on mount and
                    upgraded with live probe data when "Verify now" runs.
                    Placeholder only shows in the rare race where the
                    zone hasn't loaded yet (unlikely; defensive).
                -->
                <div v-if="currentResult && zone" class="probe-grid">
                    <ProbeResultCard
                        v-if="currentResult.hostname"
                        :title="t('domains.wizard.step4.hostname_card')"
                        :host="domain.name"
                        :result="currentResult.hostname"
                    />
                    <ProbeResultCard
                        :title="t('domains.wizard.step4.wildcard_card')"
                        :host="`*.${zone.apex}`"
                        :result="currentResult.wildcard"
                    />
                </div>
                <div v-else class="probe-placeholder">
                    <IconNetwork :size="28" />
                    <p>{{ t('domains.detail.verify.placeholder') }}</p>
                </div>
            </SCard>

            <!-- ─────────── Zona de perigo ─────────── -->
            <SCard :title="t('domains.detail.danger.title')" :sub="t('domains.detail.danger.sub')">
                <div class="danger-row">
                    <div class="danger-row__copy">
                        <strong>{{ t('domains.detail.danger.remove') }}</strong>
                        <p>{{ removeBlockedReason ?? t('domains.detail.danger.remove_hint') }}</p>
                    </div>
                    <span :title="removeBlockedReason ?? ''" class="danger-row__btn">
                        <Button
                            :label="t('domains.detail.danger.remove')"
                            icon="pi pi-trash"
                            severity="danger"
                            :disabled="!!removeBlockedReason || removing"
                            :loading="removing"
                            @click="confirmRemove"
                        />
                    </span>
                </div>
            </SCard>
        </template>

        <!-- ─────────── Confirm dialog (remove only — linkage edits live in /apps) ─────────── -->
        <Dialog
            v-model:visible="removeOpen"
            modal
            :header="t('domains.detail.danger.confirm_title')"
            :style="{ width: '440px' }"
        >
            <p class="prose">{{ t('domains.detail.danger.confirm_body') }}</p>
            <template #footer>
                <Button
                    text
                    severity="secondary"
                    :label="t('common.cancel')"
                    :disabled="removing"
                    @click="removeOpen = false"
                />
                <Button
                    :label="t('domains.detail.danger.confirm_accept')"
                    severity="danger"
                    :loading="removing"
                    @click="doRemove"
                />
            </template>
        </Dialog>
    </div>
</template>

<script setup lang="ts">
/*
    Domain detail page.

    Reached from /domains by clicking a subdomain row inside a ZoneCard.
    Replaces the kebab/trash icons that used to live there: the operator
    edits app linkage, toggles the primary flag, re-runs DNS probes, and
    deletes the domain from here.

    The layout follows the same SettingsView vocabulary used elsewhere in
    the app — a vertical stack of SCard blocks with a tiny kicker, a big
    H1, and prose subtitles. Section behaviour:

      - Visão geral: read-only badges + key/value grid.
      - Aplicação vinculada: view ⇄ edit toggle. Save calls PATCH; switching
        apps deliberately resets `is_primary` to false so the operator
        explicitly opts back in instead of stealing the primary slot from
        the destination app's existing primary.
      - Verificação DNS: same probe cards as the wizard's Step 4. We pass
        `hostname=<this domain>` so the backend returns the dedicated probe
        for this FQDN — the wizard does the same trick, but it's about the
        domain being added; here it's about the existing one.
      - Zona de perigo: hard-disables the delete button with a tooltip when
        the domain is in use by an app or by the instance URL. We also
        still handle the backend-side 409 in case of a race condition.
*/
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import axios from 'axios'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Message from 'primevue/message'
import EmptyState from '@/components/EmptyState.vue'
import SCard from '@/components/settings/SCard.vue'
import SslBadge from '@/components/SslBadge.vue'
import UiAvatar from '@/components/ui/UiAvatar.vue'
import IconChevronLeft from '@/components/icons/IconChevronLeft.vue'
import IconNetwork from '@/components/icons/IconNetwork.vue'
import ProbeResultCard from '@/components/domains/ProbeResultCard.vue'
import { useDomainsStore } from '@/stores/domains'
import { useAppsStore } from '@/stores/apps'
import { useDNSZonesStore } from '@/stores/dnsZones'
import { useInstanceStore } from '@/stores/instance'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import { formatDateTime, formatRelative } from '@/utils/format'
import type { VerificationResult } from '@/services/dnsZones'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const domainsStore = useDomainsStore()
const appsStore = useAppsStore()
const zonesStore = useDNSZonesStore()
const instanceStore = useInstanceStore()

const loading = ref(true)
const verifying = ref(false)
const removing = ref(false)
const removeOpen = ref(false)
const lastResult = ref<VerificationResult | null>(null)

const domainId = computed(() => String(route.params.id ?? ''))
const domain = computed(() => domainsStore.byId[domainId.value] ?? null)
const zone = computed(() => domain.value?.zone_id ? zonesStore.getById(domain.value.zone_id) : undefined)
const linkedApp = computed(() => domain.value?.app_id ? appsStore.byId[domain.value.app_id] ?? null : null)

const subtitle = computed(() => {
    if (!domain.value) return ''
    const kind = zone.value
        ? (domain.value.name.toLowerCase() === zone.value.apex.toLowerCase()
            ? t('domains.detail.subtitle_apex_kind')
            : t('domains.detail.subtitle_sub_kind'))
        : t('domains.detail.subtitle_sub_kind')
    const base = t('domains.detail.subtitle', { kind, apex: zone.value?.apex ?? '—' })
    if (linkedApp.value) return `${base} ${t('domains.detail.subtitle_app_suffix', { app: linkedApp.value.name })}`
    if (isInstanceDomain.value) return `${base} ${t('domains.detail.subtitle_instance_suffix')}`
    // Third state: row exists but nothing routes through it yet.
    return `${base} ${t('domains.detail.subtitle_unbound_suffix')}`
})

// Compare hostname to the instance_url. The backend stores a full URL so we
// strip the protocol/path and lowercase before comparing. If we can't load
// the settings (perm denied, etc.) the worst case is the operator gets a
// 409 from the backend, which we already toast.
const instanceHostname = computed<string | null>(() => {
    const raw = instanceStore.settings?.instance_url
    if (!raw) return null
    try {
        return new URL(raw).hostname.toLowerCase()
    } catch {
        return raw.replace(/^https?:\/\//, '').replace(/\/.*$/, '').toLowerCase() || null
    }
})

const isInstanceDomain = computed(() =>
    !!domain.value && !!instanceHostname.value
    && domain.value.name.toLowerCase() === instanceHostname.value,
)

const removeBlockedReason = computed<string | null>(() => {
    if (!domain.value) return null
    if (domain.value.app_id) return t('domains.detail.danger.in_use_app_tooltip')
    if (isInstanceDomain.value) return t('domains.detail.danger.in_use_instance_tooltip')
    return null
})

const sslLastLabel = computed(() => {
    // The SSLStatus model doesn't carry an explicit "last attempted at" yet,
    // so we surface the most informative timestamp we do have. When SSL is
    // active, `ssl_expires_at - 90d` is a fair proxy for issuance; otherwise
    // we fall back to dns_last_check which is the cadence Caddy uses to retry.
    if (!domain.value) return '—'
    if (domain.value.ssl_status === 'active' && domain.value.ssl_expires_at) {
        const issued = domain.value.ssl_expires_at - 90 * 24 * 60 * 60
        return formatRelative(issued)
    }
    if (domain.value.dns_last_check) return formatRelative(domain.value.dns_last_check)
    return '—'
})

const lastAutoCheck = computed(() => zone.value?.apex_last_check ?? domain.value?.dns_last_check ?? null)

/*
    Persistent verification view.

    The backend's 30s DNS check loop already keeps `domain.dns_verified`,
    `zone.apex_verified`, `zone.wildcard_verified` and `zone.target_ip`
    up to date on the records themselves — so even before the operator
    hits "Verify now" we have enough state to render the probe cards.

    The synthesized result is intentionally lighter than a live probe:
    we know "ok" and the expected target IP, but we don't have the live
    `resolved_to`/`error` strings. The card handles that gracefully (the
    `pending` and `ok` tones both render fine without resolved_to). Once
    the operator runs a fresh probe, `lastResult` takes precedence and
    surfaces the richer details.

    Effect: cards are ALWAYS visible on this page, reflecting the last
    automatic check. The "Verify now" button enriches them, it doesn't
    "create" them.
*/
const persistedResult = computed<VerificationResult | null>(() => {
    if (!domain.value || !zone.value) return null
    const expected = zone.value.target_ip ?? undefined
    return {
        apex: {
            ok: zone.value.apex_verified,
            expected,
            probed: zone.value.apex,
        },
        wildcard: {
            ok: zone.value.wildcard_verified,
            expected,
            probed: `*.${zone.value.apex}`,
        },
        hostname: {
            ok: domain.value.dns_verified,
            expected,
            probed: domain.value.name,
        },
    }
})

// Live probe wins (has resolved_to/error/etc.); falls back to persisted.
const currentResult = computed<VerificationResult | null>(() =>
    lastResult.value ?? persistedResult.value,
)

onMounted(async () => {
    try {
        await Promise.all([
            domainsStore.fetchAll(),
            appsStore.fetchAll(),
            zonesStore.load(),
            instanceStore.settings ? Promise.resolve(null) : instanceStore.load().catch(() => null),
        ])
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        loading.value = false
    }

    // Kick off a live probe right after the initial paint. The persisted
    // result already filled the cards with the green/grey skeleton, but
    // it lacks `resolved_to` / `error` strings — and without those, the
    // ProbeResultCard can't tell apart "no answer" from "resolved to
    // the wrong IP". A single live probe per page load is cheap (1-3 DNS
    // queries), gives the operator accurate, actionable detail, and
    // keeps the page honest: what they see is what's happening now.
    void autoVerify()
})

/*
    Silent variant of `runVerify`: same call, no toast, no error popup.
    Used on mount so the operator gets live data without clicking. Manual
    "Verify now" still uses `runVerify` and surfaces success/failure.
*/
async function autoVerify() {
    if (!zone.value || !domain.value || verifying.value) return
    verifying.value = true
    try {
        lastResult.value = await zonesStore.verify(zone.value.id, domain.value.name)
    } catch {
        // Silent. The persisted cards stay; operator can hit the button
        // to retry and see the error message.
    } finally {
        verifying.value = false
    }
}

async function runVerify() {
    if (!zone.value || !domain.value) return
    verifying.value = true
    try {
        lastResult.value = await zonesStore.verify(zone.value.id, domain.value.name)
        // Backend may have flipped dns_verified / apex_verified on a
        // successful probe. Refetch so the badges at the top of the
        // page (status row, SslBadge, "DNS verified" tag) reflect the
        // new truth instead of waiting for the next page load.
        await Promise.all([
            domainsStore.fetchAll().catch(() => null),
            zonesStore.load().catch(() => null),
        ])
        notify.success(t('domains.zones.card.verified_success'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        verifying.value = false
    }
}

function confirmRemove() {
    if (removeBlockedReason.value) return
    removeOpen.value = true
}

async function doRemove() {
    if (!domain.value) return
    removing.value = true
    try {
        await domainsStore.remove(domain.value.id)
        notify.success(t('domains.detail.updated'))
        router.push('/domains')
    } catch (e) {
        // Race: backend rejected with 409 even though our local check passed
        // (someone re-linked the app, or the panel started using this URL
        // between our last fetch and the click). Surface the backend's
        // message verbatim — it knows exactly which side is blocking.
        if (axios.isAxiosError(e)) {
            const body = e.response?.data as { error?: string; message?: string } | undefined
            if (body?.error === 'in_use_by_app' || body?.error === 'in_use_by_instance') {
                notify.error(body.message || apiErrorMessage(e))
                removeOpen.value = false
                // Re-fetch so the UI updates the disabled state.
                await domainsStore.fetchAll().catch(() => null)
                return
            }
        }
        notify.error(apiErrorMessage(e))
    } finally {
        removing.value = false
    }
}
</script>

<style scoped>
.domain-detail {
    max-width: 920px;
    margin: 0 auto;
}

.back-link {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    color: var(--p-text-muted);
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

/* ─────────── Header ─────────── */
.detail-header {
    margin-bottom: 24px;
}
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
}

/* ─────────── Status row ─────────── */
.status-row {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    margin-bottom: 18px;
}

/* ─────────── Grid meta (visão geral) ─────────── */
.grid-meta {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 18px 24px;
}
@media (max-width: 720px) {
    .grid-meta { grid-template-columns: 1fr; }
}
.key {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--p-text-muted);
    font-weight: 600;
    margin-bottom: 4px;
}
.val { font-size: 14px; color: var(--p-text); word-break: break-word; }

/* ─────────── Aplicação vinculada ─────────── */
.linked-app {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 14px;
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    background: var(--p-surface-50);
    margin-bottom: 14px;
}
.linked-app__body {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
}
.linked-app__name {
    color: var(--p-primary-700);
    font-weight: 600;
    text-decoration: none;
}
.linked-app__name:hover { text-decoration: underline; }

.banner-no-app { margin-bottom: 14px; }

.primary-row { margin-bottom: 14px; }

.actions-row {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 12px;
}

/* ─────────── Verificação DNS ─────────── */
.verify-bar {
    display: inline-flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    margin-bottom: 16px;
}

.probe-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
}
@media (max-width: 720px) {
    .probe-grid { grid-template-columns: 1fr; }
}

.probe-placeholder {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    padding: 32px 16px;
    border: 1px dashed var(--p-content-border);
    border-radius: 12px;
    color: var(--p-text-muted);
}
.probe-placeholder p { margin: 0; font-size: 13px; }

/* ─────────── Zona de perigo ─────────── */
.danger-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    flex-wrap: wrap;
}
.danger-row__copy {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
}
.danger-row__copy strong {
    font-size: 13px;
    color: var(--p-text);
}
.danger-row__copy p {
    margin: 0;
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.5;
}
.danger-row__btn {
    display: inline-flex;
    align-items: center;
}

/* ─────────── Pills (reused) ─────────── */
.pill {
    padding: 1px 8px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
}
.pill--success { background: rgba(16, 185, 129, 0.12); color: var(--p-primary-700); }
.pill--info { background: var(--p-info-bg); color: var(--p-info-text); }

.mono { font-family: ui-monospace, Menlo, monospace; }
.muted { color: var(--p-text-muted); font-size: 12px; }
.small { font-size: 12px; }

.check-row {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
    font-size: 13px;
    color: var(--p-text);
}

.prose {
    margin: 0;
    color: var(--p-text-muted);
    line-height: 1.55;
    font-size: 14px;
}
</style>
