<template>
    <div class="env-editor">
        <!-- ─────────── Env vars (.env textarea) ─────────── -->
        <section class="env-block">
            <header class="env-block__head">
                <div>
                    <h3 class="env-block__title">{{ t('envEditor.envBlock.title') }}</h3>
                    <p class="env-block__sub">
                        <i18n-t keypath="envEditor.envBlock.sub" scope="global">
                            <template #dotenv><code>.env</code></template>
                            <template #hash><code>#</code></template>
                            <template #secrets><strong>{{ t('envEditor.envBlock.secrets') }}</strong></template>
                        </i18n-t>
                    </p>
                </div>
                <div class="env-block__actions">
                    <Button
                        v-if="envText"
                        size="small"
                        severity="secondary"
                        text
                        icon="pi pi-copy"
                        :label="t('envEditor.envBlock.copy')"
                        type="button"
                        :aria-label="t('envEditor.envBlock.copyAria')"
                        @click="copyEnv"
                    />
                </div>
            </header>

            <Textarea
                v-model="envText"
                class="env-textarea"
                spellcheck="false"
                autocomplete="off"
                rows="14"
                :placeholder="t('envEditor.envBlock.placeholder')"
            />

            <!--
                Per-line parse errors (invalid key, malformed line)
                surface inline so the operator sees exactly which line
                to fix without scrolling through a generic message.
            -->
            <ul v-if="envParseErrors.length" class="env-errors">
                <li v-for="err in envParseErrors" :key="err.line">
                    <strong>{{ t('envEditor.errors.linePrefix', { n: err.line }) }}</strong> {{ err.message }}
                </li>
            </ul>

            <div v-if="envDirty || envError" class="env-actions">
                <Message v-if="envError" severity="error" :closable="false">{{ envError }}</Message>
                <div class="env-actions__btns">
                    <Button text :label="t('envEditor.envBlock.discard')" :disabled="envSaving" @click="resetEnv" />
                    <Button
                        :label="t('envEditor.envBlock.save')"
                        icon="pi pi-check"
                        :loading="envSaving"
                        :disabled="!envValid"
                        @click="saveEnv"
                    />
                </div>
            </div>
        </section>

        <!-- ─────────── Secrets ─────────── -->
        <section class="env-block">
            <header class="env-block__head">
                <div>
                    <h3 class="env-block__title">{{ t('envEditor.secretsBlock.title') }}</h3>
                    <p class="env-block__sub">
                        {{ t('envEditor.secretsBlock.sub') }}
                    </p>
                </div>
            </header>

            <!-- Existing secrets — name only, value masked -->
            <table v-if="secrets.length" class="env-table">
                <thead>
                    <tr>
                        <th class="env-table__key">{{ t('envEditor.secretsBlock.columns.key') }}</th>
                        <th>{{ t('envEditor.secretsBlock.columns.type') }}</th>
                        <th class="env-table__act"></th>
                    </tr>
                </thead>
                <tbody>
                    <tr v-for="s in secrets" :key="s.key">
                        <td class="mono">{{ s.key }}</td>
                        <td>
                            <span v-if="s.is_build_time" class="pill pill--build">{{ t('envEditor.secretsBlock.buildTime') }}</span>
                            <span v-else class="pill pill--runtime">{{ t('envEditor.secretsBlock.runtime') }}</span>
                        </td>
                        <td class="env-table__act">
                            <Button
                                text
                                severity="danger"
                                size="small"
                                icon="pi pi-trash"
                                :aria-label="t('envEditor.secretsBlock.removeAria', { key: s.key })"
                                :loading="deletingKey === s.key"
                                @click="confirmDeleteSecret(s.key)"
                            />
                        </td>
                    </tr>
                </tbody>
            </table>
            <p v-else class="env-empty muted">{{ t('envEditor.secretsBlock.empty') }}</p>

            <!-- Add new / overwrite -->
            <form class="add-secret" @submit.prevent="submitNewSecret">
                <div class="add-secret__grid">
                    <SField :label="t('envEditor.secretsBlock.addForm.keyLabel')" :error="newSecretKeyError ?? undefined">
                        <InputText
                            v-model="newSecret.key"
                            :placeholder="t('envEditor.secretsBlock.addForm.keyPlaceholder')"
                            spellcheck="false"
                            autocomplete="off"
                        />
                    </SField>
                    <SField :label="t('envEditor.secretsBlock.addForm.valueLabel')" :error="newSecretValError ?? undefined">
                        <InputText
                            v-if="!newSecret.multiline"
                            v-model="newSecret.value"
                            type="password"
                            :placeholder="t('envEditor.secretsBlock.addForm.valuePlaceholder')"
                            spellcheck="false"
                            autocomplete="new-password"
                        />
                        <Textarea
                            v-else
                            v-model="newSecret.value"
                            rows="4"
                            spellcheck="false"
                            :placeholder="t('envEditor.secretsBlock.addForm.multilinePlaceholder')"
                        />
                    </SField>
                </div>
                <div class="add-secret__flags">
                    <label class="check-row">
                        <Checkbox v-model="newSecret.buildTime" binary />
                        <span>
                            <i18n-t keypath="envEditor.secretsBlock.addForm.buildTimeText" scope="global">
                                <template #arg><code>ARG</code></template>
                            </i18n-t>
                        </span>
                    </label>
                    <label class="check-row">
                        <Checkbox v-model="newSecret.multiline" binary />
                        <span>{{ t('envEditor.secretsBlock.addForm.multilineText') }}</span>
                    </label>
                </div>
                <div class="add-secret__actions">
                    <Button
                        type="submit"
                        size="small"
                        :loading="newSecretSaving"
                        :disabled="!canSaveSecret"
                        :label="overwriteWarn ? t('envEditor.secretsBlock.addForm.overwriteSecret') : t('envEditor.secretsBlock.addForm.addSecret')"
                        :severity="overwriteWarn ? 'warn' : undefined"
                        :icon="overwriteWarn ? 'pi pi-exclamation-triangle' : 'pi pi-plus'"
                    />
                </div>
            </form>
        </section>

        <!-- Delete confirm -->
        <Dialog
            v-model:visible="deleteOpen"
            modal
            :header="t('envEditor.deleteDialog.header')"
            :style="{ width: '420px' }"
            :closable="!deletingKey"
        >
            <p class="prose">
                <i18n-t keypath="envEditor.deleteDialog.body" scope="global">
                    <template #code><code>{{ pendingDeleteKey }}</code></template>
                </i18n-t>
            </p>
            <template #footer>
                <Button text severity="secondary" :label="t('common.cancel')" :disabled="!!deletingKey" @click="deleteOpen = false" />
                <Button severity="danger" :label="t('apps.remove')" :loading="!!deletingKey" @click="doDeleteSecret" />
            </template>
        </Dialog>
    </div>
