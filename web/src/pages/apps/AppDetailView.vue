<template>
    <!--
        Layout follows the Settings shell: a sticky rail on the left
        with the app's identity + section nav, and a content pane on
        the right with the section's body. Replaces the previous
        horizontal-tabs layout. Tab state still lives in `?tab=`, so
        external links into "deployments" / "settings" / etc keep
        working unchanged.
    -->
    <div class="app-detail">
        <SettingsRail
            :groups="railGroups"
            :active-id="activeTab"
            kicker=""
            title=""
            subtitle=""
            @select="onRailSelect"
        >
            <!-- Custom brand: back link + app name + status badge +
                 origin meta. Replaces the default kicker/title/sub
                 block that doesn't fit an app context. -->
            <template #brand>
                <RouterLink to="/apps" class="rail-back">
                    <IconChevronLeft :size="14" />
                    {{ t('appDetail.nav.back') }}
                </RouterLink>
                <h2 class="rail-app-name">{{ app?.name ?? '…' }}</h2>
                <div class="rail-app-meta">
                    <StatusBadge v-if="app" :status="app.status" />
                </div>
                <div v-if="primaryDomain || app?.repo_url" class="rail-app-sub">
                    <span v-if="primaryDomain" class="rail-app-sub__line mono">{{ primaryDomain.name }}</span>
                    <span v-if="app?.repo_url" class="rail-app-sub__line muted">
                        {{ shortRepo(app.repo_url) }} · {{ app.branch }}
                    </span>
                </div>
            </template>
        </SettingsRail>

        <main class="app-detail__main">
            <!-- Top of the content pane: section header (kicker +
                 title + sub) + global app actions on the right.
                 Deploy / Logs live here (not in the rail) because
                 they apply to the app as a whole, not to a section. -->
            <header class="app-detail__head">
                <div>
                    <div class="app-detail__kicker">{{ t('appDetail.kicker') }}</div>
                    <h1 class="app-detail__title">{{ currentSection.label }}</h1>
                    <p class="app-detail__sub">{{ currentSection.desc }}</p>
                </div>
                <div class="app-detail__actions">
                    <Button :label="t('appDetail.actions.deploy')" icon="pi pi-cloud-upload" @click="deployApp" />
                    <!--
                        Restart sits next to Deploy because it's the
                        other "make this app run" lever. Hidden when
                        the app isn't running (nothing to restart) and
                        when there's a build/deploy in flight (would
                        race with the engine).
                    -->
                    <Button
                        v-if="canRestart"
                        :label="t('appDetail.actions.restart')"
                        icon="pi pi-refresh"
                        severity="secondary"
                        :loading="restarting"
                        @click="restartApp"
                    />
                    <Button text :label="t('appDetail.actions.logs')" icon="pi pi-list" @click="$router.push(`/apps/${id}/logs`)" />
                </div>
            </header>

            <!-- Section body. We keep the PrimeVue Tabs/TabPanels
                 because their conditional-render contract matches
                 our existing markup; switching them out for v-if
                 chains would mean rewriting every panel below.
                 With TabList hidden, only the active panel renders. -->
            <Tabs :value="activeTab" class="app-detail__panels" @update:value="setTab">
                <TabPanels>
                    <TabPanel value="overview">
                    <div class="overview-stack">
                        <!--
                            Hero card — answers the first 3 questions an
                            operator has about an app:
                              1. Is it running / healthy?
                              2. How do I open it?
                              3. What version is live right now?
                            Domain link opens in a new tab; "Vincular
                            domínio" jumps to the Domínios tab when nothing
                            is wired up yet.
                        -->
                        <section class="hero">
                            <div class="hero__main">
                                <div class="hero__status">
                                    <StatusBadge v-if="app" :status="app.status" />
                                </div>
                                <a
                                    v-if="primaryDomain"
                                    :href="primaryDomainURL"
                                    target="_blank"
                                    rel="noopener"
                                    class="hero__domain mono"
                                >
                                    {{ primaryDomain.name }}
                                    <i class="pi pi-external-link" aria-hidden="true" />
                                </a>
                                <div v-else class="hero__no-domain">
                                    <span>{{ t('appDetail.hero.noDomain') }}</span>
                                    <button type="button" class="link-button" @click="setTab('domains')">
                                        {{ t('appDetail.hero.linkDomain') }}
                                    </button>
                                </div>
                                <p class="hero__deploy-line">
                                    <template v-if="latestDeployment">
                                        {{ t('appDetail.hero.lastDeploy') }}
                                        <strong>{{ formatRelative(latestDeployment.finished_at ?? latestDeployment.started_at ?? latestDeployment.created_at) }}</strong>
                                        <template v-if="latestDeployment.commit_sha">
                                            · <span class="mono">{{ latestDeployment.commit_sha.slice(0, 7) }}</span>
                                        </template>
                                        <template v-if="latestDeployment.commit_msg">
                                            · {{ truncate(latestDeployment.commit_msg, 80) }}
                                        </template>
                                    </template>
                                    <template v-else>
                                        {{ t('appDetail.hero.neverDeployed') }}
                                    </template>
                                </p>
                            </div>
                            <a
                                v-if="primaryDomain"
                                :href="primaryDomainURL"
                                target="_blank"
                                rel="noopener"
                                class="p-button"
                            >
                                <i class="pi pi-external-link" aria-hidden="true" />
                                <span>{{ t('appDetail.hero.open') }}</span>
                            </a>
                        </section>

                        <!--
                            Compact activity feed: top 5 deploys with a
                            link to the full history. Mirrors the
                            "Activity" panel in Heroku / Vercel — the
                            operator usually only wants to know "what
                            happened recently?" not "list me everything".
                        -->
                        <section class="block">
                            <header class="block__head">
                                <h2 class="block__title">{{ t('appDetail.activity.title') }}</h2>
                                <button type="button" class="link-button" @click="setTab('deployments')">
                                    {{ t('appDetail.activity.viewAll') }}
                                </button>
                            </header>
                            <ul v-if="recentDeployments.length" class="activity">
                                <RouterLink
                                    v-for="d in recentDeployments"
                                    :key="d.id"
                                    :to="`/apps/${id}/deployments/${d.id}`"
                                    class="activity__row activity__row--link"
                                    custom
                                    v-slot="{ navigate, href }"
                                >
                                    <li
                                        class="activity__row activity__row--link"
                                        @click="navigate"
                                        :data-href="href"
                                    >
                                        <span :class="['activity__dot', `is-${d.status}`]" aria-hidden="true" />
                                        <div class="activity__main">
                                            <div class="activity__top">
                                                <span class="mono activity__sha">
                                                    {{ d.commit_sha?.slice(0, 7) ?? d.id.slice(0, 8) }}
                                                </span>
                                                <span class="activity__msg">{{ d.commit_msg || t('appDetail.deployments.deployLabel', { short: d.id.slice(0, 8) }) }}</span>
                                            </div>
                                            <div class="activity__meta">
                                                <StatusBadge :status="d.status" />
                                                <span class="muted">{{ d.branch ?? app?.branch ?? 'main' }}</span>
                                                <span class="muted">{{ formatRelative(d.started_at ?? d.created_at) }}</span>
                                                <span v-if="d.started_at && d.finished_at" class="muted">{{ formatDuration(d.started_at, d.finished_at) }}</span>
                                            </div>
                                        </div>
                                    </li>
                                </RouterLink>
                            </ul>
                            <p v-else class="muted activity__empty">
                                <i18n-t keypath="appDetail.activity.empty" scope="global">
                                    <template #strong><strong>{{ t('appDetail.activity.emptyStrong') }}</strong></template>
                                </i18n-t>
                            </p>
                        </section>

                        <!--
                            Source + Server cards. Lateral pair, gives
                            "what is this app built from?" and "where
                            does it run?" at a glance. Build type is part
                            of "Origem" because it pairs naturally with
                            the repo/image identity.
                        -->
                        <div class="pair-grid">
                            <div class="info-block">
                                <div class="info-key">{{ t('appDetail.info.source') }}</div>
                                <div class="info-val mono">
                                    {{ app?.repo_url ? shortRepo(app.repo_url) : imageLabel }}
                                </div>
                                <div class="info-sub">
                                    <span class="muted">{{ buildTypeLabel }}</span>
                                    <template v-if="app?.repo_url">
                                        <span class="muted"> · {{ t('appDetail.info.branch') }} </span>
                                        <span class="mono">{{ app.branch }}</span>
                                    </template>
                                </div>
                            </div>
                            <div class="info-block">
                                <div class="info-key">{{ t('appDetail.info.server') }}</div>
                                <div class="info-val">{{ serverName || '—' }}</div>
                                <div v-if="serverDockerVersion" class="info-sub muted">
                                    {{ t('appDetail.info.docker', { version: serverDockerVersion }) }}
                                </div>
                            </div>
                        </div>

                        <!--
                            Recursos — live CPU + Memory readings for
                            the running container. Empty/disabled when
                            the app isn't running (no container to
                            sample). Polling is set up in script (5s
                            cadence), backend latency is ~1s per call
                            because Docker stats stream needs two
                            samples to derive CPU%.
                        -->
                        <section class="block resources">
                            <header class="block__head">
                                <h2 class="block__title">{{ t('appDetail.resources.title') }}</h2>
                                <span v-if="stats" class="muted resources__updated">
                                    {{ t('appDetail.resources.updated', { when: formatRelative(statsUpdatedAt) }) }}
                                </span>
                            </header>

                            <div v-if="!canShowStats" class="resources__empty muted">
                                {{ t('appDetail.resources.disabledByStatus') }}
                            </div>

                            <div v-else-if="!stats && statsLoading" class="resources__empty muted">
                                {{ t('appDetail.resources.sampling') }}
                            </div>

                            <div v-else-if="stats" class="resources__grid">
                                <div class="resource-tile">
                                    <div class="resource-tile__label">{{ t('appDetail.resources.cpu') }}</div>
                                    <div class="resource-tile__value">
                                        {{ stats.cpu_percent.toFixed(1) }}<span class="unit">%</span>
                                    </div>
                                    <div class="resource-bar">
                                        <div
                                            class="resource-bar__fill"
                                            :class="cpuToneClass"
                                            :style="{ width: cpuBarWidth }"
                                        />
                                    </div>
                                </div>
                                <div class="resource-tile">
                                    <div class="resource-tile__label">{{ t('appDetail.resources.memory') }}</div>
                                    <div class="resource-tile__value">
                                        {{ formatBytes(stats.memory_used_bytes) }}
                                        <span v-if="stats.memory_limit_bytes > 0" class="unit"> / {{ formatBytes(stats.memory_limit_bytes) }}</span>
                                        <span v-else class="unit"> / {{ t('appDetail.resources.noLimit') }}</span>
                                    </div>
                                    <div class="resource-bar">
                                        <div
                                            class="resource-bar__fill"
                                            :class="memToneClass"
                                            :style="{ width: memBarWidth }"
                                        />
                                    </div>
                                </div>
                            </div>

                            <div v-else class="resources__empty muted">
                                {{ t('appDetail.resources.unavailable') }}
                            </div>
                        </section>
                    </div>
                </TabPanel>

                <TabPanel value="containers">
                    <!--
                        Per-service container view. Live containers
                        (Docker label `prexel.app_id`) merged with
                        Compose YAML preview. Operator binds domains
                        per service from here; logs/terminal buttons
                        emit events for the parent to route (logs is
                        a separate page; terminal is a modal).

                        After the container-detail page landed, rows
                        navigate to /apps/:id/containers/:name on click
                        — no events to route here anymore. The old
                        terminal-in-modal still works as a fallback
                        kept below for any other entry points.
                    -->
                    <ContainersTable :app-id="id" />
                </TabPanel>

                <TabPanel value="logs">
                    <!--
                        Aggregated runtime logs across every running
                        container of the app. Each container gets a
                        toggle chip + a stable colour so the operator
                        can follow which line came from where. Built
                        on top of the per-container SSE endpoint —
                        one stream per container.
                    -->
                    <AppLogsTab v-if="activeTab === 'logs'" :app-id="id" />
                </TabPanel>

                <TabPanel value="deployments">
                    <div class="deployment-list pv-panel">
                        <RouterLink
                            v-for="d in deployments"
                            :key="d.id"
                            :to="`/apps/${id}/deployments/${d.id}`"
                            class="deployment-row deployment-row--link"
                        >
                            <div class="deployment-row__rail">
                                <span :class="['deployment-row__dot', `is-${d.status}`]" />
                            </div>
                            <div class="deployment-row__main">
                                <div class="deployment-row__top">
                                    <strong>{{ d.commit_msg || t('appDetail.deployments.deployLabel', { short: d.id.slice(0, 8) }) }}</strong>
                                    <StatusBadge :status="d.status" />
                                </div>
                                <div class="deployment-row__meta">
                                    <span class="mono">{{ d.commit_sha?.slice(0, 7) ?? d.id.slice(0, 8) }}</span>
                                    <span>{{ d.branch ?? app?.branch ?? 'main' }}</span>
                                    <span>{{ formatRelative(d.started_at ?? d.created_at) }}</span>
                                    <span v-if="d.started_at && d.finished_at">{{ formatDuration(d.started_at, d.finished_at) }}</span>
                                </div>
                            </div>
                            <span class="deployment-row__chevron" aria-hidden="true">
                                <i class="pi pi-chevron-right" />
                            </span>
                        </RouterLink>
                        <p v-if="deployments.length === 0" class="muted center">{{ t('appDetail.deployments.empty') }}</p>
                    </div>
                </TabPanel>

                <TabPanel value="environment">
                    <!--
                        Full env editor: env vars (PUT full document)
                        + secrets (per-key upsert/delete). Replaces
                        the previous read-only table — operator now
                        configures everything pre-deploy.
                    -->
                    <EnvEditor :app="app" @app-updated="reloadApp" />
                </TabPanel>

                <TabPanel value="storage">
                    <!--
                        Per-app persistent volumes (named or bind mounts).
                        Backend table: app_volumes. CRUD only — actual
                        Docker volume create/destroy happens on deploy.
                    -->
                    <StorageTab :app="app" />
                </TabPanel>

                <TabPanel value="domains">
                    <!--
                        Roteamento consolidado — substituiu o antigo
                        "Domínios" só-binding. Mostra todos os domínios
                        do app numa tabela (com/sem service), permite
                        edit inline via dialog + primary toggle + remove.
                        O dialog é o mesmo que a tab Containers usa,
                        modo "service" forçado quando o operador clica
                        do botão atalho de uma row de container.
                    -->
                    <RoutingTab :app="app" />
                </TabPanel>

                <TabPanel value="settings">
                    <!--
                        Settings covers every PATCH-able field on the App
                        row (identity, hooks, restart/auto-deploy, limits,
                        docker labels, tags) plus the legacy single-
                        container source/build inputs. Extracted to its own
                        component to keep this view under the 1.5k-line
                        ceiling and to let the editor hydrate cleanly when
                        the operator navigates between apps.

                        `remove-app` bubbles up because the danger dialog
                        + delete API live here next to the routing /
                        navigation logic the rest of the page already owns.
                    -->
                    <SettingsTab
                        :app="app"
                        @app-updated="reloadApp"
                        @remove-app="removeApp"
                    />
                </TabPanel>
                </TabPanels>
            </Tabs>
        </main>

        <Dialog
            v-model:visible="linkDialogOpen"
            modal
            :header="t('apps.detail.domains.link_dialog.title')"
            :style="{ width: '480px' }"
        >
            <div class="link-dialog">
                <p class="muted">{{ t('apps.detail.domains.link_dialog.sub') }}</p>

                <SField :label="t('apps.detail.domains.link_dialog.select_label')">
                    <Select
                        v-model="linkSelectedId"
                        :options="availableDomains"
                        option-label="name"
                        option-value="id"
                        :placeholder="t('apps.detail.domains.link_dialog.select_placeholder')"
                        :filter="availableDomains.length > 5"
                        :disabled="availableDomains.length === 0"
                        class="link-dialog__select"
                    >
                        <template #option="slotProps">
                            <span class="mono">{{ slotProps.option.name }}</span>
                        </template>
                        <template #value="slotProps">
                            <span v-if="slotProps.value" class="mono">{{ selectedHostname }}</span>
                            <span v-else>{{ slotProps.placeholder }}</span>
                        </template>
                    </Select>
                </SField>

                <p v-if="availableDomains.length === 0" class="muted link-dialog__empty">
                    {{ t('apps.detail.domains.link_dialog.no_options') }}.
                    <RouterLink :to="newDomainHref" class="link-dialog__new">
                        {{ t('apps.detail.domains.link_dialog.create_new') }} →
                    </RouterLink>
                </p>

                <label v-else class="toggle-row">
                    <ToggleSwitch v-model="linkSetPrimary" />
                    <span>{{ t('apps.detail.domains.link_dialog.primary_toggle') }}</span>
                </label>
            </div>
            <template #footer>
                <Button
                    text
                    severity="secondary"
                    :label="$t('common.cancel')"
                    :disabled="linking"
                    @click="linkDialogOpen = false"
                />
                <Button
                    :label="t('apps.detail.domains.link_dialog.submit')"
                    :loading="linking"
                    :disabled="!linkSelectedId"
                    @click="submitLink"
                />
            </template>
        </Dialog>

        <Dialog v-model:visible="removeOpen" modal :header="t('appDetail.remove.header')" :style="{ width: '480px' }">
            <div class="danger-dialog">
                <div class="danger-dialog__icon"><i class="pi pi-exclamation-triangle" /></div>
                <div>
                    <strong>{{ t('appDetail.remove.confirmTitle', { name: app?.name ?? '' }) }}</strong>
                    <p>{{ t('appDetail.remove.confirmBody') }}</p>
                    <ul class="danger-dialog__list">
                        <li>{{ t('appDetail.remove.list.containers') }}</li>
                        <li>{{ t('appDetail.remove.list.images') }}</li>
                        <li>{{ t('appDetail.remove.list.volumes') }}</li>
                        <li>{{ t('appDetail.remove.list.routes') }}</li>
                        <li>{{ t('appDetail.remove.list.logs') }}</li>
                    </ul>
                    <p class="danger-dialog__note">
                        <i18n-t keypath="appDetail.remove.note" scope="global">
                            <template #strong><strong>{{ t('appDetail.remove.noteStrong') }}</strong></template>
                        </i18n-t>
                    </p>
                </div>
            </div>
            <template #footer>
                <Button :label="t('common.cancel')" text severity="secondary" @click="removeOpen = false" />
                <Button :label="t('apps.remove')" severity="danger" :loading="removing" @click="removeAppConfirmed" />
            </template>
        </Dialog>

        <!--
            Terminal modal — opened from the Containers tab via the
            "Terminal" button. Big dialog (80vw × 70vh) because
            xterm is unusable in a tiny window; closable by Esc or
            the header X. We unmount the component on close (v-if)
            so the WebSocket actually tears down — leaving xterm
            mounted but the Dialog hidden would keep a dead session
            open until the next navigation.
        -->
        <Dialog
            v-model:visible="terminalOpen"
            modal
            :header="terminalContainer ? t('appDetail.terminal.headerWith', { service: terminalContainer.service || terminalContainer.container_name }) : t('appDetail.terminal.headerFallback')"
            :style="{ width: '80vw', height: '70vh' }"
            :contentStyle="{ display: 'flex', flexDirection: 'column', padding: 0 }"
            :dismissableMask="false"
        >
            <ContainerTerminal
                v-if="terminalOpen && terminalContainer?.container_name"
                :app-id="id"
                :container-name="terminalContainer.container_name"
                class="terminal-modal__body"
                @closed="terminalOpen = false"
            />
        </Dialog>
    </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import Button from 'primevue/button'
