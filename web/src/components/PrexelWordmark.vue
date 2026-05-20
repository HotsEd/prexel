<template>
    <!--
        Prexel wordmark — the mark + the lowercase "prexel" lockup.

        The second `e` is rendered in emerald with a small emerald dot
        above it — same status-pulse motif as the mark itself, scaled
        down so it reads as a typographic accent. Use this on the
        login splash, setup wizard header, README hero, and anywhere
        the brand needs to "introduce itself" rather than just appear
        as chrome (where the bare mark is right).

        Variants:
          - `tone="dark"` (default): light text — for placement on the
            navy / dark surface. Wordmark text is `#F8FAFC`.
          - `tone="light"`: dark text — for light surfaces. Wordmark
            text is `#0B1726`.

        Sizing is via `size` (px height of the SVG). The intrinsic
        viewBox is 320×80 so width scales proportionally — at size=40
        you get ~160×40 on screen. Wrap in a flex container if you
        need precise placement.
    -->
    <svg
        xmlns="http://www.w3.org/2000/svg"
        :height="size"
        viewBox="0 0 320 80"
        role="img"
        :aria-label="label"
        class="prexel-wordmark"
    >
        <title>{{ label }}</title>
        <rect x="8" y="16" width="48" height="48" rx="11" fill="#063D3A" />
        <circle cx="32" cy="40" r="20" fill="none" stroke="#16C784" stroke-opacity="0.14" stroke-width="2" />
        <circle cx="32" cy="40" r="14" fill="none" stroke="#16C784" stroke-opacity="0.32" stroke-width="2" />
        <circle cx="32" cy="40" r="9" fill="#16C784" />
        <text
            x="76" y="54"
            font-family="'Manrope', system-ui, sans-serif"
            font-size="44"
            font-weight="700"
            letter-spacing="-0.045em"
            :fill="textFill"
        >prex<tspan fill="#16C784">e</tspan>l</text>
        <circle cx="226" cy="22" r="3.5" fill="#16C784" />
    </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
    /** Rendered height in CSS px. Width scales to keep the 4:1 ratio. */
    size?: number
    /** Surface tone the wordmark sits on. Affects text colour only. */
    tone?: 'dark' | 'light'
    /** Accessible label. */
    label?: string
}>(), {
    size: 40,
    tone: 'dark',
    label: 'Prexel',
})

const textFill = computed(() => (props.tone === 'light' ? '#0B1726' : '#F8FAFC'))
</script>

<style scoped>
.prexel-wordmark {
    display: inline-block;
    flex-shrink: 0;
    shape-rendering: geometricPrecision;
}
</style>
