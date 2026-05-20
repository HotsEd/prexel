<template>
    <div class="servers-view">
        <header class="page-header">
            <div>
                <h1 class="page-header__title">{{ t('servers.title') }}</h1>
                <p class="page-header__sub">{{ t('servers.sub') }}</p>
            </div>
            <Button :label="t('servers.addServer')" icon="pi pi-plus" @click="addOpen = true" />
        </header>

        <EmptyState
            v-if="!serversStore.loading && serversStore.servers.length === 0"
            :title="t('servers.emptyTitle')"
            :body="t('servers.emptyBody')"
        >
            <template #icon><IconServer :size="24" /></template>
            <template #actions>
                <Button :label="t('servers.addServer')" icon="pi pi-plus" @click="addOpen = true" />
            </template>
        </EmptyState>

        <div v-else class="server-grid">
            <article v-for="s in serversStore.servers" :key="s.id" class="server-card pv-panel">
                <header class="server-card__head">
                    <div>
                        <div class="server-card__name">{{ s.name }}</div>
                        <div class="server-card__meta">{{ s.type === 'local' ? t('servers.card.local_docker') : `${s.user ?? 'root'}@${s.host}:${s.port}` }}</div>
                    </div>
                    <StatusBadge :status="s.status" />
                </header>
                <div class="server-card__body">
                    <div><span class="muted">Docker</span> {{ s.docker_version ?? '—' }}</div>
                    <div><span class="muted">{{ t('servers.card.last_check') }}</span> {{ s.last_checked_at ? formatRelative(s.last_checked_at) : '—' }}</div>
                </div>
                <footer class="server-card__foot">
                    <Button text size="small" :label="t('servers.card.test')" @click="testServer(s.id)" />
                    <Button text size="small" :label="t('servers.card.remove')" severity="danger" @click="removeServer(s.id)" />
                </footer>
            </article>
        </div>

        <Dialog v-model:visible="addOpen" modal :header="t('servers.add.title')" :style="{ width: '520px' }">
            <div class="dialog-body">
                <div class="radio-row">
                    <label class="radio-card" :class="{ active: draft.type === 'local' }">
                        <RadioButton v-model="draft.type" value="local" name="srv-type" />
                        <div>
                            <strong>{{ t('servers.add.local_label') }}</strong>
                            <small>{{ t('servers.add.local_desc') }}</small>
                        </div>
                    </label>
                    <label class="radio-card" :class="{ active: draft.type === 'remote' }">
                        <RadioButton v-model="draft.type" value="remote" name="srv-type" />
                        <div>
                            <strong>{{ t('servers.add.remote_label') }}</strong>
                            <small>{{ t('servers.add.remote_desc') }}</small>
                        </div>
                    </label>
                </div>
                <SField :label="t('servers.add.name_label')">
                    <InputText v-model="draft.name" />
                </SField>
                <template v-if="draft.type === 'remote'">
                    <SField :label="t('servers.add.host_label')">
                        <InputText v-model="draft.host" />
                    </SField>
                    <div class="row-2">
                        <SField :label="t('servers.add.port_label')">
                            <InputNumber v-model="draft.port" :min="1" :max="65535" :use-grouping="false" />
                        </SField>
                        <SField :label="t('servers.add.user_label')">
                            <InputText v-model="draft.user" />
                        </SField>
                    </div>
                </template>
                <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
            </div>
            <template #footer>
                <Button :label="t('common.cancel')" text @click="addOpen = false" />
                <Button :label="t('servers.add.submit')" :loading="creating" @click="submitAdd" />
            </template>
        </Dialog>
    </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import InputNumber from 'primevue/inputnumber'
import RadioButton from 'primevue/radiobutton'
import Message from 'primevue/message'
import { useConfirm } from 'primevue/useconfirm'
import EmptyState from '@/components/EmptyState.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import SField from '@/components/settings/SField.vue'
import IconServer from '@/components/icons/IconServer.vue'
import { useServersStore } from '@/stores/servers'
import { formatRelative } from '@/utils/format'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'

const { t } = useI18n()
const confirm = useConfirm()
const serversStore = useServersStore()

const addOpen = ref(false)
const creating = ref(false)
const error = ref<string | null>(null)
const draft = ref({
    type: 'remote' as 'local' | 'remote',
    name: 'remote-1',
    host: '',
    port: 22,
    user: 'root',
})

onMounted(async () => {
    try { await serversStore.fetchAll() }
    catch (e) { notify.error(apiErrorMessage(e)) }
})

async function submitAdd() {
    error.value = null
    creating.value = true
    try {
        const body: Record<string, unknown> = {
            type: draft.value.type,
            name: draft.value.name,
        }
        if (draft.value.type === 'remote') {
            body.host = draft.value.host
            body.port = draft.value.port
            body.user = draft.value.user
            body.generate_key = true
        }
        await serversStore.create(body)
        addOpen.value = false
        notify.success(t('servers.toast.added'))
    } catch (e) {
        error.value = apiErrorMessage(e)
    } finally {
        creating.value = false
    }
}

async function testServer(id: string) {
    try {
        const report = await serversStore.test(id)
        const ok = report.checks.every((c) => c.ok)
        if (ok) notify.success(t('servers.toast.healthy'))
        else notify.warn(t('servers.toast.checks_failed'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

function removeServer(id: string) {
    confirm.require({
        header: t('servers.remove.title'),
        message: t('servers.remove.body'),
        icon: 'pi pi-exclamation-triangle',
        rejectLabel: t('common.cancel'),
        acceptLabel: t('servers.remove.accept'),
        acceptClass: 'p-button-danger',
        accept: () => { void removeServerConfirmed(id) },
    })
}

async function removeServerConfirmed(id: string) {
    try {
        await serversStore.remove(id)
        notify.success(t('servers.toast.removed'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}
</script>

<style scoped>
.servers-view { width: 100%; }
.server-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
    gap: 16px;
}
.server-card { padding: 18px; display: flex; flex-direction: column; gap: 12px; }
.server-card__head { display: flex; justify-content: space-between; align-items: flex-start; gap: 8px; }
.server-card__name { font-size: 15px; font-weight: 600; color: var(--p-text); }
.server-card__meta { font-size: 12px; color: var(--p-text-muted); margin-top: 2px; font-family: ui-monospace, Menlo, monospace; }
.server-card__body { display: flex; flex-direction: column; gap: 4px; font-size: 13px; }
.server-card__foot {
    display: flex;
    justify-content: space-between;
    border-top: 1px solid var(--p-divider);
    padding-top: 8px;
    margin-top: 4px;
}
.muted { color: var(--p-text-muted); font-size: 12px; margin-right: 4px; }
.dialog-body { display: flex; flex-direction: column; gap: 12px; padding-top: 8px; }
.row-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.radio-row { display: flex; flex-direction: column; gap: 8px; margin-bottom: 4px; }
.radio-card {
    display: flex; gap: 12px; align-items: center;
    padding: 12px; border: 1px solid var(--p-content-border); border-radius: 8px;
    cursor: pointer;
    background: var(--p-content-bg);
}
.radio-card.active { border-color: var(--p-primary-500); background: rgba(16, 185, 129, 0.08); }
.radio-card strong { display: block; color: var(--p-text); font-size: 13px; font-weight: 600; }
.radio-card small { color: var(--p-text-muted); font-size: 12px; }
:deep(.p-inputnumber-input), :deep(.p-inputtext) { width: 100%; }
</style>
