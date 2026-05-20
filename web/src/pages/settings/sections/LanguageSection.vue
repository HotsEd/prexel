<template>
    <SCard :title="t('settings.language.title')" :sub="t('settings.language.sub')">
        <SField :label="t('settings.language.field')">
            <UiSelect
                :model-value="preferences.locale"
                :options="LOCALE_OPTIONS"
                @update:modelValue="changeLocale"
            />
        </SField>
    </SCard>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import UiSelect from '@/components/ui/UiSelect.vue'
import { LOCALE_OPTIONS, normalizeLocale, type Locale } from '@/i18n'
import { notify } from '@/lib/notify'
import { usePreferencesStore } from '@/stores/preferences'

const { t } = useI18n()
const preferences = usePreferencesStore()

function changeLocale(value: string | number | null) {
    preferences.setLocale(normalizeLocale(String(value)))
    notify.success(t('settings.language.saved'))
}
</script>

<style scoped>
:deep(.ui-select) { width: min(360px, 100%); }
</style>
