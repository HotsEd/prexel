<template>
    <!--
        Prexel mark — the canonical brand symbol.

        It's a "status pulse": a deep-teal rounded tile, an emerald dot at
        center, and two faint concentric rings radiating outward. Direct
        abstraction of the green "ready / running / deployed" dot that
        sits next to every status indicator in the product — the brand
        symbol IS the product's most-seen element, scaled up.

        Source: Claude Design handoff (prexel-mark.svg). The tile and
        ring colours are part of the mark (NOT general design tokens) so
        they stay hardcoded here — operators choosing a different theme
        for the dashboard shouldn't drift the brand mark.

        Sizing: pass `size` in CSS px (square). Default 36 matches the
        sidebar slot. The SVG itself is viewBox-based and scales without
        loss to any size, including 16px (favicon-class) where the rings
        visually fade — they're rendered at ~14%/32% opacity by design.

        Set `pulse` to true to use the gentle animated pulse on the
        rings — reserved for "alive" moments (login splash, "deploying
        now" indicator). Default is off; the static mark is the right
        choice for sidebars / topbars / chrome.
    -->
    <svg
        xmlns="http://www.w3.org/2000/svg"
        :width="size"
        :height="size"
        viewBox="0 0 64 64"
        role="img"
        :aria-label="label"
        :class="['prexel-mark', pulse && 'prexel-mark--pulse']"
    >
        <title>{{ label }}</title>
        <rect x="6" y="6" width="52" height="52" rx="14" fill="#063D3A" />
        <circle
            class="prexel-mark__ring prexel-mark__ring--outer"
            cx="32" cy="32" r="22"
            fill="none" stroke="#16C784" stroke-opacity="0.14" stroke-width="2"
        />
        <circle
            class="prexel-mark__ring prexel-mark__ring--inner"
            cx="32" cy="32" r="16"
            fill="none" stroke="#16C784" stroke-opacity="0.32" stroke-width="2"
        />
        <circle cx="32" cy="32" r="10" fill="#16C784" />
    </svg>
</template>

<script setup lang="ts">
withDefaults(defineProps<{
    /** Square size in CSS px. */
    size?: number
    /** Accessible label. Defaults to the product name. */
    label?: string
    /** Animate the rings with a gentle outward pulse (1.8s cycle). */
    pulse?: boolean
}>(), {
    size: 36,
    label: 'Prexel',
    pulse: false,
})
</script>

<style scoped>
.prexel-mark {
    display: inline-block;
    flex-shrink: 0;
    /* Crisp tile edges at small sizes. */
    shape-rendering: geometricPrecision;
}

/*
   Pulse: scale each ring outward subtly while easing the opacity to
   near-zero, then snap back. The two rings are offset by half the
   cycle so there's always one in flight. 1.8s matches the spec
   ("deploy pulse" rhythm) and prefers-reduced-motion disables it.
*/
.prexel-mark--pulse .prexel-mark__ring {
    transform-origin: 32px 32px;
    transform-box: fill-box;
    animation: prexel-mark-pulse 1.8s ease-in-out infinite;
}
.prexel-mark--pulse .prexel-mark__ring--inner {
    animation-delay: 0.9s;
}

@keyframes prexel-mark-pulse {
    0%   { transform: scale(1);    opacity: 1; }
    70%  { transform: scale(1.18); opacity: 0; }
    100% { transform: scale(1);    opacity: 0; }
}

@media (prefers-reduced-motion: reduce) {
    .prexel-mark--pulse .prexel-mark__ring {
        animation: none;
    }
}
</style>
