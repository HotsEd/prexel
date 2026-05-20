<template>
    <div class="access-section">
        <div class="access-tabs" role="tablist" :aria-label="t('settings.access.aria')">
            <button
                v-for="tab in tabs"
                :key="tab.id"
                type="button"
                class="access-tab"
                :class="activeTab === tab.id && 'is-active'"
                role="tab"
                :aria-selected="activeTab === tab.id"
                @click="setTab(tab.id)"
            >
                <span class="access-tab__label">{{ t(tab.label) }}</span>
                <span class="access-tab__desc">{{ t(tab.desc) }}</span>
            </button>
        </div>

        <component :is="activeComponent" :key="activeTab" />
    </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import MembersSection from '@/pages/settings/sections/MembersSection.vue'
import RolesSection from '@/pages/settings/sections/RolesSection.vue'
import TeamsSection from '@/pages/settings/sections/TeamsSection.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()

const tabs = [
    { id: 'members', label: 'settings.access.tabs.members.label', desc: 'settings.access.tabs.members.desc' },
    { id: 'teams', label: 'settings.access.tabs.teams.label', desc: 'settings.access.tabs.teams.desc' },
    { id: 'permissions', label: 'settings.access.tabs.permissions.label', desc: 'settings.access.tabs.permissions.desc' },
] as const

type AccessTab = typeof tabs[number]['id']

const components = {
    members: MembersSection,
    teams: TeamsSection,
    permissions: RolesSection,
} as const

const activeTab = computed<AccessTab>(() => {
    const tab = String(route.query.tab ?? 'members')
    return tabs.some((t) => t.id === tab) ? tab as AccessTab : 'members'
})

const activeComponent = computed(() => components[activeTab.value])

function setTab(tab: AccessTab) {
    router.replace({ path: '/settings/access', query: { ...route.query, tab } })
}
</script>

<style scoped>
.access-section {
    display: flex;
    flex-direction: column;
    gap: 24px;
}
.access-tabs {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
    padding: 8px;
    border: 1px solid var(--p-divider);
    border-radius: 10px;
    background: var(--p-bg);
}
.access-tab {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 2px;
    min-width: 0;
    border: 0;
    border-radius: 8px;
    padding: 14px 16px;
    background: transparent;
    color: var(--p-text-muted);
    cursor: pointer;
    font: inherit;
    text-align: left;
}
.access-tab:hover {
    background: var(--p-hover);
    color: var(--p-text);
}
.access-tab.is-active {
    background: var(--p-content-bg);
    box-shadow: inset 0 0 0 1px var(--p-divider);
    color: var(--p-text);
}
.access-tab__label {
    font-size: 13px;
    font-weight: 750;
}
.access-tab__desc {
    font-size: 11px;
    color: var(--p-text-muted);
}
@media (max-width: 760px) {
    .access-tabs {
        grid-template-columns: 1fr;
    }
}
</style>
