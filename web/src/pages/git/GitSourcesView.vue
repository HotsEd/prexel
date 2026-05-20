<template>
    <div class="git-view">
        <header class="page-header">
            <div>
                <h1 class="page-header__title">{{ t('gitSources.title') }}</h1>
                <p class="page-header__sub">{{ t('gitSources.sub') }}</p>
            </div>
            <div class="page-actions">
                <Button :label="t('gitSources.advanced_btn')" icon="pi pi-sliders-h" severity="secondary" text @click="advancedOpen = true" />
                <Button :label="t('gitSources.connect_github')" icon="pi pi-github" :loading="connecting" @click="openConnectDialog" />
            </div>
        </header>

        <section v-if="git.githubSources.length === 0" class="github-hero">
            <div class="github-hero__mark"><i class="pi pi-github" /></div>
            <div>
                <p class="eyebrow">GitHub App</p>
                <h2>{{ t('gitSources.empty.title') }}</h2>
                <p>{{ t('gitSources.empty.body') }}</p>
                <div class="github-hero__actions">
                    <Button :label="t('gitSources.connect_github')" icon="pi pi-external-link" :loading="connecting" @click="openConnectDialog" />
                    <span v-if="connectError" class="inline-error">{{ connectError }}</span>
                </div>
            </div>
        </section>

        <section v-else class="source-grid">
            <!--
                Pending rows surface first with an explicit CTA — they
                were created by the manifest flow but never received
                an installation_id, so they can't list repos / clone
                until the operator finishes the install on GitHub.
            -->
            <article
                v-for="source in git.pendingGitHubSources"
                :key="source.id"
                class="source-card source-card--pending"
            >
                <div class="source-card__head">
                    <div class="source-card__icon"><i class="pi pi-github" /></div>
                    <div>
                        <strong>{{ source.name }}</strong>
                        <span>{{ t('gitSources.card.awaiting_install') }}</span>
                    </div>
                </div>
                <div class="source-card__meta">
                    <span>{{ t('gitSources.card.created_at', { when: formatDate(source.created_at) }) }}</span>
                </div>
                <div class="source-card__actions">
                    <Button :label="t('gitSources.card.finish_install')" size="small" severity="warn" @click="finishInstall(source.id)" />
                    <Button icon="pi pi-trash" severity="danger" size="small" text :aria-label="t('common.remove')" @click="remove(source.id)" />
                </div>
            </article>

            <article v-for="source in git.githubSources" :key="source.id" class="source-card">
                <div class="source-card__head">
                    <div class="source-card__icon"><i class="pi pi-github" /></div>
                    <div>
                        <strong>{{ source.account_login || source.name }}</strong>
                        <span>
                            <span :class="['acct-pill', source.account_type === 'Organization' ? 'acct-pill--org' : 'acct-pill--user']">
                                {{ source.account_type === 'Organization' ? 'Organization' : 'User' }}
                            </span>
                            <span v-if="source.app_slug" class="acct-slug mono">github.com/apps/{{ source.app_slug }}</span>
                        </span>
                    </div>
                </div>
                <div class="source-card__meta">
                    <span>{{ t('gitSources.card.created_at', { when: formatDate(source.created_at) }) }}</span>
                    <span>{{ t('gitSources.card.credentials_masked') }}</span>
                </div>
                <div class="source-card__actions">
                    <Button :label="t('gitSources.card.open')" size="small" text @click="openSource(source.id)" />
                    <Button icon="pi pi-trash" severity="danger" size="small" text :aria-label="t('common.remove')" @click="remove(source.id)" />
                </div>
            </article>
        </section>

        <Dialog v-model:visible="connectOpen" modal :header="t('gitSources.connect_github')" :style="{ width: '500px' }">
            <div class="connect-form">
                <div class="account-picker">
                    <button type="button" :class="{ active: connectAccount === 'personal' }" @click="connectAccount = 'personal'">
                        <i class="pi pi-user" />
                        <span>
                            <strong>{{ t('gitSources.connect.personal_label') }}</strong>
                            <small>{{ t('gitSources.connect.personal_desc') }}</small>
                        </span>
                    </button>
                    <button type="button" :class="{ active: connectAccount === 'org' }" @click="connectAccount = 'org'">
                        <i class="pi pi-building" />
                        <span>
                            <strong>{{ t('gitSources.connect.org_label') }}</strong>
                            <small>{{ t('gitSources.connect.org_desc') }}</small>
                        </span>
                    </button>
                </div>

                <label v-if="connectAccount === 'org'" class="connect-field">
                    <span>{{ t('gitSources.connect.org_slug_label') }}</span>
                    <InputText v-model.trim="orgSlug" :placeholder="t('gitSources.connect.org_slug_placeholder')" autocomplete="off" />
                    <small v-if="orgSlugError">{{ orgSlugError }}</small>
                </label>

                <Message v-if="connectError" severity="error" :closable="false">{{ connectError }}</Message>
            </div>

            <template #footer>
                <Button :label="t('common.cancel')" text severity="secondary" @click="connectOpen = false" />
                <Button :label="t('gitSources.connect.continue_btn')" icon="pi pi-external-link" :loading="connecting" @click="continueGitHub" />
            </template>
        </Dialog>

        <Dialog v-model:visible="advancedOpen" modal :header="t('gitSources.advanced.title')" :style="{ width: '500px' }">
            <div class="connect-form">
                <p class="dialog-copy">{{ t('gitSources.advanced.body') }}</p>
                <InputText v-model="advancedName" :placeholder="t('gitSources.advanced.name_placeholder')" />
                <InputText v-model="tokenValue" type="password" :placeholder="t('gitSources.advanced.token_placeholder')" />
            </div>
            <template #footer>
                <Button :label="t('common.close')" text severity="secondary" @click="advancedOpen = false" />
                <Button :label="t('gitSources.advanced.save_token')" severity="secondary" @click="createToken" />
                <Button :label="t('gitSources.advanced.generate_ssh')" severity="secondary" outlined @click="createSSH" />
            </template>
        </Dialog>
    </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import { useConfirm } from 'primevue/useconfirm'
