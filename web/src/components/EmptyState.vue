<template>
    <div class="empty">
        <div v-if="$slots.icon" class="empty__icon"><slot name="icon" /></div>
        <div class="empty__title">{{ title }}</div>
        <p v-if="body" class="empty__body">{{ body }}</p>
        <div v-if="$slots.actions" class="empty__actions"><slot name="actions" /></div>
    </div>
</template>

<script setup lang="ts">
defineProps<{ title: string; body?: string }>()
</script>

<style scoped>
.empty {
    padding: 56px 24px;
    text-align: center;
    border: 1px dashed var(--p-content-border);
    border-radius: 14px;
    background: var(--p-content-bg);
}
.empty__icon {
    width: 56px;
    height: 56px;
    margin: 0 auto 16px;
    border-radius: 14px;
    background: rgba(16, 185, 129, 0.12);
    color: var(--p-primary-500);
    display: inline-flex;
    align-items: center;
    justify-content: center;
}
.empty__title { font-size: 16px; font-weight: 600; color: var(--p-text); }
.empty__body { font-size: 13px; color: var(--p-text-muted); margin: 6px 0 18px; }
.empty__actions { display: flex; justify-content: center; gap: 8px; }
</style>
