<template>
    <IconBase v-bind="$attrs">
        <polyline points="3 6 5 6 21 6" />
        <path d="M19 6l-1.5 14a2 2 0 0 1-2 1.85H8.5a2 2 0 0 1-2-1.85L5 6" />
        <path d="M10 11v6" />
        <path d="M14 11v6" />
    </IconBase>
</template>
<script setup lang="ts">
import IconBase from './IconBase.vue'
defineOptions({ inheritAttrs: false })
</script>
