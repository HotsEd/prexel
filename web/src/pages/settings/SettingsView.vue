<template>
    <div class="settings-layout">
        <SettingsRail
            :groups="groups"
            :active-id="activeSection"
            :kicker="t('settings.rail.kicker')"
            :title="t('settings.rail.title')"
            :subtitle="t('settings.rail.subtitle')"
            @select="selectItem"
        />

        <main class="settings-main">
            <header class="settings-header">
                <div class="settings-header__crumb">{{ t('settings.title') }}</div>
                <h1>{{ currentTitle }}</h1>
                <p>{{ currentSubtitle }}</p>
            </header>

            <div class="settings-main__body" :class="activeSection === 'access' && 'settings-main__body--wide'">
                <component :is="activeComponent" :key="activeSection" />
            </div>
        </main>
    </div>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import SettingsRail, { type RailGroup, type RailItem } from '@/components/settings/SettingsRail.vue'
import IconGlobe from '@/components/icons/IconGlobe.vue'
import IconKey from '@/components/icons/IconKey.vue'
import IconLock from '@/components/icons/IconLock.vue'
import IconSettings from '@/components/icons/IconSettings.vue'
import IconUser from '@/components/icons/IconUser.vue'
import AccessSection from '@/pages/settings/sections/AccessSection.vue'
import ApiTokensSection from '@/pages/settings/sections/ApiTokensSection.vue'
import ProfileSection from '@/pages/settings/sections/ProfileSection.vue'
import SecuritySection from '@/pages/settings/sections/SecuritySection.vue'
import InstanceSection from '@/pages/settings/sections/InstanceSection.vue'
import LanguageSection from '@/pages/settings/sections/LanguageSection.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const sectionComponents = {
    profile: ProfileSection,
    security: SecuritySection,
    'api-tokens': ApiTokensSection,
    language: LanguageSection,
    access: AccessSection,
    instance: InstanceSection,
} as const

type SettingsSection = keyof typeof sectionComponents
const sectionIds = Object.keys(sectionComponents) as SettingsSection[]

const activeSection = computed<SettingsSection>(() => {
    const section = String(route.params.section ?? 'profile')
    return sectionIds.includes(section as SettingsSection) ? section as SettingsSection : 'profile'
})

/*
    Operations (Git / Servers / Domains) used to be a fourth group here as
    shortcuts to the top-level pages. They were removed because the sidebar
    already exposes those routes and duplicating them inside Settings made
    the rail noisy without adding value — Settings is for *configuration*,
    not navigation.
*/
const groups = computed<RailGroup[]>(() => [
    {
        label: t('settings.sections.account'),
        items: [
            item('profile', IconUser),
            item('security', IconLock),
            item('api-tokens', IconKey),
            item('language', IconGlobe),
        ],
    },
    {
        label: t('settings.sections.instance'),
        items: [
            item('instance', IconSettings),
        ],
    },
    {
        label: t('settings.sections.workspace'),
        items: [
            item('access', IconUser),
        ],
    },
])

const activeComponent = computed(() => sectionComponents[activeSection.value])
const currentTitle = computed(() => t(`settings.items.${activeSection.value}.label`))
const currentSubtitle = computed(() => t(`settings.items.${activeSection.value}.desc`))

function item(id: string, icon: RailItem['icon'], route?: string): RailItem {
    return {
        id,
        icon,
        route,
        label: t(`settings.items.${id}.label`),
        desc: t(`settings.items.${id}.desc`),
    }
}

function selectItem(item: RailItem) {
    if (item.route) {
        router.push(item.route)
        return
    }
    router.push(`/settings/${item.id}`)
}

watch(() => route.params.section, (section) => {
    if (!section) {
        router.replace('/settings/profile')
        return
    }
    const legacyTabs: Record<string, string> = {
        members: 'members',
        teams: 'teams',
        roles: 'permissions',
    }
    const legacyTab = legacyTabs[String(section)]
    if (legacyTab) {
        router.replace({ path: '/settings/access', query: { tab: legacyTab } })
        return
    }
    if (!sectionIds.includes(String(section) as SettingsSection)) {
        router.replace('/settings/profile')
    }
}, { immediate: true })
</script>

<style scoped>
.settings-layout {
    display: flex;
    min-height: 100dvh;
    margin: -28px -32px -48px 0;
}
.settings-main {
    flex: 1;
    min-width: 0;
    min-height: 100dvh;
    padding: 32px 40px;
    background: var(--p-content-bg);
    overflow-x: hidden;
}
.settings-header {
    margin-bottom: 24px;
}
.settings-header__crumb {
    margin-bottom: 4px;
    color: var(--p-text-muted);
    font-size: 11px;
    font-weight: 750;
    letter-spacing: .06em;
    text-transform: uppercase;
}
.settings-header h1 {
    margin: 0;
    color: var(--p-text);
    font-size: 28px;
    font-weight: 750;
    letter-spacing: 0;
}
.settings-header p {
    max-width: 760px;
    margin: 6px 0 0;
    color: var(--p-text-muted);
}
.settings-main__body {
    max-width: 920px;
}
.settings-main__body--wide {
    max-width: none;
    width: 100%;
}
@media (max-width: 900px) {
    .settings-layout {
        flex-direction: column;
        margin: 0;
        min-height: auto;
    }
    .settings-main {
        padding: 24px 0 0;
        background: transparent;
    }
    .settings-main__body {
        max-width: none;
    }
}
</style>
