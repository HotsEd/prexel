<template>
    <div class="domains-view">
        <header class="page-header">
            <div>
                <h1 class="page-header__title">{{ t('domains.title') }}</h1>
                <p class="page-header__sub">{{ t('domains.sub') }}</p>
            </div>
            <div class="page-header__actions">
                <Button
                    :label="t('domains.addDomain')"
                    icon="pi pi-plus"
                    @click="openWizard(null)"
                />
            </div>
        </header>

        <div v-if="loading" class="skeleton-list">
            <div v-for="i in 2" :key="i" class="skeleton-card" />
        </div>

        <template v-else>
            <EmptyState
                v-if="zones.length === 0"
                :title="t('domains.empty.title')"
                :body="t('domains.empty.body')"
            >
                <template #icon><IconNetwork :size="24" /></template>
                <template #actions>
                    <!-- Zones are never created standalone — they show up as
                         a side-effect of registering the first domain under
                         them. CTA points at the wizard, which auto-creates
                         the zone in step 4. -->
                    <Button
                        :label="t('domains.addDomain')"
                        icon="pi pi-plus"
                        @click="openWizard(null)"
                    />
                </template>
            </EmptyState>

            <template v-else>
                <div class="domains-toolbar">
                    <div class="domains-toolbar__search">
                        <i class="pi pi-search" />
                        <InputText
                            v-model="search"
                            :placeholder="t('domains.zones.search_placeholder')"
                        />
                    </div>
                </div>

                <div class="zone-list">
                    <ZoneCard
                        v-for="zone in visibleZones"
                        :key="zone.id"
                        :zone="zone"
                        :domains="domainsByZone[zone.id] ?? []"
                        :app-label="appLabel"
                        :verifying="verifyingId === zone.id"
                        @verify="verifyZone(zone)"
                        @edit-notes="openEditNotes(zone)"
                        @add-subdomain="openWizard(zone.id)"
                    />

                    <section v-if="orphanDomains.length" class="zone-card zone-card--orphan">
                        <header class="orphan-head">
                            <h3 class="orphan-head__title">{{ t('domains.zones.no_zone_group') }}</h3>
                            <span class="muted">{{ t('domains.zones.no_zone_hint') }}</span>
                        </header>
                        <ul class="domain-list">
                            <li v-for="d in orphanDomains" :key="d.id">
                                <RouterLink :to="`/domains/${d.id}`" class="domain-row">
                                    <div class="domain-row__main">
                                        <span class="mono">{{ d.name }}</span>
                                    </div>
                                    <div class="domain-row__meta">
                                        <span v-if="d.app_id">{{ appLabel(d.app_id) }}</span>
                                        <span v-else class="muted">{{ t('domains.instance_app') }}</span>
                                        <SslBadge :status="d.ssl_status" />
                                    </div>
                                    <div class="domain-row__chevron" aria-hidden="true">
                                        <i class="pi pi-chevron-right" />
                                    </div>
                                </RouterLink>
                            </li>
                        </ul>
                    </section>
                </div>
            </template>
        </template>

        <EditZoneNotesDialog
            v-model:visible="editNotesOpen"
            :zone="editingZone"
            @saved="onNotesSaved"
        />
    </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter, RouterLink } from 'vue-router'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import EmptyState from '@/components/EmptyState.vue'
import IconNetwork from '@/components/icons/IconNetwork.vue'
import SslBadge from '@/components/SslBadge.vue'
import ZoneCard from '@/components/domains/ZoneCard.vue'
import EditZoneNotesDialog from '@/components/domains/EditZoneNotesDialog.vue'
import { useDomainsStore } from '@/stores/domains'
import { useAppsStore } from '@/stores/apps'
import { useDNSZonesStore } from '@/stores/dnsZones'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { Domain } from '@/types/api'
import type { DNSZone } from '@/services/dnsZones'

const { t } = useI18n()
const router = useRouter()
const domainsStore = useDomainsStore()
const appsStore = useAppsStore()
const zonesStore = useDNSZonesStore()

