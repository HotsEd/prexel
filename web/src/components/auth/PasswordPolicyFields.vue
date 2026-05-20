<template>
    <div class="password-policy">
        <SField
            v-if="showCurrent"
            :label="t('settings.security.password.current')"
            required
            :error="errors?.current"
        >
            <input
                class="p-inputtext password-policy__input"
                type="password"
                :value="current"
                autocomplete="current-password"
                @input="$emit('update:current', ($event.target as HTMLInputElement).value)"
            />
        </SField>

        <SField :label="t('settings.security.password.next')" required :error="errors?.password">
            <div class="password-policy__wrap">
                <span class="password-policy__icon"><IconLock :size="16" /></span>
                <input
                    class="p-inputtext password-policy__input"
                    :type="showPassword ? 'text' : 'password'"
                    :value="password"
                    autocomplete="new-password"
                    @input="$emit('update:password', ($event.target as HTMLInputElement).value)"
                />
                <button
                    type="button"
                    class="password-policy__eye"
                    :aria-label="showPassword ? t('settings.security.password.hidePassword') : t('settings.security.password.showPassword')"
                    @click="showPassword = !showPassword"
                >
                    <IconEye :size="16" />
                </button>
            </div>
        </SField>

        <SField :label="t('settings.security.password.confirm')" required :error="errors?.password_confirmation">
            <div class="password-policy__wrap">
                <span class="password-policy__icon"><IconLock :size="16" /></span>
                <input
                    class="p-inputtext password-policy__input"
                    :type="showPassword ? 'text' : 'password'"
                    :value="confirmation"
                    autocomplete="new-password"
                    @input="$emit('update:confirmation', ($event.target as HTMLInputElement).value)"
                />
            </div>
        </SField>

        <div class="password-policy__checks">
            <div
                v-for="(check, key) in checks"
                :key="key"
                :class="['password-policy__check', check.ok && 'password-policy__check--ok']"
            >
                <span class="password-policy__dot">
                    <IconCheck v-if="check.ok" :size="11" :stroke-width="2.5" />
                </span>
                {{ check.label }}
            </div>
        </div>
    </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SField from '@/components/settings/SField.vue'
import IconCheck from '@/components/icons/IconCheck.vue'
import IconEye from '@/components/icons/IconEye.vue'
import IconLock from '@/components/icons/IconLock.vue'

const props = withDefaults(defineProps<{
    password: string
    confirmation: string
    current?: string
    showCurrent?: boolean
    errors?: {
        current?: string
        password?: string
        password_confirmation?: string
    }
}>(), {
    current: '',
    showCurrent: false,
    errors: () => ({}),
})

defineEmits<{
    'update:password': [value: string]
    'update:confirmation': [value: string]
    'update:current': [value: string]
}>()

const { t } = useI18n()
const showPassword = ref(false)

const checks = computed(() => {
    const p = props.password
    return {
        length: { label: t('settings.security.password.checks.length'), ok: p.length >= 12 },
        uppercase: { label: t('settings.security.password.checks.uppercase'), ok: /[A-Z]/.test(p) },
        number: { label: t('settings.security.password.checks.number'), ok: /\d/.test(p) },
        symbol: { label: t('settings.security.password.checks.symbol'), ok: /[^A-Za-z0-9]/.test(p) },
        match: { label: t('settings.security.password.checks.match'), ok: p.length > 0 && p === props.confirmation },
    }
})
</script>

<style scoped>
.password-policy {
    display: flex;
    flex-direction: column;
    gap: 12px;
}
.password-policy__wrap {
    position: relative;
}
.password-policy__icon {
    position: absolute;
    left: 12px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--p-text-muted);
    pointer-events: none;
}
.password-policy__input {
    width: 100%;
}
.password-policy__wrap .password-policy__input {
    padding-left: 38px;
    padding-right: 40px;
}
.password-policy__eye {
    position: absolute;
    right: 10px;
    top: 50%;
    transform: translateY(-50%);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    color: var(--p-text-muted);
    cursor: pointer;
}
.password-policy__eye:hover {
    background: var(--p-hover);
    color: var(--p-text);
}
.password-policy__checks {
    padding: 14px;
    border-radius: 10px;
    background: var(--p-content-bg);
    border: 1px solid var(--p-divider);
}
.password-policy__check {
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 26px;
    font-size: 12px;
    color: var(--p-text-muted);
    transition: color 120ms;
}
.password-policy__check--ok {
    color: #16a34a;
}
.password-policy__dot {
    width: 18px;
    height: 18px;
    border-radius: 999px;
    background: var(--p-divider);
    color: var(--p-text-muted);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    transition: background 120ms, color 120ms;
}
.password-policy__check--ok .password-policy__dot {
    background: rgba(22, 163, 74, 0.14);
    color: #16a34a;
}
</style>