</template>

<script setup lang="ts">
/*
    EnvEditor — both env vars (plaintext) and secrets (encrypted)
    editing surface for an app. Sits in the AppDetail "Variáveis"
    tab and replaces the old read-only table.

    Two separate sections because the data shapes / write semantics
    differ:
      - env_vars: full-document PUT. We mirror the full map locally,
        apply edits, and ship the whole thing on Save.
      - secrets:  per-key upsert/delete. The API never returns
        values, so we only render keys + flags + add-new form. To
        change a secret value, the operator submits the key again
        (backend treats as upsert).
*/
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Textarea from 'primevue/textarea'
import SField from '@/components/settings/SField.vue'
import { appEnv, type SecretMeta } from '@/services/appEnv'
import { useAppsStore } from '@/stores/apps'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { App } from '@/types/api'

const props = defineProps<{
    app: App | null
}>()

const emit = defineEmits<{
    /** Fired after a successful save so the parent can refetch the App. */
    'app-updated': []
}>()

const { t } = useI18n()
const appsStore = useAppsStore()

// ── Env vars (.env-formatted textarea) ───────────────────────────
//
// We persist as a map<string,string> on the backend, but the editor
// surface is a single textarea in .env format. Mirrors how Coolify /
// Vercel / Railway expose env editing — operators can paste their
// project's .env wholesale, tweak, and save without writing into a
// table cell by cell.
//
// Parse rules (loose by design, matches `dotenv`):
//   - blank lines and lines starting with `#` are dropped
//   - leading `export ` is stripped
//   - `KEY=VALUE` — first `=` is the separator; subsequent `=` are
//     part of the value
//   - value can be wrapped in `"..."` or `'...'` (quotes stripped)
//   - everything after `#` outside of quotes is treated as a comment
//     (Coolify's behaviour); leave a value bare or quote it if it
//     needs a literal `#`
//   - duplicate keys: last wins (matches shell behaviour)

