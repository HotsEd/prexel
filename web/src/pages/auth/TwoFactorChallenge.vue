<template>
    <AuthShell :width="460">
        <RouterLink to="/login" class="tfa__back">
            <IconChevronLeft :size="14" />
            {{ t('twoFactorChallenge.backToLogin') }}
        </RouterLink>

        <div class="tfa__icon">
            <IconShield :size="26" />
        </div>

        <h1 class="tfa__title">{{ t('twoFactorChallenge.title') }}</h1>

        <p v-if="!challengeId" class="tfa__server-error">{{ t('twoFactorChallenge.missingChallenge') }}</p>

        <TwoFactorPrompt
            v-else
            v-model:error="error"
            :methods="methods"
            :email-hint="null"
            @complete="onVerify"
        />

        <p v-if="serverError" class="tfa__server-error">{{ serverError }}</p>

        <div class="tfa__hint" v-if="verifying">{{ t('twoFactorChallenge.verifying') }}</div>
    </AuthShell>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AuthShell from '@/components/auth/AuthShell.vue'
import TwoFactorPrompt from '@/components/auth/TwoFactorPrompt.vue'
import IconShield from '@/components/icons/IconShield.vue'
import IconChevronLeft from '@/components/icons/IconChevronLeft.vue'
import { authService } from '@/services/auth'
import type { TwoFactorMethod } from '@/types/api'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const auth = useAuthStore()

const challengeId = (typeof route.query.id === 'string' ? route.query.id : '') as string
const emailFromQuery = (typeof route.query.email === 'string' ? route.query.email : '') as string
const methodsFromQuery = (typeof route.query.methods === 'string' ? route.query.methods : '') as string

const methods = ref<TwoFactorMethod[]>(
    methodsFromQuery
        ? (methodsFromQuery.split(',').filter(Boolean) as TwoFactorMethod[])
        : ['app', 'recovery'],
)
const verifying = ref(false)
const error = ref(false)
const serverError = ref<string | null>(null)

onMounted(async () => {
    if (!challengeId) return
    // Optional refinement of available methods. If the endpoint doesn't exist
    // or fails, we keep the query-passed methods.
    try {
        const result = await authService.twoFactorChallengeMethods(challengeId)
        if (result?.methods?.length) methods.value = result.methods
    } catch { /* keep defaults */ }
})

async function onVerify(code: string, method: TwoFactorMethod) {
    if (!challengeId || verifying.value) return
    verifying.value = true
    error.value = false
    serverError.value = null
    try {
        const data = await authService.twoFactorChallengeVerify({
            challenge_id: challengeId,
            code,
            method,
        })
        await auth.applyChallengeSuccess(data, emailFromQuery)
        router.push('/apps')
    } catch {
        error.value = true
    } finally {
        verifying.value = false
    }
}
</script>

<style scoped>
.tfa__back {
    background: none;
    border: 0;
    padding: 0;
    margin-bottom: 18px;
    font-size: 13px;
    color: var(--p-text-muted);
    display: flex;
    width: max-content;
    align-items: center;
    gap: 6px;
    cursor: pointer;
    text-decoration: none;
}
.tfa__icon {
    width: 56px;
    height: 56px;
    border-radius: 14px;
    background: rgba(16, 185, 129, 0.12);
    color: var(--p-primary-500);
    display: flex;
    align-items: center;
    justify-content: center;
    margin-bottom: 18px;
}
.tfa__title {
    font-family: var(--prexel-font-display);
    font-size: 22px;
    font-weight: 700;
    letter-spacing: -0.02em;
    color: var(--p-text);
    margin: 0 0 18px;
}
.tfa__server-error {
    padding: 10px 12px;
    margin: 12px 0;
    border-radius: 8px;
    background: rgba(220, 38, 38, 0.10);
    color: #dc2626;
    font-size: 12px;
}
.tfa__hint {
    font-size: 12px;
    color: var(--p-text-muted);
    margin-top: 12px;
    text-align: center;
}
</style>
