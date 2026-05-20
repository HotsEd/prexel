<template>
    <div :class="['s-field', `s-field--${w}`, error && 's-field--invalid']">
        <label v-if="label" class="s-field__label">
            {{ label }}
            <span v-if="required" class="s-field__required">*</span>
        </label>
        <slot />
        <div v-if="error" class="s-field__error">{{ error }}</div>
        <div v-else-if="hint" class="s-field__hint">{{ hint }}</div>
    </div>
</template>

<script setup lang="ts">
withDefaults(defineProps<{
    label?: string
    hint?: string
    w?: 'auto' | 'full' | 'sm'
    required?: boolean
    error?: string
}>(), { w: 'full' })
</script>

<style scoped>
.s-field { display: flex; flex-direction: column; }
.s-field--full { width: 100%; }
.s-field--auto { width: auto; }
/* `--sm` is for compact inputs (small numeric, etc.). The field itself
   stays full-width so the label and hint can breathe; only the slotted
   control is capped. Before this fix the whole container was 120px wide,
   which made the label "Max concurrent deploys" wrap to two lines and
   turned the explanatory hint into a 5-line vertical strip. */
.s-field--sm { width: 100%; }
.s-field--sm :deep(.p-inputnumber),
.s-field--sm :deep(.p-inputnumber-input),
.s-field--sm :deep(.p-inputtext),
.s-field--sm :deep(input[type="number"]),
.s-field--sm :deep(input[type="text"]) {
    max-width: 200px;
}

.s-field__label {
    font-size: 12px;
    font-weight: 600;
    color: var(--p-text-subtle);
    display: block;
    margin-bottom: 6px;
}
.s-field__required { color: #dc2626; }
.s-field__hint { font-size: 11px; color: var(--p-text-muted); margin-top: 4px; }
.s-field__error { font-size: 11px; color: #dc2626; margin-top: 4px; }
.s-field--invalid :deep(.p-inputtext),
.s-field--invalid :deep(input),
.s-field--invalid :deep(select),
.s-field--invalid :deep(textarea) { border-color: #dc2626 !important; }
</style>
