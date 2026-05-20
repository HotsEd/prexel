<template>
    <SCard :title="t('settings.instance.cleanup.title')" :sub="t('settings.instance.cleanup.sub')">
        <form class="form" @submit.prevent="onSave" novalidate>
            <label class="toggle-row">
                <ToggleSwitch v-model="enabled" />
                <span>
                    <strong>{{ t('settings.instance.cleanup.enabled') }}</strong>
                    <small>{{ t('settings.instance.cleanup.enabled_hint') }}</small>
                </span>
            </label>

            <div class="grid-2">
                <SField
                    :label="t('settings.instance.cleanup.schedule_label')"
                    :hint="t('settings.instance.cleanup.schedule_hint')"
                    :error="scheduleError ?? scheduleServerError ?? undefined"
                >
                    <InputText
                        v-model="schedule"
                        :placeholder="t('settings.instance.cleanup.schedule_placeholder')"
                        :disabled="!enabled"
                        autocomplete="off"
                        spellcheck="false"
                        class="mono"
                    />
                </SField>

                <SField
                    :label="t('settings.instance.cleanup.retention_label')"
                    :hint="t('settings.instance.cleanup.retention_hint')"
                    :error="retentionServerError ?? undefined"
                >
                    <InputNumber
                        v-model="retention"
                        :min="1"
                        :max="100"
                        :use-grouping="false"
                        show-buttons
                        fluid
                    />
                </SField>
            </div>

            <SField
                :label="t('settings.instance.cleanup.threshold_label')"
                :hint="t('settings.instance.cleanup.threshold_hint')"
                :error="thresholdServerError ?? undefined"
            >
                <div class="slider-row">
                    <Slider
                        v-model="threshold"
                        :min="0"
                        :max="100"
                        class="slider"
                    />
                    <InputNumber
                        v-model="threshold"
                        :min="0"
                        :max="100"
                        :use-grouping="false"
                        suffix=" %"
                        class="threshold-input"
                    />
                </div>
            </SField>

            <div class="actions">
                <Button
                    type="submit"
                    :label="t('settings.instance.cleanup.save')"
                    :loading="saving"
                    :disabled="!canSave"
                />
            </div>
        </form>
    </SCard>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import InputNumber from 'primevue/inputnumber'
import InputText from 'primevue/inputtext'
import Slider from 'primevue/slider'
import ToggleSwitch from 'primevue/toggleswitch'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import type { InstanceSettings, InstanceUpdate } from '@/services/instance'

type CleanupField = 'schedule' | 'threshold' | 'retention'

const props = defineProps<{
    settings: InstanceSettings
}>()

const emit = defineEmits<{
    save: [patch: InstanceUpdate, done: (err?: { field?: CleanupField; message?: string }) => void]
}>()

const { t } = useI18n()

const enabled = ref(props.settings.cleanup_enabled)
const schedule = ref(props.settings.cleanup_schedule)
const threshold = ref<number>(props.settings.cleanup_disk_threshold)
const retention = ref<number>(props.settings.cleanup_image_retention)
const saving = ref(false)
const scheduleServerError = ref<string | null>(null)
const thresholdServerError = ref<string | null>(null)
const retentionServerError = ref<string | null>(null)

watch(() => props.settings, (s) => {
    enabled.value = s.cleanup_enabled
    schedule.value = s.cleanup_schedule
    threshold.value = s.cleanup_disk_threshold
    retention.value = s.cleanup_image_retention
    scheduleServerError.value = null
    thresholdServerError.value = null
    retentionServerError.value = null
}, { deep: true })

watch(schedule, () => { scheduleServerError.value = null })
watch(threshold, () => { thresholdServerError.value = null })
watch(retention, () => { retentionServerError.value = null })

// Client-side cron sanity check — accepts 5 fields where each field is either
// "*" or a literal integer. Step (*/n) and range (a-b) are NOT supported by
// the v0.1 runner; we surface that early.
const CRON_FIELD = /^(\*|\d+)$/
const scheduleError = computed<string | null>(() => {
    if (!enabled.value) return null
    const v = schedule.value.trim()
    if (!v) return t('settings.instance.cleanup.schedule_required')
    const parts = v.split(/\s+/)
    if (parts.length !== 5) return t('settings.instance.cleanup.schedule_invalid')
    for (const p of parts) {
        if (!CRON_FIELD.test(p)) return t('settings.instance.cleanup.schedule_invalid')
    }
    return null
})

const hasChanges = computed(() => {
    return enabled.value !== props.settings.cleanup_enabled
        || schedule.value.trim() !== props.settings.cleanup_schedule
        || threshold.value !== props.settings.cleanup_disk_threshold
        || retention.value !== props.settings.cleanup_image_retention
})

const canSave = computed(() => {
    if (!hasChanges.value) return false
    if (scheduleError.value) return false
    if (threshold.value < 0 || threshold.value > 100) return false
    if (retention.value < 1) return false
    return true
})

function onSave() {
    if (!canSave.value) return
    saving.value = true
    scheduleServerError.value = null
    thresholdServerError.value = null
    retentionServerError.value = null
    const patch: InstanceUpdate = {
        cleanup_enabled: enabled.value,
        cleanup_schedule: schedule.value.trim(),
        cleanup_disk_threshold: threshold.value,
        cleanup_image_retention: retention.value,
    }
    emit('save', patch, (err) => {
        saving.value = false
        if (!err) return
        if (err.field === 'threshold') {
            thresholdServerError.value = err.message ?? t('errors.validation')
        } else if (err.field === 'retention') {
            retentionServerError.value = err.message ?? t('errors.validation')
        } else {
            scheduleServerError.value = err.message ?? t('errors.validation')
        }
    })
}
</script>

<style scoped>
.form {
    display: flex;
    flex-direction: column;
    gap: 18px;
}
.toggle-row {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    cursor: pointer;
}
.toggle-row span {
    display: flex;
    flex-direction: column;
    gap: 2px;
}
.toggle-row strong {
    font-size: 13px;
    font-weight: 600;
    color: var(--p-text);
}
.toggle-row small {
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.4;
}
.grid-2 {
    display: grid;
    grid-template-columns: 1.4fr 1fr;
    gap: 16px;
}
@media (max-width: 720px) {
    .grid-2 { grid-template-columns: 1fr; }
}
.mono :deep(input) {
    font-family: ui-monospace, Menlo, monospace;
}
.slider-row {
    display: flex;
    align-items: center;
    gap: 16px;
}
.slider {
    flex: 1;
}
.threshold-input {
    width: 100px;
}
.threshold-input :deep(input) {
    text-align: right;
}
.actions {
    display: flex;
    justify-content: flex-end;
}
</style>
