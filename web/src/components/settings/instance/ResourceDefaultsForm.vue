<template>
    <SCard :title="t('settings.instance.defaults.title')" :sub="t('settings.instance.defaults.sub')">
        <form class="form" @submit.prevent="onSave" novalidate>
            <div class="grid-2">
                <SField
                    :label="t('settings.instance.defaults.memory_label')"
                    :hint="t('settings.instance.defaults.memory_hint')"
                    :error="memoryError ?? memoryServerError ?? undefined"
                >
                    <InputText
                        v-model="memory"
                        :placeholder="t('settings.instance.defaults.memory_placeholder')"
                        autocomplete="off"
                        spellcheck="false"
                    />
                </SField>

                <SField
                    :label="t('settings.instance.defaults.cpu_label')"
                    :hint="t('settings.instance.defaults.cpu_hint')"
                    :error="cpuError ?? cpuServerError ?? undefined"
                >
                    <InputText
                        v-model="cpu"
                        :placeholder="t('settings.instance.defaults.cpu_placeholder')"
                        autocomplete="off"
                        spellcheck="false"
                    />
                </SField>
            </div>

            <p class="muted">{{ t('settings.instance.defaults.hint') }}</p>

            <div class="actions">
                <Button
                    type="submit"
                    :label="t('settings.instance.defaults.save')"
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
import InputText from 'primevue/inputtext'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import type { InstanceSettings, InstanceUpdate } from '@/services/instance'

const props = defineProps<{
    settings: InstanceSettings
}>()

const emit = defineEmits<{
    save: [patch: InstanceUpdate, done: (err?: { field?: 'memory' | 'cpu'; message?: string }) => void]
}>()

const { t } = useI18n()

const memory = ref(props.settings.default_memory_limit)
const cpu = ref(props.settings.default_cpu_limit)
const saving = ref(false)
const memoryServerError = ref<string | null>(null)
const cpuServerError = ref<string | null>(null)

watch(() => props.settings, (s) => {
    memory.value = s.default_memory_limit
    cpu.value = s.default_cpu_limit
    memoryServerError.value = null
    cpuServerError.value = null
}, { deep: true })

watch(memory, () => { memoryServerError.value = null })
watch(cpu, () => { cpuServerError.value = null })

const MEMORY_RE = /^\d+(\.\d+)?[mgkbMGKB]?$/
const CPU_RE = /^\d+(\.\d+)?$/

const memoryError = computed<string | null>(() => {
    const v = memory.value.trim()
    if (!v) return null
    if (!MEMORY_RE.test(v)) return t('settings.instance.defaults.memory_invalid')
    return null
})

const cpuError = computed<string | null>(() => {
    const v = cpu.value.trim()
    if (!v) return null
    if (!CPU_RE.test(v)) return t('settings.instance.defaults.cpu_invalid')
    if (parseFloat(v) <= 0) return t('settings.instance.defaults.cpu_invalid')
    return null
})

const hasChanges = computed(() => {
    return memory.value.trim() !== props.settings.default_memory_limit
        || cpu.value.trim() !== props.settings.default_cpu_limit
})

const canSave = computed(() => {
    if (!hasChanges.value) return false
    if (memoryError.value || cpuError.value) return false
    return true
})

function onSave() {
    if (!canSave.value) return
    saving.value = true
    memoryServerError.value = null
    cpuServerError.value = null
    const patch: InstanceUpdate = {
        default_memory_limit: memory.value.trim(),
        default_cpu_limit: cpu.value.trim(),
    }
    emit('save', patch, (err) => {
        saving.value = false
        if (!err) return
        if (err.field === 'memory') {
            memoryServerError.value = err.message ?? t('errors.validation')
        } else if (err.field === 'cpu') {
            cpuServerError.value = err.message ?? t('errors.validation')
        } else {
            memoryServerError.value = err.message ?? t('errors.validation')
        }
    })
}
</script>

<style scoped>
.form {
    display: flex;
    flex-direction: column;
    gap: 14px;
}
.grid-2 {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
}
@media (max-width: 720px) {
    .grid-2 { grid-template-columns: 1fr; }
}
.muted { color: var(--p-text-muted); font-size: 12px; line-height: 1.5; margin: 0; }
.actions {
    display: flex;
    justify-content: flex-end;
}
</style>
