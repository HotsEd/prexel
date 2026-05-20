<template>
    <div class="tfa-prompt">
        <div class="tfa-prompt__head">
            <div class="tfa-prompt__icon">
                <IconSmartphone v-if="activeMethod === 'app'" :size="18" />
                <IconMail v-else :size="18" />
            </div>
            <div>
                <div class="tfa-prompt__title">{{ titleFor(activeMethod) }}</div>
                <div class="tfa-prompt__body">{{ bodyFor(activeMethod) }}</div>
            </div>
        </div>

        <SField
            v-if="activeMethod !== 'recovery'"
            :label="t('twoFactorPrompt.codeLabel')"
            :error="error ? t('twoFactorPrompt.invalidCode') : undefined"
        >
            <OtpInput
                v-model="code"
                :state="error ? 'error' : 'idle'"
                @complete="onComplete"
            />
        </SField>
        <SField
            v-else
            :label="t('twoFactorPrompt.recoveryLabel')"
            :error="error ? t('twoFactorPrompt.invalidCode') : undefined"
        >
            <input
                v-model="recoveryCode"
                class="p-inputtext tfa-prompt__recovery"
                :placeholder="t('twoFactorPrompt.recoveryPlaceholder')"
                autocomplete="one-time-code"
                @keyup.enter="onComplete(recoveryCode)"
            />
        </SField>

        <div v-if="alternativeLinks.length" class="tfa-prompt__links">
            <button
                v-for="alt in alternativeLinks"
                :key="alt"
                type="button"
                class="tfa-prompt__link"
                @click="switchTo(alt)"
            >
                {{ linkLabel(alt) }}
            </button>
        </div>
    </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import OtpInput from '@/components/auth/OtpInput.vue'
import SField from '@/components/settings/SField.vue'
import IconSmartphone from '@/components/icons/IconSmartphone.vue'
import IconMail from '@/components/icons/IconMail.vue'
import type { TwoFactorMethod } from '@/types/api'

const props = withDefaults(defineProps<{
    methods: TwoFactorMethod[]
    emailHint?: string | null
    error?: boolean
}>(), { emailHint: null, error: false })

const emit = defineEmits<{
    complete: [code: string, method: TwoFactorMethod]
    'update:error': [value: boolean]
}>()

const { t } = useI18n()

const activeMethod = ref<TwoFactorMethod>(props.methods[0] ?? 'app')
const code = ref('')
const recoveryCode = ref('')

watch(activeMethod, () => {
    code.value = ''
    recoveryCode.value = ''
    emit('update:error', false)
})

watch(() => props.error, (next) => {
    if (next) {
        code.value = ''
        recoveryCode.value = ''
    }
})

const alternativeLinks = computed<TwoFactorMethod[]>(() => {
    const others = new Set<TwoFactorMethod>()
    for (const m of props.methods) {
        if (m !== activeMethod.value) others.add(m)
    }
    if (activeMethod.value !== 'recovery' && props.methods.length > 0) {
        others.add('recovery')
    }
    return Array.from(others)
})

function linkLabel(method: TwoFactorMethod): string {
    if (method === 'app') return t('twoFactorPrompt.useApp')
    if (method === 'email') return t('twoFactorPrompt.useEmail')
    return t('twoFactorPrompt.useRecovery')
}

function titleFor(method: TwoFactorMethod): string {
    if (method === 'app') return t('twoFactorPrompt.appTitle')
    if (method === 'email') return t('twoFactorPrompt.emailTitle')
    return t('twoFactorPrompt.recoveryTitle')
}

function bodyFor(method: TwoFactorMethod): string {
    if (method === 'app') return t('twoFactorPrompt.appBody')
    if (method === 'email') {
        return props.emailHint
            ? t('twoFactorPrompt.emailBodyHinted', { email: props.emailHint })
            : t('twoFactorPrompt.emailBody')
    }
    return t('twoFactorPrompt.recoveryBody')
}

function switchTo(method: TwoFactorMethod) {
    activeMethod.value = method
    emit('update:error', false)
}

function onComplete(value: string) {
    const trimmed = value.trim()
    if (activeMethod.value === 'recovery') {
        if (trimmed.length < 4) return
        emit('complete', trimmed, 'recovery')
        return
    }
    if (trimmed.length < 6) return
    emit('complete', trimmed, activeMethod.value)
}
</script>

<style scoped>
.tfa-prompt { display: flex; flex-direction: column; gap: 14px; }
.tfa-prompt__head { display: flex; align-items: flex-start; gap: 12px; }
.tfa-prompt__icon {
    width: 36px; height: 36px;
    border-radius: 10px;
    background: rgba(16, 185, 129, 0.12);
    color: var(--p-primary-500);
    display: inline-flex; align-items: center; justify-content: center;
    flex-shrink: 0;
}
.tfa-prompt__title { font-size: 14px; font-weight: 600; color: var(--p-text); }
.tfa-prompt__body { font-size: 12px; color: var(--p-text-muted); margin-top: 2px; line-height: 1.45; }
.tfa-prompt__recovery {
    width: 100%;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    font-family: ui-monospace, Menlo, monospace;
}
.tfa-prompt__links { display: flex; gap: 14px; flex-wrap: wrap; }
.tfa-prompt__link {
    background: none; border: 0; padding: 0;
    font-size: 12px;
    color: var(--p-primary-500);
    font-weight: 500;
    cursor: pointer;
    font-family: inherit;
}
</style>
