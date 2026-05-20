<template>
    <div class="storage">
        <!-- ─────────── Header / explanatory copy ─────────── -->
        <section class="storage__intro">
            <header class="storage__head">
                <div>
                    <h3 class="storage__title">{{ t('storage.title') }}</h3>
                    <p class="storage__sub">
                        <i18n-t keypath="storage.sub" scope="global">
                            <template #named><strong>{{ t('storage.named') }}</strong></template>
                            <template #bind><strong>{{ t('storage.bind') }}</strong></template>
                        </i18n-t>
                    </p>
                </div>
                <Button
                    v-if="volumes.length > 0"
                    :label="t('storage.add')"
                    icon="pi pi-plus"
                    size="small"
                    :disabled="!app"
                    @click="openCreate"
                />
            </header>
        </section>

        <!-- ─────────── Empty state ─────────── -->
        <div v-if="!loading && volumes.length === 0" class="storage__empty">
            <p class="storage__empty-text">
                {{ t('storage.emptyText') }}
            </p>
            <Button
                :label="t('storage.add')"
                icon="pi pi-plus"
                size="small"
                :disabled="!app"
                @click="openCreate"
            />
        </div>

        <!-- ─────────── Volumes table ─────────── -->
        <table v-else-if="volumes.length > 0" class="vt">
            <thead>
                <tr>
                    <th>{{ t('storage.columns.mountPath') }}</th>
                    <th>{{ t('storage.columns.type') }}</th>
                    <th>{{ t('storage.columns.origin') }}</th>
                    <th v-if="isCompose">{{ t('storage.columns.service') }}</th>
                    <th>{{ t('storage.columns.readOnly') }}</th>
                    <th class="vt__act"></th>
                </tr>
            </thead>
            <tbody>
                <tr v-for="v in volumes" :key="v.id">
                    <td>
                        <code class="vt__path">{{ v.mount_path }}</code>
                    </td>
                    <td>
                        <span v-if="v.is_named" class="pill pill--named">{{ t('storage.pillNamed') }}</span>
                        <span v-else class="pill pill--bind">{{ t('storage.pillBind') }}</span>
                    </td>
                    <td>
                        <code v-if="v.is_named" class="vt__origin mono" :title="namedDisplay(v)">
                            {{ namedDisplay(v) }}
                        </code>
                        <code v-else class="vt__origin mono" :title="v.host_path ?? ''">
                            {{ v.host_path }}
                        </code>
                    </td>
                    <td v-if="isCompose">
                        <span v-if="v.service" class="pill pill--service mono">{{ v.service }}</span>
                        <span v-else class="muted">—</span>
                    </td>
                    <td>
                        <span v-if="v.read_only" class="pill pill--ro">{{ t('storage.pillRo') }}</span>
                        <span v-else class="muted">—</span>
                    </td>
                    <td class="vt__act">
                        <Button
                            text
                            size="small"
                            icon="pi pi-pencil"
                            :aria-label="t('storage.editAria')"
                            :title="t('storage.editAria')"
                            @click="openEdit(v)"
                        />
                        <Button
                            text
                            severity="danger"
                            size="small"
                            icon="pi pi-trash"
                            :aria-label="t('storage.removeAria')"
                            :title="t('storage.removeAria')"
                            :loading="removingId === v.id"
                            @click="confirmRemove(v)"
                        />
                    </td>
                </tr>
            </tbody>
        </table>

        <!-- ─────────── Create / edit dialog ─────────── -->
        <Dialog
            v-model:visible="formOpen"
            modal
            :header="editing ? t('storage.form.editHeader') : t('storage.form.newHeader')"
            :style="{ width: '560px' }"
            :closable="!saving"
        >
            <div class="form">
                <SField :label="t('storage.form.typeLabel')">
                    <div class="form__radios">
                        <label class="form__radio">
                            <RadioButton v-model="form.isNamed" :value="true" name="vol-type" />
                            <span>
                                <strong>{{ t('storage.form.namedLabel') }}</strong>
                                <small>{{ t('storage.form.namedHint') }}</small>
                            </span>
                        </label>
                        <label class="form__radio">
                            <RadioButton v-model="form.isNamed" :value="false" name="vol-type" />
                            <span>
                                <strong>{{ t('storage.form.bindLabel') }}</strong>
                                <small>{{ t('storage.form.bindHint') }}</small>
                            </span>
                        </label>
                    </div>
                </SField>

                <SField
                    :label="t('storage.form.mountPathLabel')"
                    :hint="t('storage.form.mountPathHint')"
                    :error="mountPathError ?? undefined"
                >
                    <InputText
                        v-model="form.mountPath"
                        placeholder="/var/lib/postgresql/data"
                        spellcheck="false"
                        autocomplete="off"
                    />
                </SField>

                <SField
                    v-if="!form.isNamed"
                    :label="t('storage.form.hostPathLabel')"
                    :hint="t('storage.form.hostPathHint')"
                    :error="hostPathError ?? undefined"
                >
                    <InputText
                        v-model="form.hostPath"
                        placeholder="/opt/myapp/data"
                        spellcheck="false"
                        autocomplete="off"
                    />
                </SField>

                <SField
                    v-if="isCompose"
                    :label="t('storage.form.serviceLabel')"
                    :hint="t('storage.form.serviceHint')"
                    :error="serviceError ?? undefined"
                >
                    <InputText
                        v-model="form.service"
                        :placeholder="t('storage.form.servicePlaceholder')"
                        spellcheck="false"
                        autocomplete="off"
                    />
                </SField>

                <label class="form__check">
                    <Checkbox v-model="form.readOnly" binary />
                    <span>{{ t('storage.form.readOnlyText') }}</span>
                </label>

                <Message v-if="formError" severity="error" :closable="false">{{ formError }}</Message>
            </div>
            <template #footer>
                <Button text severity="secondary" :label="t('common.cancel')" :disabled="saving" @click="formOpen = false" />
                <Button
                    :label="editing ? t('storage.form.save') : t('storage.form.add')"
                    :loading="saving"
                    :disabled="!canSave"
                    @click="submit"
                />
            </template>
        </Dialog>

        <!-- ─────────── Remove confirm dialog ─────────── -->
        <Dialog
            v-model:visible="removeOpen"
            modal
            :header="t('storage.removeDialog.header')"
            :style="{ width: '480px' }"
            :closable="!removingId"
        >
            <p class="prose">
                <i18n-t keypath="storage.removeDialog.body" scope="global">
                    <template #strong><strong>{{ t('storage.removeDialog.bodyStrong') }}</strong></template>
                    <template #cmd><code>docker volume rm …</code></template>
                </i18n-t>
            </p>
            <p v-if="pendingRemove" class="prose">
                {{ t('storage.removeDialog.mountPathLine') }} <code>{{ pendingRemove.mount_path }}</code><br>
                {{ t('storage.removeDialog.originLine') }} <code class="mono">{{ originLabel(pendingRemove) }}</code>
            </p>
            <template #footer>
                <Button text severity="secondary" :label="t('common.cancel')" :disabled="!!removingId" @click="removeOpen = false" />
                <Button severity="danger" :label="t('apps.remove')" :loading="!!removingId" @click="doRemove" />
            </template>
        </Dialog>
    </div>
