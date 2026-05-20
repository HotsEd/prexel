<template>
    <SCard :title="t('settings.profile.title')" :sub="t('settings.profile.sub')">
        <div class="profile-head">
            <UiAvatar :name="displayName" :src="auth.user?.avatar_url" size="xl" />
            <div class="profile-head__body">
                <div class="profile-head__name">{{ displayName }}</div>
                <div class="profile-head__meta">{{ email || '-' }}</div>
                <div class="profile-head__actions">
                    <input
                        ref="avatarInputRef"
                        class="profile-head__file"
                        type="file"
                        accept="image/png,image/jpeg,image/webp"
                        @change="onAvatarPicked"
                    />
                    <Button
                        type="button"
                        severity="secondary"
                        outlined
                        size="small"
                        icon="pi pi-upload"
                        :label="avatarBusy ? t('settings.profile.avatar.uploading') : t('settings.profile.avatar.change')"
                        :disabled="avatarBusy"
                        @click="avatarInputRef?.click()"
                    />
                    <Button
                        v-if="auth.user?.avatar_url"
                        type="button"
                        text
                        severity="danger"
                        size="small"
                        :label="t('settings.profile.avatar.remove')"
                        :disabled="avatarBusy"
                        @click="removeAvatar"
                    />
                </div>
            </div>
        </div>

        <form class="profile-form" @submit.prevent="saveProfile">
            <div class="profile-form-grid">
                <SField
                    :label="t('settings.profile.name')"
                    required
                    :error="profileErrorField === 'name' ? profileError ?? undefined : undefined"
                >
                    <InputText v-model="profileName" autocomplete="name" />
                </SField>
            </div>

            <Message v-if="profileError && !profileErrorField" severity="error" :closable="false" class="profile-server-error">
                {{ profileError }}
            </Message>

            <footer class="profile-actions">
                <Button
                    type="button"
                    text
                    severity="secondary"
                    :label="t('settings.profile.discard')"
                    :disabled="profileSaving || !profileDirty"
                    @click="resetProfileForm"
                />
                <Button
                    type="submit"
                    :label="profileSaving ? t('settings.profile.saving') : t('settings.profile.save')"
                    :loading="profileSaving"
                    :disabled="!profileDirty"
                />
            </footer>
        </form>
    </SCard>

    <SCard :title="t('settings.profile.emailCard.title')" :sub="t('settings.profile.emailCard.sub')">
        <template v-if="emailPhase === 'idle'">
            <div class="state-row">
                <div class="state-icon"><IconMail :size="20" /></div>
                <div>
                    <div class="state-title">{{ t('settings.profile.emailCard.idleTitle') }}</div>
                    <div class="state-body">{{ email || '-' }}</div>
                </div>
            </div>
            <div class="profile-actions">
                <Button
                    type="button"
                    icon="pi pi-envelope"
                    :label="t('settings.profile.emailCard.change')"
                    @click="openEmailFlow"
                />
            </div>
        </template>

        <form v-else-if="emailPhase === 'form'" class="profile-form" @submit.prevent="submitEmailForm">
            <div class="confirm-head">
                <div class="state-icon"><IconMail :size="18" /></div>
                <div>
                    <div class="state-title">{{ t('settings.profile.emailCard.formTitle') }}</div>
                    <div class="state-body">{{ t('settings.profile.emailCard.formBody') }}</div>
                </div>
            </div>
            <div class="profile-form-grid">
                <SField
                    :label="t('settings.profile.email')"
                    required
                    :hint="t('settings.profile.emailCard.hint')"
                    :error="emailErrorField === 'email' ? emailError ?? undefined : undefined"
                >
                    <InputText v-model="emailDraft" type="email" autocomplete="email" />
                </SField>
                <SField
                    :label="t('settings.profile.currentPassword')"
                    required
                    :error="emailErrorField === 'password' ? emailError ?? undefined : undefined"
                >
                    <Password v-model="emailPassword" :feedback="false" toggle-mask fluid autocomplete="current-password" />
                </SField>
            </div>
            <Message v-if="emailError && !emailErrorField" severity="error" :closable="false">{{ emailError }}</Message>
            <footer class="profile-actions">
                <Button text type="button" :label="t('common.cancel')" :disabled="emailSaving" @click="resetEmailFlow" />
                <Button type="submit" :label="emailRequiresTfa ? t('common.continue') : t('settings.profile.emailCard.confirm')" :loading="emailSaving" />
            </footer>
        </form>

        <div v-else class="profile-form">
            <div class="confirm-head">
                <div class="state-icon"><IconMail :size="18" /></div>
                <div>
                    <div class="state-title">{{ t('settings.profile.emailCard.confirmStepTitle') }}</div>
                    <div class="state-body">{{ t('settings.profile.emailCard.tfaHint') }}</div>
                </div>
            </div>
            <TwoFactorPrompt
                v-model:error="emailTfaInvalid"
                :methods="['app', 'recovery']"
                @complete="submitEmailWithTfa"
            />
            <Message v-if="emailError" severity="error" :closable="false">{{ emailError }}</Message>
            <footer class="profile-actions">
                <Button text type="button" :label="t('common.back')" :disabled="emailSaving" @click="emailPhase = 'form'" />
            </footer>
        </div>
    </SCard>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Password from 'primevue/password'
