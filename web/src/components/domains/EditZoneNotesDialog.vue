<template>
    <Dialog
        :visible="visible"
        modal
        :header="t('domains.zones.edit_notes.title', { apex: zone?.apex ?? '' })"
        :style="{ width: '460px' }"
        :closable="!saving"
        @update:visible="onVisibleChange"
    >
        <div class="dialog-body">
            <SField :label="t('domains.zones.edit_notes.notes_label')">
                <Textarea v-model="notes" rows="4" auto-resize :placeholder="t('domains.zones.edit_notes.notes_placeholder')" />
            </SField>
        </div>
        <template #footer>
            <Button text :label="t('common.cancel')" :disabled="saving" @click="close" />
            <Button
                :label="t('domains.zones.edit_notes.save')"
                :loading="saving"
                @click="submit"
            />
        </template>
    </Dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import Textarea from 'primevue/textarea'
import SField from '@/components/settings/SField.vue'
import { useDNSZonesStore } from '@/stores/dnsZones'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { DNSZone } from '@/services/dnsZones'

const props = defineProps<{ visible: boolean; zone: DNSZone | null }>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; 'saved': [] }>()

const { t } = useI18n()
const zonesStore = useDNSZonesStore()

const notes = ref('')
const saving = ref(false)

watch(() => props.visible, (v) => {
    if (v) {
        notes.value = props.zone?.notes ?? ''
        saving.value = false
    }
})

function onVisibleChange(v: boolean) {
    if (!v) close()
}

function close() {
    if (saving.value) return
    emit('update:visible', false)
}

async function submit() {
    if (!props.zone) return
    saving.value = true
    try {
        await zonesStore.updateNotes(props.zone.id, notes.value)
        notify.success(t('domains.zones.edit_notes.saved'))
        emit('saved')
        emit('update:visible', false)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        saving.value = false
    }
}
</script>

<style scoped>
.dialog-body {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding-top: 8px;
}
:deep(.p-textarea) {
    width: 100%;
}
</style>
