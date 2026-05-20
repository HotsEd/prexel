<template>
    <Select
        :input-id="inputId"
        v-model="innerValue"
        :options="normalizedOptions"
        option-label="label"
        option-value="value"
        option-disabled="disabled"
        :placeholder="placeholder"
        :disabled="disabled"
        :editable="editable"
        :filter="filter"
        :show-clear="clearable"
        :class="['ui-select', invalid && 'ui-select--invalid']"
        overlay-class="ui-select-overlay"
        @change="onChange"
        @blur="emit('blur', $event)"
    >
        <template v-if="$slots.option" #option="slotProps">
            <slot name="option" v-bind="slotProps" />
        </template>
        <template v-if="$slots.value" #value="slotProps">
            <slot name="value" v-bind="slotProps" />
        </template>
    </Select>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Select from 'primevue/select'

type UiSelectValue = string | number | null
export type UiSelectOption =
    | string
    | number
    | {
        label: string
        value: UiSelectValue
        disabled?: boolean
        [key: string]: unknown
    }

const props = withDefaults(defineProps<{
    modelValue: UiSelectValue | undefined
    options: UiSelectOption[]
    inputId?: string
    placeholder?: string
    disabled?: boolean
    invalid?: boolean
    editable?: boolean
    filter?: boolean
    clearable?: boolean
}>(), {
    placeholder: '',
    inputId: undefined,
    disabled: false,
    invalid: false,
    editable: false,
    filter: false,
    clearable: false,
})

const emit = defineEmits<{
    'update:modelValue': [value: UiSelectValue]
    change: [event: { originalEvent?: Event; value: UiSelectValue }]
    blur: [event: FocusEvent]
}>()

const normalizedOptions = computed(() => props.options.map((option) => {
    if (typeof option === 'object') return option
    return { label: String(option), value: option }
}))

const innerValue = computed<UiSelectValue | undefined>({
    get: () => props.modelValue,
    set: (value) => emit('update:modelValue', value ?? null),
})

function onChange(event: { originalEvent?: Event; value: UiSelectValue }) {
    emit('update:modelValue', event.value ?? null)
    emit('change', event)
}
</script>

<style scoped>
.ui-select {
    display: flex;
    width: 100%;
    box-sizing: border-box;
    border: 1px solid var(--p-input-border);
    border-radius: var(--p-form-field-border-radius);
    background: var(--p-input-bg);
    color: var(--p-text);
    transition: border-color 120ms ease, box-shadow 120ms ease, background 120ms ease;
}
.ui-select:hover {
    border-color: var(--p-inputtext-hover-border-color, var(--p-input-border));
}
.ui-select.p-focus,
.ui-select:focus-within {
    border-color: var(--p-primary-color);
    box-shadow: var(--p-focus-ring);
}
.ui-select :deep(.p-select-label) {
    display: flex;
    align-items: center;
    flex: 1;
    width: 100%;
    border: 0;
    background: transparent;
    color: var(--p-text);
    padding: var(--p-form-field-padding-y) var(--p-form-field-padding-x);
    font-size: var(--p-form-field-font-size);
    line-height: 1.5;
}
.ui-select :deep(.p-select-label.p-placeholder) {
    color: var(--p-inputtext-placeholder-color, var(--p-text-muted));
}
.ui-select :deep(.p-select-dropdown) {
    color: var(--p-text-muted);
}
.ui-select :deep(.p-select-clear-icon) {
    color: var(--p-text-muted);
    right: 36px;
}
.ui-select :deep(.p-select-clear-icon:hover) {
    color: var(--p-text);
}
.ui-select--invalid {
    border-color: var(--p-danger) !important;
}

</style>
