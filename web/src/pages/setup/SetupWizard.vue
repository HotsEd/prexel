<template>
    <AuthShell :width="600" :trust="false">
        <div class="setup-brand">
            <!--
                Same logic as the login splash: the setup wizard is a
                first-encounter surface, so we use the full wordmark
                lockup rather than the bare mark + manual text. The
                "Setup inicial" subline is positioning text.
            -->
            <PrexelWordmark :size="36" tone="dark" />
            <div class="setup-brand-product">{{ t('setup.brand.product') }}</div>
        </div>

        <ol class="step-rail">
            <li
                v-for="(s, i) in steps"
                :key="s"
                :class="{ active: i === step, done: i < step }"
            >
                <span class="num">{{ i + 1 }}</span>
                <span class="lbl">{{ s }}</span>
            </li>
        </ol>

        <Transition :name="slideDir === 'next' ? 'slide' : 'slide-back'" mode="out-in">
            <!-- WELCOME -->
            <section v-if="step === 0" key="welcome" class="step">
                <h1 class="step-title">{{ t('setup.welcome.title') }}</h1>
                <p class="step-sub">
                    {{ t('setup.welcome.body') }}
                </p>
                <div class="instance-meta">
                    <div>
                        <span class="meta-key">{{ t('setup.welcome.instanceId') }}</span>
                        <span class="meta-val mono">{{ instanceId || '—' }}</span>
                    </div>
                    <div>
                        <span class="meta-key">{{ t('setup.welcome.version') }}</span>
                        <span class="meta-val mono">{{ version || '—' }}</span>
                    </div>
                </div>
                <div class="actions">
                    <Button :label="t('setup.welcome.start')" icon="pi pi-arrow-right" icon-pos="right" @click="next" />
                </div>
            </section>

            <!-- ADMIN -->
            <section v-else-if="step === 1" key="admin" class="step">
                <h1 class="step-title">{{ t('setup.admin.title') }}</h1>
                <p class="step-sub">{{ t('setup.admin.sub') }}</p>
                <SField :label="t('setup.admin.email')">
                    <InputText v-model="admin.email" type="email" autocomplete="email" />
                </SField>
                <SField :label="t('setup.admin.password')">
                    <Password v-model="admin.password" :feedback="false" toggle-mask fluid autocomplete="new-password" />
                </SField>
                <SField :label="t('setup.admin.confirmPassword')">
                    <Password v-model="admin.confirm" :feedback="false" toggle-mask fluid />
                </SField>
                <Message v-if="adminError" severity="error" :closable="false">{{ adminError }}</Message>
                <div class="actions">
                    <Button text :label="t('common.back')" icon="pi pi-arrow-left" @click="back" />
                    <Button :loading="adminLoading" :label="t('common.continue')" icon="pi pi-arrow-right" icon-pos="right" @click="submitAdmin" />
                </div>
            </section>

            <!-- INSTANCE -->
            <section v-else-if="step === 2" key="instance" class="step">
                <h1 class="step-title">{{ t('setup.instance.title') }}</h1>
                <p class="step-sub">{{ t('setup.instance.sub') }}</p>
                <div class="radio-row">
                    <label class="radio-card" :class="{ active: instance.mode === 'domain' }">
                        <RadioButton v-model="instance.mode" value="domain" name="instance-mode" />
                        <div>
                            <strong>{{ t('setup.instance.modeDomain') }}</strong>
                            <small>{{ t('setup.instance.modeDomainHint') }}</small>
                        </div>
                    </label>
                    <label class="radio-card" :class="{ active: instance.mode === 'ip' }">
                        <RadioButton v-model="instance.mode" value="ip" name="instance-mode" />
                        <div>
                            <strong>{{ t('setup.instance.modeIp') }}</strong>
                            <small>{{ t('setup.instance.modeIpHint') }}</small>
                        </div>
                    </label>
                </div>
                <template v-if="instance.mode === 'domain'">
                    <SField :label="t('setup.instance.urlLabel')">
                        <InputText v-model="instance.url" placeholder="prexel.example.com" />
                    </SField>
                </template>
                <Message v-if="instanceError" severity="error" :closable="false">{{ instanceError }}</Message>
                <div class="actions">
                    <Button text :label="t('common.back')" icon="pi pi-arrow-left" @click="back" />
                    <Button :loading="instanceLoading" :label="t('common.continue')" icon="pi pi-arrow-right" icon-pos="right" @click="submitInstance" />
                </div>
            </section>

            <!-- SERVER -->
            <section v-else-if="step === 3" key="server" class="step">
                <h1 class="step-title">{{ t('setup.server.title') }}</h1>
                <p class="step-sub">{{ t('setup.server.sub') }}</p>
                <div class="radio-row">
                    <label class="radio-card" :class="{ active: server.type === 'local' }">
                        <RadioButton v-model="server.type" value="local" name="server-type" />
                        <div>
                            <strong>{{ t('setup.server.localLabel') }}</strong>
                            <small>{{ t('setup.server.localHint') }}</small>
                        </div>
                    </label>
                    <label class="radio-card" :class="{ active: server.type === 'remote' }">
                        <RadioButton v-model="server.type" value="remote" name="server-type" />
                        <div>
                            <strong>{{ t('setup.server.remoteLabel') }}</strong>
                            <small>{{ t('setup.server.remoteHint') }}</small>
                        </div>
                    </label>
                </div>
                <template v-if="server.type === 'remote'">
                    <div class="grid-2">
                        <SField :label="t('setup.server.nameLabel')">
                            <InputText v-model="server.name" placeholder="prod-1" />
                        </SField>
                        <SField :label="t('setup.server.hostLabel')">
                            <InputText v-model="server.host" :placeholder="t('setup.server.hostPlaceholder')" />
                        </SField>
                        <SField :label="t('setup.server.sshPort')">
                            <InputNumber v-model="server.port" :min="1" :max="65535" :use-grouping="false" />
                        </SField>
                        <SField :label="t('setup.server.sshUser')">
                            <InputText v-model="server.user" />
                        </SField>
                    </div>
                </template>
                <Message v-if="serverError" severity="error" :closable="false">{{ serverError }}</Message>
                <div class="actions">
                    <Button text :label="t('common.back')" icon="pi pi-arrow-left" @click="back" />
                    <Button :loading="serverLoading" :label="t('setup.server.submit')" icon="pi pi-arrow-right" icon-pos="right" @click="submitServer" />
                </div>
            </section>

            <!-- DONE -->
            <section v-else key="done" class="step">
                <div class="success-icon"><IconCheck :size="32" :stroke-width="2.5" /></div>
                <h1 class="step-title">{{ t('setup.done.title') }}</h1>
                <p class="step-sub">{{ t('setup.done.sub') }}</p>
                <div v-if="serverResult?.public_key" class="pubkey-note">
                    <strong>{{ t('setup.done.pubkeyTitle') }}</strong>
                    <p class="muted">{{ t('setup.done.pubkeyHint') }}</p>
                    <pre class="mono pubkey">{{ serverResult.public_key }}</pre>
                </div>
                <Message v-if="completeError" severity="error" :closable="false">{{ completeError }}</Message>
                <div class="actions">
                    <Button text :label="t('common.back')" icon="pi pi-arrow-left" @click="back" />
                    <Button :loading="completeLoading" :label="t('setup.done.cta')" icon="pi pi-check" @click="submitComplete" />
                </div>
            </section>
        </Transition>
    </AuthShell>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import axios from 'axios'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import InputNumber from 'primevue/inputnumber'
