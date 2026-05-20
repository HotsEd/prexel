<template>
    <div :class="['ui-avatar', sizeClass, square && 'is-square']" :title="name">
        <img
            v-if="src"
            :src="src"
            :alt="name"
            loading="lazy"
            referrerpolicy="no-referrer"
            class="ui-avatar__img"
        />
        <slot v-else>{{ initials }}</slot>
    </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { getInitials } from '@/utils/format'

const props = withDefaults(defineProps<{
    name?: string
    src?: string | null
    size?: 'sm' | 'md' | 'lg' | 'xl'
    square?: boolean
}>(), { name: '', src: null, size: 'md', square: false })

const sizeClass = computed(() => `ui-avatar--${props.size}`)
const initials = computed(() => getInitials(props.name ?? ''))
</script>

<style scoped>
.ui-avatar {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 999px;
    background: linear-gradient(135deg, var(--prexel-deploy-500), var(--prexel-network-500));
    color: #fff;
    font-weight: 600;
    flex-shrink: 0;
    font-family: inherit;
    overflow: hidden;
}
.ui-avatar.is-square { border-radius: 8px; }
.ui-avatar__img { width: 100%; height: 100%; object-fit: cover; display: block; }
.ui-avatar--sm { width: 28px; height: 28px; font-size: 11px; }
.ui-avatar--md { width: 36px; height: 36px; font-size: 12px; }
.ui-avatar--lg { width: 44px; height: 44px; font-size: 14px; }
.ui-avatar--xl { width: 64px; height: 64px; font-size: 18px; font-weight: 700; }
</style>
