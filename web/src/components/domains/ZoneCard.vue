<template>
    <section class="zone-card">
        <header class="zone-card__head">
            <div class="zone-card__title-row">
                <span class="zone-card__apex mono">{{ zone.apex }}</span>
                <span :class="['p-tag', apexTagClass]">
                    <span class="p-tag-dot" />
                    {{ apexLabel }}
                </span>
                <span v-if="zone.wildcard_verified" class="p-tag p-tag-info zone-card__wildcard">
                    <IconNetwork :size="12" :stroke-width="2" />
                    {{ t('domains.zones.badges.wildcard_active') }}
                </span>
            </div>
            <p v-if="zone.notes" class="zone-card__notes" :title="zone.notes">
                {{ zone.notes }}
            </p>
            <div class="zone-card__head-meta">
                <span class="muted">{{ lastCheckLabel }}</span>
            </div>
            <div class="zone-card__head-actions">
                <Button
                    size="small"
                    text
                    :label="t('domains.zones.card.verify_now')"
                    icon="pi pi-refresh"
                    :loading="verifying"
                    @click="onVerify"
                />
                <!--
                    Notes are edited via a direct text button now: the kebab
                    menu was removed because the only other action used to be
                    "Remove zone" — but zones aren't removable directly any
                    more. They disappear automatically once the last domain
                    inside them is deleted (handled server-side), so exposing
                    that as a manual action would only ever hit an empty zone.
                -->
                <Button
                    size="small"
                    text
                    severity="secondary"
                    icon="pi pi-pencil"
                    :title="t('domains.zones.card.edit_notes')"
                    :aria-label="t('domains.zones.card.edit_notes')"
                    @click="emit('edit-notes')"
                />
            </div>
        </header>

        <div class="zone-card__body">
            <div class="zone-card__section-head">
                <h4 class="zone-card__section-title">
                    {{ t('domains.zones.card.subdomains_label', { n: domains.length }) }}
                </h4>
                <Button
                    v-if="domains.length"
                    size="small"
                    text
                    icon="pi pi-plus"
                    :label="t('domains.zones.card.add_subdomain')"
                    @click="emit('add-subdomain')"
                />
            </div>

            <p v-if="!domains.length" class="zone-card__empty">
                {{ t('domains.zones.card.no_subdomains') }}
                <Button
                    text
                    size="small"
                    icon="pi pi-plus"
                    :label="t('domains.zones.card.add_subdomain')"
                    @click="emit('add-subdomain')"
                />
            </p>

            <!--
                Each subdomain row is a RouterLink that navigates to the
                detail page. The trash icon was removed: deletion now lives
                inside the detail page's "Zona de perigo" section, which
                also enforces the unbind-first rule visually.
            -->
            <ul v-else class="domain-list">
                <li v-for="d in sortedDomains" :key="d.id">
                    <RouterLink :to="`/domains/${d.id}`" class="domain-row" :aria-label="d.name">
                        <div class="domain-row__main">
                            <span class="mono domain-row__name">{{ d.name }}</span>
                            <span v-if="isApexDomain(d)" class="pill pill--neutral">
                                {{ t('domains.zones.card.apex_tag') }}
                            </span>
                            <span v-if="d.is_primary" class="pill pill--success">
                                {{ t('domains.primary') }}
                            </span>
                        </div>
                        <div class="domain-row__meta">
                            <!--
                                Three linkage states (panel is a target like
                                an app, not the default for "no app"):
                                  1. app_id set → show app name
                                  2. matches instance_url → "Painel"
                                  3. otherwise → "Sem vinculação" (warn-ish)
                            -->
                            <span class="domain-row__app">
                                <span v-if="d.app_id">{{ appLabel(d.app_id) }}</span>
                                <span v-else-if="isInstanceDomain(d)" class="pill pill--info">
                                    {{ t('domains.linkage_panel') }}
                                </span>
                                <span v-else class="pill pill--warn">
                                    {{ t('domains.linkage_unbound') }}
                                </span>
                            </span>
                            <SslBadge :status="d.ssl_status" />
                            <span :class="['p-tag', d.dns_verified ? 'p-tag-success' : 'p-tag-warn']">
                                <span class="p-tag-dot" />
                                {{ d.dns_verified ? t('domains.dns_verified') : t('domains.dns_pending') }}
                            </span>
                            <span v-if="d.covered_by_wildcard" class="pill pill--info" :title="t('domains.wildcard_covered_hint')">
                                {{ t('domains.wildcard_covered') }}
                            </span>
                        </div>
                        <div class="domain-row__chevron" aria-hidden="true">
                            <i class="pi pi-chevron-right" />
                        </div>
                    </RouterLink>
                </li>
            </ul>
        </div>
    </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import Button from 'primevue/button'
import IconNetwork from '@/components/icons/IconNetwork.vue'
import SslBadge from '@/components/SslBadge.vue'
import type { DNSZone } from '@/services/dnsZones'
import type { Domain } from '@/types/api'
import { formatRelative } from '@/utils/format'
import { useInstanceStore } from '@/stores/instance'

const props = defineProps<{
    zone: DNSZone
    domains: Domain[]
    appLabel: (id: string | null | undefined) => string
    verifying?: boolean
}>()

// We kept the legacy emits in the signature even though most are no longer
// fired by the template, so existing parents (DomainsView) don't break.
// Only `verify`, `edit-notes`, and `add-subdomain` are still raised. The
// `remove-zone` / `remove-domain` events are intentionally vestigial — they
// can be dropped once nothing in the tree binds to them.
const emit = defineEmits<{
    'verify': []
    'edit-notes': []
    'add-subdomain': []
}>()

const { t } = useI18n()
const instanceStore = useInstanceStore()

