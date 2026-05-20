<template>
    <!--
        SettingsTab — single mega-form for every editable field on the
        App row. Lives in AppDetailView's "Configurações" panel.

        Why sectioned vs tabbed:
          - operators expect Settings to be a long scroll, not a nested
            tab hop (Heroku / Coolify / Vercel all do the same).
          - splitting into multiple PrimeVue Tabs would also fragment
            the Save button — the backend's PATCH /apps/:id is one
            payload, so the editor's commit boundary is one button.

        We mirror every backend field as a local ref (rather than
        v-model'ing app.value directly) so Cancel reverts cleanly and
        validation can block save without writing back an invalid value.
    -->
    <div class="settings-tab">
        <!-- ───────────── A · Identidade + tags ───────────── -->
        <section class="env-block">
            <header class="env-block__head">
                <div>
                    <h3 class="env-block__title">{{ t('appSettings.identity.title') }}</h3>
                    <p class="env-block__sub">
                        {{ t('appSettings.identity.sub') }}
                    </p>
                </div>
            </header>

            <SField :label="t('appSettings.identity.name')">
                <InputText v-model="form.name" />
            </SField>
            <SField :label="t('appSettings.identity.description')">
                <InputText v-model="form.description" />
            </SField>

            <SField :label="t('appSettings.identity.tagsLabel')" :hint="t('appSettings.identity.tagsHint')">
                <div class="chips" :class="{ 'is-focused': chipFocus }">
                    <span v-for="(tag, i) in form.tags" :key="tag" class="chip">
                        <span class="chip__name">{{ tag }}</span>
                        <button
                            type="button"
                            class="chip__remove"
                            :aria-label="t('appSettings.identity.removeTagAria', { tag })"
                            @click="removeTagAt(i)"
                        >
                            <i class="pi pi-times" aria-hidden="true" />
                        </button>
                    </span>
                    <input
                        v-model="newTagDraft"
                        type="text"
                        class="chip-input"
                        :placeholder="form.tags.length ? '' : t('appSettings.identity.tagsPlaceholder')"
                        autocomplete="off"
                        spellcheck="false"
                        :list="tagSuggestId"
                        @focus="chipFocus = true"
                        @blur="onChipBlur"
                        @keydown="onChipKey"
                    />
                    <!-- Browser-native datalist gives us autocomplete-from-dictionary
                         without pulling AutoComplete + its styling overhead. Cheap
                         win for parity with the global /tags list. -->
                    <datalist :id="tagSuggestId">
                        <option v-for="t in tagSuggestions" :key="t" :value="t" />
                    </datalist>
                </div>
            </SField>
        </section>

        <!-- ───────────── B · Source & Build (only single-container) ───────────── -->
        <section v-if="!isComposeApp" class="env-block">
            <header class="env-block__head">
                <div>
                    <h3 class="env-block__title">{{ t('appSettings.source.title') }}</h3>
                    <p class="env-block__sub">
                        {{ t('appSettings.source.sub') }}
                    </p>
                </div>
            </header>

            <SField :label="t('appSettings.source.branchLabel')" :hint="t('appSettings.source.branchHint')">
                <InputText v-model="form.branch" placeholder="main" />
            </SField>

            <div class="form-grid-2">
                <SField
                    :label="t('appSettings.source.portLabel')"
                    :hint="t('appSettings.source.portHint')"
                    :error="portError ?? undefined"
                >
                    <InputText v-model="form.port" placeholder="3000" :invalid="!!portError" />
                </SField>
                <SField :label="t('appSettings.source.healthPathLabel')" :hint="t('appSettings.source.healthPathHint')">
                    <InputText v-model="form.healthPath" placeholder="/" />
                </SField>
            </div>

            <SField>
                <label class="toggle-row">
                    <ToggleSwitch v-model="form.healthEnabled" />
                    <span>{{ form.healthEnabled ? t('appSettings.source.healthOn') : t('appSettings.source.healthOff') }}</span>
                </label>
            </SField>
        </section>

        <div v-else class="compose-note">
            <i class="pi pi-info-circle" aria-hidden="true" />
            <div>
                <strong>{{ t('appSettings.compose.title') }}</strong>
                <small>
                    <i18n-t keypath="appSettings.compose.body" scope="global">
                        <template #file><code>docker-compose.yml</code></template>
                        <template #routing><em>{{ t('appSettings.compose.routing') }}</em></template>
                    </i18n-t>
                </small>
            </div>
        </div>

        <!-- ───────────── C · Comandos customizados (collapsible) ───────────── -->
        <details class="advanced">
            <summary class="advanced__summary">
                <span class="advanced__title">{{ t('appSettings.commands.title') }}</span>
                <span class="advanced__hint">
                    {{ t('appSettings.commands.hint') }}
                </span>
                <i class="pi pi-chevron-down advanced__chevron" aria-hidden="true" />
            </summary>
            <div class="advanced__body">
                <SField
                    :label="t('appSettings.commands.installLabel')"
                    :hint="t('appSettings.commands.installHint')"
                >
                    <InputText v-model="form.installCommand" spellcheck="false" />
                </SField>
                <SField
                    :label="t('appSettings.commands.buildLabel')"
                    :hint="t('appSettings.commands.buildHint')"
                >
                    <InputText v-model="form.buildCommand" spellcheck="false" />
                </SField>
                <SField
                    :label="t('appSettings.commands.startLabel')"
                    :hint="t('appSettings.commands.startHint')"
                >
                    <InputText v-model="form.startCommand" spellcheck="false" />
                </SField>
            </div>
        </details>

        <!-- ───────────── D · Hooks de deploy ───────────── -->
        <section class="env-block">
            <header class="env-block__head">
                <div>
                    <h3 class="env-block__title">{{ t('appSettings.hooks.title') }}</h3>
                    <p class="env-block__sub">
                        {{ t('appSettings.hooks.sub') }}
                    </p>
                </div>
            </header>

            <SField
                :label="t('appSettings.hooks.preLabel')"
                :hint="t('appSettings.hooks.preHint')"
            >
                <Textarea
                    v-model="form.preDeployCommand"
                    class="mono-text"
                    rows="2"
                    spellcheck="false"
                    placeholder="rails db:migrate"
                />
            </SField>

            <SField
                :label="t('appSettings.hooks.postLabel')"
                :hint="t('appSettings.hooks.postHint')"
            >
                <Textarea
                    v-model="form.postDeployCommand"
                    class="mono-text"
                    rows="2"
                    spellcheck="false"
                    placeholder="curl -X POST https://hooks.example.com/deployed"
                />
            </SField>
        </section>

        <!-- ───────────── E · Container lifecycle ───────────── -->
        <section class="env-block">
            <header class="env-block__head">
                <div>
                    <h3 class="env-block__title">{{ t('appSettings.lifecycle.title') }}</h3>
                    <p class="env-block__sub">
                        {{ t('appSettings.lifecycle.sub') }}
                    </p>
                </div>
            </header>

            <div class="form-grid-2">
                <SField :label="t('appSettings.lifecycle.restartLabel')" :hint="t('appSettings.lifecycle.restartHint')">
                    <Select
                        v-model="form.restartPolicy"
                        :options="RESTART_OPTIONS"
                        option-label="label"
                        option-value="value"
                        fluid
                    />
                </SField>
                <SField
                    :label="t('appSettings.lifecycle.autoBranchLabel')"
                    :hint="t('appSettings.lifecycle.autoBranchHint')"
                >
                    <InputText v-model="form.autoDeployBranch" placeholder="main" spellcheck="false" />
                </SField>
            </div>
        </section>

        <!-- ───────────── F · Limites básicos ───────────── -->
        <section class="env-block">
            <header class="env-block__head">
                <div>
                    <h3 class="env-block__title">{{ t('appSettings.limits.title') }}</h3>
                    <p class="env-block__sub">
                        {{ t('appSettings.limits.sub') }}
                    </p>
                </div>
            </header>

            <div class="form-grid-2">
                <SField
                    :label="t('appSettings.limits.memoryLabel')"
                    :hint="t('appSettings.limits.memoryHint')"
                    :error="limitsMemoryError ?? undefined"
                >
                    <InputText v-model="form.limitsMemory" placeholder="512m" spellcheck="false" />
                </SField>
                <SField
                    :label="t('appSettings.limits.cpusLabel')"
                    :hint="t('appSettings.limits.cpusHint')"
                    :error="limitsCpusError ?? undefined"
                >
                    <InputText v-model="form.limitsCpus" placeholder="1.0" spellcheck="false" />
                </SField>
            </div>
        </section>

        <!-- ───────────── G · Avançado (collapsible) ───────────── -->
        <details class="advanced">
            <summary class="advanced__summary">
                <span class="advanced__title">{{ t('appSettings.advanced.title') }}</span>
                <span class="advanced__hint">
                    {{ t('appSettings.advanced.hint') }}
                </span>
                <i class="pi pi-chevron-down advanced__chevron" aria-hidden="true" />
            </summary>
            <div class="advanced__body">
                <div class="form-grid-2">
                    <SField :label="t('appSettings.advanced.memorySwapLabel')" :hint="t('appSettings.advanced.memorySwapHint')">
                        <InputText v-model="form.limitsMemorySwap" placeholder="1g" spellcheck="false" />
                    </SField>
                    <SField :label="t('appSettings.advanced.swappinessLabel')" :hint="t('appSettings.advanced.swappinessHint')">
                        <InputNumber
                            v-model="form.limitsMemorySwappiness"
                            :min="0"
                            :max="100"
                            :use-grouping="false"
                            show-buttons
                            fluid
                            :allow-empty="true"
                        />
                    </SField>
                </div>

                <div class="form-grid-2">
                    <SField :label="t('appSettings.advanced.memoryReservationLabel')" :hint="t('appSettings.advanced.memoryReservationHint')">
                        <InputText v-model="form.limitsMemoryReservation" placeholder="256m" spellcheck="false" />
                    </SField>
                    <SField :label="t('appSettings.advanced.cpusetLabel')" :hint="t('appSettings.advanced.cpusetHint')">
                        <InputText v-model="form.limitsCpuset" placeholder="0,2-4,7" spellcheck="false" />
                    </SField>
                </div>

                <SField :label="t('appSettings.advanced.cpuSharesLabel')" :hint="t('appSettings.advanced.cpuSharesHint')">
                    <InputNumber
                        v-model="form.limitsCpuShares"
                        :min="2"
                        :max="262144"
                        :use-grouping="false"
                        show-buttons
                        fluid
                        :allow-empty="true"
                    />
                </SField>

                <div class="toggles">
                    <label class="toggle-row">
                        <ToggleSwitch v-model="form.buildArgsInject" />
                        <span>
                            {{ t('appSettings.advanced.buildArgsInject') }}
                            (<code>PREXEL_APP_ID</code>, <code>PREXEL_DEPLOYMENT_ID</code>, …)
                        </span>
                    </label>
                    <label class="toggle-row">
                        <ToggleSwitch v-model="form.buildArgsSourceCommit" />
                        <span>
                            <i18n-t keypath="appSettings.advanced.buildArgsSourceCommit" scope="global">
                                <template #code><code>SOURCE_COMMIT</code></template>
                            </i18n-t>
                            <small class="muted">{{ t('appSettings.advanced.buildArgsSourceCommitHint') }}</small>
                        </span>
                    </label>
                </div>

                <SField
                    :label="t('appSettings.advanced.dockerLabelsLabel')"
                    :hint="dockerLabelsHint"
                    :error="dockerLabelsError ?? undefined"
                >
                    <Textarea
                        v-model="dockerLabelsText"
                        class="mono-text"
                        rows="4"
                        spellcheck="false"
                        placeholder="com.example.team=infra&#10;traefik.enable=true"
                    />
                </SField>
            </div>
        </details>

        <!-- ───────────── Save bar ───────────── -->
        <div class="actions-row">
            <Button
                :label="t('appSettings.actions.save')"
                icon="pi pi-check"
                :loading="saving"
                :disabled="!canSave"
                @click="save"
            />
        </div>

        <p v-if="globalError" class="global-error">{{ globalError }}</p>

        <!-- ───────────── Danger zone ─────────────
             Dedicated card at the bottom so the destructive action
             sits visually apart from "save my edits" — the operator
             needs to leave the editing context to reach it. Lists
             exactly what gets wiped so there's no ambiguity. -->
        <section class="danger-zone">
            <header class="danger-zone__head">
                <i class="pi pi-exclamation-triangle" aria-hidden="true" />
                <div>
                    <h3>{{ t('appSettings.danger.title') }}</h3>
                    <p>{{ t('appSettings.danger.sub') }}</p>
                </div>
            </header>

            <div class="danger-zone__row">
                <div class="danger-zone__copy">
                    <strong>{{ t('appSettings.danger.removeTitle') }}</strong>
                    <p>{{ t('appSettings.danger.removeIntro') }}</p>
                    <ul>
                        <li>{{ t('appSettings.danger.list.containers') }}</li>
                        <li>
                            <i18n-t keypath="appSettings.danger.list.images" scope="global">
                                <template #pattern><code>prexel-{{ app?.name ?? 'nome' }}-*</code></template>
                            </i18n-t>
                        </li>
                        <li>{{ t('appSettings.danger.list.volumes') }}</li>
                        <li>{{ t('appSettings.danger.list.routes') }}</li>
                        <li>{{ t('appSettings.danger.list.logs') }}</li>
                        <li>{{ t('appSettings.danger.list.db') }}</li>
                    </ul>
                    <p class="danger-zone__note">
                        <i18n-t keypath="appSettings.danger.note" scope="global">
                            <template #strong><strong>{{ t('appSettings.danger.noteStrong') }}</strong></template>
                        </i18n-t>
                    </p>
                </div>
                <Button
                    :label="t('appSettings.danger.remove')"
                    icon="pi pi-trash"
                    severity="danger"
                    @click="$emit('remove-app')"
                />
            </div>
        </section>
    </div>
</template>

<script setup lang="ts">
/*
    SettingsTab — exhaustive editor for every PATCH-able field on an
    App row. Lives in AppDetailView and replaces the previous inline
    block that only covered name/branch/port/healthcheck.

    Architecture:
      - one `form` object mirrors every editable field as a local ref.
        Mutating `form.*` never touches `props.app`, so Cancel/swap-app
        is just `hydrate()` against the prop again.
      - Save batches two writes when needed: PATCH /apps/:id for the
        row fields, then PUT /apps/:id/tags ONLY if tags changed. We
        settle both before emitting `app-updated` so the parent's
        single refetch is consistent.
      - tags input is a thin chip editor backed by a browser-native
        <datalist> — no PrimeVue AutoComplete dependency, suggestions
        still come from the global /tags dictionary.
      - docker_labels is a textarea of `key=value` lines. A table
        editor would inflate the form by ~80 lines for a feature most
        operators touch maybe once per app; the textarea matches the
        EnvEditor mental model and keeps validation simple.
*/
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import InputNumber from 'primevue/inputnumber'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Textarea from 'primevue/textarea'
import ToggleSwitch from 'primevue/toggleswitch'
import SField from '@/components/settings/SField.vue'
import { useAppsStore } from '@/stores/apps'
import { appsService } from '@/services/apps'
import { tagsService } from '@/services/tags'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { App, RestartPolicy } from '@/types/api'
import {
    normaliseTag,
    nullableTrim,
    parseDockerLabels,
    serializeDockerLabels,
    tagsEqual,
    validateCpus,
    validateCpuset,
    validateMemory,
    validateMemoryReservation,
    validateMemorySwap,
    validatePort,
} from './settingsTabHelpers'

const props = defineProps<{
    app: App | null
}>()

const emit = defineEmits<{
    'app-updated': []
    'remove-app': []
}>()

const { t } = useI18n()
const appsStore = useAppsStore()

// ─── Restart policy options ────────────────────────────────────
//
// Values are Docker semantics; labels translated for the parenthetical
// hints only — values stay literal.
const RESTART_OPTIONS = computed<Array<{ label: string; value: RestartPolicy }>>(() => [
    { label: t('appSettings.lifecycle.restartOptions.unlessStopped'), value: 'unless-stopped' },
    { label: t('appSettings.lifecycle.restartOptions.always'),        value: 'always' },
    { label: t('appSettings.lifecycle.restartOptions.onFailure'),     value: 'on-failure' },
    { label: t('appSettings.lifecycle.restartOptions.no'),            value: 'no' },
])

interface SettingsForm {
    name: string
    description: string
    branch: string
    // Kept as string so the user can type freely without InputNumber's
    // increment widgets; we validate before sending.
    port: string
    healthEnabled: boolean
    healthPath: string

    installCommand: string
    buildCommand: string
    startCommand: string
    preDeployCommand: string
    postDeployCommand: string

    restartPolicy: RestartPolicy
    autoDeployBranch: string

    limitsMemory: string
    limitsCpus: string
    limitsMemorySwap: string
    limitsMemorySwappiness: number | null
    limitsMemoryReservation: string
    limitsCpuset: string
    limitsCpuShares: number | null

    buildArgsInject: boolean
    buildArgsSourceCommit: boolean

    tags: string[]
}

const form = reactive<SettingsForm>({
    name: '',
    description: '',
    branch: 'main',
    port: '',
    healthEnabled: true,
    healthPath: '/',

    installCommand: '',
    buildCommand: '',
    startCommand: '',
    preDeployCommand: '',
    postDeployCommand: '',

    restartPolicy: 'unless-stopped',
    autoDeployBranch: '',

    limitsMemory: '',
    limitsCpus: '',
    limitsMemorySwap: '',
    limitsMemorySwappiness: null,
    limitsMemoryReservation: '',
    limitsCpuset: '',
    limitsCpuShares: null,

    buildArgsInject: true,
    buildArgsSourceCommit: false,

    tags: [],
})

const dockerLabelsText = ref('')
const savedTags = ref<string[]>([])
const savedDockerLabels = ref<Record<string, string>>({})
const saving = ref(false)
const globalError = ref<string | null>(null)

// Compose apps don't expose a single internal port — those settings
// would lie. Branch/port/healthcheck are hidden in that case; every
// other field still applies.
const isComposeApp = computed(() => props.app?.build_type === 'docker_compose')

// ─── Tag chip editor ──────────────────────────────────────────
//
// Renders chips for `form.tags`, with a free-text input that emits a
// chip on Enter / comma. Backspace from an empty input pops the last
// chip — same micro-UX as Gmail's recipient field. The <datalist>
// gives us "completable from dictionary" without an AutoComplete dep.

const newTagDraft = ref('')
const chipFocus = ref(false)
const tagSuggestId = `tag-suggest-${Math.random().toString(36).slice(2, 8)}`
const tagSuggestions = ref<string[]>([])

function addTagFromDraft(): boolean {
    const n = normaliseTag(newTagDraft.value)
    if (!n) return false
    if (form.tags.includes(n)) {
        newTagDraft.value = ''
        return false
    }
    form.tags.push(n)
    newTagDraft.value = ''
    return true
}

function onChipKey(e: KeyboardEvent) {
    if (e.key === 'Enter' || e.key === ',') {
        e.preventDefault()
        addTagFromDraft()
    } else if (e.key === 'Backspace' && newTagDraft.value === '' && form.tags.length) {
        // Pop-on-backspace is the convention every chip editor follows;
        // skipping it would feel broken to anyone who's used Gmail.
        e.preventDefault()
        form.tags.pop()
    }
}

function onChipBlur() {
    chipFocus.value = false
    // Flush pending draft so a user who types "prod" and tabs away
    // doesn't silently lose the chip.
    addTagFromDraft()
}

function removeTagAt(i: number) {
    form.tags.splice(i, 1)
}

// ─── Docker labels editor (textarea key=value lines) ───────────
//
// Parsed on the fly into a Record<string,string>. Errors render
// inline under the field; save is blocked while errors exist. The
// parse/serialise helpers live in `settingsTabHelpers.ts` so the
// component itself stays focused on state wiring.

const parsedDockerLabels = computed(() => parseDockerLabels(dockerLabelsText.value))
const dockerLabelsError = computed(() => parsedDockerLabels.value.error)
const dockerLabelsHint = computed(() => {
    const n = Object.keys(parsedDockerLabels.value.labels).length
    return n
        ? t('appSettings.advanced.dockerLabelsCount', { n })
        : t('appSettings.advanced.dockerLabelsFormat')
})

// ─── Per-field validation ──────────────────────────────────────
//
// Each computed delegates to the matching helper. Keeping the
// computeds (vs `v-if`-ing against helpers directly) lets the
// template stay declarative and gives us a single `validation`
// pipeline to drive `canSave`.

const portError = computed(() =>
    isComposeApp.value ? null : validatePort(form.port),
)
const limitsMemoryError = computed(() => validateMemory(form.limitsMemory))
const limitsCpusError = computed(() => validateCpus(form.limitsCpus))
const limitsMemorySwapError = computed(() => validateMemorySwap(form.limitsMemorySwap))
const limitsMemoryReservationError = computed(() =>
    validateMemoryReservation(form.limitsMemoryReservation),
)
const limitsCpusetError = computed(() => validateCpuset(form.limitsCpuset))

const validation = computed(() => {
    return [
        portError.value,
        limitsMemoryError.value,
        limitsCpusError.value,
        limitsMemorySwapError.value,
        limitsMemoryReservationError.value,
        limitsCpusetError.value,
        dockerLabelsError.value,
    ].filter(Boolean) as string[]
})

const canSave = computed(() => validation.value.length === 0 && !saving.value)

// ─── Hydrate from prop ─────────────────────────────────────────
//
// Called on mount + whenever the route swaps to a different app
// (the parent re-uses this component instance across navigations).

function hydrate() {
    const a = props.app
    if (!a) return
    form.name = a.name
    form.description = a.description ?? ''
    form.branch = a.branch ?? 'main'
    form.port = a.port != null ? String(a.port) : ''
    form.healthEnabled = a.health_check_enabled ?? true
    form.healthPath = a.health_check_path ?? '/'

    form.installCommand = a.install_command ?? ''
    form.buildCommand = a.build_command ?? ''
    form.startCommand = a.start_command ?? ''
    form.preDeployCommand = a.pre_deploy_command ?? ''
    form.postDeployCommand = a.post_deploy_command ?? ''

    form.restartPolicy = a.restart_policy ?? 'unless-stopped'
    form.autoDeployBranch = a.auto_deploy_branch ?? ''

    form.limitsMemory = a.limits_memory ?? ''
    form.limitsCpus = a.limits_cpus ?? ''
    form.limitsMemorySwap = a.limits_memory_swap ?? ''
    form.limitsMemorySwappiness = a.limits_memory_swappiness ?? null
    form.limitsMemoryReservation = a.limits_memory_reservation ?? ''
    form.limitsCpuset = a.limits_cpuset ?? ''
    form.limitsCpuShares = a.limits_cpu_shares ?? null

    form.buildArgsInject = a.build_args_inject ?? true
    form.buildArgsSourceCommit = a.build_args_source_commit ?? false

    form.tags = [...(a.tags ?? [])]
    savedTags.value = [...(a.tags ?? [])]

    savedDockerLabels.value = { ...(a.docker_labels ?? {}) }
    dockerLabelsText.value = serializeDockerLabels(savedDockerLabels.value)
}

async function loadTagSuggestions() {
    try {
        const all = await tagsService.list()
        tagSuggestions.value = all.map((t) => t.name)
    } catch {
        // Suggestions are optional — failing here just means the
        // operator types without autocomplete. No need to toast.
        tagSuggestions.value = []
    }
}

onMounted(() => {
    hydrate()
    void loadTagSuggestions()
})

watch(() => props.app?.id, (next, prev) => {
    if (next !== prev) hydrate()
})

// ─── Save ──────────────────────────────────────────────────────
//
// PATCH the row, then PUT tags if they changed. We send the FULL set
// of fields the editor controls — the backend treats absent keys as
// "leave alone", but always sending the snapshot keeps the save's
// intent unambiguous (no "did I forget to clear that?" worries).

async function save() {
    if (!props.app) return
    // Force-commit any pending chip draft before we read form.tags —
    // otherwise typing a tag and hitting Save would silently drop it.
    addTagFromDraft()

    if (!canSave.value) {
        notify.error(t('appSettings.validation.fixFields'))
        return
    }
    saving.value = true
    globalError.value = null
    try {
        // PATCH body: ship everything the form owns. Empty strings on
        // nullable fields turn into nulls so the server clears them
        // (otherwise "" round-trips back as "" and the operator can
        // never empty a field once set).
        const patch: Record<string, unknown> = {
            name: form.name,
            description: form.description,

            install_command: nullableTrim(form.installCommand),
            build_command: nullableTrim(form.buildCommand),
            start_command: nullableTrim(form.startCommand),
            pre_deploy_command: nullableTrim(form.preDeployCommand),
            post_deploy_command: nullableTrim(form.postDeployCommand),

            restart_policy: form.restartPolicy,
            auto_deploy_branch: nullableTrim(form.autoDeployBranch),

            limits_memory: nullableTrim(form.limitsMemory),
            limits_cpus: nullableTrim(form.limitsCpus),
            limits_memory_swap: nullableTrim(form.limitsMemorySwap),
            limits_memory_swappiness: form.limitsMemorySwappiness,
            limits_memory_reservation: nullableTrim(form.limitsMemoryReservation),
            limits_cpuset: nullableTrim(form.limitsCpuset),
            limits_cpu_shares: form.limitsCpuShares,

            build_args_inject: form.buildArgsInject,
            build_args_source_commit: form.buildArgsSourceCommit,

            docker_labels: parsedDockerLabels.value.labels,
        }
        if (!isComposeApp.value) {
            patch.branch = form.branch.trim() || 'main'
            patch.port = Number(form.port)
            patch.health_check_enabled = form.healthEnabled
            patch.health_check_path = form.healthPath || '/'
        }
        await appsStore.patch(props.app.id, patch as Partial<App>)

        // Tags travel through a dedicated endpoint (PUT /apps/:id/tags)
        // — we only call it when something actually changed so an
        // unrelated save doesn't bump the tags table's audit log.
        if (!tagsEqual(form.tags, savedTags.value)) {
            await appsService.updateTags(props.app.id, form.tags)
            savedTags.value = [...form.tags]
        }

        savedDockerLabels.value = { ...parsedDockerLabels.value.labels }
        notify.success(t('appSettings.toast.saved'))
        emit('app-updated')
    } catch (e) {
        globalError.value = apiErrorMessage(e)
        notify.error(globalError.value)
    } finally {
        saving.value = false
    }
}
</script>

<style scoped>
.settings-tab {
    display: flex;
    flex-direction: column;
    gap: 20px;
}

/* Card / section frame — same vocabulary EnvEditor uses so the two
   tabs read as siblings. */
.env-block {
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    background: var(--p-content-bg);
    padding: 18px 20px;
    display: flex;
    flex-direction: column;
    gap: 12px;
}
.env-block__head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
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
.env-block__sub code,
.env-block__sub em {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
    padding: 0 4px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 3px;
    font-style: normal;
}

/* Side-by-side fields. Collapses on narrow viewports so the inputs
   never squeeze below readable width. */
.form-grid-2 {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
}
@media (max-width: 640px) {
    .form-grid-2 { grid-template-columns: 1fr; }
}

/* Toggle row — same shape as the dialogs use, keeps Settings consistent. */
.toggle-row {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    font-size: 13px;
    color: var(--p-text);
    cursor: pointer;
}
.toggle-row code {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
    padding: 0 4px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 3px;
}
.toggle-row small { margin-left: 4px; }
.toggles { display: flex; flex-direction: column; gap: 8px; margin: 4px 0; }

/* ── Compose note (shown in place of Source & Build for compose apps) ── */
.compose-note {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 12px 14px;
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    background: color-mix(in srgb, var(--p-primary-500), transparent 95%);
}
.compose-note i {
    color: var(--p-primary-500);
    font-size: 16px;
    margin-top: 1px;
}
.compose-note strong { display: block; font-size: 13px; color: var(--p-text); }
.compose-note small {
    display: block;
    margin-top: 2px;
    color: var(--p-text-muted);
    font-size: 12px;
    line-height: 1.45;
}
.compose-note code,
.compose-note em {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
    padding: 0 4px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 3px;
    font-style: normal;
}

/* ── Advanced collapse (native <details>, matches Instance settings) ── */
.advanced {
    border: 1px dashed var(--p-content-border);
    border-radius: 12px;
    background: transparent;
    transition: border-color 120ms ease, background 120ms ease;
}
.advanced[open] {
    border-style: solid;
    border-color: color-mix(in srgb, var(--p-primary-500), transparent 75%);
    background: var(--p-content-bg);
}
.advanced__summary {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 14px 18px;
    cursor: pointer;
    list-style: none;
    user-select: none;
}
.advanced__summary::-webkit-details-marker { display: none; }
.advanced__title {
    font-size: 13px;
    font-weight: 700;
    color: var(--p-text);
    text-transform: uppercase;
    letter-spacing: 0.06em;
}
.advanced__hint {
    flex: 1;
    font-size: 12px;
    color: var(--p-text-muted);
    min-width: 0;
}
.advanced__chevron {
    color: var(--p-text-muted);
    font-size: 12px;
    transition: transform 160ms ease;
}
.advanced[open] .advanced__chevron { transform: rotate(180deg); }
.advanced__body {
    padding: 4px 18px 18px;
    display: flex;
    flex-direction: column;
    gap: 12px;
}

/* ── Save bar ── */
.actions-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 12px;
    /* Sticky so the operator can save without scrolling all the way back
       up after a long edit. `bottom: 0` is enough — the parent panel has
       its own padding that keeps the bar above the viewport edge. */
    position: sticky;
    bottom: 0;
    margin-top: 4px;
    padding: 12px 0;
    background: linear-gradient(to top, var(--p-content-bg) 70%, transparent);
}
.global-error {
    margin: 0;
    color: var(--p-danger, #dc2626);
    font-size: 13px;
}

/* ── Tag chip editor ── */
.chips {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    padding: 6px 8px;
    min-height: 38px;
    border: 1px solid var(--p-content-border);
    border-radius: 6px;
    background: var(--p-content-bg);
    transition: border-color 120ms;
}
.chips.is-focused {
    border-color: var(--p-primary-500);
    box-shadow: 0 0 0 1px var(--p-primary-500);
}
.chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 2px 6px 2px 10px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--p-primary-500), transparent 82%);
    color: var(--p-text);
    font-size: 12px;
    line-height: 1.6;
}
.chip__remove {
    border: 0;
    background: transparent;
    color: var(--p-text-muted);
    cursor: pointer;
    padding: 2px;
    border-radius: 999px;
    display: inline-flex;
    align-items: center;
}
.chip__remove:hover { color: var(--p-danger, #dc2626); }
.chip__remove i { font-size: 10px; }
.chip-input {
    flex: 1 0 120px;
    min-width: 80px;
    border: 0;
    outline: 0;
    background: transparent;
    color: var(--p-text);
    font: inherit;
    font-size: 13px;
    padding: 4px 2px;
}

/* Mono-style textareas for command / labels inputs. Same baseline
   as EnvEditor so they all "look like code". */
.mono-text :deep(textarea),
.mono-text {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 12px;
    line-height: 1.5;
}

/* PrimeVue's inputs default to inline width; we want them filling
   the SField slot so multi-column layouts line up. */
:deep(.p-inputtext),
:deep(.p-textarea),
:deep(.p-inputnumber),
:deep(.p-select) { width: 100%; }

.muted { color: var(--p-text-muted); font-size: 11px; }

/* ───────────── Danger zone ───────────── */
/* Card sits at the bottom of the tab with a red-tinted frame so it
   reads as "different territory" without flashing red the moment
   the operator opens the tab. The action button is the only red
   pixel by default. */
.danger-zone {
    margin-top: 32px;
    border: 1px solid color-mix(in srgb, var(--p-red-500, #dc2626), transparent 60%);
    background: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 94%);
    border-radius: 12px;
    overflow: hidden;
}
.danger-zone__head {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 16px 20px;
    border-bottom: 1px solid color-mix(in srgb, var(--p-red-500, #dc2626), transparent 70%);
    background: color-mix(in srgb, var(--p-red-500, #dc2626), transparent 88%);
}
.danger-zone__head i {
    color: var(--p-red-400, #f87171);
    font-size: 18px;
    margin-top: 2px;
}
.danger-zone__head h3 {
    margin: 0;
    font-size: 13px;
    font-weight: 700;
    color: var(--p-red-400, #f87171);
    text-transform: uppercase;
    letter-spacing: 0.04em;
}
.danger-zone__head p {
    margin: 4px 0 0;
    font-size: 12px;
    color: var(--p-text-muted);
}
.danger-zone__row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 20px;
    padding: 18px 20px;
    flex-wrap: wrap;
}
.danger-zone__copy {
    flex: 1;
    min-width: 280px;
}
.danger-zone__copy strong {
    color: var(--p-text);
    font-size: 14px;
}
.danger-zone__copy p {
    margin: 6px 0;
    font-size: 13px;
    color: var(--p-text-muted);
    line-height: 1.5;
}
.danger-zone__copy ul {
    margin: 6px 0 8px;
    padding-left: 20px;
    color: var(--p-text-muted);
    font-size: 12px;
    line-height: 1.7;
}
.danger-zone__copy ul li { margin: 0; }
.danger-zone__copy code {
    font-family: ui-monospace, "JetBrains Mono", Menlo, monospace;
    font-size: 11px;
    padding: 1px 5px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 86%);
    border-radius: 4px;
    color: var(--p-text);
}
.danger-zone__note {
    font-size: 12px;
    color: var(--p-text-muted);
    font-style: italic;
}
</style>
