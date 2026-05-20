<template>
    <SCard :title="t('settings.security.password.title')" :sub="t('settings.security.password.sub')">
        <template v-if="passwordPhase === 'idle'">
            <div class="state-row">
                <div class="state-icon"><IconLock :size="20" /></div>
                <div>
                    <div class="state-title">{{ t('settings.security.password.idleTitle') }}</div>
                    <div class="state-body">{{ t('settings.security.password.idleBody') }}</div>
                </div>
            </div>
            <div class="actions">
                <Button :label="t('settings.security.password.change')" icon="pi pi-key" @click="openPasswordForm" />
            </div>
        </template>

        <form v-else-if="passwordPhase === 'form'" class="security-form" @submit.prevent="submitPasswordForm" novalidate>
            <div class="confirm-head">
                <div class="state-icon"><IconLock :size="18" /></div>
                <div>
                    <div class="state-title">{{ t('settings.security.password.formTitle') }}</div>
                    <div class="state-body">{{ t('settings.security.password.formBody') }}</div>
                </div>
            </div>
            <PasswordPolicyFields
                show-current
                v-model:current="currentPassword"
                v-model:password="newPassword"
                v-model:confirmation="confirmPassword"
                :errors="passwordFieldErrors"
            />
            <Message v-if="passwordError && !passwordErrorField" severity="error" :closable="false">{{ passwordError }}</Message>
            <div class="actions actions--row actions--end">
                <Button text type="button" :label="t('common.cancel')" :disabled="passwordBusy" @click="resetPasswordFlow" />
                <Button type="submit" :label="status?.enabled ? t('common.continue') : t('settings.security.password.update')" :loading="passwordBusy" />
            </div>
        </form>

        <div v-else class="security-form">
            <div class="confirm-head">
                <div class="state-icon"><IconLock :size="18" /></div>
                <div>
                    <div class="state-title">{{ t('settings.security.password.confirmStepTitle') }}</div>
                    <div class="state-body">{{ t('settings.security.password.confirmStepBody') }}</div>
                </div>
            </div>
            <TwoFactorPrompt
                v-model:error="passwordTfaInvalid"
                :methods="tfaMethods"
                @complete="submitPasswordChangeWith2FA"
            />
            <Message v-if="passwordError" severity="error" :closable="false">{{ passwordError }}</Message>
            <div class="actions">
                <Button text :label="t('common.back')" :disabled="passwordBusy" @click="passwordPhase = 'form'" />
            </div>
        </div>
    </SCard>

    <SCard :title="t('settings.tabs.security')" :sub="t('settings.security.twoFactor.sub')">
        <div v-if="loading" class="muted">{{ t('common.loading') }}</div>

        <template v-else-if="!status?.enabled">
            <div class="state-row">
                <div class="state-icon"><IconShield :size="20" /></div>
                <div>
                    <div class="state-title">{{ t('twoFactorStatus.disabledTitle') }}</div>
                    <div class="state-body">{{ t('twoFactorStatus.disabledBody') }}</div>
                </div>
            </div>
            <div class="actions">
                <Button :label="t('twoFactorStatus.enable')" icon="pi pi-shield" @click="openSetup" />
            </div>
        </template>

        <template v-else>
            <div class="state-row">
                <div class="state-icon state-icon--success"><IconCheck :size="20" /></div>
                <div>
                    <div class="state-title">{{ t('twoFactorStatus.enabledTitle') }}</div>
                    <div class="state-body">
                        {{ t('twoFactorStatus.enabledOn', { date: status.confirmed_at ? formatDate(status.confirmed_at) : '—' }) }}
                        <br />
                        {{ t('twoFactorStatus.recoveryCodesRemaining', { n: status.recovery_codes_remaining }) }}
                    </div>
                </div>
            </div>
            <div class="actions actions--row">
                <Button text :label="t('twoFactorStatus.regenerate')" icon="pi pi-refresh" @click="openRegenerate" />
                <Button severity="danger" outlined :label="t('twoFactorStatus.disable')" icon="pi pi-shield-slash" @click="openDisable" />
            </div>
        </template>
    </SCard>

    <Dialog v-model:visible="setupOpen" modal :showHeader="true" :header="t('twoFactorSetup.title')" :style="{ width: '480px' }" :closable="!setupBusy">
        <div v-if="setupStep === 'password'" class="dialog-body">
            <p class="muted">{{ t('twoFactorSetup.passwordBody') }}</p>
            <SField :label="t('settings.security.password.current')" :error="setupError ?? undefined">
                <Password v-model="setupPassword" :feedback="false" toggle-mask fluid autocomplete="current-password" />
            </SField>
        </div>
        <div v-else-if="setupStep === 'scan'" class="dialog-body">
            <p class="muted">{{ t('twoFactorSetup.scanBody') }}</p>
            <div class="qr-wrap">
                <img v-if="setupData?.qr_code_png_b64" :src="`data:image/png;base64,${setupData.qr_code_png_b64}`" class="qr" :alt="t('settings.security.twoFactor.qrAlt')" />
            </div>
            <div class="secret-wrap">
                <div class="muted">{{ t('twoFactorSetup.secretLabel') }}</div>
                <div class="secret-row">
                    <code class="secret">{{ setupData?.secret }}</code>
                    <Button text icon="pi pi-copy" @click="copySecret" />
                </div>
            </div>
            <SField :label="t('twoFactorSetup.confirmTitle')" :hint="t('twoFactorSetup.confirmBody')" :error="setupError ?? undefined">
                <OtpInput v-model="setupCode" :state="setupError ? 'error' : 'idle'" />
            </SField>
        </div>
        <div v-else class="dialog-body">
            <div class="state-row">
                <div class="state-icon"><IconShield :size="20" /></div>
                <div>
                    <div class="state-title">{{ t('twoFactorSetup.recoveryTitle') }}</div>
                    <div class="state-body">{{ t('twoFactorSetup.recoveryBody') }}</div>
                </div>
            </div>
            <pre class="recovery-codes">{{ recoveryCodes.join('\n') }}</pre>
            <div class="actions actions--row">
                <Button text icon="pi pi-download" :label="t('twoFactorSetup.download')" @click="downloadCodes" />
                <Button text icon="pi pi-copy" :label="t('common.copy')" @click="copyCodes" />
            </div>
        </div>

        <template #footer>
            <template v-if="setupStep === 'password'">
                <Button text :label="t('common.cancel')" :disabled="setupBusy" @click="setupOpen = false" />
                <Button :label="t('common.continue')" :loading="setupBusy" @click="startSetup" />
            </template>
            <template v-else-if="setupStep === 'scan'">
                <Button text :label="t('common.cancel')" :disabled="setupBusy" @click="setupOpen = false" />
                <Button :label="t('twoFactorSetup.confirmCta')" :loading="setupBusy" @click="confirmSetup" />
            </template>
            <template v-else>
                <Button :label="t('twoFactorSetup.recoveryAck')" @click="closeSetup" />
            </template>
        </template>
    </Dialog>

    <Dialog v-model:visible="regenerateOpen" modal :header="t('twoFactorStatus.regenerate')" :style="{ width: '440px' }">
        <div v-if="!regenerateCodes.length" class="dialog-body">
            <p class="muted">{{ t('twoFactorStatus.passwordPrompt') }}</p>
            <SField :label="t('settings.security.twoFactor.passwordLabel')" :error="regenerateError ?? undefined">
                <Password v-model="regeneratePassword" :feedback="false" toggle-mask fluid />
            </SField>
        </div>
        <div v-else class="dialog-body">
            <pre class="recovery-codes">{{ regenerateCodes.join('\n') }}</pre>
        </div>
        <template #footer>
            <template v-if="!regenerateCodes.length">
                <Button text :label="t('common.cancel')" @click="regenerateOpen = false" />
                <Button :label="t('settings.security.twoFactor.regenerate')" :loading="regenerateBusy" @click="submitRegenerate" />
            </template>
            <template v-else>
                <Button :label="t('settings.security.twoFactor.acknowledge')" @click="regenerateOpen = false" />
            </template>
        </template>
    </Dialog>

    <Dialog v-model:visible="disableOpen" modal :header="t('twoFactorStatus.disable')" :style="{ width: '440px' }">
        <div class="dialog-body">
            <p class="muted">{{ t('settings.security.twoFactor.disableBody') }}</p>
            <SField :label="t('settings.security.twoFactor.passwordLabel')">
                <Password v-model="disablePassword" :feedback="false" toggle-mask fluid />
            </SField>
            <SField :label="t('settings.security.twoFactor.totpLabel')" :error="disableError ?? undefined">
                <OtpInput v-model="disableCode" :state="disableError ? 'error' : 'idle'" />
            </SField>
        </div>
        <template #footer>
            <Button text :label="t('common.cancel')" @click="disableOpen = false" />
            <Button severity="danger" :label="t('settings.security.twoFactor.disableButton')" :loading="disableBusy" @click="submitDisable" />
        </template>
    </Dialog>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Message from 'primevue/message'
