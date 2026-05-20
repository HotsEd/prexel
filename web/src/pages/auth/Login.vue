<template>
    <div class="login-screen">
        <div class="login-form-wrap">
            <form class="login-form" @submit.prevent="form.submit">
                <div class="login-brand">
                    <!--
                        Wordmark (mark + lockup) is the right call on the
                        login splash — it's the brand introducing itself.
                        Sidebar / topbar use the bare mark; here we get
                        the full lockup with the emerald `e` accent and
                        the small dot above. The tagline below carries
                        the product positioning.
                    -->
                    <PrexelWordmark :size="40" tone="dark" />
                    <div class="login-brand-product">Self-hosted deploys</div>
                </div>

                <h1 class="login-title">{{ t('auth.welcomeBack') }}</h1>
                <p class="login-sub">{{ t('auth.signInPrompt') }}</p>

                <LoginField icon="mail" :error="form.errors.email" class="login-field--gap">
                    <input
                        v-model="form.values.email"
                        type="email"
                        class="p-inputtext"
                        :placeholder="t('auth.emailPlaceholder')"
                        autocomplete="email"
                        @blur="form.validateField('email')"
                        @input="form.clearError('email')"
                    />
                </LoginField>

                <LoginField icon="lock" :error="form.errors.password">
                    <input
                        v-model="form.values.password"
                        type="password"
                        class="p-inputtext"
                        :placeholder="t('auth.passwordPlaceholder')"
                        autocomplete="current-password"
                        @blur="form.validateField('password')"
                        @input="form.clearError('password')"
                    />
                </LoginField>

                <p v-if="form.serverError.value" class="login-error">{{ form.serverError.value }}</p>

                <button type="submit" class="login-submit" :disabled="form.submitting.value">
                    <span v-if="form.submitting.value" class="login-spinner" />
                    <span v-else>{{ t('auth.login') }}</span>
                    <IconChevronRight v-if="!form.submitting.value" :size="16" />
                </button>
            </form>
        </div>

        <LoginAside />
    </div>
</template>

<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import PrexelWordmark from '@/components/PrexelWordmark.vue'
import IconChevronRight from '@/components/icons/IconChevronRight.vue'
import LoginField from '@/components/auth/LoginField.vue'
import LoginAside from '@/components/auth/LoginAside.vue'
import { useForm } from '@/composables/useForm'
import { loginSchema } from '@/validation/login.schema'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const auth = useAuthStore()

/**
 * Validate the `?next=` redirect target before handing it to the
 * router. We only allow same-origin absolute paths to prevent open
 * redirects (e.g. attacker-supplied `?next=https://evil.example`
 * or protocol-relative `//evil.example` that the browser would
 * resolve against the current origin).
 */
function safeNext(raw: unknown, fallback = '/apps'): string {
    if (typeof raw !== 'string') return fallback
    if (!raw.startsWith('/')) return fallback        // only absolute internal
    if (raw.startsWith('//')) return fallback        // block protocol-relative
    if (raw.startsWith('/\\')) return fallback       // edge: /\evil
    return raw
}

const form = useForm({
    schema: loginSchema,
    initialValues: { email: '', password: '' },
    onSubmit: async (values) => {
        const result = await auth.login(values.email, values.password)
        if (result.kind === 'two-factor-required') {
            router.push({
                name: 'auth.two-factor-challenge',
                query: {
                    id: result.challengeId,
                    email: values.email,
                    methods: result.methods.join(','),
                },
            })
            return
        }
        router.push(safeNext(route.query.next))
    },
})
</script>

<style scoped>
.login-brand-suffix {
    color: var(--p-primary-500);
    margin-left: 3px;
}

.login-field--gap { margin-bottom: 16px; }

.login-error {
    margin: 12px 0 0;
    padding: 10px 12px;
    border-radius: 8px;
    background: rgba(220, 38, 38, 0.10);
    color: #dc2626;
    font-size: 12px;
    line-height: 1.4;
}

.login-submit {
    width: 100%;
    padding: 11px 16px;
    border-radius: 10px;
    border: 1px solid var(--p-primary-500);
    background: var(--p-primary-500);
    color: #fff;
    font-size: 14px;
    font-weight: 500;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    font-family: inherit;
    margin-top: 24px;
    transition: background 120ms;
}
.login-submit:hover:not(:disabled) { background: var(--p-primary-600); }
.login-submit:disabled { opacity: 0.6; cursor: not-allowed; }

.login-spinner {
    width: 14px;
    height: 14px;
    border-radius: 999px;
    border: 2px solid currentColor;
    border-top-color: transparent;
    animation: spin 600ms linear infinite;
    display: block;
}
</style>