const envText = ref('')
const envSaving = ref(false)
const envError = ref<string | null>(null)

// Snapshot of the saved state — compared against parsed text to detect dirty.
const savedEnv = ref<Record<string, string>>({})

interface EnvParseError { line: number; message: string }
interface EnvParseResult {
    vars: Record<string, string>
    errors: EnvParseError[]
}

// Serialize map → .env text. Keeps things deterministic (alpha sort)
// so the textarea doesn't shuffle on every reload.
function serializeEnv(m: Record<string, string>): string {
    return Object.entries(m)
        .sort((a, b) => a[0].localeCompare(b[0]))
        .map(([k, v]) => `${k}=${needsQuote(v) ? JSON.stringify(v) : v}`)
        .join('\n')
}

// Quote when the value contains spaces, comments, or unprintable
// chars. JSON.stringify covers the common escapes (\n, \", etc.).
function needsQuote(v: string): boolean {
    return /[\s"#'\\]/.test(v) || v === ''
}

// Parse .env text → { vars, errors }. errors is non-empty when the
// operator wrote something we couldn't classify (e.g. line without
// `=`); save is blocked while errors exist.
function parseEnv(text: string): EnvParseResult {
    const out: Record<string, string> = {}
    const errors: EnvParseError[] = []
    const lines = text.split(/\r?\n/)
    for (let i = 0; i < lines.length; i++) {
        let raw = lines[i] ?? ''
        const trimmed = raw.trim()
        if (trimmed === '' || trimmed.startsWith('#')) continue
        let line = trimmed
        if (line.startsWith('export ')) line = line.slice(7).trimStart()
        const eq = line.indexOf('=')
        if (eq <= 0) {
            errors.push({ line: i + 1, message: t('envEditor.errors.expectedKv') })
            continue
        }
        const key = line.slice(0, eq).trim()
        let value = line.slice(eq + 1)
        if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
            errors.push({ line: i + 1, message: t('envEditor.errors.invalidKey', { key }) })
            continue
        }
        // Strip surrounding quotes if balanced. Inline comments only
        // apply when value is NOT quoted — quoted values preserve `#`.
        const quoted = (value.startsWith('"') && value.endsWith('"') && value.length >= 2)
            || (value.startsWith("'") && value.endsWith("'") && value.length >= 2)
        if (quoted) {
            value = value.slice(1, -1)
            // Unescape \n and \" for double-quoted values (dotenv-ish).
            if (line.charAt(eq + 1) === '"') {
                value = value.replace(/\\n/g, '\n').replace(/\\"/g, '"').replace(/\\\\/g, '\\')
            }
        } else {
            // Inline comment: drop everything from a `#` preceded by
            // whitespace (matches dotenv).
            const cmt = value.search(/\s#/)
            if (cmt >= 0) value = value.slice(0, cmt)
            value = value.trim()
        }
        out[key] = value
    }
    return { vars: out, errors }
}

const parsed = computed<EnvParseResult>(() => parseEnv(envText.value))
const envParseErrors = computed(() => parsed.value.errors)

const envValid = computed(() => envParseErrors.value.length === 0)
const envDirty = computed(() => {
    if (!envValid.value) return true
    const next = parsed.value.vars
    const curr = savedEnv.value
    const a = Object.keys(next).sort().join(',')
    const b = Object.keys(curr).sort().join(',')
    if (a !== b) return true
    for (const k of Object.keys(next)) if (next[k] !== curr[k]) return true
    return false
})

function resetEnv() {
    envText.value = serializeEnv(savedEnv.value)
    envError.value = null
}

async function saveEnv() {
    if (!props.app || !envValid.value) return
    envSaving.value = true
    envError.value = null
    try {
        const payload = parsed.value.vars
        await appEnv.setEnvVars(props.app.id, payload)
        savedEnv.value = payload
        // Re-serialise from the canonical map: ordering + quoting
        // become normalised, which signals to the operator that the
        // save took effect (textarea visibly reformats).
        envText.value = serializeEnv(payload)
        emit('app-updated')
        notify.success(t('envEditor.toast.varsSaved'))
    } catch (e) {
        envError.value = apiErrorMessage(e)
    } finally {
        envSaving.value = false
    }
}

async function copyEnv() {
    try {
        await navigator.clipboard.writeText(envText.value)
        notify.success(t('envEditor.toast.copied'))
    } catch {
        notify.error(t('errors.clipboard'))
    }
}

// ── Secrets (per-key upsert/delete) ───────────────────────────────
const secrets = ref<SecretMeta[]>([])
const newSecret = ref({ key: '', value: '', buildTime: false, multiline: false })
const newSecretSaving = ref(false)
const deleteOpen = ref(false)
const pendingDeleteKey = ref('')
const deletingKey = ref<string | null>(null)

const newSecretKeyError = computed(() => {
    const k = newSecret.value.key.trim()
    if (!k) return null
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(k)) return t('envEditor.secretsBlock.validation.keyFormat')
    return null
})
const newSecretValError = computed(() => {
    if (newSecret.value.key.trim() && !newSecret.value.value) {
        return t('envEditor.secretsBlock.validation.valueRequired')
    }
    return null
})

// Heads-up when the operator is about to overwrite an existing key.
const overwriteWarn = computed(() => {
    const k = newSecret.value.key.trim()
    if (!k) return false
    return secrets.value.some((s) => s.key === k)
})
const canSaveSecret = computed(() => {
    return !!newSecret.value.key.trim()
        && !!newSecret.value.value
        && !newSecretKeyError.value
        && !newSecretSaving.value
})

async function loadSecrets() {
    if (!props.app) return
    try {
        secrets.value = await appEnv.listSecrets(props.app.id)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

async function submitNewSecret() {
    if (!props.app || !canSaveSecret.value) return
    newSecretSaving.value = true
    try {
        const k = newSecret.value.key.trim()
        secrets.value = await appEnv.upsertSecrets(props.app.id, {
            [k]: {
                value: newSecret.value.value,
                is_build_time: newSecret.value.buildTime,
                is_multiline: newSecret.value.multiline,
            },
        })
        notify.success(overwriteWarn.value ? t('envEditor.toast.secretOverwritten') : t('envEditor.toast.secretAdded'))
        // Reset form. Keep build-time / multiline flags so the
        // operator can quickly add a series of related secrets
        // without re-checking the boxes each time.
        newSecret.value.key = ''
        newSecret.value.value = ''
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        newSecretSaving.value = false
    }
}

function confirmDeleteSecret(key: string) {
    pendingDeleteKey.value = key
    deleteOpen.value = true
}

async function doDeleteSecret() {
    if (!props.app) return
    deletingKey.value = pendingDeleteKey.value
    try {
        await appEnv.deleteSecret(props.app.id, pendingDeleteKey.value)
        secrets.value = secrets.value.filter((s) => s.key !== pendingDeleteKey.value)
        notify.success(t('envEditor.toast.secretRemoved'))
        deleteOpen.value = false
        pendingDeleteKey.value = ''
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        deletingKey.value = null
    }
}

// ── Init ─────────────────────────────────────────────────────────
function hydrateFromApp() {
    savedEnv.value = { ...(props.app?.env_vars ?? {}) }
    envText.value = serializeEnv(savedEnv.value)
    void loadSecrets()
}

onMounted(hydrateFromApp)

// Re-hydrate if the parent swaps the app (route change between apps
// re-uses this component instance).
watch(() => props.app?.id, (next, prev) => {
    if (next !== prev) hydrateFromApp()
})

// Reuse store imports for completeness — silences "imported and not
// used" if we later move env save to the store layer.
void appsStore
</script>

<style scoped>
.env-editor { display: flex; flex-direction: column; gap: 24px; }

.env-block {
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    background: var(--p-content-bg);
    padding: 18px 20px;
}
.env-block__head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 14px;
}
.env-block__title {
    margin: 0;
    font-size: 14px;
    font-weight: 700;
    color: var(--p-text);
    text-transform: uppercase;
    letter-spacing: 0.04em;
}
.env-block__sub {
    margin: 4px 0 0;
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.5;
    max-width: 580px;
}

/* ── .env textarea ── */
.env-block__actions {
    display: inline-flex;
    align-items: center;
    gap: 6px;
}
.env-textarea {
    width: 100%;
    font-family: ui-monospace, Menlo, monospace;
    font-size: 12px;
    line-height: 1.5;
    /* tab-size in case the operator pastes content with hard tabs */
    tab-size: 4;
    min-height: 240px;
    resize: vertical;
}
:deep(.env-textarea) {
    width: 100%;
    font-family: ui-monospace, Menlo, monospace;
    font-size: 12px;
    line-height: 1.5;
}

/* ── Parse errors (inline per-line) ── */
.env-errors {
    margin: 10px 0 0;
    padding: 10px 14px;
    list-style: none;
    border: 1px solid color-mix(in srgb, var(--p-danger, #dc2626), transparent 60%);
    background: color-mix(in srgb, var(--p-danger, #dc2626), transparent 92%);
    border-radius: 8px;
    color: var(--p-danger, #dc2626);
    font-size: 12px;
    line-height: 1.5;
}
.env-errors li { margin: 0; padding: 0; }
.env-errors strong { font-weight: 700; }
.env-block__sub code {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
    padding: 0 4px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 3px;
}

/* ── Save bar ── */
.env-actions {
    margin-top: 14px;
    display: flex;
    flex-direction: column;
    gap: 8px;
}
.env-actions__btns {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
}

/* ── Secrets table (existing secrets) ── */
.env-table {
    width: 100%;
    border-collapse: collapse;
    /*
        Cards live on var(--p-content-bg); the table needs a bit of
        contrast so rows read as a distinct surface. Same trick as the
        other tabular surfaces in app detail.
    */
    background: color-mix(in srgb, var(--p-surface-900, #0b0e14), transparent 40%);
    border: 1px solid var(--p-content-border);
    border-radius: 8px;
    overflow: hidden;
}
.env-table thead th {
    text-align: left;
    padding: 10px 12px;
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--p-text-muted);
    background: color-mix(in srgb, var(--p-text-muted), transparent 92%);
    border-bottom: 1px solid var(--p-content-border);
}
.env-table tbody td {
    padding: 10px 12px;
    font-size: 13px;
    color: var(--p-text);
    border-bottom: 1px solid color-mix(in srgb, var(--p-content-border), transparent 40%);
    vertical-align: middle;
}
.env-table tbody tr:last-child td { border-bottom: none; }
.env-table tbody tr:hover td {
    background: color-mix(in srgb, var(--p-primary-500), transparent 94%);
}
.env-table__key { width: 60%; }
.env-table__act {
    width: 48px;
    text-align: right;
    padding-right: 8px !important;
}

.env-empty {
    margin: 0;
    padding: 14px;
    text-align: center;
    font-size: 13px;
    border: 1px dashed var(--p-content-border);
    border-radius: 8px;
}

/* ── Add secret form ── */
.add-secret {
    margin-top: 14px;
    padding-top: 14px;
    border-top: 1px dashed var(--p-content-border);
    display: flex;
    flex-direction: column;
    gap: 12px;
}
.add-secret__grid {
    display: grid;
    grid-template-columns: minmax(180px, 1fr) 2fr;
    gap: 12px;
}
@media (max-width: 640px) {
    .add-secret__grid { grid-template-columns: 1fr; }
}
.add-secret__flags {
    display: flex;
    flex-direction: column;
    gap: 6px;
}
.add-secret__actions {
    display: flex;
    justify-content: flex-end;
}
.check-row {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    color: var(--p-text);
    font-size: 13px;
}
.check-row code {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
    padding: 0 4px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 4px;
}

/* ── Pills ── */
.pill {
    display: inline-block;
    padding: 1px 7px;
    border-radius: 999px;
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
}
.pill--build {
    background: color-mix(in srgb, var(--p-warning, #d97706), transparent 85%);
    color: var(--p-warning, #d97706);
}
.pill--runtime {
    background: color-mix(in srgb, var(--p-primary-500), transparent 85%);
    color: var(--p-primary-500);
}

.mono { font-family: ui-monospace, Menlo, monospace; font-size: 12px; }
.muted { color: var(--p-text-muted); }
.prose { margin: 0 0 12px; color: var(--p-text); font-size: 14px; line-height: 1.5; }
.prose code {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 12px;
    padding: 1px 6px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 4px;
}
</style>