const editNotesOpen = ref(false)
const editingZone = ref<DNSZone | null>(null)
const verifyingId = ref<string | null>(null)
const search = ref('')

const loading = computed(() => zonesStore.loading || domainsStore.loading)

const zones = computed(() => [...zonesStore.zones].sort((a, b) => a.apex.localeCompare(b.apex)))

const visibleZones = computed(() => {
    const q = search.value.trim().toLowerCase()
    if (!q) return zones.value
    return zones.value.filter((z) =>
        z.apex.toLowerCase().includes(q) ||
        (domainsByZone.value[z.id] ?? []).some((d) => d.name.toLowerCase().includes(q))
    )
})

const domainsByZone = computed<Record<string, Domain[]>>(() => {
    const map: Record<string, Domain[]> = {}
    for (const d of domainsStore.domains) {
        if (!d.zone_id) continue
        if (!map[d.zone_id]) map[d.zone_id] = []
        map[d.zone_id]!.push(d)
    }
    return map
})

const orphanDomains = computed<Domain[]>(() => {
    return domainsStore.domains.filter((d) => !d.zone_id)
})

onMounted(async () => {
    try {
        await Promise.all([
            zonesStore.load(true),
            domainsStore.fetchAll(),
            appsStore.fetchAll(),
        ])
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
})

function appLabel(appId: string | null | undefined): string {
    if (!appId) return t('domains.instance_app')
    return appsStore.byId[appId]?.name ?? appId
}

function openWizard(zoneId: string | null) {
    // Wizard lives on its own page now (/domains/new). When invoked from a
    // zone card we pass the zone id as a query string so the page boots into
    // "zone-locked" mode and skips the type-picker step.
    router.push({ path: '/domains/new', query: zoneId ? { zone: zoneId } : {} })
}

function openEditNotes(zone: DNSZone) {
    editingZone.value = zone
    editNotesOpen.value = true
}

async function verifyZone(zone: DNSZone) {
    verifyingId.value = zone.id
    try {
        await zonesStore.verify(zone.id)
        notify.success(t('domains.zones.card.verified_success'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        verifyingId.value = null
    }
}

function onNotesSaved() {
    // The store already updated; nothing else to do — but keep this hook for symmetry.
    editingZone.value = null
}
</script>

<style scoped>
.domains-view { width: 100%; }
.page-header__actions {
    display: inline-flex;
    align-items: center;
    gap: 8px;
}

.skeleton-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
}
.skeleton-card {
    height: 140px;
    border-radius: 14px;
    background: linear-gradient(90deg, var(--p-surface-50) 0%, var(--p-hover) 50%, var(--p-surface-50) 100%);
    background-size: 200% 100%;
    animation: skeleton 1.4s ease-in-out infinite;
}
@keyframes skeleton {
    0% { background-position: 200% 0; }
    100% { background-position: -200% 0; }
}

.domains-toolbar {
    display: flex;
    justify-content: flex-end;
    margin-bottom: 14px;
}
.domains-toolbar__search {
    position: relative;
    display: inline-flex;
    align-items: center;
    width: 280px;
    max-width: 100%;
}
.domains-toolbar__search i {
    position: absolute;
    left: 10px;
    color: var(--p-text-muted);
    font-size: 13px;
    pointer-events: none;
}
.domains-toolbar__search :deep(.p-inputtext) {
    width: 100%;
    padding-left: 30px;
}

.zone-list {
    display: flex;
    flex-direction: column;
    gap: 14px;
}

.zone-card--orphan {
    border: 1px dashed var(--p-content-border);
    border-radius: 14px;
    padding: 16px 18px;
    background: var(--p-content-bg);
}
.orphan-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 10px;
}
.orphan-head__title {
    margin: 0;
    font-size: 13px;
    font-weight: 600;
    color: var(--p-text);
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
.domain-row__main {
    display: inline-flex;
    align-items: center;
    gap: 8px;
}
.domain-row__meta {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    justify-content: flex-end;
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
</style>