import Password from 'primevue/password'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import OtpInput from '@/components/auth/OtpInput.vue'
import PasswordPolicyFields from '@/components/auth/PasswordPolicyFields.vue'
import TwoFactorPrompt from '@/components/auth/TwoFactorPrompt.vue'
import IconShield from '@/components/icons/IconShield.vue'
import IconCheck from '@/components/icons/IconCheck.vue'
import IconLock from '@/components/icons/IconLock.vue'
import { authService } from '@/services/auth'
import { formatDate } from '@/utils/format'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { TwoFactorMethod, TwoFactorStatusResponse, TwoFactorSetupInitiateResponse } from '@/types/api'

const { t } = useI18n()
const tfaMethods: TwoFactorMethod[] = ['app', 'recovery']

const status = ref<TwoFactorStatusResponse | null>(null)
const loading = ref(true)

const passwordPhase = ref<'idle' | 'form' | 'tfa'>('idle')
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const passwordError = ref<string | null>(null)
const passwordErrorField = ref<'current' | 'new' | 'confirm' | null>(null)
const passwordBusy = ref(false)
const passwordTfaInvalid = ref(false)
const passwordFieldErrors = computed(() => ({
    current: passwordErrorField.value === 'current' ? passwordError.value ?? undefined : undefined,
    password: passwordErrorField.value === 'new' ? passwordError.value ?? undefined : undefined,
    password_confirmation: passwordErrorField.value === 'confirm' ? passwordError.value ?? undefined : undefined,
}))

