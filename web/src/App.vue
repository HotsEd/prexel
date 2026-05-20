<template>
    <RouterView />
    <ConfirmDialog />
    <Toast>
        <template #message="slotProps">
            <div :class="['activity-icon', toneFor(slotProps.message.severity)]">
                <component :is="iconFor(slotProps.message.severity)" :size="16" :stroke-width="2.25" />
            </div>
            <div class="p-toast-message-text">
                <span class="p-toast-summary">{{ slotProps.message.summary }}</span>
                <div v-if="slotProps.message.detail" class="p-toast-detail">{{ slotProps.message.detail }}</div>
            </div>
        </template>
    </Toast>
</template>

<script setup lang="ts">
import { onMounted, watch, type Component } from 'vue'
import Toast from 'primevue/toast'
import ConfirmDialog from 'primevue/confirmdialog'
import { usePrimeVue } from 'primevue/config'
import { useToast } from 'primevue/usetoast'
import { setToast } from '@/lib/notify'
import { primeVueLocale } from '@/i18n/primevue'
import { usePreferencesStore } from '@/stores/preferences'
import IconCheck from '@/components/icons/IconCheck.vue'
import IconInfo from '@/components/icons/IconInfo.vue'
import IconClock from '@/components/icons/IconClock.vue'
import IconAlert from '@/components/icons/IconAlert.vue'

const toast = useToast()
const primevue = usePrimeVue()
const preferences = usePreferencesStore()

onMounted(() => setToast(toast))

watch(() => preferences.locale, (locale) => {
    primevue.config.locale = primeVueLocale(locale)
}, { immediate: true })

function toneFor(severity?: string): string {
    switch (severity) {
        case 'success': return 'green'
        case 'warn': return 'amber'
        case 'error': return 'red'
        case 'info':
        default: return 'primary'
    }
}

function iconFor(severity?: string): Component {
    switch (severity) {
        case 'success': return IconCheck
        case 'warn': return IconClock
        case 'error': return IconAlert
        case 'info':
        default: return IconInfo
    }
}
</script>
