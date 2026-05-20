<template>
    <div class="git-detail">
        <header class="page-header">
            <div>
                <Button :label="t('gitSources.title')" icon="pi pi-arrow-left" text severity="secondary" class="back-button" @click="router.push('/git')" />
                <h1 class="page-header__title">{{ source?.name || t('gitSources.detail.fallback_title') }}</h1>
                <p class="page-header__sub">{{ t('gitSources.detail.sub') }}</p>
            </div>
            <div class="page-actions">
                <Button :label="t('gitSources.detail.delete_btn')" icon="pi pi-trash" severity="danger" outlined @click="removeOpen = true" />
                <Button :label="t('gitSources.detail.refresh_btn')" icon="pi pi-refresh" severity="secondary" :loading="git.repoLoading" @click="loadRepos" />
            </div>
        </header>

        <section v-if="source" class="source-summary">
            <div class="source-summary__icon"><i class="pi pi-github" /></div>
            <div>
                <p class="eyebrow">GitHub App</p>
                <h2>{{ source.account_login || source.name }}</h2>
                <p>
                    <template v-if="source.account_type">{{ source.account_type }} · </template>
                    <template v-if="source.app_slug">github.com/apps/{{ source.app_slug }} · </template>
                    {{ t('gitSources.detail.created_at', { when: formatDate(source.created_at) }) }}
                </p>
                <p v-if="source.pending_install" class="pending-warn">
                    {{ t('gitSources.detail.pending_install_warning') }}
                </p>
            </div>
        </section>

        <section class="repo-panel">
            <div class="repo-panel__head">
                <div>
                    <h2>{{ t('gitSources.detail.repos_title') }}</h2>
                    <p>{{ t('gitSources.detail.repos_sub') }}</p>
                </div>
                <div class="repo-search">
                    <i class="pi pi-search" />
                    <input v-model="repoQuery" type="search" :placeholder="t('gitSources.detail.search_placeholder')" @keyup.enter="loadRepos" />
                </div>
            </div>

            <Message v-if="git.lastRepoError" severity="warn" :closable="false">{{ git.lastRepoError }}</Message>
            <div v-if="git.repoLoading" class="state-row">{{ t('gitSources.detail.loading_repos') }}</div>
            <div v-else-if="repositories.length === 0" class="empty-row">{{ t('gitSources.detail.no_repos') }}</div>
            <div v-else class="repo-list">
                <a v-for="repo in repositories" :key="repo.id" class="repo-row" :href="repo.html_url" target="_blank" rel="noreferrer">
                    <span>
                        <strong>{{ repo.full_name }}</strong>
                        <small>{{ repo.private ? t('gitSources.detail.private') : t('gitSources.detail.public') }} · {{ repo.default_branch }}<template v-if="repo.language"> · {{ repo.language }}</template></small>
                    </span>
                    <i class="pi pi-external-link" />
                </a>
            </div>
        </section>

        <Dialog v-model:visible="removeOpen" modal :header="t('gitSources.detail.delete_dialog_title')" :style="{ width: '440px' }">
            <div class="danger-dialog">
                <div class="danger-dialog__icon"><i class="pi pi-exclamation-triangle" /></div>
                <div>
                    <strong>{{ t('gitSources.detail.delete_confirm_strong') }}</strong>
                    <p>{{ t('gitSources.detail.delete_confirm_body') }}</p>
                </div>
            </div>
            <template #footer>
                <Button :label="t('common.cancel')" text severity="secondary" @click="removeOpen = false" />
                <Button :label="t('gitSources.detail.delete_btn')" severity="danger" :loading="removing" @click="removeSource" />
            </template>
        </Dialog>
    </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Message from 'primevue/message'
import { useGitSourcesStore } from '@/stores/gitSources'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const git = useGitSourcesStore()
const repoQuery = ref('')
const removeOpen = ref(false)
const removing = ref(false)

const sourceId = computed(() => String(route.params.id || ''))
const source = computed(() => git.sources.find((item) => item.id === sourceId.value) ?? null)
const repositories = computed(() => git.repositories[sourceId.value] ?? [])

onMounted(async () => {
    await git.fetchAll()
    if (!source.value) {
        notify.error(t('gitSources.errors.not_found'))
        await router.push('/git')
        return
    }
    await loadRepos()
})

async function loadRepos() {
    if (!sourceId.value) return
    try {
        await git.fetchRepositories(sourceId.value, repoQuery.value)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

async function removeSource() {
    if (!sourceId.value) return
    removing.value = true
    try {
        await git.remove(sourceId.value)
        removeOpen.value = false
        notify.success(t('gitSources.toast.deleted'))
        await router.push('/git')
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        removing.value = false
    }
}

const formatDate = formatDateTime
</script>

<style scoped>
.git-detail { width: 100%; }
.back-button { margin: 0 0 8px -10px; }
.page-actions {
    display: flex;
    align-items: center;
    gap: 8px;
}
.eyebrow {
    margin: 0 0 8px;
    color: var(--p-primary-color);
    font-size: 11px;
    font-weight: 750;
    letter-spacing: .08em;
    text-transform: uppercase;
}
.source-summary,
.repo-panel {
    border: 1px solid var(--p-content-border);
    border-radius: 14px;
    background: var(--p-content-bg);
    padding: 22px;
}
.source-summary {
    display: flex;
    align-items: center;
    gap: 16px;
    margin-bottom: 18px;
}
.source-summary__icon {
    display: grid;
    place-items: center;
    width: 54px;
    height: 54px;
    border-radius: 12px;
    background: color-mix(in srgb, var(--p-primary-color), transparent 88%);
    color: var(--p-primary-color);
    font-size: 22px;
}
.source-summary h2,
.repo-panel h2 {
    margin: 0;
    font-size: 20px;
    letter-spacing: 0;
}
.source-summary p,
.repo-panel p {
    margin: 8px 0 0;
    color: var(--p-text-muted);
}
.repo-panel__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 18px;
    margin-bottom: 14px;
}
.repo-search {
    display: flex;
    align-items: center;
    gap: 9px;
    width: min(340px, 100%);
    min-height: 40px;
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    padding: 0 12px;
    color: var(--p-text-muted);
}
.repo-search input {
    width: 100%;
    border: 0;
    outline: 0;
    background: transparent;
    color: var(--p-text);
}
.state-row,
.empty-row {
    padding: 24px;
    color: var(--p-text-muted);
}
.repo-list {
    display: grid;
    gap: 8px;
}
.repo-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    padding: 12px;
    color: var(--p-text-muted);
    text-decoration: none;
}
.repo-row:hover { background: var(--p-hover); }
.repo-row strong {
    display: block;
    color: var(--p-text);
}
.repo-row small {
    display: block;
    color: var(--p-text-muted);
    font-size: 12px;
}
.danger-dialog {
    display: flex;
    gap: 12px;
    color: var(--p-text);
}
.danger-dialog__icon {
    display: grid;
    place-items: center;
    flex: 0 0 38px;
    width: 38px;
    height: 38px;
    border-radius: 10px;
    background: color-mix(in srgb, var(--p-red-500), transparent 88%);
    color: var(--p-red-400);
}
.danger-dialog strong {
    display: block;
    margin-bottom: 4px;
}
.danger-dialog p {
    margin: 0;
    color: var(--p-text-muted);
}
@media (max-width: 780px) {
    .page-actions { width: 100%; }
    .page-actions .p-button { flex: 1; }
    .repo-panel__head { display: block; }
    .repo-search { margin-top: 14px; }
}
</style>
