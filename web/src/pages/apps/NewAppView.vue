<template>
    <div class="new-app">
        <!-- Top bar mirrors AddDomainView so the two "new X" pages
             feel identical in chrome. -->
        <header class="new-app__top">
            <RouterLink to="/apps" class="back-link">
                <IconChevronLeft :size="14" />
                <span>{{ t('newApp.back') }}</span>
            </RouterLink>
        </header>

        <div class="new-app__grid">
            <!-- ─────────── Stepper rail ─────────── -->
            <aside class="rail">
                <h1 class="rail__title">{{ t('newApp.title') }}</h1>
                <p class="rail__sub">
                    {{ t('newApp.sub') }}
                </p>

                <ol class="rail__steps">
                    <li
                        v-for="(s, i) in steps"
                        :key="s.label"
                        :class="[
                            'rail-step',
                            i === step && 'is-current',
                            i < step && 'is-done',
                            i > step && 'is-future',
                        ]"
                    >
                        <div class="rail-step__bullet">
                            <IconCheck v-if="i < step" :size="14" />
                            <span v-else>{{ i + 1 }}</span>
                        </div>
                        <div class="rail-step__text">
                            <span class="rail-step__label">{{ s.label }}</span>
                            <span class="rail-step__desc">{{ s.desc }}</span>
                        </div>
                    </li>
                </ol>
            </aside>

            <!--
                The pane is one big <form>. Enter inside any input submits
                it; the handler routes to "advance step" or "create app"
                based on which step is current. Buttons that should NOT
                fire on Enter are explicit type="button".
            -->
            <form class="pane" @submit.prevent="onFormSubmit">
                <div class="pane__card">
                    <header class="pane__head">
                        <span class="pane__kicker">{{ t('newApp.stepKicker', { current: step + 1, total: steps.length }) }}</span>
                        <h2 class="pane__title">{{ steps[step].label }}</h2>
                        <p class="pane__sub">{{ steps[step].desc }}</p>
                    </header>

                    <div class="pane__body">
                        <!-- ─────────── Step 1: Origem (picker + source-specific details) ─────────── -->
                        <section v-if="step === 0" class="step">
                            <!-- Source picker -->
                            <div class="source-picker">
                                <button
                                    v-for="source in sourceOptions"
                                    :key="source.id"
                                    type="button"
                                    :class="['source-card', draft.source === source.id && 'is-active']"
                                    @click="draft.source = source.id"
                                >
                                    <i :class="source.icon" />
                                    <span>
                                        <strong>{{ source.label }}</strong>
                                        <small>{{ source.description }}</small>
                                    </span>
                                </button>
                            </div>

                            <!-- Subtle divider between the picker and the
                                 source-specific fields — same step but
                                 visually grouped so the operator parses
                                 it as "first decide, then fill in". -->
                            <hr class="step-divider" />

                            <!-- Source-specific details (below) -->

                            <template v-if="draft.source === 'github'">
                                <div v-if="gitSourcesStore.githubSources.length === 0" class="github-connect-card">
                                    <div>
                                        <strong>{{ t('newApp.github.connectTitle') }}</strong>
                                        <span>{{ t('newApp.github.connectBody') }}</span>
                                    </div>
                                    <Button :label="t('newApp.github.goToGit')" icon="pi pi-github" type="button" severity="secondary" @click="router.push('/git')" />
                                </div>
                                <template v-else>
                                    <div class="form-grid">
                                        <SField :label="t('newApp.github.connectionLabel')">
                                            <Select
                                                v-model="draft.github_source_id"
                                                :options="gitSourcesStore.githubSources"
                                                optionLabel="name"
                                                optionValue="id"
                                                :placeholder="t('newApp.github.connectionPlaceholder')"
                                                @change="loadGitHubRepos"
                                            />
                                        </SField>
                                        <SField :label="t('newApp.github.searchLabel')">
                                            <div class="inline-search">
                                                <InputText v-model="repoSearch" :placeholder="t('newApp.github.searchPlaceholder')" @keyup.enter.stop="loadGitHubRepos" />
                                                <Button icon="pi pi-search" type="button" :aria-label="t('newApp.github.searchAria')" :loading="gitSourcesStore.repoLoading" @click="loadGitHubRepos" />
                                            </div>
                                        </SField>
                                    </div>

                                    <div class="repo-picker">
                                        <button
                                            v-for="repo in githubRepos"
                                            :key="repo.id"
                                            type="button"
                                            :class="['repo-picker__row', draft.github_repo === repo.full_name && 'is-active']"
                                            @click="selectGitHubRepo(repo)"
                                        >
                                            <span>
                                                <strong>{{ repo.full_name }}</strong>
                                                <small>{{ repo.private ? t('newApp.github.repoPrivate') : t('newApp.github.repoPublic') }} · {{ repo.default_branch }}</small>
                                            </span>
                                            <i class="pi pi-check" />
                                        </button>
                                    </div>

                                    <!-- Build hints only show after a repo is picked
                                         (auto-inspect already filled the defaults). -->
                                    <template v-if="draft.github_repo">
                                        <!--
                                            Inspect status card. Surfaces exactly what
                                            the backend found in the repo so the
                                            operator doesn't discover "no Dockerfile"
                                            only after a failed deploy. Three states:

                                              loading  → grey, spinner, "Verificando…"
                                              ok       → green, lists files found,
                                                         shows suggested build type
                                              missing  → amber, explains that Prexel
                                                         only deploys Docker today
                                                         and offers Retry
                                              error    → red, raw error from API
                                        -->
                                        <div
                                            v-if="inspecting"
                                            class="inspect-card inspect-card--loading"
                                        >
                                            <i class="pi pi-spin pi-spinner inspect-card__icon" aria-hidden="true" />
                                            <div>
                                                <strong>{{ t('newApp.github.inspect.loadingTitle') }}</strong>
                                                <small>{{ t('newApp.github.inspect.loadingBody') }}</small>
                                            </div>
                                        </div>

                                        <div
                                            v-else-if="inspectError"
                                            class="inspect-card inspect-card--error"
                                        >
                                            <i class="pi pi-times-circle inspect-card__icon" aria-hidden="true" />
                                            <div>
                                                <strong>{{ t('newApp.github.inspect.errorTitle') }}</strong>
                                                <small>{{ inspectError }}</small>
                                            </div>
                                            <Button
                                                size="small"
                                                severity="secondary"
                                                :label="t('newApp.github.inspect.retry')"
                                                type="button"
                                                @click="inspectSelectedRepo"
                                            />
                                        </div>

                                        <div
                                            v-else-if="inspectResult && inspectIsDeployable"
                                            class="inspect-card inspect-card--ok"
                                        >
                                            <i class="pi pi-check-circle inspect-card__icon" aria-hidden="true" />
                                            <div>
                                                <strong>
                                                    {{ t('newApp.github.inspect.okTitle', { kind: inspectResult.suggested_type === 'docker_compose' ? t('newApp.github.buildOptions.compose') : t('newApp.github.buildOptions.dockerfile') }) }}
                                                </strong>
                                                <small>
                                                    {{ t('newApp.github.inspect.okFound') }}
                                                    <code v-for="f in inspectResult.found_files" :key="f" class="found-file">{{ f }}</code>
                                                </small>
                                            </div>
                                        </div>

                                        <div
                                            v-else-if="inspectResult"
                                            class="inspect-card inspect-card--missing"
                                        >
                                            <i class="pi pi-exclamation-triangle inspect-card__icon" aria-hidden="true" />
                                            <div>
                                                <strong>{{ t('newApp.github.inspect.missingTitle') }}</strong>
                                                <i18n-t keypath="newApp.github.inspect.missingBody" tag="small" scope="global">
                                                    <template #dockerfile><code>Dockerfile</code></template>
                                                    <template #compose><code>docker-compose.yml</code></template>
                                                </i18n-t>
                                            </div>
                                            <Button
                                                size="small"
                                                severity="secondary"
                                                :label="t('newApp.github.inspect.retry')"
                                                type="button"
                                                @click="inspectSelectedRepo"
                                            />
                                        </div>

                                        <!--
                                            Branch / build type / file path only show
                                            AFTER the inspect succeeds with something
                                            deployable. During loading / error /
                                            missing states they're hidden — the
                                            inline status card is the focal point
                                            and these fields would just be noise
                                            (or worse: encourage the operator to
                                            "fix it manually" with values the
                                            backend would reject anyway).
                                        -->
                                        <div v-if="inspectIsDeployable" class="form-grid form-grid--3">
                                            <SField :label="t('newApp.github.branchLabel')">
                                                <!--
                                                    `filter` enables typeahead inside the
                                                    dropdown (PrimeVue's built-in search
                                                    box). `editable` is kept so the
                                                    operator can also type a brand-new
                                                    branch name (e.g. one that doesn't
                                                    exist on origin yet but they're about
                                                    to push). `@change` re-runs the
                                                    Dockerfile/Compose probe against
                                                    whatever they just picked or typed.
                                                -->
                                                <Select
                                                    v-model="draft.github_branch"
                                                    :options="repoBranchOptions"
                                                    optionLabel="name"
                                                    optionValue="name"
                                                    placeholder="main"
                                                    editable
                                                    filter
                                                    :filterPlaceholder="t('newApp.github.branchSearchPlaceholder')"
                                                    :showClear="false"
                                                    @change="inspectSelectedRepo"
                                                />
                                            </SField>
                                            <SField :label="t('newApp.github.buildTypeLabel')">
                                                <Select v-model="draft.github_build_type" :options="githubBuildOptions" optionLabel="label" optionValue="value" />
                                            </SField>
                                            <SField :label="draft.github_build_type === 'docker_compose' ? t('newApp.github.composeFileLabel') : t('newApp.github.dockerfileLabel')">
                                                <InputText v-if="draft.github_build_type === 'docker_compose'" v-model="draft.compose_file" placeholder="docker-compose.yml" />
                                                <InputText v-else v-model="draft.dockerfile_path" placeholder="Dockerfile" />
                                            </SField>
                                        </div>
                                    </template>
                                </template>
                            </template>

                            <template v-else-if="draft.source === 'git'">
                                <SField :label="t('newApp.git.repoLabel')">
                                    <InputText v-model="draft.repo_url" :placeholder="t('newApp.git.repoPlaceholder')" autofocus />
                                </SField>
                                <div class="form-grid form-grid--3">
                                    <SField :label="t('newApp.git.branchLabel')">
                                        <InputText v-model="draft.branch" :placeholder="t('newApp.git.branchPlaceholder')" />
                                    </SField>
                                    <SField :label="t('newApp.git.dockerfileLabel')">
                                        <InputText v-model="draft.dockerfile_path" :placeholder="t('newApp.git.dockerfilePlaceholder')" />
                                    </SField>
                                    <SField :label="t('newApp.git.contextLabel')">
                                        <InputText v-model="draft.build_context" :placeholder="t('newApp.git.contextPlaceholder')" />
                                    </SField>
                                </div>
                            </template>

                            <template v-else-if="draft.source === 'image'">
                                <div class="form-grid">
                                    <SField :label="t('newApp.image.imageLabel')">
                                        <InputText v-model="draft.image_name" :placeholder="t('newApp.image.imagePlaceholder')" autofocus />
                                    </SField>
                                    <SField :label="t('newApp.image.tagLabel')">
                                        <InputText v-model="draft.image_tag" :placeholder="t('newApp.image.tagPlaceholder')" />
                                    </SField>
                                </div>
                            </template>

                            <template v-else-if="draft.source === 'compose'">
                                <SField :label="t('newApp.git.repoLabel')">
                                    <InputText v-model="draft.repo_url" :placeholder="t('newApp.git.repoPlaceholder')" autofocus />
                                </SField>
                                <div class="form-grid form-grid--3">
                                    <SField :label="t('newApp.git.branchLabel')">
                                        <InputText v-model="draft.branch" :placeholder="t('newApp.git.branchPlaceholder')" />
                                    </SField>
                                    <SField :label="t('newApp.compose.fileLabel')">
                                        <InputText v-model="draft.compose_file" placeholder="docker-compose.yml" />
                                    </SField>
                                    <SField :label="t('newApp.git.contextLabel')">
                                        <InputText v-model="draft.build_context" :placeholder="t('newApp.git.contextPlaceholder')" />
                                    </SField>
                                </div>
                                <Message severity="info" :closable="false">
                                    {{ t('newApp.compose.composeInfo') }}
                                </Message>
                            </template>

                            <template v-else-if="draft.source === 'compose-inline'">
                                <SField :label="t('newApp.compose.inlineLabel')">
                                    <Textarea v-model="draft.compose_inline" rows="10" autoResize placeholder="services:&#10;  web:&#10;    image: nginx:alpine&#10;    ports:&#10;      - &quot;8080:80&quot;" />
                                </SField>
                                <Message severity="info" :closable="false">
                                    {{ t('newApp.compose.composeInfo') }}
                                </Message>
                            </template>

                            <template v-else>
                                <SField :label="t('newApp.inline.label')">
                                    <Textarea v-model="draft.dockerfile_inline" rows="8" autoResize placeholder="FROM node:22-alpine&#10;WORKDIR /app&#10;COPY . .&#10;CMD [&quot;npm&quot;, &quot;start&quot;]" />
                                </SField>
                            </template>
                        </section>

                        <!-- ─────────── Step 2: Configuração ─────────── -->
                        <section v-else-if="step === 1" class="step">
                            <div class="form-grid">
                                <SField :label="t('newApp.config.nameLabel')" :error="step3Errors.name ?? undefined">
                                    <InputText v-model="draft.name" :placeholder="t('newApp.config.namePlaceholder')" autofocus />
                                </SField>
                                <SField :label="t('newApp.config.serverLabel')" :error="step3Errors.server_id ?? undefined">
                                    <Select v-model="draft.server_id" :options="serversStore.servers" optionLabel="name" optionValue="id" :placeholder="t('newApp.config.serverPlaceholder')" />
                                </SField>
                            </div>

                            <SField :label="t('newApp.config.descriptionLabel')">
                                <InputText v-model="draft.description" :placeholder="t('newApp.config.descriptionPlaceholder')" />
                            </SField>

                            <!--
                                No port / healthcheck question here on purpose.

                                Modelo decidido com o usuário:
                                  - Compose: porta vem do YAML, runner descobre
                                    automaticamente quando subir o stack.
                                  - Dockerfile/Image: ideal é ler EXPOSE do
                                    Dockerfile / `docker inspect` da imagem
                                    pós-pull. Hoje o backend não faz auto-
                                    detect ainda, então enviamos um default
                                    sensato (3000 / "/") que o operador edita
                                    em Apps → Configurações depois.
                                  - Containers só recebem tráfego externo
                                    quando ganham domínio (igual Coolify) —
                                    nada precisa ser declarado no momento
                                    da criação.

                                TODO(backend): /git-sources/{id}/repositories/inspect
                                deveria também devolver `expose_ports` (parse
                                `EXPOSE` no Dockerfile / `services.*.ports` no
                                Compose) para alimentar o default em vez do
                                hardcoded 3000.
                            -->
                            <Message severity="info" :closable="false">
                                <i18n-t keypath="newApp.config.portInfo" scope="global">
                                    <template #strong><strong>{{ t('newApp.config.portInfoStrong') }}</strong></template>
                                </i18n-t>
                            </Message>
                        </section>

                        <!-- ─────────── Step 3: Revisar & criar ─────────── -->
                        <section v-else class="step">
                            <p class="prose">
                                {{ t('newApp.review.intro') }}
                            </p>

                            <div class="summary">
                                <h3>{{ t('newApp.review.identity') }}</h3>
                                <dl>
                                    <dt>{{ t('newApp.review.fields.name') }}</dt>
                                    <dd class="mono">{{ draft.name }}</dd>
                                    <template v-if="draft.description">
                                        <dt>{{ t('newApp.review.fields.description') }}</dt>
                                        <dd>{{ draft.description }}</dd>
                                    </template>
                                    <dt>{{ t('newApp.review.fields.server') }}</dt>
                                    <dd>{{ selectedServerName }}</dd>
                                </dl>
                            </div>

                            <div class="summary">
                                <h3>{{ t('newApp.review.origin') }}</h3>
                                <dl>
                                    <dt>{{ t('newApp.review.fields.type') }}</dt>
                                    <dd>
                                        <i :class="selectedSource.icon" class="summary__icon" />
                                        {{ selectedSource.label }}
                                    </dd>
                                    <template v-for="line in originSummary" :key="line.k">
                                        <dt>{{ line.k }}</dt>
                                        <dd :class="line.mono ? 'mono' : ''">{{ line.v }}</dd>
                                    </template>
                                </dl>
                            </div>

                            <Message severity="info" :closable="false">
                                <i18n-t keypath="newApp.review.nextSteps" scope="global">
                                    <template #settings><strong>{{ t('newApp.review.settings') }}</strong></template>
                                    <template #domains><strong>{{ t('newApp.review.domains') }}</strong></template>
                                </i18n-t>
                            </Message>

                            <Message v-if="createError" severity="error" :closable="false">{{ createError }}</Message>
                        </section>
                    </div>
                </div>

                <!--
                    Sticky footer with actions.
                    - "Continuar" / "Criar projeto" are type=submit so Enter
                      inside any focused input triggers the right action.
                    - "Voltar" / "Cancelar" are explicit type=button so
                      Enter in the inputs never closes/regresses.
                -->
                <footer class="pane__footer">
                    <Button type="button" text :label="t('common.cancel')" :disabled="creating" @click="router.push('/apps')" />
                    <div class="pane__footer-right">
                        <Button
                            v-if="step > 0"
                            type="button"
                            text
                            :label="t('newApp.footer.back')"
                            icon="pi pi-arrow-left"
                            :disabled="creating"
                            @click="goBack"
                        />
                        <Button
                            v-if="step < steps.length - 1"
                            type="submit"
                            :label="t('newApp.footer.continue')"
                            icon="pi pi-arrow-right"
                            icon-pos="right"
                            :disabled="!canContinue"
                        />
                        <Button
                            v-else
                            type="submit"
                            :label="t('newApp.footer.create')"
                            icon="pi pi-check"
                            :loading="creating"
                            :disabled="!canContinue"
                        />
                    </div>
                </footer>
            </form>
        </div>
    </div>
