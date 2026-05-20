<template>
    <!-- ─────────── Existing tokens ─────────── -->
    <SCard
        :title="t('settings.apiTokens.list.title')"
        :sub="t('settings.apiTokens.list.sub')"
    >
        <div v-if="loading && tokens.length === 0" class="muted">{{ t('common.loading') }}</div>

        <!-- Empty state: first-time users land here with nothing -->
        <div v-else-if="tokens.length === 0" class="empty">
            <div class="empty__icon"><i class="pi pi-key" aria-hidden="true" /></div>
            <div class="empty__copy">
                <strong>{{ t('settings.apiTokens.empty.title') }}</strong>
                <p>{{ t('settings.apiTokens.empty.body') }}</p>
            </div>
        </div>

        <ul v-else class="token-list">
            <!--
                Loop variable is `tok`, NOT `t` — `t` is the i18n function
                from useI18n(), and shadowing it inside v-for would make
                every `t(...)` call inside the row throw "t is not a
                function", silently failing the render.
            -->
            <li v-for="tok in tokens" :key="tok.id" class="token">
                <div class="token__main">
                    <div class="token__name">{{ tok.name }}</div>
                    <div class="token__meta">
                        <span class="mono token__hint">prx_pat_…{{ tok.last_four }}</span>
                        <span class="dot" aria-hidden="true">·</span>
                        <span>{{ t('settings.apiTokens.list.created', { when: formatRelative(toEpoch(tok.created_at)) }) }}</span>
                        <template v-if="tok.last_used_at">
                            <span class="dot" aria-hidden="true">·</span>
                            <span>{{ t('settings.apiTokens.list.last_used', { when: formatRelative(toEpoch(tok.last_used_at)) }) }}</span>
                        </template>
                        <template v-else>
                            <span class="dot" aria-hidden="true">·</span>
                            <span class="muted">{{ t('settings.apiTokens.list.never_used') }}</span>
                        </template>
                        <template v-if="tok.expires_at">
                            <span class="dot" aria-hidden="true">·</span>
                            <span :class="expiryClass(tok.expires_at)">
                                {{ t('settings.apiTokens.list.expires', { when: formatRelative(toEpoch(tok.expires_at)) }) }}
                            </span>
                        </template>
                    </div>
                </div>
                <Button
                    text
                    severity="danger"
                    size="small"
                    icon="pi pi-trash"
                    :aria-label="t('settings.apiTokens.list.revoke')"
                    :title="t('settings.apiTokens.list.revoke')"
                    :loading="busyId === tok.id"
                    :disabled="busyId !== null && busyId !== tok.id"
                    @click="confirmRevoke(tok)"
                />
            </li>
        </ul>

        <div class="actions">
            <Button
                :label="t('settings.apiTokens.create.cta')"
                icon="pi pi-plus"
                @click="openCreate"
            />
        </div>
    </SCard>

    <!-- ─────────── Create dialog ─────────── -->
    <Dialog
        v-model:visible="createOpen"
        modal
        :header="t('settings.apiTokens.create.title')"
        :style="{ width: '480px' }"
        :closable="!createBusy && !createdRaw"
    >
        <!-- Step 1 (default): form -->
        <form v-if="!createdRaw" class="form" @submit.prevent="submitCreate" novalidate>
            <SField :label="t('settings.apiTokens.create.name_label')" :hint="t('settings.apiTokens.create.name_hint')" :error="nameError ?? undefined">
                <InputText
                    v-model="newName"
                    :placeholder="t('settings.apiTokens.create.name_placeholder')"
                    autofocus
                    maxlength="100"
                />
            </SField>

            <SField :label="t('settings.apiTokens.create.expiry_label')" :hint="t('settings.apiTokens.create.expiry_hint')">
                <div class="expiry-row">
                    <UiSelect
                        v-model="expirySelection"
                        :options="expiryOptions"
                        :placeholder="t('settings.apiTokens.create.expiry_label')"
                    />
                </div>
            </SField>

            <Message v-if="createError" severity="error" :closable="false">{{ createError }}</Message>

            <div class="actions actions--row actions--end">
                <Button text type="button" :label="t('common.cancel')" :disabled="createBusy" @click="closeCreate" />
                <Button
                    type="submit"
                    :label="t('settings.apiTokens.create.submit')"
                    :loading="createBusy"
                />
            </div>
        </form>

        <!-- Step 2: success — show raw token ONCE -->
        <div v-else class="created">
            <Message severity="warn" :closable="false">
                <strong>{{ t('settings.apiTokens.created.warn_strong') }}</strong>
                {{ t('settings.apiTokens.created.warn_body') }}
            </Message>
            <div class="raw-box">
                <code class="mono raw-box__value">{{ createdRaw }}</code>
                <Button
                    text
                    icon="pi pi-copy"
                    size="small"
                    :aria-label="t('common.copy')"
                    @click="copyRaw"
                />
            </div>
            <p class="hint">{{ t('settings.apiTokens.created.usage_hint') }}</p>
            <div class="actions actions--row actions--end">
                <Button :label="t('common.done')" @click="closeCreate" />
            </div>
        </div>
    </Dialog>

    <!-- ─────────── Revoke confirm ─────────── -->
    <Dialog
        v-model:visible="revokeOpen"
        modal
        :header="t('settings.apiTokens.revoke.title')"
        :style="{ width: '440px' }"
        :closable="!revokeBusy"
    >
        <p class="prose">
            {{ t('settings.apiTokens.revoke.body', { name: revokingName }) }}
        </p>
        <Message severity="warn" :closable="false">
            {{ t('settings.apiTokens.revoke.warn') }}
        </Message>
        <template #footer>
            <Button text severity="secondary" :label="t('common.cancel')" :disabled="revokeBusy" @click="revokeOpen = false" />
            <Button severity="danger" :label="t('settings.apiTokens.revoke.confirm')" :loading="revokeBusy" @click="doRevoke" />
        </template>
    </Dialog>