import Tabs from 'primevue/tabs'
import TabPanels from 'primevue/tabpanels'
import TabPanel from 'primevue/tabpanel'
import Dialog from 'primevue/dialog'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import StatusBadge from '@/components/StatusBadge.vue'
import SslBadge from '@/components/SslBadge.vue'
import SField from '@/components/settings/SField.vue'
import SettingsRail, { type RailGroup, type RailItem } from '@/components/settings/SettingsRail.vue'
import AppLogsTab from '@/components/apps/AppLogsTab.vue'
import ContainersTable from '@/components/apps/ContainersTable.vue'
import ContainerTerminal from '@/components/apps/ContainerTerminal.vue'
import EnvEditor from '@/components/apps/EnvEditor.vue'
import RoutingTab from '@/components/apps/RoutingTab.vue'
import SettingsTab from '@/components/apps/SettingsTab.vue'
import StorageTab from '@/components/apps/StorageTab.vue'
import IconChevronLeft from '@/components/icons/IconChevronLeft.vue'
import IconBox from '@/components/icons/IconBox.vue'
import IconClock from '@/components/icons/IconClock.vue'
import IconKey from '@/components/icons/IconKey.vue'
import IconDatabase from '@/components/icons/IconDatabase.vue'
import IconGlobe from '@/components/icons/IconGlobe.vue'
import IconList from '@/components/icons/IconList.vue'
import IconServer from '@/components/icons/IconServer.vue'
import IconSettings from '@/components/icons/IconSettings.vue'
import { useAppsStore } from '@/stores/apps'
import { useDomainsStore } from '@/stores/domains'
import { useInstanceStore } from '@/stores/instance'
import { useServersStore } from '@/stores/servers'
import { formatRelative } from '@/utils/format'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { App, AppContainer, AppStats, Deployment, Domain } from '@/types/api'