</template>

<script setup lang="ts">
/*
    StorageTab — manages per-app persistent volumes (app_volumes).

    Two flavours behind one form:
      - Named volume:  Docker-managed. host_path empty; backend derives
        a deterministic name like `prexel-vol-<app>-<hash>`.
      - Bind mount:    host_path required; the operator owns the path.

    Validation mirrors the backend rules so the operator gets inline
    feedback instead of an HTTP 400 round-trip:
      - mount_path: absolute, 1-512 chars, no `..` segments
      - host_path:  absolute, 1-512 chars (when set)
      - is_named=false ⇒ host_path required
      - is_named=true  ⇒ host_path must be empty
      - service:    optional, ^[a-zA-Z0-9][a-zA-Z0-9._-]*$
*/
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import RadioButton from 'primevue/radiobutton'
import SField from '@/components/settings/SField.vue'
import { appVolumes } from '@/services/appVolumes'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { App, AppVolume } from '@/types/api'

const props = defineProps<{
    app: App | null
}>()

const { t } = useI18n()
const isCompose = computed(() => props.app?.build_type === 'docker_compose')

const volumes = ref<AppVolume[]>([])
const loading = ref(false)

// ── Form state ───────────────────────────────────────────────────
//
// Single form backs both create and edit. `editing` distinguishes
// the two; on Save we POST or PATCH accordingly.
interface FormState {
    isNamed: boolean
    mountPath: string
    hostPath: string
    service: string
    readOnly: boolean
}
const emptyForm = (): FormState => ({
    isNamed: true,
    mountPath: '',
    hostPath: '',
    service: '',
    readOnly: false,
})
const form = reactive<FormState>(emptyForm())
const formOpen = ref(false)
const editing = ref<AppVolume | null>(null)
const saving = ref(false)
const formError = ref<string | null>(null)

