<template>
    <RouterLink :to="to" custom v-slot="{ isActive, navigate }">
        <button
            v-tooltip.right="title"
            type="button"
            :class="['pv-sidebar-item', isActive && 'active']"
            :title="title"
            @click="navigate"
        >
            <slot />
            <span v-if="badge !== undefined" class="pv-badge">{{ badge }}</span>
        </button>
    </RouterLink>
</template>

<script setup lang="ts">
defineProps<{
    to: string
    title: string
    badge?: number | string
}>()
</script>