import Password from 'primevue/password'
import RadioButton from 'primevue/radiobutton'
import Message from 'primevue/message'
import AuthShell from '@/components/auth/AuthShell.vue'
import PrexelWordmark from '@/components/PrexelWordmark.vue'
import IconCheck from '@/components/icons/IconCheck.vue'
import SField from '@/components/settings/SField.vue'
import { fetchSetupStatus } from '@/composables/useSetupStatus'
import { apiErrorMessage } from '@/composables/useApi'

const router = useRouter()
const { t } = useI18n()

const step = ref(0)
const slideDir = ref<'next' | 'back'>('next')

const instanceId = ref('')
const version = ref('')

const admin = ref({ email: '', password: '', confirm: '' })
const adminError = ref<string | null>(null)
const adminLoading = ref(false)

const instance = ref({ mode: 'domain' as 'domain' | 'ip', url: '', tls_mode: 'letsencrypt' as 'letsencrypt' | 'self-signed' })
const instanceError = ref<string | null>(null)
const instanceLoading = ref(false)

const server = ref({
    type: 'local' as 'local' | 'remote',
    name: 'local',
    host: '',
    port: 22,
    user: 'root',
    generate_key: true,
})
const serverError = ref<string | null>(null)
const serverLoading = ref(false)
const serverResult = ref<{ id?: string; public_key?: string; docker_version?: string } | null>(null)

