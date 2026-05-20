<template>
    <aside :class="['pv-sidebar', sidebar.isOpen.value && 'pv-sidebar--open']">
        <div class="pv-sidebar-logo">
            <PrexelMark :size="36" />
        </div>

        <SidebarLink to="/apps" :title="t('nav.apps')"><IconBox :size="20" :stroke-width="1.8" /></SidebarLink>
        <SidebarLink to="/git" :title="t('nav.git')"><IconGitBranch :size="20" :stroke-width="1.8" /></SidebarLink>
        <SidebarLink to="/servers" :title="t('nav.servers')"><IconServer :size="20" :stroke-width="1.8" /></SidebarLink>
        <SidebarLink to="/domains" :title="t('nav.domains')"><IconGlobe :size="20" :stroke-width="1.8" /></SidebarLink>

        <div class="pv-sidebar-spacer" />

        <button
            type="button"
            class="pv-sidebar-item pv-sidebar-search"
            :title="t('commandPalette.triggerLabel')"
            @click="palette.open"
        >
            <IconSearch :size="20" :stroke-width="1.8" />
        </button>

        <SidebarLink to="/settings" :title="t('nav.settings')"><IconSettings :size="20" :stroke-width="1.8" /></SidebarLink>

        <button
            type="button"
            :class="['pv-sidebar-avatar', settingsActive && 'is-active']"
            :title="auth.user?.email ?? ''"
            @click="toggleMenu"
        >
            {{ initials }}
        </button>

        <Popover ref="userMenu" :pt="popoverPt">
            <UserMenu
                :name="auth.user?.email ?? 'admin'"
                :email="auth.user?.email ?? ''"
                :initials="initials"
                @navigate="goTo"
                @logout="handleLogout"
            />
        </Popover>
    </aside>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Popover from 'primevue/popover'
import { useAuthStore } from '@/stores/auth'
import { getInitials } from '@/utils/format'
import PrexelMark from '@/components/PrexelMark.vue'
import SidebarLink from '@/components/layout/SidebarLink.vue'
import UserMenu from '@/components/layout/UserMenu.vue'
import IconBox from '@/components/icons/IconBox.vue'
import IconGitBranch from '@/components/icons/IconGitBranch.vue'
import IconServer from '@/components/icons/IconServer.vue'
import IconGlobe from '@/components/icons/IconGlobe.vue'
import IconSearch from '@/components/icons/IconSearch.vue'
import IconSettings from '@/components/icons/IconSettings.vue'
import { useSidebar } from '@/composables/useSidebar'
import { useCommandPalette } from '@/composables/useCommandPalette'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const sidebar = useSidebar()
const palette = useCommandPalette()
const { t } = useI18n()

const userMenu = ref<InstanceType<typeof Popover> | null>(null)
const settingsActive = computed(() => route.path.startsWith('/settings'))

const initials = computed(() => getInitials(auth.user?.email ?? 'P'))

const popoverPt = {
    root: { style: 'padding: 0; border-radius: 12px; overflow: hidden;' },
    content: { style: 'padding: 0;' },
}

function toggleMenu(event: Event) {
    userMenu.value?.toggle(event)
}

function goTo(path: string) {
    userMenu.value?.hide()
    router.push(path)
}

async function handleLogout() {
    userMenu.value?.hide()
    await auth.logout()
    router.push({ name: 'login' })
}
</script>
