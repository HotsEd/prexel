<template>
    <IconBase v-bind="$attrs">
        <!-- Stylised key glyph used by the API Tokens rail item. Lines
             tuned to match the rest of the icon set's stroke weight. -->
        <circle cx="8" cy="15" r="4" />
        <path d="M10.85 12.15 21 2" />
        <path d="m18 5 3 3" />
        <path d="m15 8 3 3" />
    </IconBase>
</template>
<script setup lang="ts">
import IconBase from './IconBase.vue'
defineOptions({ inheritAttrs: false })
</script>
