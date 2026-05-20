<template>
    <section class="pv-panel s-card">
        <header v-if="title || sub || $slots.action" class="s-card__head">
            <div class="s-card__heading">
                <div v-if="title" class="s-card__title">{{ title }}</div>
                <div v-if="sub" class="s-card__sub">{{ sub }}</div>
            </div>
            <slot name="action" />
        </header>
        <slot />
    </section>
</template>

<script setup lang="ts">
defineProps<{ title?: string; sub?: string }>()
</script>

<style scoped>
.s-card { padding: 24px; margin-bottom: 16px; }
.s-card__head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    margin-bottom: 18px;
    gap: 16px;
    flex-wrap: wrap;
}
.s-card__title { font-size: 15px; font-weight: 600; color: var(--p-text); }
.s-card__sub { font-size: 12px; color: var(--p-text-muted); margin-top: 3px; line-height: 1.5; }
</style>