const route = useRoute()
const router = useRouter()
const appsStore = useAppsStore()
const domainsStore = useDomainsStore()
const instanceStore = useInstanceStore()
const serversStore = useServersStore()
const { t } = useI18n()
const confirm = useConfirm()

const id = computed(() => route.params.id as string)
const app = ref<App | null>(null)
const deployments = ref<Deployment[]>([])

// Compose apps need different framing in places (the overview's
// stats card stays empty until per-service runner metrics ship, for
// example), so we keep the predicate here even though the Settings
// tab now owns the per-field hide logic itself.
const isComposeApp = computed(() => app.value?.build_type === 'docker_compose')

const removeOpen = ref(false)
const removing = ref(false)

// ─── Section / tab state ───
//
// The 5 sections rendered in the rail. Order here drives rail order;
// `id` doubles as the `?tab=` value so deep links keep working.
type SectionId = 'overview' | 'containers' | 'logs' | 'deployments' | 'environment' | 'storage' | 'domains' | 'settings'
type TabValue = SectionId

const SECTIONS = computed<Array<{ id: SectionId; label: string; desc: string; icon: unknown }>>(() => [
    { id: 'overview',    label: t('appDetail.sections.overview.label'),    desc: t('appDetail.sections.overview.desc'),    icon: IconBox },
    { id: 'containers',  label: t('appDetail.sections.containers.label'),  desc: t('appDetail.sections.containers.desc'),  icon: IconServer },
    { id: 'logs',        label: t('appDetail.sections.logs.label'),        desc: t('appDetail.sections.logs.desc'),        icon: IconList },
    { id: 'deployments', label: t('appDetail.sections.deployments.label'), desc: t('appDetail.sections.deployments.desc'), icon: IconClock },
    { id: 'environment', label: t('appDetail.sections.environment.label'), desc: t('appDetail.sections.environment.desc'), icon: IconKey },
    { id: 'storage',     label: t('appDetail.sections.storage.label'),     desc: t('appDetail.sections.storage.desc'),     icon: IconDatabase },
    { id: 'domains',     label: t('appDetail.sections.domains.label'),     desc: t('appDetail.sections.domains.desc'),     icon: IconGlobe },
    { id: 'settings',    label: t('appDetail.sections.settings.label'),    desc: t('appDetail.sections.settings.desc'),    icon: IconSettings },
])
const TAB_IDS: readonly SectionId[] = ['overview', 'containers', 'logs', 'deployments', 'environment', 'storage', 'domains', 'settings']

