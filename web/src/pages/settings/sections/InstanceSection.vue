<template>
    <div v-if="loading && !settings" class="muted">{{ t('common.loading') }}</div>
    <div v-else-if="loadError" class="muted">{{ loadError }}</div>
    <template v-else-if="settings">
        <!--
            Three visual tiers on this page, in order of "how often will the
            operator touch this?":

            1. Essentials (always-visible cards at the top): identity,
               public address + cert, default resource limits. These are
               the only configs a brand-new instance really needs.

            2. Advanced (collapsed by default): cleanup loop. Has sane
               defaults that cover 99% of cases; surfacing the cron knob
               on first load is intimidating and rarely useful.

            3. Danger zone (footer, visually separated): maintenance mode.
               Same vocabulary used in DomainDetailView — destructive or
               site-wide impactful actions live at the bottom under their
               own heading so they don't compete with day-to-day configs.

            PerformanceForm (`max_concurrent_deploys`) was removed: the
            backend's deploy semaphore is allocated lazily and never
            resized, so the UI promised a live knob that only took effect
            after process restart. We'll bring it back when the semaphore
            is either dynamic or the UI gains an honest "requires restart"
            label.
        -->
        <InstanceInfoCard :settings="settings" :status="status" />
        <DomainTLSForm :settings="settings" @save="onSave" />
        <ResourceDefaultsForm :settings="settings" @save="onSave" />
        <BackupForm />

        <details class="advanced">
            <summary class="advanced__summary">
                <span class="advanced__title">{{ t('settings.instance.advanced.title') }}</span>
                <span class="advanced__hint">{{ t('settings.instance.advanced.hint') }}</span>
                <i class="pi pi-chevron-down advanced__chevron" aria-hidden="true" />
            </summary>
            <div class="advanced__body">
                <CleanupForm :settings="settings" @save="onSave" />
            </div>
        </details>

        <section class="danger-zone">
            <header class="danger-zone__head">
                <h2 class="danger-zone__title">{{ t('settings.instance.danger_zone.title') }}</h2>
                <p class="danger-zone__sub">{{ t('settings.instance.danger_zone.sub') }}</p>
            </header>
            <MaintenanceForm :settings="settings" @save="onSave" />
        </section>
    </template>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import axios from 'axios'
import InstanceInfoCard from '@/components/settings/instance/InstanceInfoCard.vue'
import DomainTLSForm from '@/components/settings/instance/DomainTLSForm.vue'
import ResourceDefaultsForm from '@/components/settings/instance/ResourceDefaultsForm.vue'
import BackupForm from '@/components/settings/instance/BackupForm.vue'
import CleanupForm from '@/components/settings/instance/CleanupForm.vue'
import MaintenanceForm from '@/components/settings/instance/MaintenanceForm.vue'
import { useInstanceStore } from '@/stores/instance'
import { fetchSetupStatus } from '@/composables/useSetupStatus'
import { apiErrorMessage } from '@/composables/useApi'
import { notify } from '@/lib/notify'
import type { InstanceUpdate } from '@/services/instance'
import type { SetupStatus } from '@/types/api'

const { t } = useI18n()
const store = useInstanceStore()
const status = ref<SetupStatus | null>(null)
const loadError = ref<string | null>(null)

const loading = computed(() => store.loading)
const settings = computed(() => store.settings)

type SaveDone = (err?: { field?: string; message?: string }) => void

// Maps backend error slugs to the sub-form's field id so the message can render
// inline next to the offending input. The backend uses `error` for the slug
// and `message` for the human text.
const FIELD_BY_ERROR: Record<string, string> = {
    letsencrypt_needs_url: 'instance_url',
    invalid_instance_url: 'instance_url',
    invalid_memory: 'memory',
    invalid_cpu: 'cpu',
    invalid_schedule: 'schedule',
    invalid_cron: 'schedule',
    invalid_threshold: 'threshold',
    invalid_retention: 'retention',
    maintenance_message_too_long: 'message',
}

onMounted(async () => {
    void loadStatus()
    await loadSettings()
})

async function loadSettings() {
    loadError.value = null
    try {
        await store.load()
    } catch (e) {
        loadError.value = apiErrorMessage(e)
    }
}

async function loadStatus() {
    try { status.value = await fetchSetupStatus() } catch { /* non-critical */ }
}

async function onSave(patch: InstanceUpdate, done: SaveDone) {
    try {
        await store.update(patch)
        notify.success(t('settings.instance.saved'))
        done()
    } catch (e) {
        const message = apiErrorMessage(e)
        let field: string | undefined
        if (axios.isAxiosError(e)) {
            const body = e.response?.data as { error?: string; message?: string } | undefined
            if (body?.error && FIELD_BY_ERROR[body.error]) {
                field = FIELD_BY_ERROR[body.error]
            }
        }
        if (!field) notify.error(message)
        done({ field, message })
    }
}
</script>

<style scoped>
.muted {
    color: var(--p-text-muted);
    font-size: 13px;
    padding: 12px 0;
}

/* ─────────────── Advanced collapse ─────────────── */
/*
    Native <details> styled to fit between the essential cards above and
    the danger zone below. Closed by default — only the summary row shows
    until the operator opts in. Same vocabulary as the DomainTLSForm
    advanced collapse so the page feels consistent.
*/
.advanced {
    margin-top: 24px;
    border: 1px dashed var(--p-content-border);
    border-radius: 12px;
    background: transparent;
    transition: border-color 120ms ease, background 120ms ease;
}
.advanced[open] {
    border-style: solid;
    border-color: color-mix(in srgb, var(--p-primary-500), transparent 75%);
    background: var(--p-content-bg);
}
.advanced__summary {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 14px 18px;
    cursor: pointer;
    list-style: none;
    user-select: none;
}
.advanced__summary::-webkit-details-marker { display: none; }
.advanced__title {
    font-size: 13px;
    font-weight: 700;
    color: var(--p-text);
    text-transform: uppercase;
    letter-spacing: 0.06em;
}
.advanced__hint {
    flex: 1;
    font-size: 12px;
    color: var(--p-text-muted);
    min-width: 0;
}
.advanced__chevron {
    color: var(--p-text-muted);
    font-size: 12px;
    transition: transform 160ms ease;
}
.advanced[open] .advanced__chevron {
    transform: rotate(180deg);
}
.advanced__body {
    padding: 4px 18px 18px;
}
/* Strip the inner SCard's outer margin so it sits flush inside the body. */
.advanced__body :deep(> :first-child) { margin-top: 0; }

/* ─────────────── Danger zone ─────────────── */
/*
    Visual separation from the essentials. A short rule + a small caps
    heading is enough — we don't need a full SCard wrapping the section
    because the MaintenanceForm itself already brings the red-tinted card.
*/
.danger-zone {
    margin-top: 40px;
    padding-top: 24px;
    border-top: 1px solid var(--p-content-border);
}
.danger-zone__head {
    margin-bottom: 12px;
}
.danger-zone__title {
    margin: 0;
    font-size: 11px;
    font-weight: 800;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--p-danger);
}
.danger-zone__sub {
    margin: 4px 0 0;
    font-size: 12px;
    color: var(--p-text-muted);
}
</style>