const setupOpen = ref(false)
const setupStep = ref<'password' | 'scan' | 'recovery'>('password')
const setupData = ref<TwoFactorSetupInitiateResponse | null>(null)
const setupPassword = ref('')
const setupCode = ref('')
const setupError = ref<string | null>(null)
const setupBusy = ref(false)
const recoveryCodes = ref<string[]>([])

const regenerateOpen = ref(false)
const regeneratePassword = ref('')
const regenerateCodes = ref<string[]>([])
const regenerateError = ref<string | null>(null)
const regenerateBusy = ref(false)

const disableOpen = ref(false)
const disablePassword = ref('')
const disableCode = ref('')
const disableError = ref<string | null>(null)
const disableBusy = ref(false)

onMounted(async () => {
    await refreshStatus()
})

async function refreshStatus() {
    loading.value = true
    try {
        status.value = await authService.twoFactorStatus()
    } catch {
        status.value = { enabled: false, confirmed_at: null, recovery_codes_remaining: 0 }
    } finally {
        loading.value = false
    }
}

function openPasswordForm() {
    currentPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
    passwordError.value = null
    passwordErrorField.value = null
    passwordTfaInvalid.value = false
    passwordPhase.value = 'form'
}

function resetPasswordFlow() {
    openPasswordForm()
    passwordPhase.value = 'idle'
}

async function submitPasswordForm() {
    passwordError.value = null
    passwordErrorField.value = null
    if (!currentPassword.value) {
        passwordError.value = t('validation.required')
        passwordErrorField.value = 'current'
        return
    }
    if (!newPassword.value) {
        passwordError.value = t('validation.required')
        passwordErrorField.value = 'new'
        return
    }
    if (!isStrongPassword(newPassword.value)) {
        passwordError.value = t('settings.security.password.weak')
        passwordErrorField.value = 'new'
        return
    }
    if (newPassword.value !== confirmPassword.value) {
        passwordError.value = t('settings.security.password.mismatch')
        passwordErrorField.value = 'confirm'
        return
    }
    if (status.value?.enabled) {
        passwordPhase.value = 'tfa'
        return
    }
    await submitPasswordChange()
}

function isStrongPassword(value: string) {
    return value.length >= 12 && /[A-Z]/.test(value) && /\d/.test(value) && /[^A-Za-z0-9]/.test(value)
}

async function submitPasswordChangeWith2FA(code: string, method: TwoFactorMethod) {
    await submitPasswordChange(code, method)
}

async function submitPasswordChange(code?: string, method?: TwoFactorMethod) {
    passwordBusy.value = true
    passwordError.value = null
    passwordErrorField.value = null
    passwordTfaInvalid.value = false
    try {
        await authService.changePassword({
            current_password: currentPassword.value,
            new_password: newPassword.value,
            two_factor_code: code,
            two_factor_method: method,
        })
        notify.success(t('settings.security.password.updated'))
        resetPasswordFlow()
    } catch (e) {
        passwordError.value = apiErrorMessage(e)
        if (code) passwordTfaInvalid.value = true
    } finally {
        passwordBusy.value = false
    }
}

function openSetup() {
    setupPassword.value = ''
    setupCode.value = ''
    setupError.value = null
    setupData.value = null
    recoveryCodes.value = []
    setupStep.value = 'password'
    setupOpen.value = true
}

async function startSetup() {
    if (!setupPassword.value) {
        setupError.value = t('validation.required')
        return
    }
    setupBusy.value = true
    setupError.value = null
    try {
        setupData.value = await authService.twoFactorSetupInitiate({ password: setupPassword.value })
        setupCode.value = ''
        setupStep.value = 'scan'
    } catch (e) {
        setupError.value = apiErrorMessage(e)
    } finally {
        setupBusy.value = false
    }
}