</template>

<script setup lang="ts">
/*
    Settings → API Tokens.

    UX model:
      - List shows active tokens with name + last 4 chars + audit fields.
      - "Create" opens a dialog. After submit, the dialog flips into a
        success state that displays the raw token ONCE with a copy
        button + warning. The operator can't reopen this — if they
        miss the copy, they revoke and recreate.
      - "Revoke" is a destructive confirm dialog. We don't soft-delete
        on the client; rely on the backend soft-delete + filter.

    Why a dedicated section page (vs. cramming into Security):
      Security already has 2FA + password change with custom flows.
      Adding a third destructive surface there would compete for the
      operator's attention. PATs deserve their own page so the rail
      navigation is honest about what this section does.
*/
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import UiSelect from '@/components/ui/UiSelect.vue'
import { apiTokensService, type ApiToken } from '@/services/apiTokens'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import { formatRelative } from '@/utils/format'

const { t } = useI18n()

const tokens = ref<ApiToken[]>([])
const loading = ref(true)
const busyId = ref<string | null>(null)

// ── Create state ────────────────────────────────────────────────
const createOpen = ref(false)
const newName = ref('')
const expirySelection = ref<string>('never')
const createBusy = ref(false)
const createError = ref<string | null>(null)
const nameError = ref<string | null>(null)
const createdRaw = ref<string | null>(null)

const expiryOptions = computed(() => [
    { label: t('settings.apiTokens.create.expiry_never'), value: 'never' },
    { label: t('settings.apiTokens.create.expiry_30'), value: '30' },
    { label: t('settings.apiTokens.create.expiry_90'), value: '90' },
    { label: t('settings.apiTokens.create.expiry_365'), value: '365' },
])

// ── Revoke state ────────────────────────────────────────────────
const revokeOpen = ref(false)
const revokingId = ref<string | null>(null)
const revokingName = ref('')
const revokeBusy = ref(false)

onMounted(() => load())

async function load() {
    loading.value = true
    try {
        tokens.value = await apiTokensService.list()
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        loading.value = false
    }
}

function openCreate() {
    newName.value = ''
    expirySelection.value = 'never'
    nameError.value = null
    createError.value = null
    createdRaw.value = null
    createOpen.value = true
}

function closeCreate() {
    createOpen.value = false
    // Reload silently — a newly created token is now in the list.
    if (createdRaw.value) {
        createdRaw.value = null
        void load()
    }
}

