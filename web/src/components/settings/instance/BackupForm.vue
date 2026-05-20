<template>
    <SCard
        :title="t('settings.instance.backup.title')"
        :sub="t('settings.instance.backup.sub')"
    >
        <Message severity="info" :closable="false" class="intro">
            <i18n-t keypath="settings.instance.backup.intro" tag="span">
                <template #strong>
                    <strong>{{ t('settings.instance.backup.intro_strong') }}</strong>
                </template>
            </i18n-t>
        </Message>

        <!-- ─────────── List ─────────── -->
        <div v-if="loading && entries.length === 0" class="muted">{{ t('common.loading') }}</div>

        <div v-else-if="entries.length === 0" class="empty">
            <div class="empty__icon"><i class="pi pi-database" aria-hidden="true" /></div>
            <div class="empty__copy">
                <strong>{{ t('settings.instance.backup.empty.title') }}</strong>
                <p>{{ t('settings.instance.backup.empty.body') }}</p>
            </div>
        </div>

        <ul v-else class="entry-list">
            <li v-for="e in entries" :key="e.id" class="entry">
                <div class="entry__main">
                    <div class="entry__name mono">{{ e.file_name }}</div>
                    <div class="entry__meta">
                        <span>{{ formatBytes(e.size_bytes) }}</span>
                        <span class="dot" aria-hidden="true">·</span>
                        <span>{{ formatDateTime(toEpoch(e.created_at)) }}</span>
                        <span class="dot" aria-hidden="true">·</span>
                        <span class="muted">{{ formatRelative(toEpoch(e.created_at)) }}</span>
                    </div>
                </div>
                <div class="entry__actions">
                    <a
                        class="p-button p-button-text p-button-sm"
                        :href="backupDownloadURL(e.id)"
                        :download="e.file_name"
                        :title="t('settings.instance.backup.list.download')"
                        :aria-label="t('settings.instance.backup.list.download')"
                    >
                        <i class="pi pi-download" aria-hidden="true" />
                    </a>
                    <Button
                        text
                        severity="danger"
                        size="small"
                        icon="pi pi-trash"
                        :aria-label="t('settings.instance.backup.list.delete')"
                        :title="t('settings.instance.backup.list.delete')"
                        :loading="busyId === e.id"
                        :disabled="busyId !== null && busyId !== e.id"
                        @click="confirmDelete(e)"
                    />
                </div>
            </li>
        </ul>

        <div class="actions">
            <Button
                :label="t('settings.instance.backup.create.cta')"
                icon="pi pi-cloud-download"
                @click="openCreate"
            />
        </div>
    </SCard>

    <!-- ─────────── Create dialog ─────────── -->
    <Dialog
        v-model:visible="createOpen"
        modal
        :header="t('settings.instance.backup.create.title')"
        :style="{ width: '520px' }"
        :closable="!createBusy"
    >
        <form class="form" @submit.prevent="submitCreate" novalidate>
            <Message severity="warn" :closable="false">
                <i18n-t keypath="settings.instance.backup.create.warn" tag="span">
                    <template #strong>
                        <strong>{{ t('settings.instance.backup.create.warn_strong') }}</strong>
                    </template>
                </i18n-t>
            </Message>

            <SField
                :label="t('settings.instance.backup.create.passphrase_label')"
                :hint="t('settings.instance.backup.create.passphrase_hint')"
                :error="passphraseError ?? undefined"
            >
                <InputText
                    v-model="passphrase"
                    type="password"
                    autocomplete="new-password"
                    autofocus
                />
            </SField>

            <SField
                :label="t('settings.instance.backup.create.confirm_label')"
                :error="confirmError ?? undefined"
            >
                <InputText
                    v-model="passphraseConfirm"
                    type="password"
                    autocomplete="new-password"
                />
            </SField>

            <Message v-if="createError" severity="error" :closable="false">{{ createError }}</Message>

            <div class="actions actions--row actions--end">
                <Button text type="button" :label="t('common.cancel')" :disabled="createBusy" @click="closeCreate" />
                <Button
                    type="submit"
                    :label="t('settings.instance.backup.create.submit')"
                    :loading="createBusy"
                />
            </div>
        </form>
    </Dialog>

    <!-- ─────────── Delete confirm ─────────── -->
    <Dialog
        v-model:visible="deleteOpen"
        modal
        :header="t('settings.instance.backup.delete.title')"
        :style="{ width: '440px' }"
        :closable="!deleteBusy"
    >
        <p class="prose">
            {{ t('settings.instance.backup.delete.body', { name: deletingName }) }}
        </p>
        <Message severity="warn" :closable="false">
            {{ t('settings.instance.backup.delete.warn') }}
        </Message>
        <template #footer>
            <Button text severity="secondary" :label="t('common.cancel')" :disabled="deleteBusy" @click="deleteOpen = false" />
            <Button severity="danger" :label="t('settings.instance.backup.delete.confirm')" :loading="deleteBusy" @click="doDelete" />
        </template>
    </Dialog>
</template>

