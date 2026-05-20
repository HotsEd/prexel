<template>
    <div class="user-menu">
        <header class="user-menu__header">
            <UiAvatar :name="name" size="lg" />
            <div class="user-menu__id">
                <div class="user-menu__name">{{ name }}</div>
                <div class="user-menu__email">{{ email }}</div>
            </div>
        </header>

        <div class="user-menu__group">
            <button class="user-menu__item" type="button" @click="emit('navigate', '/settings')">
                <IconSettings :size="15" :stroke-width="1.75" />
                {{ t('nav.settings') }}
            </button>
        </div>

        <div class="user-menu__group user-menu__group--footer">
            <button class="user-menu__item is-danger" type="button" @click="emit('logout')">
                <IconLogout :size="15" :stroke-width="1.75" />
                {{ t('userMenu.logout') }}
            </button>
        </div>
    </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import UiAvatar from '@/components/ui/UiAvatar.vue'
import IconSettings from '@/components/icons/IconSettings.vue'
import IconLogout from '@/components/icons/IconLogout.vue'

defineProps<{
    name: string
    email: string
    initials: string
}>()
const emit = defineEmits<{ navigate: [path: string]; logout: [] }>()
const { t } = useI18n()
</script>

<style scoped>
.user-menu { width: 260px; }
.user-menu__header {
    padding: 16px 16px 12px;
    border-bottom: 1px solid var(--p-divider);
    display: flex;
    align-items: center;
    gap: 12px;
}
.user-menu__id { min-width: 0; }
.user-menu__name { font-size: 13px; font-weight: 600; color: var(--p-text); }
.user-menu__email {
    font-size: 11px;
    color: var(--p-text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.user-menu__group { padding: 6px; }
.user-menu__group--footer { border-top: 1px solid var(--p-divider); }
.user-menu__item {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 8px 10px;
    border: 0;
    background: transparent;
    color: var(--p-text);
    font-size: 13px;
    border-radius: 8px;
    cursor: pointer;
    text-align: left;
    font-family: inherit;
    transition: background 120ms;
}
.user-menu__item:hover { background: rgba(16, 185, 129, 0.08); }
.user-menu__item.is-danger { color: #dc2626; }
</style>