const activeTab = computed<TabValue>(() => {
    const raw = String(route.query.tab ?? 'overview')
    return ((TAB_IDS as readonly string[]).includes(raw) ? raw : 'overview') as TabValue
})

const currentSection = computed(() =>
    SECTIONS.value.find((s) => s.id === activeTab.value) ?? SECTIONS.value[0]!,
)

const railGroups = computed<RailGroup[]>(() => [
    {
        // No visible group label — the rail's `#brand` slot already
        // carries the app identity, an extra "App" header here would
        // be redundant.
        label: '',
        items: SECTIONS.value.map((s) => ({
            id: s.id,
            label: s.label,
            desc: s.desc,
            icon: s.icon as never,
        })),
    },
])

function setTab(value: string | number | undefined) {
    const next = String(value ?? 'overview')
    if (next === activeTab.value) return
    router.replace({ query: { ...route.query, tab: next === 'overview' ? undefined : next } })
}

// SettingsRail emits `select` with the full RailItem when a row is
// clicked. We only care about the id (== tab value).
function onRailSelect(item: RailItem) {
    setTab(item.id)
}

const linkDialogOpen = ref(false)
const linkSelectedId = ref<string | null>(null)
const linkSetPrimary = ref(false)
const linking = ref(false)
const primaryBusyId = ref<string | null>(null)
const unbindBusyId = ref<string | null>(null)

const linkedDomains = computed<Domain[]>(() =>
    domainsStore.domains
        .filter((d) => d.app_id === id.value)
        .slice()
        .sort((a, b) => {
            if (a.is_primary !== b.is_primary) return a.is_primary ? -1 : 1
            return a.name.localeCompare(b.name)
        }),
)