async function confirmSetup() {
    if (setupCode.value.length !== 6) {
        setupError.value = t('twoFactorSetup.invalidCode')
        return
    }
    setupBusy.value = true
    setupError.value = null
    try {
        const res = await authService.twoFactorSetupConfirm(setupCode.value)
        recoveryCodes.value = res.recovery_codes
        setupStep.value = 'recovery'
    } catch (e) {
        setupError.value = apiErrorMessage(e)
    } finally {
        setupBusy.value = false
    }
}

async function closeSetup() {
    setupOpen.value = false
    await refreshStatus()
}

function copySecret() {
    if (!setupData.value?.secret) return
    navigator.clipboard.writeText(setupData.value.secret).then(() => notify.success(t('common.copied')))
}

function copyCodes() {
    navigator.clipboard.writeText(recoveryCodes.value.join('\n')).then(() => notify.success(t('common.copied')))
}

function downloadCodes() {
    const blob = new Blob([recoveryCodes.value.join('\n')], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'prexel-recovery-codes.txt'
    a.click()
    URL.revokeObjectURL(url)
}

function openRegenerate() {
    regeneratePassword.value = ''
    regenerateCodes.value = []
    regenerateError.value = null
    regenerateOpen.value = true
}

async function submitRegenerate() {
    if (!regeneratePassword.value) {
        regenerateError.value = t('settings.security.twoFactor.passwordRequired')
        return
    }
    regenerateBusy.value = true
    regenerateError.value = null
    try {
        const res = await authService.twoFactorRecoveryCodes({ password: regeneratePassword.value })
        regenerateCodes.value = res.recovery_codes
        await refreshStatus()
    } catch (e) {
        regenerateError.value = apiErrorMessage(e)
    } finally {
        regenerateBusy.value = false
    }
}

function openDisable() {
    disablePassword.value = ''
    disableCode.value = ''
    disableError.value = null
    disableOpen.value = true
}

async function submitDisable() {
    if (!disablePassword.value || disableCode.value.length !== 6) {
        disableError.value = t('settings.security.twoFactor.disableMissingFields')
        return
    }
    disableBusy.value = true
    disableError.value = null
    try {
        await authService.twoFactorDisable({ password: disablePassword.value, code: disableCode.value })
        disableOpen.value = false
        notify.success(t('settings.security.twoFactor.disabledToast'))
        await refreshStatus()
    } catch (e) {
        disableError.value = apiErrorMessage(e)
    } finally {
        disableBusy.value = false
    }
}
</script>

<style scoped>
.security-form {
    display: flex;
    flex-direction: column;
    gap: 14px;
}
.confirm-head {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 12px;
    border: 1px solid var(--p-divider);
    border-radius: 10px;
    background: var(--p-content-bg);
}
.state-row {
    display: flex;
    gap: 14px;
    align-items: flex-start;
    margin-bottom: 16px;
}
.state-icon {
    width: 40px; height: 40px;
    border-radius: 12px;
    background: rgba(16, 185, 129, 0.12);
    color: var(--p-primary-500);
    display: inline-flex; align-items: center; justify-content: center;
    flex-shrink: 0;
}
.state-icon--success {
    background: rgba(34, 197, 94, 0.12);
    color: var(--p-success);
}
.state-title { font-size: 14px; font-weight: 600; color: var(--p-text); }
.state-body { font-size: 13px; color: var(--p-text-muted); margin-top: 4px; line-height: 1.5; }
.actions { display: flex; justify-content: flex-end; }
.actions--row { justify-content: space-between; gap: 8px; }
.actions--end { justify-content: flex-end; }
.muted { color: var(--p-text-muted); font-size: 13px; }
.dialog-body { display: flex; flex-direction: column; gap: 14px; padding-top: 8px; }
.qr-wrap { display: flex; justify-content: center; }
.qr {
    width: 196px;
    height: 196px;
    border-radius: 8px;
    background: #fff;
    padding: 8px;
}
.secret-wrap { display: flex; flex-direction: column; gap: 4px; }
.secret-row {
    display: flex;
    align-items: center;
    gap: 8px;
    background: var(--p-hover);
    border-radius: 8px;
    padding: 6px 6px 6px 12px;
}
.secret {
    flex: 1;
    font-family: ui-monospace, Menlo, monospace;
    font-size: 13px;
    letter-spacing: 0.04em;
    color: var(--p-text);
    word-break: break-all;
}
.recovery-codes {
    background: var(--p-hover);
    border-radius: 8px;
    padding: 12px 14px;
    font-family: ui-monospace, Menlo, monospace;
    font-size: 13px;
    letter-spacing: 0.04em;
    color: var(--p-text);
    margin: 0;
    line-height: 1.7;
}
:deep(.p-password) { width: 100%; }
</style>
