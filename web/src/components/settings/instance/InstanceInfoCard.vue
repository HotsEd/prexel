<template>
    <SCard :title="t('settings.instance.info.title')" :sub="t('settings.instance.info.sub')">
        <div class="grid-meta">
            <div>
                <div class="key">{{ t('settings.instance.info.instance_id') }}</div>
                <div class="val mono row">
                    <span class="row__text">{{ status?.instance_id || '—' }}</span>
                    <Button
                        v-if="status?.instance_id"
                        text
                        size="small"
                        icon="pi pi-copy"
                        :aria-label="t('common.copy')"
                        @click="copyId"
                    />
                </div>
            </div>
            <div>
                <div class="key">{{ t('settings.instance.info.version') }}</div>
                <div class="val mono version-row">
                    <span class="row__text">{{ status?.version || '—' }}</span>
                    <!--
                        Passive update awareness. Renders only when:
                          (a) the GitHub-releases cache produced a tag,
                          (b) the running version is unambiguously older.
                        On any failure / parity / unknown state we say
                        nothing — better to hide than to nag.
                    -->
                    <a
                        v-if="updateAvailable && latest.url"
                        :href="latest.url"
                        target="_blank"
                        rel="noopener"
                        class="update-pill"
                        :title="t('settings.instance.info.update_tooltip', { latest: latest.version })"
                    >
                        <i class="pi pi-arrow-up update-pill__icon" aria-hidden="true" />
                        <span>{{ t('settings.instance.info.update_available', { v: latest.version }) }}</span>
                    </a>
                </div>
            </div>
            <div>
                <div class="key">{{ t('settings.instance.info.tls_mode') }}</div>
                <div class="val">
                    <span class="p-tag" :class="tlsTagClass">
                        <span class="p-tag-dot" />
                        {{ tlsLabel }}
                    </span>
                </div>
            </div>
            <div>
                <div class="key">{{ t('settings.instance.info.updated_at') }}</div>
                <div class="val">{{ updatedAt }}</div>
            </div>
        </div>
    </SCard>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import SCard from '@/components/settings/SCard.vue'
import { notify } from '@/lib/notify'
import { formatDateTime } from '@/utils/format'
import { instanceService, type InstanceSettings, type LatestVersion } from '@/services/instance'
import type { SetupStatus } from '@/types/api'

const props = defineProps<{
    settings: InstanceSettings
    status: SetupStatus | null
}>()

const { t } = useI18n()

const tlsLabel = computed(() => {
    return props.settings.tls_mode === 'letsencrypt'
        ? t('settings.instance.domain.cert_letsencrypt')
        : t('settings.instance.domain.cert_self_signed')
})

const tlsTagClass = computed(() => {
    return props.settings.tls_mode === 'letsencrypt' ? 'p-tag-success' : 'p-tag-neutral'
})

const updatedAt = computed(() => {
    if (!props.settings.updated_at) return '—'
    return formatDateTime(props.settings.updated_at)
})

function copyId() {
    const id = props.status?.instance_id
    if (!id) return
    navigator.clipboard.writeText(id).then(() => notify.success(t('common.copied')))
}

// ── Update awareness ───────────────────────────────────────────────
// Best-effort, fire-and-forget. Failures don't surface anywhere — the
// computed `updateAvailable` only flips true when we have enough info
// to be sure.
const latest = ref<LatestVersion>({ version: '', url: '', published_at: '' })

onMounted(async () => {
    try {
        latest.value = await instanceService.latestVersion()
    } catch {
        // Silent. Either backend not wired or GitHub unreachable; either
        // way we just won't show the badge.
    }
})

const updateAvailable = computed(() => {
    const running = props.status?.version || ''
    const remote = latest.value.version
    if (!running || !remote) return false
    return compareSemver(remote, running) > 0
})

/*
    Deliberately tiny semver-ish comparator. Splits "1.2.3" into
    numeric parts, ignores any pre-release suffix (so 0.2.0 is
    considered newer than 0.1.0-dev). Good enough for the "should I
    nag?" question; we'd reach for `compare-versions` only if the
    decision actually mattered for safety.
*/
function compareSemver(a: string, b: string): number {
    const pa = a.replace(/^v/, '').split('-')[0].split('.').map(Number)
    const pb = b.replace(/^v/, '').split('-')[0].split('.').map(Number)
    for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
        const xa = pa[i] || 0
        const xb = pb[i] || 0
        if (xa !== xb) return xa - xb
    }
    return 0
}
</script>

<style scoped>
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
.val { font-size: 14px; color: var(--p-text); }
.mono { font-family: ui-monospace, Menlo, monospace; font-size: 12px; }
.row {
    display: inline-flex;
    align-items: center;
    gap: 4px;
}
.row__text {
    word-break: break-all;
}

/* ── Update badge ── small, non-pushy pill next to the running version */
.version-row {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
}
.update-pill {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 2px 8px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
    font-family: var(--p-font-family, system-ui);
    text-decoration: none;
    color: var(--p-primary-500);
    background: color-mix(in srgb, var(--p-primary-500), transparent 88%);
    border: 1px solid color-mix(in srgb, var(--p-primary-500), transparent 70%);
    transition: background 120ms ease, color 120ms ease;
}
.update-pill:hover {
    background: color-mix(in srgb, var(--p-primary-500), transparent 78%);
    color: var(--p-primary-500);
    text-decoration: none;
}
.update-pill__icon {
    font-size: 10px;
}
</style>