const completeLoading = ref(false)
const completeError = ref<string | null>(null)

const steps = computed(() => [
    t('setup.stepLabels.welcome'),
    t('setup.stepLabels.admin'),
    t('setup.stepLabels.instance'),
    t('setup.stepLabels.server'),
    t('setup.stepLabels.done'),
])

onMounted(async () => {
    try {
        const status = await fetchSetupStatus(true)
        instanceId.value = status.instance_id
        version.value = status.version
    } catch (e) {
        adminError.value = apiErrorMessage(e)
    }
})

function next() {
    slideDir.value = 'next'
    step.value = Math.min(steps.value.length - 1, step.value + 1)
}
function back() {
    slideDir.value = 'back'
    step.value = Math.max(0, step.value - 1)
}

async function submitAdmin() {
    adminError.value = null
    if (!admin.value.email.includes('@')) {
        adminError.value = t('setup.admin.invalidEmail')
        return
    }
    if (admin.value.password.length < 12) {
        adminError.value = t('setup.admin.passwordTooShort')
        return
    }
    if (admin.value.password !== admin.value.confirm) {
        adminError.value = t('setup.admin.passwordMismatch')
        return
    }
    adminLoading.value = true
    try {
        await axios.post('/api/v1/setup/admin', {
            email: admin.value.email.trim(),
            password: admin.value.password,
            password_confirmation: admin.value.confirm,
        })
        next()
    } catch (e) {
        adminError.value = apiErrorMessage(e)
    } finally {
        adminLoading.value = false
    }
}

async function submitInstance() {
    instanceError.value = null
    instanceLoading.value = true
    try {
        const url = instance.value.mode === 'domain' ? instance.value.url.trim() : ''
        const tls_mode = instance.value.mode === 'domain' ? instance.value.tls_mode : 'self-signed'
        await axios.post('/api/v1/setup/instance', { instance_url: url, tls_mode })
        next()
    } catch (e) {
        instanceError.value = apiErrorMessage(e)
    } finally {
        instanceLoading.value = false
    }
}

async function submitServer() {
    serverError.value = null
    serverLoading.value = true
    serverResult.value = null
    try {
        const body: Record<string, unknown> = {
            type: server.value.type,
            name: server.value.name || (server.value.type === 'local' ? 'local' : 'remote-1'),
        }
        if (server.value.type === 'remote') {
            body.host = server.value.host
            body.port = server.value.port
            body.user = server.value.user
            body.generate_key = true
        }
        const res = await axios.post('/api/v1/setup/server', body)
        serverResult.value = res.data
        next()
    } catch (e) {
        serverError.value = apiErrorMessage(e)
    } finally {
        serverLoading.value = false
    }
}

async function submitComplete() {
    completeError.value = null
    completeLoading.value = true
    try {
        await axios.post('/api/v1/setup/complete')
        await fetchSetupStatus(true)
        router.push('/login')
    } catch (e) {
        completeError.value = apiErrorMessage(e)
    } finally {
        completeLoading.value = false
    }
}
</script>