// The panel's instance_url, if any. The panel is a binding target just
// like an app — we exclude it from the "available to link" list so the
// operator can't accidentally rebind the panel's own hostname to one
// of their apps. (Detaching the panel from this domain is a separate
// action that lives in Settings → Instância.)
const instanceHostname = computed<string | null>(() => {
    const raw = instanceStore.settings?.instance_url
    if (!raw) return null
    try {
        return new URL(raw).hostname.toLowerCase()
    } catch {
        return raw.replace(/^https?:\/\//, '').replace(/\/.*$/, '').toLowerCase() || null
    }
})

const availableDomains = computed<Domain[]>(() =>
    domainsStore.domains
        .filter((d) => !d.app_id && d.name.toLowerCase() !== instanceHostname.value)
        .slice()
        .sort((a, b) => a.name.localeCompare(b.name)),
)

const selectedHostname = computed(() => {
    if (!linkSelectedId.value) return ''
    return domainsStore.byId[linkSelectedId.value]?.name ?? ''
})

const newDomainHref = computed(() => ({ path: '/domains/new', query: { app: id.value } }))

const envEntries = computed(() => Object.entries(app.value?.env_vars ?? {}))
const primaryDomain = computed(() => domainsStore.domains.find((domain) => domain.app_id === id.value && domain.is_primary) ?? null)
const latestDeployment = computed(() => deployments.value[0] ?? null)
const imageLabel = computed(() => {
    if (!app.value) return '—'
    if (app.value.build_type === 'docker_image') return `${app.value.image_name ?? 'docker'}:${app.value.image_tag ?? 'latest'}`
    if (app.value.build_type === 'docker_compose') return app.value.compose_file ?? 'Docker Compose'
    return 'Dockerfile'
})

// ── Overview helpers ──
//
// All derived state the new overview needs lives here so the template
// stays declarative. Lookups are cheap (small lists) so no caching.

// Always HTTPS — the panel only ever issues / serves on TLS, even
// when the cert is self-signed. Falls back to '#' so the <a> tag
// stays valid markup when no domain is bound.
const primaryDomainURL = computed(() =>
    primaryDomain.value ? `https://${primaryDomain.value.name}` : '#',
)

// Top 5 deploys for the activity feed. Sliced inline rather than
// stored separately so we don't have a derived field drifting from
// `deployments.value` when a new deploy lands.
const recentDeployments = computed(() => deployments.value.slice(0, 5))

// Human label for app.build_type. The DB stores enum values like
// `dockerfile` / `docker_image` / `docker_compose`; the overview
// surfaces friendlier names.
const buildTypeLabel = computed(() => {
    switch (app.value?.build_type) {
        case 'docker_compose': return t('apps.row.dockerCompose')
        case 'docker_image':   return t('apps.row.dockerImage')
        case 'dockerfile':     return t('apps.row.dockerfile')
        default: return app.value?.build_type ?? '—'
    }
})

const appServer = computed(() => {
    if (!app.value?.server_id) return null
    return serversStore.servers.find((s) => s.id === app.value!.server_id) ?? null
})
const serverName = computed(() => appServer.value?.name ?? '')
const serverDockerVersion = computed(() => appServer.value?.docker_version ?? '')

const restarting = ref(false)

// Restart sits in the header next to Deploy. We hide it (vs disable)
// when it doesn't make sense — operators don't need to see a greyed
// button on a stopped app.
const canRestart = computed(() => {
    if (!app.value) return false
    return app.value.status === 'running' || app.value.status === 'unreachable'
})

function truncate(s: string, max: number): string {
    if (s.length <= max) return s
    return s.slice(0, max - 1) + '…'
}

// ── Resources (CPU / Memory) ─────────────────────────────────────
//
// Stats are polled while the user is on this view AND the app is
// running. Polling pauses when the tab becomes hidden (default
// `setInterval` behaviour in modern browsers throttles it anyway,
// but we also gate on `canShowStats` to avoid pointless network
// calls). The first call fires immediately on mount; subsequent
// ones at STATS_INTERVAL_MS spacing.
const STATS_INTERVAL_MS = 5000
const stats = ref<AppStats | null>(null)
const statsLoading = ref(false)
const statsUpdatedAt = ref<number>(0)
let statsTimer: ReturnType<typeof setInterval> | null = null

// Operators expect "Recursos" to be empty for apps that aren't up —
// fetching stats on a stopped container always 404s so we don't even
// try. Compose apps don't have a single container yet, so stats also
// stay empty until the runner ships and we get per-service metrics.
const canShowStats = computed(() => {
    if (!app.value || isComposeApp.value) return false
    return app.value.status === 'running' || app.value.status === 'unreachable'
})

async function refreshStats() {
    if (!canShowStats.value) {
        stats.value = null
        return
    }
    statsLoading.value = true
    try {
        const snap = await appsStore.fetchStats(id.value)
        if (snap) {
            stats.value = snap
            statsUpdatedAt.value = Math.floor(Date.now() / 1000)
        }
    } finally {
        statsLoading.value = false
    }
}

function startStatsPolling() {
    stopStatsPolling()
    if (!canShowStats.value) return
    // Fire once immediately so the card never shows the empty state
    // longer than absolutely necessary, then settle into the interval.
    void refreshStats()
    statsTimer = setInterval(() => { void refreshStats() }, STATS_INTERVAL_MS)
}

function stopStatsPolling() {
    if (statsTimer) {
        clearInterval(statsTimer)
        statsTimer = null
    }
}

// React to status flips (e.g. operator stops the app from another
// tab): kick off / tear down the poll loop without waiting for the
// next interval. Cheap because `canShowStats` is cached.
watch(canShowStats, (next) => {
    if (next) startStatsPolling()
    else {
        stopStatsPolling()
        stats.value = null
    }
})

// ── Display helpers for resources ───────────────────────────────
function formatBytes(n: number): string {
    if (!Number.isFinite(n) || n <= 0) return '0 B'
    if (n < 1024) return `${n} B`
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
    if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
    return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}

const cpuBarWidth = computed(() => {
    const p = stats.value?.cpu_percent ?? 0
    return `${Math.min(100, Math.max(0, p)).toFixed(1)}%`
})
const memBarWidth = computed(() => {
    const s = stats.value
    if (!s || s.memory_limit_bytes <= 0) return '0%'
    const pct = (s.memory_used_bytes / s.memory_limit_bytes) * 100
    return `${Math.min(100, Math.max(0, pct)).toFixed(1)}%`
})

// Color the bar by load — green up to 70%, amber up to 90%, red beyond.
// Same thresholds for both CPU and Memory.
function toneFor(percent: number): string {
    if (percent >= 90) return 'is-danger'
    if (percent >= 70) return 'is-warn'
    return 'is-ok'
}
const cpuToneClass = computed(() => toneFor(stats.value?.cpu_percent ?? 0))
const memToneClass = computed(() => {
    const s = stats.value
    if (!s || s.memory_limit_bytes <= 0) return 'is-ok'
    return toneFor((s.memory_used_bytes / s.memory_limit_bytes) * 100)
})

onMounted(async () => {
    await load()
    // Stats poll boots after load() so we know the app's status
    // (canShowStats reads app.value). When the app is stopped/idle,
    // startStatsPolling is a noop — no needless HTTP.
    startStatsPolling()
})

// Cleanup any active poll loop when the route changes or the
// component unmounts. Without this we'd keep firing fetches against
// an app the user just navigated away from.
onUnmounted(() => stopStatsPolling())

async function load() {
    try {
        app.value = await appsStore.fetchOne(id.value)
        const [deploys] = await Promise.all([
            appsStore.listDeployments(id.value),
            domainsStore.fetchAll(),
            // Servers list — Overview shows the server name + docker
            // version this app runs on. Cached aggressively so this is
            // a noop when the operator navigates between apps.
            serversStore.servers.length ? Promise.resolve(null) : serversStore.fetchAll().catch(() => null),
            // Best-effort: needed to exclude the panel's instance domain
            // from the "available to link" list. If settings are already
            // cached the load() is a noop; permission errors are swallowed
            // because they only affect the dropdown filter, never security.
            instanceStore.settings ? Promise.resolve(null) : instanceStore.load().catch(() => null),
        ])
        deployments.value = deploys
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

function formatDuration(start: number, finish: number): string {
    const seconds = Math.max(1, Math.round((finish - start) / 1000))
    if (seconds < 60) return `${seconds}s`
    const minutes = Math.floor(seconds / 60)
    return `${minutes}m ${seconds % 60}s`
}

function shortRepo(url: string): string {
    return url.replace(/^https?:\/\//, '').replace(/\.git$/, '')
}

async function deployApp() {
    try {
        const dep = await appsStore.deploy(id.value)
        notify.success(t('apps.deployStarted'))
        // Navigate straight into the deployment-detail page so the
        // operator can watch the build log streaming. The list of
        // recent deployments will re-populate when they come back via
        // the breadcrumb link.
        await router.push(`/apps/${id.value}/deployments/${dep.id}?watch=1`)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

async function restartApp() {
    restarting.value = true
    try {
        await appsStore.restart(id.value)
        notify.success(t('appDetail.toast.restartSent'))
        // Refetch the app so its `status` reflects the action (likely
        // "deploying" briefly, then "running"). The hero card reads
        // app.status directly so this is what makes the badge update.
        app.value = await appsStore.fetchOne(id.value)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        restarting.value = false
    }
}

// Stop / Rollback intentionally not on the overview — Stop is a rare
// destructive action that belongs in the Deployments tab (future,
// alongside per-version controls); Rollback similarly works best with
// version selection in the deployments list. The overview keeps only
// the two everyday levers: Deploy + Restart (header).

/*
   Container action handlers.

   Logs: forward to /apps/:id/logs?container=<name>. The logs page
   already supports the `?container=` query param (added by the
   logs-per-container agent in parallel); pre-multi-container apps
   ignore it and stream the single container as before.

   Terminal: open the ContainerTerminal modal. Implementation comes
   from the terminal-exec agent; we just wire the open state here.
*/
/*
   The Terminal modal + per-row "view logs" handler used to live
   here. Both moved to the container-detail page
   (/apps/:id/containers/:name) which has dedicated tabs for Logs +
   Terminal. The modal markup below + its open state stay for now
   as a defensive shell — any callers that still emit the old
   events would land here — but in practice ContainersTable no
   longer fires them after the rows-clickable rewrite.
*/
const terminalOpen = ref(false)
const terminalContainer = ref<AppContainer | null>(null)

// Fired by child editors (EnvEditor, etc.) after they mutate the
// app. Refetches so the overview/header/badges reflect the new
// state without making each child duplicate the load logic.
async function reloadApp() {
    try {
        app.value = await appsStore.fetchOne(id.value)
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

function removeApp() {
    removeOpen.value = true
}

async function removeAppConfirmed() {
    removing.value = true
    try {
        await appsStore.remove(id.value)
        removeOpen.value = false
        router.push('/apps')
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        removing.value = false
    }
}

// ─── Domains tab actions ───
function openLinkDialog() {
    linkSelectedId.value = null
    linkSetPrimary.value = linkedDomains.value.length === 0
    linkDialogOpen.value = true
}

async function submitLink() {
    if (!linkSelectedId.value) return
    linking.value = true
    try {
        await domainsStore.update(linkSelectedId.value, {
            app_id: id.value,
            is_primary: linkSetPrimary.value,
        })
        notify.success(t('apps.detail.domains.toast.linked'))
        linkDialogOpen.value = false
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        linking.value = false
    }
}

async function setPrimary(domain: Domain) {
    if (domain.is_primary) return
    primaryBusyId.value = domain.id
    try {
        // The store handles the demote-old-primary mirroring locally, so
        // there's no need to refetch — the sibling's is_primary flips in
        // place and the row order updates via the computed sort.
        await domainsStore.update(domain.id, { is_primary: true })
        notify.success(t('apps.detail.domains.toast.primary_set'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        primaryBusyId.value = null
    }
}

function confirmUnbind(domain: Domain) {
    confirm.require({
        header: t('apps.detail.domains.unbind_confirm.title'),
        message: t('apps.detail.domains.unbind_confirm.body'),
        icon: 'pi pi-exclamation-triangle',
        rejectLabel: t('common.cancel'),
        acceptLabel: t('apps.detail.domains.unbind_confirm.accept'),
        acceptClass: 'p-button-danger',
        accept: () => { void unbindDomain(domain.id) },
    })
}

async function unbindDomain(domainId: string) {
    unbindBusyId.value = domainId
    try {
        await domainsStore.update(domainId, { clear_app_id: true })
        notify.success(t('apps.detail.domains.toast.unbinded'))
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        unbindBusyId.value = null
    }
}
</script>

<style scoped>
/*
   Layout — Settings-style two-column shell. Same negative margins
   trick the SettingsView uses so the rail goes all the way to the
   left edge of the route, ignoring the AppLayout's content padding.
*/
.app-detail {
    display: flex;
    min-height: 100dvh;
    margin: -28px -32px -48px 0;
    width: auto;
}
.app-detail__main {
    flex: 1;
    min-width: 0;
    min-height: 100dvh;
    padding: 32px 40px;
    /*
       Match the rail+main bg so there's no visible "gap" tone
       between cards. Cards inside use the same `var(--p-content-bg)`
       and are separated by border alone — same vocabulary the
       SettingsView uses. Anything that breaks this uniformity (e.g.
       a hero gradient) shows up as a visible bg shift and reads as
       a bug, so we keep it flat.
    */
    background: var(--p-content-bg);
    overflow-x: hidden;
}

/* ── Content header (kicker + section title + global actions) ── */
.app-detail__head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    flex-wrap: wrap;
    margin-bottom: 24px;
}
.app-detail__kicker {
    color: var(--p-text-muted);
    font-size: 11px;
    font-weight: 750;
    letter-spacing: .06em;
    text-transform: uppercase;
    margin-bottom: 4px;
}
.app-detail__title {
    margin: 0;
    color: var(--p-text);
    font-size: 28px;
    font-weight: 750;
    letter-spacing: 0;
}
.app-detail__sub {
    max-width: 760px;
    margin: 6px 0 0;
    color: var(--p-text-muted);
}
.app-detail__actions {
    display: inline-flex;
    gap: 8px;
}
/*
   Tame PrimeVue's <Tabs> container chrome. Aura injects backgrounds
   AND padding on every level (.p-tabs, .p-tabpanels, .p-tabpanel) —
   on a normal tabbed page those create the familiar "card" frame
   around the active panel. We don't want that here: navigation lives
   in the rail, content lives in `.overview-stack` which already has
   its own card chrome per item. Letting Aura's bg through created
   the visible tone-mismatch the operator just flagged.

   Override scope is limited to `.app-detail__panels` so we don't
   accidentally flatten <Tabs> anywhere else in the app.
*/
.app-detail__panels :deep(.p-tablist) {
    display: none; /* navigation lives in the rail */
}
.app-detail__panels,
.app-detail__panels :deep(.p-tabpanels),
.app-detail__panels :deep(.p-tabpanel) {
    background: transparent;
    padding: 0;
    border: 0;
}

/* ── Custom brand block in the rail (#brand slot) ── */
.rail-back {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--p-text-muted);
    text-decoration: none;
    margin-bottom: 12px;
    transition: color 120ms;
}
.rail-back:hover { color: var(--p-text); }
.rail-app-name {
    margin: 0;
    color: var(--p-text);
    font-size: 18px;
    font-weight: 750;
    letter-spacing: -0.01em;
    word-break: break-word;
}
.rail-app-meta {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    margin-top: 6px;
}
.rail-app-sub {
    margin-top: 8px;
    display: flex;
    flex-direction: column;
    gap: 2px;
}
.rail-app-sub__line {
    display: block;
    font-size: 11px;
    line-height: 1.4;
    word-break: break-all;
}
.rail-app-sub__line.mono {
    font-family: ui-monospace, Menlo, monospace;
    color: var(--p-text);
}

@media (max-width: 900px) {
    .app-detail {
        flex-direction: column;
        margin: 0;
        min-height: auto;
    }
    .app-detail__main {
        padding: 24px 0 0;
        background: transparent;
    }
    .app-detail__head { flex-direction: column; align-items: stretch; }
    .app-detail__actions { justify-content: flex-end; }
}
.overview-stack {
    display: grid;
    gap: 16px;
}

/* ── Hero card ── */
.hero {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
    border: 1px solid var(--p-content-border);
    border-radius: 14px;
    background:
        linear-gradient(135deg, rgba(16, 185, 129, 0.10), transparent 44%),
        var(--p-content-bg);
    padding: 22px 24px;
}
.hero__main {
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
}
.hero__status {
    display: inline-flex;
    align-items: center;
}
.hero__domain {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font-size: 20px;
    font-weight: 700;
    color: var(--p-text);
    text-decoration: none;
    word-break: break-all;
}
.hero__domain:hover { color: var(--p-primary-500); }
.hero__domain i { font-size: 13px; opacity: 0.6; }
.hero__no-domain {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    color: var(--p-text);
    font-size: 16px;
}
.hero__deploy-line {
    margin: 0;
    color: var(--p-text-muted);
    font-size: 13px;
    word-break: break-word;
}
.hero__deploy-line strong { color: var(--p-text); }

/* ── Section block (Atividade / Ações) ── */
.block {
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    background: var(--p-content-bg);
    padding: 16px 18px;
}
.block__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 10px;
}
.block__title {
    margin: 0;
    color: var(--p-text);
    font-size: 13px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
}

/* ── Activity feed (compact recent deploys) ── */
.activity {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
}
.activity__row {
    display: grid;
    grid-template-columns: 14px 1fr;
    gap: 12px;
    padding: 10px 0;
    border-bottom: 1px solid var(--p-divider);
}
.activity__row:last-child { border-bottom: 0; }
.activity__dot {
    width: 8px;
    height: 8px;
    border-radius: 999px;
    background: var(--p-text-muted);
    margin-top: 7px;
    justify-self: center;
}
.activity__dot.is-success { background: var(--p-success, #10b981); }
.activity__dot.is-failed,
.activity__dot.is-error { background: var(--p-danger, #dc2626); }
.activity__dot.is-building,
.activity__dot.is-deploying,
.activity__dot.is-pending { background: var(--p-warning, #d97706); }
.activity__main { min-width: 0; }
.activity__top {
    display: inline-flex;
    align-items: baseline;
    gap: 10px;
    min-width: 0;
}
.activity__sha {
    color: var(--p-text-muted);
    font-size: 11px;
}
.activity__msg {
    color: var(--p-text);
    font-size: 13px;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.activity__meta {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    margin-top: 4px;
    font-size: 12px;
}
.activity__empty { padding: 8px 0; }

/* ── Origin + Server pair ── */
.pair-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
}
@media (max-width: 720px) {
    .pair-grid { grid-template-columns: 1fr; }
}
.info-block {
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    background: var(--p-content-bg);
    padding: 14px 16px;
}

/* ── Recursos (CPU + Memória) ── */
.resources__updated { font-size: 11px; }
.resources__empty {
    padding: 12px 0 4px;
    font-size: 13px;
}
.resources__grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 18px;
}
@media (max-width: 640px) {
    .resources__grid { grid-template-columns: 1fr; }
}
.resource-tile {
    display: flex;
    flex-direction: column;
    gap: 6px;
}
.resource-tile__label {
    font-size: 11px;
    color: var(--p-text-muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-weight: 600;
}
.resource-tile__value {
    font-size: 18px;
    font-weight: 600;
    color: var(--p-text);
}
.resource-tile__value .unit {
    font-size: 13px;
    color: var(--p-text-muted);
    font-weight: 400;
}
.resource-bar {
    height: 6px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 85%);
    overflow: hidden;
}
.resource-bar__fill {
    height: 100%;
    border-radius: 999px;
    transition: width 400ms ease-out, background 200ms;
}
.resource-bar__fill.is-ok     { background: var(--p-success, #10b981); }
.resource-bar__fill.is-warn   { background: var(--p-warning, #d97706); }
.resource-bar__fill.is-danger { background: var(--p-danger,  #dc2626); }

/* ── Misc helpers shared by overview ── */
.link-button {
    background: none;
    border: 0;
    padding: 0;
    color: var(--p-primary-500);
    font: inherit;
    cursor: pointer;
    font-size: 12px;
    font-weight: 600;
}
.link-button:hover { text-decoration: underline; }
.info-key {
    font-size: 11px;
    color: var(--p-text-muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    margin-bottom: 4px;
    font-weight: 600;
}
.info-val { font-size: 14px; color: var(--p-text); }
.info-val code {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 12px;
    padding: 1px 6px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 4px;
}
.info-sub {
    margin-top: 6px;
    color: var(--p-text-muted);
    font-size: 12px;
    line-height: 1.45;
}
.info-sub code {
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
    padding: 0 4px;
    background: color-mix(in srgb, var(--p-text-muted), transparent 88%);
    border-radius: 4px;
}
/* The compose explanation card has more vertical content than its
   single-port siblings — let it occupy the full row so the text
   doesn't get cramped against an empty cell. */
.metric-card--compose {
    grid-column: 1 / -1;
}
.muted { color: var(--p-text-muted); font-size: 13px; }
.center { text-align: center; padding: 24px !important; }
.mono { font-family: ui-monospace, Menlo, monospace; font-size: 12px; }
/* The Settings tab used to live inline here and brought its own
   .actions-row / .form-grid-2 / .compose-note rules with it. Those
   moved into components/apps/SettingsTab.vue along with the markup;
   nothing in this view references them anymore. */

.deployment-list {
    padding: 0;
}
.deployment-row {
    display: grid;
    grid-template-columns: 28px 1fr auto;
    gap: 12px;
    padding: 16px 18px;
    border-bottom: 1px solid var(--p-divider);
    align-items: center;
}
.deployment-row:last-child { border-bottom: 0; }

/* Anchor rows (history list) — make them feel clickable without
   losing the calm density of the panel. */
.deployment-row--link {
    text-decoration: none;
    color: inherit;
    cursor: pointer;
    transition: background 120ms ease;
}
.deployment-row--link:hover {
    background: color-mix(in srgb, var(--p-primary-500), transparent 94%);
}
.deployment-row__chevron {
    color: var(--p-text-muted);
    font-size: 12px;
    opacity: 0;
    transition: opacity 120ms ease, transform 120ms ease;
}
.deployment-row--link:hover .deployment-row__chevron {
    opacity: 1;
    transform: translateX(2px);
}
/* The "recent activity" cards on Overview use this same component
   set — they also benefit from the link affordance. */
.activity__row--link {
    cursor: pointer;
    border-radius: 8px;
    transition: background 120ms ease;
}
.activity__row--link:hover {
    background: color-mix(in srgb, var(--p-primary-500), transparent 94%);
}
.deployment-row__rail {
    display: flex;
    justify-content: center;
    position: relative;
}
.deployment-row__rail::after {
    content: "";
    position: absolute;
    top: 20px;
    bottom: -18px;
    width: 1px;
    background: var(--p-divider);
}
.deployment-row:last-child .deployment-row__rail::after { display: none; }
.deployment-row__dot {
    position: relative;
    z-index: 1;
    width: 10px;
    height: 10px;
    margin-top: 5px;
    border-radius: 999px;
    background: var(--p-text-muted);
    box-shadow: 0 0 0 4px var(--p-content-bg);
}
.deployment-row__dot.is-success { background: var(--p-success); }
.deployment-row__dot.is-failed,
.deployment-row__dot.is-cancelled { background: var(--p-danger); }
.deployment-row__dot.is-building,
.deployment-row__dot.is-deploying,
.deployment-row__dot.is-pending { background: var(--p-warning); }
.deployment-row__top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
}
.deployment-row__top strong {
    color: var(--p-text);
    font-size: 14px;
}
.deployment-row__meta {
    margin-top: 6px;
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    color: var(--p-text-muted);
    font-size: 12px;
}
.danger-dialog {
    display: flex;
    gap: 12px;
    color: var(--p-text);
}

/* Terminal needs to fill the whole Dialog body — xterm sizes itself
   to its container, so we give the wrapper 100% height/width.
   Background is intentionally near-black to match xterm's own
   default theme; otherwise the Dialog's white-ish content-bg
   "frames" the terminal awkwardly. */
.terminal-modal__body {
    flex: 1;
    min-height: 0;
    width: 100%;
    background: #000;
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
    margin: 0 0 8px;
    color: var(--p-text-muted);
    font-size: 13px;
    line-height: 1.5;
}
.danger-dialog__list {
    margin: 6px 0 8px;
    padding-left: 20px;
    color: var(--p-text-muted);
    font-size: 12px;
    line-height: 1.65;
}
.danger-dialog__note {
    font-size: 12px;
    font-style: italic;
}
:deep(.p-inputtext) { width: 100%; margin-bottom: 8px; }

/* ─── Domains tab ─── */
.domains-empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    padding: 32px 24px;
    text-align: center;
}
.domains-empty__title {
    margin: 0;
    color: var(--p-text);
    font-size: 15px;
    font-weight: 600;
}
.domains-empty__body {
    margin: 0 0 8px;
    max-width: 440px;
}
.domains-empty__new {
    margin-top: 6px;
    font-size: 13px;
    color: var(--p-primary-500);
    text-decoration: none;
}
.domains-empty__new:hover { text-decoration: underline; }

.domains-panel { padding: 0; }
.domains-panel__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 16px 20px;
    border-bottom: 1px solid var(--p-divider);
}
.domains-panel__title {
    margin: 0;
    font-size: 13px;
    font-weight: 600;
    color: var(--p-text);
}
.domains-list {
    list-style: none;
    margin: 0;
    padding: 0;
}
.domain-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
    padding: 14px 20px;
    border-bottom: 1px solid var(--p-divider);
}
.domain-row:last-child { border-bottom: 0; }
.domain-row__main {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
    flex: 1 1 280px;
}
.domain-row__name {
    color: var(--p-primary-500);
    text-decoration: none;
    font-size: 14px;
    word-break: break-all;
}
.domain-row__name:hover { text-decoration: underline; }
.domain-row__badges {
    display: inline-flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
}
.domain-row__actions {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    flex-shrink: 0;
}

.pill {
    padding: 1px 8px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
}
.pill--success {
    background: rgba(16, 185, 129, 0.12);
    color: var(--p-primary-700);
}

/* ─── Link dialog ─── */
.link-dialog {
    display: flex;
    flex-direction: column;
    gap: 12px;
}
.link-dialog__select { width: 100%; }
.link-dialog__empty {
    font-size: 12px;
}
.link-dialog__new {
    color: var(--p-primary-500);
    text-decoration: none;
}
.link-dialog__new:hover { text-decoration: underline; }
.toggle-row {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    font-size: 13px;
    color: var(--p-text);
    cursor: pointer;
}

@media (max-width: 760px) {
    .info-grid { grid-template-columns: 1fr; }
    .production-card { flex-direction: column; }
    .domain-row__actions { width: 100%; justify-content: flex-end; }
}
</style>