// Extract the hostname from instance_url (which is a full URL) once per
// reactive read of the cached settings — used to tell "domain is the panel"
// from "domain has no linkage at all".
const instanceHostname = computed<string | null>(() => {
    const raw = instanceStore.settings?.instance_url
    if (!raw) return null
    try {
        return new URL(raw).hostname.toLowerCase()
    } catch {
        return raw.replace(/^https?:\/\//, '').replace(/\/.*$/, '').toLowerCase() || null
    }
})

function isInstanceDomain(d: Domain): boolean {
    return !!instanceHostname.value && d.name.toLowerCase() === instanceHostname.value
}

const apexTagClass = computed(() => {
    if (props.zone.apex_verified) return 'p-tag-success'
    if (props.zone.apex_last_check) return 'p-tag-danger'
    return 'p-tag-neutral'
})

const apexLabel = computed(() => {
    if (props.zone.apex_verified) return t('domains.zones.badges.apex_verified')
    if (props.zone.apex_last_check) return t('domains.zones.badges.apex_failed')
    return t('domains.zones.badges.apex_pending')
})

const lastCheckLabel = computed(() => {
    const ts = props.zone.apex_last_check ?? props.zone.wildcard_last_check ?? null
    if (!ts) return t('domains.zones.card.never_checked')
    return t('domains.zones.card.last_check', { when: formatRelative(ts) })
})

const sortedDomains = computed(() => {
    return [...props.domains].sort((a, b) => {
        // apex first, then alphabetical
        const aIsApex = isApexDomain(a) ? 0 : 1
        const bIsApex = isApexDomain(b) ? 0 : 1
        if (aIsApex !== bIsApex) return aIsApex - bIsApex
        return a.name.localeCompare(b.name)
    })
})

function isApexDomain(d: Domain): boolean {
    return d.name.toLowerCase() === props.zone.apex.toLowerCase()
}

function onVerify() {
    emit('verify')
}
</script>

<style scoped>
.zone-card {
    border: 1px solid var(--p-content-border);
    border-radius: 14px;
    background: var(--p-content-bg);
    overflow: hidden;
    transition: border-color 120ms;
}
.zone-card:hover { border-color: var(--p-primary-300); }

.zone-card__head {
    display: grid;
    grid-template-columns: 1fr auto;
    grid-template-areas:
        "title actions"
        "notes actions"
        "meta actions";
    gap: 6px 16px;
    align-items: center;
    padding: 16px 18px;
    background: var(--p-surface-50);
    border-bottom: 1px solid var(--p-divider);
}
.zone-card__title-row {
    grid-area: title;
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
}
.zone-card__apex {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 14px;
    font-weight: 600;
    color: var(--p-text);
}
.zone-card__wildcard {
    display: inline-flex;
    align-items: center;
    gap: 4px;
}
.zone-card__notes {
    grid-area: notes;
    margin: 0;
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.45;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
}
.zone-card__head-meta {
    grid-area: meta;
    font-size: 12px;
}
.zone-card__head-actions {
    grid-area: actions;
    display: inline-flex;
    align-items: center;
    gap: 4px;
}

.zone-card__body {
    padding: 14px 18px 16px;
    display: flex;
    flex-direction: column;
    gap: 10px;
}
.zone-card__section-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
}
.zone-card__section-title {
    margin: 0;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-weight: 600;
    color: var(--p-text-muted);
}
.zone-card__empty {
    margin: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    color: var(--p-text-muted);
    font-size: 13px;
    padding: 6px 0 2px;
}

.domain-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
}
.domain-row {
    display: grid;
    grid-template-columns: minmax(160px, 1.2fr) 2fr auto;
    align-items: center;
    gap: 12px;
    padding: 8px 10px;
    border-radius: 10px;
    background: var(--p-surface-50);
    border: 1px solid transparent;
    cursor: pointer;
    text-decoration: none;
    color: inherit;
    transition: border-color 120ms, background 120ms;
}
.domain-row:hover {
    border-color: var(--p-primary-300);
    background: var(--p-content-bg);
}
.domain-row:focus-visible {
    outline: none;
    border-color: var(--p-primary-500);
    box-shadow: var(--p-focus-ring);
}
.domain-row__main {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    min-width: 0;
}
.domain-row__name {
    color: var(--p-text);
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.domain-row__meta {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    justify-content: flex-end;
}
.domain-row__app {
    font-size: 12px;
}
.domain-row__chevron {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: var(--p-text-muted);
    font-size: 12px;
}
.domain-row:hover .domain-row__chevron { color: var(--p-text); }

.mono { font-family: ui-monospace, Menlo, monospace; font-size: 13px; }
.muted { color: var(--p-text-muted); font-size: 12px; }

.pill {
    padding: 1px 8px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
}
.pill--success { background: rgba(16, 185, 129, 0.12); color: var(--p-primary-700); }
.pill--info { background: var(--p-info-bg); color: var(--p-info-text); }
/* Warn signals "needs attention" — used for the "unlinked" state so the
   operator notices the row exists in the DB but isn't routing anywhere. */
.pill--warn { background: var(--p-warning-bg); color: var(--p-warning-text); }
.pill--neutral {
    background: var(--p-neutral-bg);
    color: var(--p-text-muted);
}

@media (max-width: 720px) {
    .domain-row {
        grid-template-columns: 1fr;
        gap: 6px;
    }
    .domain-row__meta {
        justify-content: flex-start;
    }
    .domain-row__chevron { display: none; }
    .zone-card__head {
        grid-template-columns: 1fr;
        grid-template-areas:
            "title"
            "notes"
            "meta"
            "actions";
    }
    .zone-card__head-actions {
        justify-content: flex-end;
    }
}
</style>