</template>

<script setup lang="ts">
/*
    Create App — full-page wizard at /apps/new.

    Same chrome as AddDomainView (rail + pane + sticky footer) so both
    "new X" pages share visual vocabulary. Form logic is unchanged from
    the previous flat version — only the step partitioning is new:

      1. Origem            — pick one of 6 source types.
      2. Repositório/Imagem — source-specific fields. GitHub flow has
                              auto-inspect: clicking a repo runs the
                              backend's Dockerfile/compose detector and
                              pre-fills build_type / paths / context.
      3. Configuração      — name, description, server, port, health.
      4. Revisar & criar    — read-only summary + submit.

    All four steps share the single `draft` ref. `canContinue` per step
    drives both the Continue button's disabled state and the Enter-key
    advancement. Submit on Step 4 sends the assembled payload and lands
    on /apps/:id of the created app.
*/
import { computed, onMounted, ref } from 'vue'
import { useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import Select from 'primevue/select'
import Message from 'primevue/message'
import SField from '@/components/settings/SField.vue'
import IconCheck from '@/components/icons/IconCheck.vue'
import IconChevronLeft from '@/components/icons/IconChevronLeft.vue'
import { useAppsStore } from '@/stores/apps'
import { useServersStore } from '@/stores/servers'
import { useGitSourcesStore } from '@/stores/gitSources'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { GitBranch, GitRepository, RepositoryInspect } from '@/types/api'

const router = useRouter()
const { t } = useI18n()
const appsStore = useAppsStore()
const serversStore = useServersStore()
const gitSourcesStore = useGitSourcesStore()

const steps = computed(() => [
    // Step 1 combines source choice + source-specific details. The
    // two were split originally for guided pacing, but in practice
    // the operator never wants to "pick GitHub" without immediately
    // picking the repo — splitting them just added an extra click.
    { label: t('newApp.steps.origin.label'), desc: t('newApp.steps.origin.desc') },
    { label: t('newApp.steps.config.label'), desc: t('newApp.steps.config.desc') },
    { label: t('newApp.steps.review.label'), desc: t('newApp.steps.review.desc') },
])

const step = ref(0)
const creating = ref(false)
const repoSearch = ref('')
const repoBranchOptions = ref<GitBranch[]>([])
const createError = ref<string | null>(null)

/*
   Inspect state — the backend probes the repo for Dockerfile /
   docker-compose.yml / compose.yml and reports back what it found.
   We surface that result inline so the operator can:

     - see what the build engine will run (Dockerfile vs Compose)
     - know UP FRONT that Prexel can't deploy this repo, instead of
       only finding out after the first failed deploy

   Today Prexel deploys via Docker only. When we add Bun / Node-native
   runtimes later, this same screen grows additional "this works too"
   states; for now anything that's neither Dockerfile nor Compose is
   a hard block.
*/
const inspecting = ref(false)
const inspectResult = ref<RepositoryInspect | null>(null)
const inspectError = ref<string | null>(null)

type AppSource = 'github' | 'git' | 'image' | 'inline' | 'compose' | 'compose-inline'
const sourceOptions = computed<Array<{ id: AppSource; label: string; description: string; icon: string }>>(() => [
    { id: 'github', label: t('newApp.sourceCards.github.label'), description: t('newApp.sourceCards.github.description'), icon: 'pi pi-github' },
    { id: 'git', label: t('newApp.sourceCards.git.label'), description: t('newApp.sourceCards.git.description'), icon: 'pi pi-code' },
    { id: 'image', label: t('newApp.sourceCards.image.label'), description: t('newApp.sourceCards.image.description'), icon: 'pi pi-box' },
    { id: 'inline', label: t('newApp.sourceCards.inline.label'), description: t('newApp.sourceCards.inline.description'), icon: 'pi pi-file-edit' },
    { id: 'compose', label: t('newApp.sourceCards.compose.label'), description: t('newApp.sourceCards.compose.description'), icon: 'pi pi-sitemap' },
    { id: 'compose-inline', label: t('newApp.sourceCards.composeInline.label'), description: t('newApp.sourceCards.composeInline.description'), icon: 'pi pi-list-check' },
])

const draft = ref({
    source: 'github' as AppSource,
    name: '',
    description: '',
    server_id: null as string | null,
    github_source_id: null as string | null,
    github_repo: '',
    github_branch: 'main',
    github_build_type: 'dockerfile' as 'dockerfile' | 'docker_compose',
    repo_url: '',
    branch: 'main',
    dockerfile_path: 'Dockerfile',
    build_context: '.',
    image_name: '',
    image_tag: 'latest',
    dockerfile_inline: '',
    compose_file: 'docker-compose.yml',
    compose_inline: '',
    port: '3000',
    health_check_path: '/',
})

const githubBuildOptions = computed(() => [
    { label: t('newApp.github.buildOptions.dockerfile'), value: 'dockerfile' },
    { label: t('newApp.github.buildOptions.compose'), value: 'docker_compose' },
])

const selectedSource = computed(() => sourceOptions.value.find((s) => s.id === draft.value.source) ?? sourceOptions.value[0]!)
const githubRepos = computed(() => draft.value.github_source_id ? (gitSourcesStore.repositories[draft.value.github_source_id] ?? []) : [])
const selectedGitHubRepo = computed(() => githubRepos.value.find((r) => r.full_name === draft.value.github_repo) ?? null)
const selectedServerName = computed(() => {
    if (!draft.value.server_id) return serversStore.servers.length ? t('newApp.config.selectServer') : t('newApp.config.noServer')
    return serversStore.servers.find((s) => s.id === draft.value.server_id)?.name ?? t('newApp.config.serverLabel')
})

// ── Step 3 inline error map ──
// We surface inline errors on Step 3 fields once the operator has
// attempted to advance OR is on Step 4 trying to come back through.
// Until then the inputs stay clean.
const step3Errors = computed<Record<string, string | null>>(() => {
    if (!draft.value.name.trim() && createError.value === '__step3_attempted__') {
        // sentinel never set; the lazy validation lives in canContinue
    }
    return {
        name: null,
        server_id: null,
        port: null,
    }
})

// ── Step 4 origin summary ──
// Reads `draft` and surfaces only the fields that matter for the
// currently-picked source — keeps the review card honest (no empty
// rows from sibling source types).
const originSummary = computed<Array<{ k: string; v: string; mono?: boolean }>>(() => {
    const d = draft.value
    const f = (key: string) => t(`newApp.review.fields.${key}`)
    switch (d.source) {
        case 'github':
            return [
                { k: f('connection'), v: gitSourcesStore.githubSources.find((s) => s.id === d.github_source_id)?.name ?? '—' },
                { k: f('repo'), v: d.github_repo, mono: true },
                { k: f('branch'), v: d.github_branch, mono: true },
                { k: f('build'), v: d.github_build_type === 'docker_compose' ? t('newApp.review.buildKindCompose', { file: d.compose_file }) : t('newApp.review.buildKindDockerfile', { path: d.dockerfile_path }), mono: true },
                { k: f('context'), v: d.build_context, mono: true },
            ]
        case 'git':
            return [
                { k: f('repo'), v: d.repo_url, mono: true },
                { k: f('branch'), v: d.branch, mono: true },
                { k: f('dockerfile'), v: d.dockerfile_path, mono: true },
                { k: f('context'), v: d.build_context, mono: true },
            ]
        case 'image':
            return [
                { k: f('image'), v: `${d.image_name}:${d.image_tag}`, mono: true },
            ]
        case 'inline':
            return [
                { k: f('dockerfileInline'), v: t('newApp.review.lines', { n: d.dockerfile_inline.split('\n').length }) },
            ]
        case 'compose':
            return [
                { k: f('repo'), v: d.repo_url, mono: true },
                { k: f('branch'), v: d.branch, mono: true },
                { k: f('composeFile'), v: d.compose_file, mono: true },
                { k: f('context'), v: d.build_context, mono: true },
            ]
        case 'compose-inline':
            return [
                { k: f('composeInline'), v: t('newApp.review.lines', { n: d.compose_inline.split('\n').length }) },
            ]
        default:
            return []
    }
})

// ── Per-step validation ──
// Returns the first reason the operator can't advance, or null when
// they can. The footer's Continue/Create button reads this via
// canContinue; the submit handler uses it for the same gate.
//
// Step 0 (Origem) now folds source picker + source-specific fields:
// the source always has a default ('github'), so the meaningful gate
// is the source-specific completeness — the operator can't advance
// without filling in what makes that source actually deployable.
function stepError(s: number): string | null {
    const d = draft.value
    if (s === 0) {
        switch (d.source) {
            case 'github':
                if (!d.github_source_id) return t('newApp.validation.githubConnection')
                if (!d.github_repo) return t('newApp.validation.githubRepo')
                // Wait for the auto-inspect to finish before letting the
                // operator advance — otherwise the Step 2 defaults might
                // be wrong (build_type='dockerfile' on a Compose repo).
                if (inspecting.value) return t('newApp.validation.githubInspecting')
                // Hard block: Prexel can't deploy this repo today.
                // The inline status card already explains why; the
                // Continue button stays disabled.
                if (inspectResult.value && !inspectIsDeployable.value) {
                    return t('newApp.validation.githubNotDeployable')
                }
                return null
            case 'git':
                if (!d.repo_url.trim()) return t('newApp.validation.gitRepoUrl')
                return null
            case 'image':
                if (!d.image_name.trim()) return t('newApp.validation.imageName')
                return null
            case 'inline':
                if (!d.dockerfile_inline.trim()) return t('newApp.validation.dockerfileInline')
                return null
            case 'compose':
                if (!d.repo_url.trim()) return t('newApp.validation.composeRepoUrl')
                return null
            case 'compose-inline':
                if (!d.compose_inline.trim()) return t('newApp.validation.composeInline')
                return null
        }
    }
    if (s === 1) {
        if (!d.name.trim()) return t('newApp.validation.name')
        if (!d.server_id) return t('newApp.validation.server')
        // Port + healthcheck aren't asked in the wizard anymore —
        // operator edits them later in the app detail page. The
        // submit ships defaults (3000 / "/") so the backend always
        // gets valid values.
        return null
    }
    return null
}

const canContinue = computed(() => {
    if (creating.value) return false
    return stepError(step.value) === null
})

onMounted(async () => {
    try {
        await Promise.all([
            serversStore.fetchAll(),
            gitSourcesStore.fetchAll(),
        ])
        if (!draft.value.server_id && serversStore.servers.length > 0) {
            draft.value.server_id = serversStore.servers[0]!.id
        }
        if (!draft.value.github_source_id && gitSourcesStore.githubSources.length > 0) {
            draft.value.github_source_id = gitSourcesStore.githubSources[0]!.id
            await loadGitHubRepos()
        }
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
})

function goBack() {
    if (step.value === 0) return
    step.value -= 1
    createError.value = null
}

function goNext() {
    if (!canContinue.value) return
    step.value += 1
    createError.value = null
}

// Single Enter-key handler for the whole wizard form. Step < last →
// advance. Last step → submit. Guards on canContinue / creating keep
// rapid Enter presses from firing twice.
function onFormSubmit() {
    if (creating.value) return
    if (step.value < steps.value.length - 1) {
        goNext()
        return
    }
    void submitNew()
}

async function submitNew() {
    // Final guard — covers the case where Step 4 was reached via Back
    // from a state that no longer validates (rare but possible if the
    // operator edited the draft from devtools).
    for (let s = 0; s < steps.value.length - 1; s++) {
        const err = stepError(s)
        if (err) {
            createError.value = err
            step.value = s
            return
        }
    }
    creating.value = true
    createError.value = null
    try {
        const body: Record<string, unknown> = {
            name: draft.value.name.trim(),
            description: draft.value.description.trim() || undefined,
            branch: draft.value.branch || 'main',
            port: Number(draft.value.port),
            health_check_enabled: true,
            health_check_path: draft.value.health_check_path || '/',
            health_check_method: 'GET',
        }
        if (draft.value.server_id) body.server_id = draft.value.server_id

        if (draft.value.source === 'github') {
            const repo = selectedGitHubRepo.value
            body.git_source_id = draft.value.github_source_id
            body.repo_url = repo?.clone_url || `https://github.com/${draft.value.github_repo}`
            body.branch = draft.value.github_branch || repo?.default_branch || 'main'
            body.build_context = draft.value.build_context.trim() || '.'
            body.build_type = draft.value.github_build_type
            if (draft.value.github_build_type === 'docker_compose') {
                body.compose_file = draft.value.compose_file.trim() || 'docker-compose.yml'
            } else {
                body.dockerfile_path = draft.value.dockerfile_path.trim() || 'Dockerfile'
            }
        } else if (draft.value.source === 'image') {
            body.build_type = 'docker_image'
            body.image_name = draft.value.image_name.trim()
            body.image_tag = draft.value.image_tag.trim() || 'latest'
        } else if (draft.value.source === 'compose' || draft.value.source === 'compose-inline') {
            body.build_type = 'docker_compose'
            body.build_context = draft.value.build_context.trim() || '.'
            if (draft.value.source === 'compose') {
                body.repo_url = draft.value.repo_url.trim()
                body.compose_file = draft.value.compose_file.trim() || 'docker-compose.yml'
            } else {
                body.compose_inline = draft.value.compose_inline.trim()
            }
        } else {
            body.build_type = 'dockerfile'
            body.dockerfile_path = draft.value.dockerfile_path.trim() || 'Dockerfile'
            body.build_context = draft.value.build_context.trim() || '.'
            if (draft.value.source === 'git') {
                body.repo_url = draft.value.repo_url.trim()
            } else {
                body.dockerfile_inline = draft.value.dockerfile_inline.trim()
            }
        }

        const created = await appsStore.create(body)
        notify.success(t('apps.deletedSuccess'))
        if (created?.id) {
            router.push(`/apps/${created.id}`)
        } else {
            router.push('/apps')
        }
    } catch (e) {
        createError.value = apiErrorMessage(e)
    } finally {
        creating.value = false
    }
}

async function loadGitHubRepos() {
    if (!draft.value.github_source_id) return
    try {
        await gitSourcesStore.fetchRepositories(draft.value.github_source_id, repoSearch.value)
        if (gitSourcesStore.lastRepoError) notify.warn(gitSourcesStore.lastRepoError)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

async function selectGitHubRepo(repo: GitRepository) {
    draft.value.github_repo = repo.full_name
    draft.value.github_branch = repo.default_branch || 'main'
    // Auto-derive the project name from the repo on first pick, so the
    // operator doesn't have to retype it in Step 3 99% of the time.
    if (!draft.value.name.trim()) {
        draft.value.name = repo.name.toLowerCase().replace(/[^a-z0-9-]/g, '-').replace(/^-+|-+$/g, '').slice(0, 50)
    }
    // Reset previous inspect — different repo, different verdict. We
    // also clear inspectError so the inline error message disappears
    // until we know the result for THIS repo.
    inspectResult.value = null
    inspectError.value = null
    if (!draft.value.github_source_id) return
    try {
        repoBranchOptions.value = await gitSourcesStore.fetchBranches(draft.value.github_source_id, repo.full_name)
        await inspectSelectedRepo()
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

// Probes the repo for Dockerfile/compose. Auto-applies the detected
// build settings so Step 2 lands with sensible defaults, AND stashes
// the full result so the inline status card can show what was found
// (or warn when nothing was). Re-runs whenever the branch changes
// — Dockerfile may exist on `main` but not on the selected branch.
async function inspectSelectedRepo() {
    if (!draft.value.github_source_id || !draft.value.github_repo) return
    inspecting.value = true
    inspectError.value = null
    try {
        const result = await gitSourcesStore.inspectRepository(draft.value.github_source_id, draft.value.github_repo, draft.value.github_branch)
        inspectResult.value = result
        // Only adopt defaults that the inspect actually found —
        // otherwise we'd overwrite a manual entry with the backend's
        // fallback values (build_type='dockerfile', context='.').
        if (result.dockerfile_path || result.compose_file) {
            draft.value.github_build_type = result.suggested_type
            draft.value.build_context = result.build_context || '.'
        }
        if (result.dockerfile_path) draft.value.dockerfile_path = result.dockerfile_path
        if (result.compose_file) draft.value.compose_file = result.compose_file
    } catch (e) {
        inspectError.value = apiErrorMessage(e)
        inspectResult.value = null
    } finally {
        inspecting.value = false
    }
}

// "Did the inspect find anything deployable?" — used by the inline
// status card AND the step-0 gate. Today Prexel only deploys via
// Docker, so the gate is "Dockerfile OR compose found". When we add
// Bun/Node-native runtimes later, this also checks `package.json`
// / `bun.lock` etc and broadens the verdict.
const inspectIsDeployable = computed<boolean>(() => {
    const r = inspectResult.value
    if (!r) return false
    return !!r.dockerfile_path || !!r.compose_file
})
</script>

<style scoped>
/* ────────────────────────────────────────────────────────────────────
   Layout — copied verbatim from AddDomainView so the two `new X`
   wizards share the same chrome. If we ever need to evolve this,
   extract to a shared <WizardShell> component.
   ──────────────────────────────────────────────────────────────────── */
.new-app {
    max-width: 1100px;
    margin: 0 auto;
}
.new-app__top { margin-bottom: 16px; }

.back-link {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    color: var(--p-text-muted);
    text-decoration: none;
    transition: color 120ms;
}
.back-link:hover { color: var(--p-text); }

.new-app__grid {
    display: grid;
    grid-template-columns: 280px 1fr;
    gap: 32px;
    align-items: start;
}
@media (max-width: 880px) {
    .new-app__grid { grid-template-columns: 1fr; gap: 16px; }
}

/* ────────────────────────────────────────────────────────────────────
   Stepper rail
   ──────────────────────────────────────────────────────────────────── */
.rail {
    position: sticky;
    top: 24px;
    padding: 24px;
    background: var(--p-content-bg);
    border: 1px solid var(--p-content-border);
    border-radius: 14px;
}
.rail__title { margin: 0 0 4px; font-size: 18px; font-weight: 700; color: var(--p-text); }
.rail__sub { margin: 0 0 20px; font-size: 12px; color: var(--p-text-muted); line-height: 1.45; }

.rail__steps { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0; }
.rail-step { position: relative; display: flex; gap: 12px; padding: 10px 0; }
.rail-step:not(:last-child)::before {
    content: '';
    position: absolute;
    left: 13px;
    top: 36px;
    bottom: 0;
    width: 2px;
    background: var(--p-content-border);
}
.rail-step.is-done:not(:last-child)::before {
    background: var(--p-primary-500);
    opacity: 0.45;
}
.rail-step__bullet {
    flex-shrink: 0;
    width: 28px;
    height: 28px;
    border-radius: 999px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    font-weight: 700;
    border: 2px solid var(--p-content-border);
    background: var(--p-content-bg);
    color: var(--p-text-muted);
    transition: all 160ms ease;
    z-index: 1;
}
.rail-step.is-current .rail-step__bullet {
    border-color: var(--p-primary-500);
    color: var(--p-primary-500);
    box-shadow: 0 0 0 4px color-mix(in srgb, var(--p-primary-500), transparent 85%);
}
.rail-step.is-done .rail-step__bullet {
    background: var(--p-primary-500);
    border-color: var(--p-primary-500);
    color: #fff;
}
.rail-step__text { display: flex; flex-direction: column; gap: 2px; padding-top: 3px; }
.rail-step__label { font-size: 13px; font-weight: 600; color: var(--p-text); line-height: 1.2; }
.rail-step.is-future .rail-step__label { color: var(--p-text-muted); }
.rail-step__desc { font-size: 11px; color: var(--p-text-muted); line-height: 1.4; }

@media (max-width: 880px) {
    .rail { position: static; padding: 16px; }
    .rail__title { font-size: 16px; }
    .rail__sub { display: none; }
    .rail__steps { flex-direction: row; gap: 8px; overflow-x: auto; padding-bottom: 4px; }
    .rail-step { flex-direction: column; align-items: center; gap: 6px; padding: 0; min-width: 64px; }
    .rail-step:not(:last-child)::before { display: none; }
    .rail-step__text { padding-top: 0; align-items: center; text-align: center; }
    .rail-step__desc { display: none; }
}

/* ────────────────────────────────────────────────────────────────────
   Main pane
   ──────────────────────────────────────────────────────────────────── */
.pane { display: flex; flex-direction: column; gap: 16px; min-height: calc(100vh - 200px); }

.pane__card {
    background: var(--p-content-bg);
    border: 1px solid var(--p-content-border);
    border-radius: 14px;
    overflow: hidden;
}
.pane__head {
    padding: 28px 32px 18px;
    border-bottom: 1px solid var(--p-content-border);
    background: linear-gradient(180deg, color-mix(in srgb, var(--p-primary-500), transparent 96%) 0%, transparent 100%);
}
.pane__kicker {
    display: block;
    margin-bottom: 6px;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--p-primary-500);
}
.pane__title { margin: 0; font-size: 22px; font-weight: 700; color: var(--p-text); letter-spacing: -0.01em; }
.pane__sub { margin: 6px 0 0; font-size: 13px; color: var(--p-text-muted); line-height: 1.5; max-width: 580px; }

.pane__body { padding: 28px 32px; }

.pane__footer {
    position: sticky;
    bottom: 0;
    z-index: 5;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 18px;
    background: var(--p-content-bg);
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    box-shadow: 0 -4px 12px -6px rgba(0, 0, 0, 0.12);
}
.pane__footer-right { display: inline-flex; align-items: center; gap: 8px; }

/* ────────────────────────────────────────────────────────────────────
   Step content primitives
   ──────────────────────────────────────────────────────────────────── */
.step { display: flex; flex-direction: column; gap: 18px; }
.prose { margin: 0; color: var(--p-text-muted); line-height: 1.55; font-size: 14px; }

/* Divider inside Step 1 — separates the source picker from the
   source-specific fields without introducing visual weight. */
.step-divider {
    margin: 4px 0;
    border: 0;
    border-top: 1px dashed var(--p-content-border);
}

/* ── Source picker (Step 1) ── */
.source-picker {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px;
}
.source-card {
    min-height: 118px;
    text-align: left;
    display: flex;
    flex-direction: column;
    gap: 10px;
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    padding: 16px;
    background: var(--p-content-bg);
    color: var(--p-text);
    cursor: pointer;
    transition: border-color 120ms, background 120ms, transform 120ms;
}
.source-card:hover {
    border-color: rgba(148, 163, 184, 0.45);
    transform: translateY(-1px);
}
.source-card.is-active {
    border-color: rgba(16, 185, 129, 0.65);
    background: rgba(16, 185, 129, 0.08);
}
.source-card i { color: var(--p-primary-400); font-size: 18px; }
.source-card strong { display: block; font-size: 13px; margin-bottom: 4px; }
.source-card small { display: block; color: var(--p-text-muted); font-size: 12px; line-height: 1.35; }

/* ── Repo picker (Step 2 / GitHub) ── */
.github-connect-card {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 14px;
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    background: color-mix(in srgb, var(--p-primary-color), transparent 92%);
    padding: 14px;
}
.github-connect-card strong, .github-connect-card span { display: block; }
.github-connect-card span { margin-top: 4px; color: var(--p-text-muted); font-size: 12px; }

.repo-picker {
    display: grid;
    max-height: 300px;
    overflow: auto;
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    background: var(--p-content-bg);
}
.repo-picker:empty { display: none; }
.repo-picker__row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    border: 0;
    border-bottom: 1px solid var(--p-divider);
    background: transparent;
    color: var(--p-text);
    padding: 11px 14px;
    text-align: left;
    cursor: pointer;
}
.repo-picker__row:last-child { border-bottom: 0; }
.repo-picker__row:hover { background: var(--p-hover); }
.repo-picker__row.is-active { background: rgba(16, 185, 129, 0.10); }
.repo-picker__row i { opacity: 0; color: var(--p-primary-color); }
.repo-picker__row.is-active i { opacity: 1; }
.repo-picker__row strong, .repo-picker__row small { display: block; }
.repo-picker__row small { color: var(--p-text-muted); font-size: 12px; }

/* ── Inspect status card (Step 1, GitHub source) ──
   Reports what the Dockerfile/compose probe found. Border-left
   stripe drives the color so the same shape works for 4 states
   without 4 different layouts. */
.inspect-card {
    display: grid;
    grid-template-columns: 28px 1fr auto;
    align-items: center;
    gap: 12px;
    padding: 12px 14px;
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    background: var(--p-content-bg);
    border-left-width: 3px;
}
.inspect-card__icon {
    font-size: 18px;
    line-height: 1;
    grid-column: 1;
}
.inspect-card strong {
    display: block;
    font-size: 13px;
    color: var(--p-text);
}
.inspect-card small {
    display: block;
    margin-top: 2px;
    color: var(--p-text-muted);
    font-size: 12px;
    line-height: 1.45;
}
.inspect-card code,
.inspect-card .found-file {
    display: inline-block;
    margin: 0 4px 0 0;
    padding: 1px 6px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 4px;
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
    color: var(--p-text);
}
.inspect-card--loading {
    border-left-color: var(--p-text-muted);
}
.inspect-card--loading .inspect-card__icon { color: var(--p-text-muted); }
.inspect-card--ok {
    border-left-color: var(--p-primary-500);
    background: color-mix(in srgb, var(--p-primary-500), transparent 95%);
}
.inspect-card--ok .inspect-card__icon { color: var(--p-primary-500); }
.inspect-card--missing {
    border-left-color: #d97706;
    background: color-mix(in srgb, #d97706, transparent 92%);
}
.inspect-card--missing .inspect-card__icon { color: #d97706; }
.inspect-card--error {
    border-left-color: #dc2626;
    background: color-mix(in srgb, #dc2626, transparent 94%);
}
.inspect-card--error .inspect-card__icon { color: #dc2626; }

/* ── Form grids (Steps 2 + 3) ── */
.form-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
}
.form-grid--3 { grid-template-columns: 0.8fr 1fr 0.8fr; }
.inline-search {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 42px;
    gap: 8px;
}

/* ── Review summary (Step 4) ── */
.summary {
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    padding: 16px 18px;
    background: var(--p-content-bg);
}
.summary h3 {
    margin: 0 0 10px;
    font-size: 12px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--p-text-muted);
}
.summary dl {
    display: grid;
    grid-template-columns: minmax(120px, auto) 1fr;
    column-gap: 16px;
    row-gap: 6px;
    margin: 0;
}
.summary dt { font-size: 12px; color: var(--p-text-muted); }
.summary dd {
    margin: 0;
    font-size: 13px;
    color: var(--p-text);
    word-break: break-word;
}
.summary__icon { color: var(--p-primary-400); margin-right: 6px; }

.mono { font-family: ui-monospace, Menlo, monospace; font-size: 12px; }

:deep(.p-select), :deep(.p-inputtext) { width: 100%; }
:deep(textarea.p-textarea) {
    width: 100%;
    min-height: 164px;
    font-family: ui-monospace, Menlo, monospace;
    font-size: 12px;
}

@media (max-width: 880px) {
    .source-picker, .form-grid, .form-grid--3 { grid-template-columns: 1fr; }
}
</style>
