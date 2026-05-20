<template>
    <div :class="['otp', state === 'error' && 'otp--error', state === 'success' && 'otp--success']">
        <input
            v-for="(_, i) in length"
            :key="i"
            :ref="(el) => setRef(el, i)"
            type="text"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="1"
            :disabled="disabled"
            :value="digits[i]"
            class="otp__cell"
            @input="onInput(i, $event)"
            @keydown="onKey(i, $event)"
            @paste="onPaste"
            @focus="($event.target as HTMLInputElement).select()"
        />
    </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'

const props = withDefaults(defineProps<{
    modelValue: string
    length?: number
    disabled?: boolean
    state?: 'idle' | 'success' | 'error'
}>(), { length: 6, disabled: false, state: 'idle' })

const emit = defineEmits<{
    'update:modelValue': [v: string]
    complete: [v: string]
}>()

const cells = ref<HTMLInputElement[]>([])

const digits = computed(() => {
    const padded = props.modelValue.padEnd(props.length, ' ').slice(0, props.length)
    return padded.split('').map((c) => (c === ' ' ? '' : c))
})

function setRef(el: unknown, i: number) {
    if (el) cells.value[i] = el as HTMLInputElement
}

function emitValue(arr: string[]) {
    const joined = arr.join('').replace(/ +$/, '')
    emit('update:modelValue', joined)
    if (joined.length === props.length && !joined.includes(' ')) {
        emit('complete', joined)
    }
}

function onInput(i: number, e: Event) {
    const value = (e.target as HTMLInputElement).value.replace(/\D/g, '').slice(0, 1)
    const arr = digits.value.slice()
    arr[i] = value
    emitValue(arr)
    if (value && i < props.length - 1) {
        nextTick(() => cells.value[i + 1]?.focus())
    }
}

function onKey(i: number, e: KeyboardEvent) {
    if (e.key === 'Backspace' && !digits.value[i] && i > 0) {
        nextTick(() => cells.value[i - 1]?.focus())
    }
    if (e.key === 'ArrowLeft' && i > 0) cells.value[i - 1]?.focus()
    if (e.key === 'ArrowRight' && i < props.length - 1) cells.value[i + 1]?.focus()
}

function onPaste(e: ClipboardEvent) {
    e.preventDefault()
    const text = e.clipboardData?.getData('text') ?? ''
    const cleaned = text.replace(/\D/g, '').slice(0, props.length)
    const arr = cleaned.padEnd(props.length, ' ').split('').map((c) => (c === ' ' ? '' : c))
    emitValue(arr)
    const focusAt = Math.min(cleaned.length, props.length - 1)
    nextTick(() => cells.value[focusAt]?.focus())
}

watch(() => props.state, (s) => {
    if (s === 'error') {
        nextTick(() => cells.value[0]?.focus())
    }
})
</script>

<style scoped>
.otp { display: flex; gap: 10px; }

.otp__cell {
    width: 52px;
    height: 64px;
    border-radius: 10px;
    background: var(--p-content-bg);
    border: 1.5px solid var(--p-input-border);
    text-align: center;
    font: inherit;
    font-family: ui-monospace, Menlo, monospace;
    font-size: 22px;
    font-weight: 600;
    color: var(--p-text);
    outline: none;
    transition: border-color 120ms, background 120ms;
}
.otp__cell:focus {
    border-color: var(--p-primary-500);
    box-shadow: 0 0 0 3px rgba(16, 185, 129, 0.16);
}
.otp__cell:not(:placeholder-shown) {
    border-color: var(--p-primary-500);
}
.otp--success .otp__cell {
    background: rgba(34, 197, 94, 0.08);
    border-color: #22c55e;
    color: #16a34a;
}
.otp--error .otp__cell {
    border-color: #dc2626;
    color: #dc2626;
}
@media (max-width: 480px) {
    .otp { gap: 6px; }
    .otp__cell { width: 44px; height: 56px; font-size: 20px; }
}
</style>
