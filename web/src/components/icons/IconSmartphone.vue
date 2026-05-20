<template>
    <IconBase v-bind="$attrs">
        <rect x="5" y="2" width="14" height="20" rx="2" ry="2" />
        <line x1="12" y1="18" x2="12.01" y2="18" />
    </IconBase>
</template>
<script setup lang="ts">
import IconBase from './IconBase.vue'
defineOptions({ inheritAttrs: false })
</script>
