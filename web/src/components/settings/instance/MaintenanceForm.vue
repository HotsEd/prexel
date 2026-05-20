<template>
    <SCard
        :title="t('settings.instance.maintenance.title')"
        :sub="t('settings.instance.maintenance.sub')"
        :class="cardClasses"
    >
        <form class="form" @submit.prevent="onAttemptSave" novalidate>
            <label class="toggle-row">
                <ToggleSwitch v-model="enabled" />
                <span>
                    <strong>{{ t('settings.instance.maintenance.enabled') }}</strong>
                    <small>{{ enabled ? t('settings.instance.maintenance.enabled_on') : t('settings.instance.maintenance.enabled_off') }}</small>
                </span>
            </label>

            <SField
                :label="t('settings.instance.maintenance.message_label')"
                :hint="messageHint"
                :error="messageError ?? messageServerError ?? undefined"
            >
                <Textarea
                    v-model="message"
                    rows="3"
                    auto-resize
                    maxlength="200"
                    :placeholder="t('settings.instance.maintenance.message_placeholder')"
                />
            </SField>

            <Message severity="warn" :closable="false">
                <i18n-t keypath="settings.instance.maintenance.warning" tag="span">
                    <template #strong>
                        <strong>{{ t('settings.instance.maintenance.warning_strong') }}</strong>
                    </template>
                </i18n-t>
            </Message>

            <div class="actions">
                <Button
                    type="submit"
                    :label="t('settings.instance.maintenance.save')"
                    :severity="willActivate ? 'danger' : undefined"
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
import { useConfirm } from 'primevue/useconfirm'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Textarea from 'primevue/textarea'
import ToggleSwitch from 'primevue/toggleswitch'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import type { InstanceSettings, InstanceUpdate } from '@/services/instance'

const props = defineProps<{
    settings: InstanceSettings
}>()

const emit = defineEmits<{
    save: [patch: InstanceUpdate, done: (err?: { field?: 'message'; message?: string }) => void]
}>()

const { t } = useI18n()
const confirm = useConfirm()

const enabled = ref(props.settings.maintenance_mode)
const message = ref(props.settings.maintenance_message)
const saving = ref(false)
const messageServerError = ref<string | null>(null)

watch(() => props.settings, (s) => {
    enabled.value = s.maintenance_mode
    message.value = s.maintenance_message
    messageServerError.value = null
}, { deep: true })

watch(message, () => { messageServerError.value = null })

const messageError = computed<string | null>(() => {
    if (message.value.length > 200) return t('settings.instance.maintenance.message_too_long')
    return null
})

const messageHint = computed(() => {
    return t('settings.instance.maintenance.message_count', { n: message.value.length })
})

const hasChanges = computed(() => {
    return enabled.value !== props.settings.maintenance_mode
        || message.value !== props.settings.maintenance_message
})

const willActivate = computed(() => enabled.value && !props.settings.maintenance_mode)

// Card chrome states:
//   --on      maintenance is persisted as ON (red border, "live")
//   --staged  user flipped switch ON locally, not yet saved (amber stripe)
//   neither   neutral, blends with other settings cards
const cardClasses = computed(() => [
    'maintenance-card',
    props.settings.maintenance_mode && 'maintenance-card--on',
    !props.settings.maintenance_mode && enabled.value && 'maintenance-card--staged',
])

const canSave = computed(() => {
    if (!hasChanges.value) return false
    if (messageError.value) return false
    return true
})

function onAttemptSave() {
    if (!canSave.value) return
    if (willActivate.value) {
        confirm.require({
            header: t('settings.instance.maintenance.confirm_title'),
            message: t('settings.instance.maintenance.confirm_body'),
            icon: 'pi pi-exclamation-triangle',
            rejectLabel: t('common.cancel'),
            acceptLabel: t('settings.instance.maintenance.confirm_accept'),
            acceptClass: 'p-button-danger',
            accept: () => { doSave() },
        })
        return
    }
    doSave()
}

function doSave() {
    saving.value = true
    messageServerError.value = null
    const patch: InstanceUpdate = {
        maintenance_mode: enabled.value,
        maintenance_message: message.value,
    }
    emit('save', patch, (err) => {
        saving.value = false
        if (err) messageServerError.value = err.message ?? t('errors.validation')
    })
}
</script>

<style scoped>
.maintenance-card {
    /* Default: blends with the rest of the form. The inline <Message
       severity="warn"> already calls out the section as sensitive — a
       permanently amber border was alarming even when nothing was wrong.
       We only paint the card now when:
         - the user staged a "turn ON" change (amber stripe, "pending"),
         - or maintenance is actually persisted as ON (red border, "live"). */
    transition: border-color 160ms ease, box-shadow 160ms ease;
}
.maintenance-card--staged {
    box-shadow: inset 3px 0 0 rgba(245, 158, 11, 0.85);
}
.maintenance-card--on {
    border-color: rgba(220, 38, 38, 0.65);
    box-shadow: inset 3px 0 0 rgba(220, 38, 38, 0.9);
}
.form {
    display: flex;
    flex-direction: column;
    gap: 16px;
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
.actions {
    display: flex;
    justify-content: flex-end;
}
:deep(.p-textarea) {
    width: 100%;
}
</style>
