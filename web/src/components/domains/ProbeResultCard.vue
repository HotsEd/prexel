<template>
    <article :class="['probe', toneClass]">
        <header class="probe__head">
            <span class="probe__title">{{ title }}</span>
            <div class="probe__badge">
                <component :is="iconFor()" :size="14" />
                {{ statusLabel }}
            </div>
        </header>
        <div class="probe__host mono">{{ host }}</div>
        <dl v-if="hasDetails" class="probe__details">
            <template v-if="result.probed">
                <dt>{{ t('domains.wizard.step4.probed') }}</dt>
                <dd class="mono">{{ result.probed }}</dd>
            </template>
            <template v-if="result.resolved_to?.length">
                <dt>{{ t('domains.wizard.step4.resolved_to') }}</dt>
                <dd class="mono">{{ result.resolved_to.join(', ') }}</dd>
            </template>
            <template v-if="result.expected">
                <dt>{{ t('domains.wizard.step4.expected') }}</dt>
                <dd class="mono">{{ result.expected }}</dd>
            </template>
        </dl>
        <p v-if="result.error" class="probe__error">{{ result.error }}</p>
    </article>
</template>

<script setup lang="ts">
/*
    Single probe outcome card used by Step 4 of the add-domain wizard.
    Two of these render side-by-side: one for the apex A check, one for
    the wildcard random-subdomain probe.

    The card chrome is driven by `result.ok` + presence of resolved_to /
    error / expected, mapping to four visual tones:
      - probe--ok       green badge, success border
      - probe--warn     amber (resolved but wrong IP)
      - probe--err      red (real DNS error / timeout)
      - probe--pending  neutral grey (NXDOMAIN or never probed)
*/
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import IconCheck from '@/components/icons/IconCheck.vue'
import IconAlert from '@/components/icons/IconAlert.vue'
import IconClose from '@/components/icons/IconClose.vue'
import type { ProbeResult } from '@/services/dnsZones'

const props = defineProps<{
    title: string
    host: string
    result: ProbeResult
}>()

const { t } = useI18n()

const toneClass = computed(() => {
    if (props.result.ok) return 'probe--ok'
    if (props.result.resolved_to?.length && props.result.expected && !props.result.ok) return 'probe--warn'
    if (props.result.error) return 'probe--err'
    return 'probe--pending'
})

const statusLabel = computed(() => {
    if (props.result.ok) return t('domains.wizard.step4.status_ok')
    if (props.result.resolved_to?.length && props.result.expected) return t('domains.wizard.step4.status_wrong_ip')
    if (props.result.error) return t('domains.wizard.step4.status_error')
    return t('domains.wizard.step4.status_nxdomain')
})

function iconFor() {
    if (props.result.ok) return IconCheck
    if (props.result.error || (props.result.resolved_to?.length && !props.result.ok)) return IconAlert
    return IconClose
}

const hasDetails = computed(() =>
    !!props.result.probed
    || !!props.result.resolved_to?.length
    || !!props.result.expected,
)
</script>

<style scoped>
.probe {
    --tone: var(--p-text-muted);
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 18px 18px 16px;
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    background: var(--p-content-bg);
    position: relative;
    overflow: hidden;
    transition: border-color 160ms ease;
}
/* Coloured stripe on the left edge, drawn with a pseudo so it can fade. */
.probe::before {
    content: '';
    position: absolute;
    left: 0; top: 0; bottom: 0;
    width: 3px;
    background: var(--tone);
}

.probe--ok      { --tone: var(--p-success); border-color: color-mix(in srgb, var(--p-success), transparent 65%); }
.probe--warn    { --tone: var(--p-warning); border-color: color-mix(in srgb, var(--p-warning), transparent 65%); }
.probe--err     { --tone: var(--p-danger);  border-color: color-mix(in srgb, var(--p-danger),  transparent 60%); }
.probe--pending { --tone: var(--p-text-muted); }

.probe__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
}
.probe__title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--p-text-muted);
}
.probe__badge {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 8px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
    background: color-mix(in srgb, var(--tone), transparent 85%);
    color: var(--tone);
}

.probe__host {
    font-size: 15px;
    font-weight: 600;
    color: var(--p-text);
    word-break: break-all;
}

.probe__details {
    display: grid;
    grid-template-columns: 86px 1fr;
    row-gap: 4px;
    column-gap: 12px;
    margin: 0;
    font-size: 12px;
}
.probe__details dt {
    color: var(--p-text-muted);
    text-transform: uppercase;
    font-size: 10px;
    letter-spacing: 0.06em;
    align-self: center;
}
.probe__details dd {
    margin: 0;
    color: var(--p-text);
    word-break: break-all;
}

.probe__error {
    margin: 0;
    padding: 8px 10px;
    border-radius: 8px;
    background: color-mix(in srgb, var(--p-danger), transparent 90%);
    color: var(--p-danger);
    font-size: 12px;
    line-height: 1.4;
}
</style>