<style scoped>
.setup-brand {
    /* Stack the wordmark over the positioning subline; the wordmark
       already carries the brand name. */
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
    margin-bottom: 24px;
}
.setup-brand-name {
    /* Retained for legacy markup. */
    font-family: var(--prexel-font-display);
    font-weight: 800;
    font-size: 18px;
    color: var(--p-text);
    line-height: 1;
}
.setup-brand-product {
    font-size: 11px;
    color: var(--p-text-muted);
    letter-spacing: 0.06em;
    text-transform: uppercase;
    font-weight: 600;
    /* Sits below wordmark, slight indent to align under the mark tile. */
    margin-left: 50px;
}

.step-rail {
    list-style: none;
    display: flex;
    gap: 14px;
    margin: 0 0 24px;
    padding: 0 0 16px;
    font-size: 12px;
    border-bottom: 1px solid var(--p-divider);
    flex-wrap: wrap;
}
.step-rail li {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--p-text-muted);
}
.step-rail li.active { color: var(--p-text); font-weight: 600; }
.step-rail li.done { color: var(--p-success-text); }
.step-rail .num {
    display: inline-flex; align-items: center; justify-content: center;
    width: 20px; height: 20px; border-radius: 50%;
    background: var(--p-hover); color: inherit;
    font-size: 11px; font-weight: 600;
}
.step-rail li.active .num { background: var(--p-primary-500); color: #fff; }
.step-rail li.done .num { background: var(--p-success); color: #fff; }

.step { display: flex; flex-direction: column; gap: 14px; min-height: 340px; }
.step-title {
    font-family: var(--prexel-font-display);
    font-size: 22px;
    font-weight: 700;
    letter-spacing: -0.02em;
    margin: 0;
}
.step-sub { font-size: 13px; color: var(--p-text-muted); margin: 0; line-height: 1.5; }

.actions { display: flex; justify-content: space-between; gap: 8px; margin-top: 8px; }
.instance-meta {
    display: flex;
    gap: 24px;
    padding: 12px;
    background: var(--p-hover);
    border-radius: 8px;
}
.instance-meta div { display: flex; flex-direction: column; gap: 2px; font-size: 12px; }
.meta-key { color: var(--p-text-muted); }
.meta-val { color: var(--p-text); font-size: 13px; }
.mono { font-family: ui-monospace, Menlo, monospace; }

.radio-row { display: flex; flex-direction: column; gap: 8px; }
.radio-card {
    display: flex; gap: 12px; align-items: center;
    padding: 12px; border: 1px solid var(--p-content-border); border-radius: 8px;
    cursor: pointer;
    background: var(--p-content-bg);
}
.radio-card.active { border-color: var(--p-primary-500); background: rgba(16, 185, 129, 0.08); }
.radio-card strong { display: block; color: var(--p-text); font-size: 13px; font-weight: 600; }
.radio-card small { color: var(--p-text-muted); font-size: 12px; }

.grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }

.success-icon {
    width: 64px; height: 64px;
    margin: 8px auto 0;
    border-radius: 16px;
    background: rgba(34, 197, 94, 0.10);
    color: var(--p-success);
    display: inline-flex; align-items: center; justify-content: center;
}
.pubkey-note { background: var(--p-hover); border-radius: 8px; padding: 12px; }
.pubkey {
    background: #0a0a0f;
    border: 1px solid var(--p-content-border);
    border-radius: 6px;
    padding: 8px;
    font-size: 12px;
    word-break: break-all;
    white-space: pre-wrap;
    color: #d4d4d8;
}
.muted { color: var(--p-text-muted); font-size: 12px; }

.slide-enter-active, .slide-leave-active { transition: opacity 200ms, transform 200ms; }
.slide-enter-from { opacity: 0; transform: translateX(20px); }
.slide-leave-to { opacity: 0; transform: translateX(-20px); }
.slide-back-enter-active, .slide-back-leave-active { transition: opacity 200ms, transform 200ms; }
.slide-back-enter-from { opacity: 0; transform: translateX(-20px); }
.slide-back-leave-to { opacity: 0; transform: translateX(20px); }

:deep(.p-password), :deep(.p-password-input), :deep(.p-inputnumber-input), :deep(.p-inputtext) {
    width: 100%;
}
</style>