import { useGitSourcesStore } from '@/stores/gitSources'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n()
const git = useGitSourcesStore()
const router = useRouter()
const confirm = useConfirm()
const connecting = ref(false)
const connectOpen = ref(false)
const advancedOpen = ref(false)
const connectError = ref<string | null>(null)
const connectAccount = ref<'personal' | 'org'>('personal')
const orgSlug = ref('')
const advancedName = ref('')
const tokenValue = ref('')

/*
   Holds the id of the github_app row that ManifestCallback just
   created, while we wait for the install-callback to come back with
   the chosen installation_id. The two postMessages can arrive in
   either order or with a long gap — the operator may stop at the
   GitHub install screen to think about which repos to grant — so we
   keep this in a ref instead of a local closure.
*/
const pendingSourceId = ref<string | null>(null)

const orgSlugError = computed(() => {
    if (connectAccount.value !== 'org') return ''
    if (!orgSlug.value.trim()) return t('gitSources.connect.org_required')
    if (!/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/.test(orgSlug.value.trim())) {
        return t('gitSources.connect.org_invalid')
    }
    return ''
})

onMounted(async () => {
    window.addEventListener('message', onGitHubInstalled)
    await git.fetchAll()
})

onUnmounted(() => window.removeEventListener('message', onGitHubInstalled))

function openConnectDialog() {
    connectError.value = null
    connectOpen.value = true
}