import TwoFactorPrompt from '@/components/auth/TwoFactorPrompt.vue'
import IconMail from '@/components/icons/IconMail.vue'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import UiAvatar from '@/components/ui/UiAvatar.vue'
import { apiErrorMessage } from '@/composables/useApi'
import { notify } from '@/lib/notify'
import { authService } from '@/services/auth'
import { useAuthStore } from '@/stores/auth'
import type { TwoFactorMethod, TwoFactorStatusResponse } from '@/types/api'

const { t } = useI18n()
const auth = useAuthStore()

const avatarInputRef = ref<HTMLInputElement | null>(null)
const avatarBusy = ref(false)
const profileName = ref('')
const profileSaving = ref(false)
const profileError = ref<string | null>(null)
const profileErrorField = ref<'name' | null>(null)
const twoFactorStatus = ref<TwoFactorStatusResponse | null>(null)
const emailDraft = ref('')
const emailPassword = ref('')
const emailPhase = ref<'idle' | 'form' | 'tfa'>('idle')
const emailSaving = ref(false)
const emailError = ref<string | null>(null)
const emailErrorField = ref<'email' | 'password' | null>(null)
const emailTfaInvalid = ref(false)

const email = computed(() => auth.user?.email ?? '')
const displayName = computed(() => auth.user?.name || email.value || 'Prexel')
const normalizedEmailDraft = computed(() => emailDraft.value.trim().toLowerCase())
const emailChanged = computed(() => normalizedEmailDraft.value !== email.value.toLowerCase())
const nameChanged = computed(() => profileName.value.trim() !== (auth.user?.name ?? ''))
const profileDirty = computed(() => nameChanged.value)
const emailRequiresTfa = computed(() => twoFactorStatus.value?.enabled === true)

watch(() => auth.user, (user) => {
    profileName.value = user?.name ?? ''
    if (emailPhase.value === 'idle') emailDraft.value = user?.email ?? ''
}, { immediate: true })

onMounted(async () => {
    try {
        twoFactorStatus.value = await authService.twoFactorStatus()
    } catch {
        twoFactorStatus.value = null
    }
})

async function saveProfile() {
    profileError.value = null
    profileErrorField.value = null
    const name = profileName.value.trim()
    if (name.length > 120) {
        profileError.value = t('settings.profile.nameTooLong')
        profileErrorField.value = 'name'
        return
    }
    profileSaving.value = true
    try {
        const updated = await authService.updateProfile({ name })
        auth.setUser(updated)
        notify.success(t('settings.profile.updated'))
    } catch (e) {
        profileError.value = apiErrorMessage(e)
    } finally {
        profileSaving.value = false
    }
}

function resetProfileForm() {
    profileName.value = auth.user?.name ?? ''
    profileError.value = null
    profileErrorField.value = null
}

function openEmailFlow() {
    emailDraft.value = email.value
    emailPassword.value = ''
    emailError.value = null
    emailErrorField.value = null
    emailTfaInvalid.value = false
    emailPhase.value = 'form'
}