async function submitCreate() {
    nameError.value = null
    createError.value = null
    const name = newName.value.trim()
    if (!name) {
        nameError.value = t('settings.apiTokens.create.name_required')
        return
    }
    createBusy.value = true
    try {
        const expiresAt = expiryToISO(expirySelection.value)
        const res = await apiTokensService.create({
            name,
            expires_at: expiresAt,
        })
        createdRaw.value = res.raw
    } catch (e) {
        createError.value = apiErrorMessage(e)
    } finally {
        createBusy.value = false
    }
}

function expiryToISO(value: string): string | null {
    if (value === 'never') return null
    const days = Number(value)
    if (!Number.isFinite(days) || days <= 0) return null
    return new Date(Date.now() + days * 24 * 60 * 60 * 1000).toISOString()
}

function copyRaw() {
    if (!createdRaw.value) return
    navigator.clipboard.writeText(createdRaw.value)
        .then(() => notify.success(t('common.copied')))
        .catch(() => notify.error(t('errors.clipboard')))
}

function confirmRevoke(tok: ApiToken) {
    revokingId.value = tok.id
    revokingName.value = tok.name
    revokeOpen.value = true
}

async function doRevoke() {
    if (!revokingId.value) return
    revokeBusy.value = true
    busyId.value = revokingId.value
    try {
        await apiTokensService.revoke(revokingId.value)
        tokens.value = tokens.value.filter((x) => x.id !== revokingId.value)
        notify.success(t('settings.apiTokens.revoke.revoked'))
        revokeOpen.value = false
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        revokeBusy.value = false
        busyId.value = null
        revokingId.value = null
    }
}

// formatRelative takes an epoch in SECONDS; our API returns ISO strings,
// so convert here. Returns 0 (= "agora") on parse failure — not ideal
// but safer than throwing on a malformed timestamp.
function toEpoch(iso: string | null | undefined): number {
    if (!iso) return 0
    const d = new Date(iso).getTime()
    return Number.isFinite(d) ? Math.floor(d / 1000) : 0
}

// Visual hint when expiry is imminent. Below 14 days we tint amber so
// the operator notices before their CI job fails on a Friday afternoon.
function expiryClass(iso: string | null | undefined): string {
    if (!iso) return ''
    const days = (new Date(iso).getTime() - Date.now()) / (1000 * 60 * 60 * 24)
    if (days < 0) return 'expiry expiry--past'
    if (days < 14) return 'expiry expiry--soon'
    return 'expiry'
}
</script>

<style scoped>
.muted { color: var(--p-text-muted); font-size: 13px; padding: 12px 0; }
.mono { font-family: ui-monospace, Menlo, monospace; }

/* ── Empty state ── */
.empty {
    display: flex;
    align-items: center;
    gap: 16px;
    padding: 16px;
    border: 1px dashed var(--p-content-border);
    border-radius: 12px;
    background: var(--p-surface-50);
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

/* ── Token list ── */
.token-list {
    list-style: none;
    padding: 0;
    margin: 0 0 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
}
.token {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 12px 14px;
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    background: var(--p-content-bg);
}
.token__main { min-width: 0; flex: 1; }
.token__name {
    font-size: 13px;
    font-weight: 600;
    color: var(--p-text);
    margin-bottom: 4px;
}
.token__meta {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    color: var(--p-text-muted);
    font-size: 11px;
}
.token__hint { color: var(--p-text); }
.dot { color: var(--p-text-muted); }
.expiry--soon { color: var(--p-warning, #d97706); font-weight: 500; }
.expiry--past { color: var(--p-danger, #dc2626); font-weight: 500; }

/* ── Actions row ── */
.actions { display: flex; justify-content: flex-end; gap: 8px; }
.actions--row { flex-direction: row; }
.actions--end { justify-content: flex-end; }

/* ── Create dialog form ── */
.form { display: flex; flex-direction: column; gap: 16px; }
.expiry-row { width: 220px; max-width: 100%; }

/* ── Success state (raw token shown once) ── */
.created { display: flex; flex-direction: column; gap: 14px; }
.raw-box {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 12px;
    border-radius: 10px;
    border: 1px solid var(--p-content-border);
    background: var(--p-surface-50);
}
.raw-box__value {
    flex: 1;
    font-size: 12px;
    color: var(--p-text);
    word-break: break-all;
    user-select: all;
}
.hint { font-size: 12px; color: var(--p-text-muted); margin: 0; }

.prose { margin: 0 0 12px; color: var(--p-text); line-height: 1.5; font-size: 14px; }
</style>