async function continueGitHub() {
    if (orgSlugError.value) return
    connecting.value = true
    connectError.value = null
    try {
        const popup = await git.connectGitHub({
            account: connectAccount.value,
            org: connectAccount.value === 'org' ? orgSlug.value.trim() : undefined,
        })
        if (!popup) {
            connectError.value = t('gitSources.errors.popup_blocked')
            return
        }
        connectOpen.value = false
    } catch (e) {
        connectError.value = apiErrorMessage(e)
    } finally {
        connecting.value = false
    }
}

async function onGitHubInstalled(event: MessageEvent) {
    const data = event.data as { type?: string; source_id?: string; installation_id?: string }

    // First message of the flow — manifest callback created a pending
    // git_source row and handed us its id. Remember it; the install
    // callback will send installation_id separately, after the user
    // picks an account on GitHub.
    if (data?.type === 'prexel:github-app-created' && data.source_id) {
        pendingSourceId.value = data.source_id
        notify.success(t('gitSources.toast.app_created'))
        // Refresh the list so the new (pending) row appears with
        // a "Finalizar instalação" badge while we wait.
        void git.fetchAll()
        return
    }

    if (data?.type !== 'prexel:github-app-installed' || !data.installation_id) return

    // No pending source id means the operator already closed the
    // tab from a previous run, the popup blocker swallowed the
    // created message, or two flows are racing. Bail loudly — the
    // backend has no way to know which pending row to finalise.
    if (!pendingSourceId.value) {
        notify.error(t('gitSources.errors.no_pending_source'))
        await git.fetchAll()
        return
    }

    try {
        const source = await git.finalizeGitHubSource(pendingSourceId.value, data.installation_id)
        notify.success(t('gitSources.toast.connected', { account: source.account_login ?? source.name }))
        pendingSourceId.value = null
        await router.push({ name: 'git.detail', params: { id: source.id } })
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

function openSource(id: string) {
    router.push({ name: 'git.detail', params: { id } })
}

// "Finalizar instalação" CTA on a pending row — reopens the GitHub
// install picker so the operator can pick an account. The install
// callback will arrive via postMessage and we re-use the same
// finalization path. We seed pendingSourceId here so onGitHubInstalled
// knows which row to update.
async function finishInstall(sourceId: string) {
    pendingSourceId.value = sourceId
    try {
        const popup = await git.openInstall(sourceId)
        if (!popup) {
            notify.error(t('gitSources.errors.popup_blocked_install'))
        }
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

function remove(id: string) {
    confirm.require({
        header: t('gitSources.remove.title'),
        message: t('gitSources.remove.body'),
        icon: 'pi pi-exclamation-triangle',
        rejectLabel: t('common.cancel'),
        acceptLabel: t('common.remove'),
        acceptClass: 'p-button-danger',
        accept: () => { void removeSource(id) },
    })
}

async function removeSource(id: string) {
    try {
        await git.remove(id)
        notify.success(t('gitSources.toast.removed'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

async function createToken() {
    if (!advancedName.value.trim() || !tokenValue.value.trim()) return
    try {
        await git.createTokenSource(advancedName.value.trim(), tokenValue.value.trim())
        advancedName.value = ''
        tokenValue.value = ''
        advancedOpen.value = false
        notify.success(t('gitSources.toast.token_saved'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

async function createSSH() {
    try {
        const source = await git.createSSHSource(advancedName.value.trim() || t('gitSources.advanced.default_ssh_name'))
        advancedName.value = ''
        advancedOpen.value = false
        notify.success(source.public_key ? t('gitSources.toast.ssh_generated_copy') : t('gitSources.toast.ssh_generated'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

const formatDate = formatDateTime
</script>

<style scoped>
.git-view { width: 100%; }
.eyebrow {
    margin: 0 0 8px;
    color: var(--p-primary-color);
    font-size: 11px;
    font-weight: 750;
    letter-spacing: .08em;
    text-transform: uppercase;
}
.page-actions {
    display: flex;
    align-items: center;
    gap: 8px;
}
.github-hero {
    border: 1px solid var(--p-content-border);
    border-radius: 14px;
    background: var(--p-content-bg);
    padding: 22px;
}
.github-hero {
    display: grid;
    grid-template-columns: 72px minmax(0, 1fr);
    gap: 18px;
    align-items: start;
}
.github-hero__mark,
.source-card__icon {
    display: grid;
    place-items: center;
    border-radius: 12px;
    background: color-mix(in srgb, var(--p-primary-color), transparent 88%);
    color: var(--p-primary-color);
}
.github-hero__mark { width: 72px; height: 72px; font-size: 30px; }
.github-hero h2 {
    margin: 0;
    font-size: 20px;
    letter-spacing: 0;
}
.github-hero p {
    margin: 8px 0 0;
    color: var(--p-text-muted);
}
.github-hero__actions {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 18px;
}
.inline-error { color: var(--p-danger-text); font-size: 13px; }
.source-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: 12px;
}
.source-card {
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    background: var(--p-content-bg);
    padding: 16px;
}
/* Pending = manifest finished, install pending. Amber stripe to draw
   the eye without being alarming. */
.source-card--pending {
    border-color: color-mix(in srgb, var(--p-warning, #d97706), transparent 55%);
    background: color-mix(in srgb, var(--p-warning, #d97706), transparent 95%);
}

/* Account type pills — distinguish personal (User) from organisational
   integrations at a glance. */
.acct-pill {
    display: inline-block;
    padding: 1px 8px;
    margin-right: 6px;
    border-radius: 999px;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
}
.acct-pill--user {
    background: color-mix(in srgb, var(--p-primary-500), transparent 85%);
    color: var(--p-primary-500);
}
.acct-pill--org {
    background: color-mix(in srgb, var(--p-info, #3b82f6), transparent 85%);
    color: var(--p-info, #3b82f6);
}
.acct-slug {
    font-size: 11px;
    color: var(--p-text-muted);
}
.mono { font-family: ui-monospace, Menlo, monospace; }
.source-card__head {
    display: flex;
    align-items: center;
    gap: 12px;
}
.source-card__icon { width: 42px; height: 42px; font-size: 18px; }
.source-card__head strong { display: block; color: var(--p-text); }
.source-card__head span {
    display: block;
    color: var(--p-text-muted);
    font-size: 12px;
}
.source-card__meta {
    display: flex;
    justify-content: space-between;
    gap: 10px;
    margin-top: 16px;
    color: var(--p-text-muted);
    font-size: 12px;
}
.source-card__actions {
    display: flex;
    justify-content: space-between;
    margin-top: 12px;
}
.connect-form {
    display: grid;
    gap: 10px;
}
.dialog-copy {
    margin: 0 0 4px;
    color: var(--p-text-muted);
}
.account-picker {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 10px;
}
.account-picker button {
    display: flex;
    gap: 10px;
    width: 100%;
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    background: var(--p-content-bg);
    color: var(--p-text);
    padding: 12px;
    text-align: left;
    cursor: pointer;
}
.account-picker button.active {
    border-color: var(--p-primary-color);
    background: color-mix(in srgb, var(--p-primary-color), transparent 90%);
}
.account-picker i { margin-top: 2px; color: var(--p-primary-color); }
.account-picker strong,
.account-picker small {
    display: block;
}
.account-picker small,
.connect-field small {
    color: var(--p-text-muted);
    font-size: 12px;
}
.connect-field {
    display: grid;
    gap: 6px;
}
.connect-field span {
    color: var(--p-text);
    font-size: 13px;
    font-weight: 700;
}
.connect-field small { color: var(--p-danger-text); }
@media (max-width: 780px) {
    .github-hero,
    .page-actions,
    .account-picker { display: block; }
    .github-hero__mark { margin-bottom: 14px; }
    .page-actions .p-button { width: 100%; margin-top: 8px; }
    .account-picker button + button { margin-top: 10px; }
}
</style>