// ── Validation (mirrors backend; surfaces inline) ────────────────
const ABS_PATH_RE = /^\/[^\0]*$/
const SERVICE_RE = /^[a-zA-Z0-9][a-zA-Z0-9._-]*$/

const mountPathError = computed<string | null>(() => {
    const p = form.mountPath.trim()
    if (!p) return null // don't yell while still typing
    if (!ABS_PATH_RE.test(p)) return t('storage.validation.absolutePath')
    if (p.length > 512) return t('storage.validation.maxLength')
    if (p.split('/').some((seg) => seg === '..')) return t('storage.validation.noParentSeg')
    return null
})

const hostPathError = computed<string | null>(() => {
    if (form.isNamed) return null
    const p = form.hostPath.trim()
    if (!p) return null
    if (!ABS_PATH_RE.test(p)) return t('storage.validation.absolutePath')
    if (p.length > 512) return t('storage.validation.maxLength')
    return null
})

const serviceError = computed<string | null>(() => {
    const s = form.service.trim()
    if (!s) return null
    if (!SERVICE_RE.test(s)) return t('storage.validation.serviceFormat')
    return null
})

const canSave = computed(() => {
    if (saving.value) return false
    if (!form.mountPath.trim()) return false
    if (mountPathError.value) return false
    if (!form.isNamed && !form.hostPath.trim()) return false
    if (hostPathError.value) return false
    if (serviceError.value) return false
    return true
})

// ── Display helpers ──────────────────────────────────────────────
//
// For named volumes the backend computes the actual Docker name on
// create/deploy, but it isn't returned in `AppVolume`. We show a
// best-effort preview using the same convention so the operator
// recognises the volume in `docker volume ls`.
function namedDisplay(v: AppVolume): string {
    const appName = props.app?.name ?? 'app'
    const shortId = v.id.slice(0, 8)
    const suffix = v.service ? `-${v.service}` : ''
    return `prexel-vol-${appName}${suffix}-${shortId}`
}
function originLabel(v: AppVolume): string {
    return v.is_named ? namedDisplay(v) : (v.host_path ?? '')
}

// ── Load ─────────────────────────────────────────────────────────
async function load() {
    if (!props.app) return
    loading.value = true
    try {
        volumes.value = await appVolumes.list(props.app.id)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        loading.value = false
    }
}

// ── Create / Edit ────────────────────────────────────────────────
function openCreate() {
    editing.value = null
    Object.assign(form, emptyForm())
    formError.value = null
    formOpen.value = true
}

function openEdit(v: AppVolume) {
    editing.value = v
    form.isNamed = v.is_named
    form.mountPath = v.mount_path
    form.hostPath = v.host_path ?? ''
    form.service = v.service ?? ''
    form.readOnly = v.read_only
    formError.value = null
    formOpen.value = true
}

// Whenever the operator flips type, clear the field that no longer
// applies — keeps the submitted payload coherent and avoids a stale
// host_path slipping through on a named volume save.
watch(() => form.isNamed, (isNamed) => {
    if (isNamed) form.hostPath = ''
})

async function submit() {
    if (!props.app || !canSave.value) return
    saving.value = true
    formError.value = null
    try {
        const payload = {
            mount_path: form.mountPath.trim(),
            host_path: form.isNamed ? null : form.hostPath.trim(),
            is_named: form.isNamed,
            read_only: form.readOnly,
            service: form.service.trim() || null,
        }
        if (editing.value) {
            const updated = await appVolumes.update(props.app.id, editing.value.id, payload)
            volumes.value = volumes.value.map((v) => (v.id === updated.id ? updated : v))
            notify.success(t('storage.toast.updated'))
        } else {
            const created = await appVolumes.create(props.app.id, payload)
            volumes.value = [...volumes.value, created]
            notify.success(t('storage.toast.added'))
        }
        formOpen.value = false
    } catch (e) {
        formError.value = apiErrorMessage(e)
    } finally {
        saving.value = false
    }
}

// ── Remove ───────────────────────────────────────────────────────
const removeOpen = ref(false)
const pendingRemove = ref<AppVolume | null>(null)
const removingId = ref<string | null>(null)

function confirmRemove(v: AppVolume) {
    pendingRemove.value = v
    removeOpen.value = true
}