<script setup lang="ts">
/*
    Backup card. Lives in Settings → Instância, above the Advanced
    collapse — backups are "operational hygiene", same tier as
    Resource Defaults.

    UX shape:
      - Intro Message explains the trust model: panel encrypts the
        file, operator owns the passphrase, restore is CLI-only.
      - List shows existing snapshots with download (plain <a> so the
        browser streams to disk) and delete (destructive confirm).
      - "Create" opens a dialog that asks for a passphrase + confirm.
        Strength gate is light (12 chars min) — the backend enforces
        the same; we mirror it inline so the operator doesn't lose
        their typed value to a backend 400.

    There is NO restore path here. Hot-swapping the running SQLite is
    unsafe, so restore lives in the CLI as `prexel admin restore`. The
    intro message points the operator at that workflow.
*/
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import { backupsService, backupDownloadURL, type BackupEntry } from '@/services/backups'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import { formatDateTime, formatRelative } from '@/utils/format'

const { t } = useI18n()

const entries = ref<BackupEntry[]>([])
const loading = ref(true)
const busyId = ref<string | null>(null)

// ── Create ──
const createOpen = ref(false)
const passphrase = ref('')
const passphraseConfirm = ref('')
const createBusy = ref(false)
const createError = ref<string | null>(null)

const passphraseError = computed<string | null>(() => {
    if (!createOpen.value) return null
    if (passphrase.value.length === 0) return null
    if (passphrase.value.length < 12) return t('settings.instance.backup.create.passphrase_too_short')
    return null
})
const confirmError = computed<string | null>(() => {
    if (!createOpen.value) return null
    if (passphraseConfirm.value.length === 0) return null
    if (passphrase.value !== passphraseConfirm.value) return t('settings.instance.backup.create.confirm_mismatch')
    return null
})

// ── Delete ──
const deleteOpen = ref(false)
const deletingId = ref<string | null>(null)
const deletingName = ref('')
const deleteBusy = ref(false)

onMounted(() => load())

async function load() {
    loading.value = true
    try {
        entries.value = await backupsService.list()
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        loading.value = false
    }
}

function openCreate() {
    passphrase.value = ''
    passphraseConfirm.value = ''
    createError.value = null
    createOpen.value = true
}

function closeCreate() {
    if (createBusy.value) return
    createOpen.value = false
}

async function submitCreate() {
    createError.value = null
    if (passphrase.value.length < 12) {
        createError.value = t('settings.instance.backup.create.passphrase_too_short')
        return
    }
    if (passphrase.value !== passphraseConfirm.value) {
        createError.value = t('settings.instance.backup.create.confirm_mismatch')
        return
    }
    createBusy.value = true
    try {
        const created = await backupsService.create(passphrase.value)
        entries.value = [created, ...entries.value]
        notify.success(t('settings.instance.backup.create.success'))
        createOpen.value = false
    } catch (e) {
        createError.value = apiErrorMessage(e)
    } finally {
        createBusy.value = false
    }
}

function confirmDelete(entry: BackupEntry) {
    deletingId.value = entry.id
    deletingName.value = entry.file_name
    deleteOpen.value = true
}

async function doDelete() {
    if (!deletingId.value) return
    deleteBusy.value = true
    busyId.value = deletingId.value
    try {
        await backupsService.remove(deletingId.value)
        entries.value = entries.value.filter((x) => x.id !== deletingId.value)
        notify.success(t('settings.instance.backup.delete.deleted'))
        deleteOpen.value = false
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        deleteBusy.value = false
        busyId.value = null
        deletingId.value = null
    }
}

function toEpoch(iso: string | null | undefined): number {
    if (!iso) return 0
    const d = new Date(iso).getTime()
    return Number.isFinite(d) ? Math.floor(d / 1000) : 0
}

// Format bytes into a human-readable string. Stays small (1KB-ish)
// so we keep it inline rather than pulling in a fmt utility.
function formatBytes(n: number): string {
    if (n < 1024) return `${n} B`
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
    if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
    return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}
</script>

<style scoped>
.muted { color: var(--p-text-muted); font-size: 13px; padding: 12px 0; }
.mono { font-family: ui-monospace, Menlo, monospace; }

.intro { margin-bottom: 16px; }

/* ── Empty state ── */
.empty {
    display: flex;
    align-items: center;
    gap: 16px;
    padding: 16px;
    border: 1px dashed var(--p-content-border);
    border-radius: 12px;
    background: var(--p-surface-50);
    margin-bottom: 12px;
}
.empty__icon {
    width: 44px;
    height: 44px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 10px;
    background: rgba(16, 185, 129, 0.12);
    color: var(--p-primary-500);
    font-size: 18px;
}
.empty__copy strong {
    display: block;
    font-size: 13px;
    color: var(--p-text);
    font-weight: 600;
}
.empty__copy p {
    margin: 4px 0 0;
    color: var(--p-text-muted);
    font-size: 12px;
    line-height: 1.5;
}

/* ── Entry list ── */
.entry-list {
    list-style: none;
    padding: 0;
    margin: 0 0 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
}
.entry {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 12px 14px;
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    background: var(--p-content-bg);
}
.entry__main { min-width: 0; flex: 1; }
.entry__name {
    font-size: 13px;
    color: var(--p-text);
    margin-bottom: 4px;
    word-break: break-all;
}
.entry__meta {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    color: var(--p-text-muted);
    font-size: 11px;
}
.entry__actions {
    display: inline-flex;
    align-items: center;
    gap: 4px;
}
.dot { color: var(--p-text-muted); }

/* ── Actions row ── */
.actions { display: flex; justify-content: flex-end; gap: 8px; }
.actions--row { flex-direction: row; }
.actions--end { justify-content: flex-end; }

/* ── Form ── */
.form { display: flex; flex-direction: column; gap: 16px; }
.prose { margin: 0 0 12px; color: var(--p-text); line-height: 1.5; font-size: 14px; }
</style>
