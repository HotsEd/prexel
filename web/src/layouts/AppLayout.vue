<template>
    <div :class="['app-shell', sidebar.isOpen.value && 'app-shell--sidebar-open']">
        <button
            type="button"
            class="app-shell__menu-btn"
            aria-label="Abrir menu"
            @click="sidebar.toggle"
        >
            <IconMenu :size="20" :stroke-width="2" />
        </button>

        <div
            class="app-shell__backdrop"
            :aria-hidden="!sidebar.isOpen.value"
            @click="sidebar.close"
        />

        <AppSidebar />
        <main class="pv-content">
            <RouterView />
        </main>

        <CommandPalette />
    </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppSidebar from '@/components/layout/AppSidebar.vue'
import CommandPalette from '@/components/command/CommandPalette.vue'
import IconMenu from '@/components/icons/IconMenu.vue'
import { useSidebar } from '@/composables/useSidebar'
import { useCommandPalette } from '@/composables/useCommandPalette'

const sidebar = useSidebar()
const palette = useCommandPalette()
const route = useRoute()

watch(() => route.fullPath, () => sidebar.close())

function onKeydown(e: KeyboardEvent) {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        palette.toggle()
    }
}
onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => window.removeEventListener('keydown', onKeydown))
</script>

<style scoped>
.app-shell {
    min-height: 100vh;
    background: var(--p-bg);
}

.app-shell__menu-btn {
    display: none;
    position: fixed;
    top: 16px;
    left: 16px;
    z-index: 30;
    width: 40px;
    height: 40px;
    border-radius: 10px;
    border: 1px solid var(--p-content-border);
    background: var(--p-content-bg);
    color: var(--p-text);
    align-items: center;
    justify-content: center;
    cursor: pointer;
    box-shadow: 0 4px 12px -4px rgba(0, 0, 0, 0.08);
    font-family: inherit;
    transition: opacity 200ms ease;
}
.app-shell__menu-btn:hover { background: var(--p-hover); }

.app-shell__backdrop {
    display: none;
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.35);
    opacity: 0;
    transition: opacity 200ms ease;
    z-index: 15;
    pointer-events: none;
}

@media (max-width: 900px) {
    .app-shell__menu-btn { display: inline-flex; }
    .app-shell__backdrop { display: block; }
    .app-shell--sidebar-open .app-shell__menu-btn {
        opacity: 0;
        pointer-events: none;
    }
    .app-shell--sidebar-open .app-shell__backdrop {
        opacity: 1;
        pointer-events: auto;
    }
}
</style>