function resetEmailFlow() {
    emailDraft.value = email.value
    emailPassword.value = ''
    emailError.value = null
    emailErrorField.value = null
    emailTfaInvalid.value = false
    emailPhase.value = 'idle'
}

async function submitEmailForm() {
    emailError.value = null
    emailErrorField.value = null
    emailTfaInvalid.value = false
    if (!normalizedEmailDraft.value.includes('@')) {
        emailError.value = t('validation.email')
        emailErrorField.value = 'email'
        return
    }
    if (!emailChanged.value) {
        resetEmailFlow()
        return
    }
    if (!emailPassword.value) {
        emailError.value = t('validation.required')
        emailErrorField.value = 'password'
        return
    }
    if (emailRequiresTfa.value) {
        emailPhase.value = 'tfa'
        return
    }
    await persistEmailChange()
}

async function submitEmailWithTfa(code: string, method: TwoFactorMethod) {
    await persistEmailChange(code, method)
}

async function persistEmailChange(code = '', method: TwoFactorMethod = 'app') {
    emailSaving.value = true
    emailError.value = null
    emailErrorField.value = null
    emailTfaInvalid.value = false
    try {
        const updated = await authService.changeEmail({
            email: normalizedEmailDraft.value,
            current_password: emailPassword.value,
            two_factor_code: code,
            two_factor_method: method,
        })
        auth.setUser(updated)
        notify.success(t('settings.profile.emailCard.updated'))
        resetEmailFlow()
    } catch (e) {
        emailError.value = apiErrorMessage(e)
        if (code) emailTfaInvalid.value = true
    } finally {
        emailSaving.value = false
    }
}

async function onAvatarPicked(event: Event) {
    const file = (event.target as HTMLInputElement).files?.[0]
    if (!file || avatarBusy.value) return
    avatarBusy.value = true
    try {
        const updated = await authService.uploadAvatar(file)
        auth.setUser(updated)
        notify.success(t('settings.profile.avatar.updated'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        avatarBusy.value = false
        if (avatarInputRef.value) avatarInputRef.value.value = ''
    }
}

async function removeAvatar() {
    if (avatarBusy.value) return
    avatarBusy.value = true
    try {
        await authService.removeAvatar()
        if (auth.user) auth.setUser({ ...auth.user, avatar_url: undefined })
        notify.success(t('settings.profile.avatar.removed'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        avatarBusy.value = false
    }
}
</script>

<style scoped>
.profile-head {
    display: flex;
    align-items: center;
    gap: 18px;
    flex-wrap: wrap;
}

.profile-head__body {
    min-width: 0;
    flex: 1;
}

.profile-head__name {
    color: var(--p-text);
    font-size: 16px;
    font-weight: 700;
}

.profile-head__meta {
    margin-top: 2px;
    color: var(--p-text-muted);
    font-size: 13px;
}

.profile-head__actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    margin-top: 10px;
}

.profile-head__file {
    display: none;
}

.profile-form-grid {
    display: grid;
    gap: 14px;
    grid-template-columns: repeat(2, minmax(0, 1fr));
}

.profile-form {
    display: flex;
    flex-direction: column;
    gap: 14px;
    margin-top: 22px;
}

.state-row,
.confirm-head {
    display: flex;
    align-items: flex-start;
    gap: 12px;
}

.state-icon {
    width: 36px;
    height: 36px;
    border-radius: 10px;
    background: rgba(16, 185, 129, 0.12);
    color: var(--p-primary-500);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
}

.state-title {
    color: var(--p-text);
    font-size: 14px;
    font-weight: 650;
}

.state-body {
    color: var(--p-text-muted);
    font-size: 12px;
    line-height: 1.45;
    margin-top: 2px;
}

.profile-actions {
    display: flex;
    gap: 10px;
    align-items: flex-end;
    justify-content: flex-end;
    margin-top: 18px;
}

.profile-server-error {
    margin-top: 16px;
}

@media (max-width: 640px) {
    .profile-form-grid {
        grid-template-columns: 1fr;
    }

    .profile-actions {
        align-items: stretch;
        flex-direction: column-reverse;
    }
}
</style>