async function doRemove() {
    if (!props.app || !pendingRemove.value) return
    removingId.value = pendingRemove.value.id
    try {
        await appVolumes.remove(props.app.id, pendingRemove.value.id)
        volumes.value = volumes.value.filter((v) => v.id !== pendingRemove.value!.id)
        notify.success(t('storage.toast.removed'))
        removeOpen.value = false
        pendingRemove.value = null
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        removingId.value = null
    }
}

// ── Init ─────────────────────────────────────────────────────────
onMounted(load)
watch(() => props.app?.id, (next, prev) => {
    if (next !== prev) void load()
})
</script>

<style scoped>
.storage { display: flex; flex-direction: column; gap: 16px; }

/* ── Intro / header card ── */
.storage__intro {
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    background: var(--p-content-bg);
    padding: 18px 20px;
}
.storage__head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
}
.storage__title {
    margin: 0;
    font-size: 14px;
    font-weight: 700;
    color: var(--p-text);
    text-transform: uppercase;
    letter-spacing: 0.04em;
}
.storage__sub {
    margin: 4px 0 0;
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.5;
    max-width: 620px;
}
.storage__sub strong { color: var(--p-text); font-weight: 600; }

/* ── Empty state ── */
.storage__empty {
    padding: 28px 20px;
    text-align: center;
    border: 1px dashed var(--p-content-border);
    border-radius: 12px;
    background: var(--p-content-bg);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 12px;
}
.storage__empty-text {
    margin: 0;
    font-size: 13px;
    color: var(--p-text-muted);
    max-width: 460px;
    line-height: 1.5;
}

/* ── Volumes table ── */
.vt {
    width: 100%;
    border-collapse: collapse;
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    overflow: hidden;
    background: var(--p-content-bg);
}
.vt thead th {
    text-align: left;
    padding: 10px 14px;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--p-text-muted);
    font-weight: 600;
    background: color-mix(in srgb, var(--p-text-muted), transparent 92%);
    border-bottom: 1px solid var(--p-content-border);
}
.vt tbody td {
    padding: 10px 14px;
    border-bottom: 1px solid color-mix(in srgb, var(--p-content-border), transparent 40%);
    vertical-align: middle;
    font-size: 13px;
    /* td color doesn't always inherit from var(--p-text) on the dark
       theme — pin it explicitly so paths stay readable. Same fix we
       applied to EnvEditor's secrets table. */
    color: var(--p-text);
}
.vt tbody tr:last-child td { border-bottom: 0; }
.vt tbody tr:hover td {
    background: color-mix(in srgb, var(--p-primary-500), transparent 94%);
}
.vt__path {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 12px;
    padding: 2px 6px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 4px;
    color: var(--p-text);
}
.vt__origin {
    font-size: 12px;
    color: var(--p-text);
    word-break: break-all;
}
.vt__act {
    white-space: nowrap;
    text-align: right;
    width: 1%;
}

/* ── Pills ── */
.pill {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.03em;
}
.pill--named {
    background: color-mix(in srgb, var(--p-primary-500), transparent 85%);
    color: var(--p-primary-500);
}
.pill--bind {
    background: color-mix(in srgb, var(--p-warning, #d97706), transparent 80%);
    color: var(--p-warning, #d97706);
}
.pill--service {
    background: color-mix(in srgb, var(--p-info, #3b82f6), transparent 85%);
    color: var(--p-info, #3b82f6);
}
.pill--ro {
    background: color-mix(in srgb, var(--p-text-muted), transparent 80%);
    color: var(--p-text);
}

/* ── Form (create/edit dialog) ── */
.form { display: flex; flex-direction: column; gap: 14px; }
.form__radios { display: flex; flex-direction: column; gap: 8px; }
.form__radio {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 10px 12px;
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    cursor: pointer;
}
.form__radio:hover { border-color: color-mix(in srgb, var(--p-primary-500), transparent 60%); }
.form__radio span { display: flex; flex-direction: column; gap: 2px; }
.form__radio strong { font-size: 13px; color: var(--p-text); }
.form__radio small { font-size: 11px; color: var(--p-text-muted); line-height: 1.4; }
.form__check {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    color: var(--p-text);
    font-size: 13px;
}

.muted { color: var(--p-text-muted); }
.mono { font-family: ui-monospace, Menlo, monospace; }
.prose { margin: 0 0 12px; color: var(--p-text); font-size: 14px; line-height: 1.5; }
.prose code {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 12px;
    padding: 1px 6px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 4px;
}
</style>
