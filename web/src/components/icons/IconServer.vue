<template>
    <IconBase v-bind="$attrs">
        <rect x="2" y="3" width="20" height="8" rx="2" />
        <rect x="2" y="13" width="20" height="8" rx="2" />
        <line x1="6" y1="7" x2="6.01" y2="7" />
        <line x1="6" y1="17" x2="6.01" y2="17" />
    </IconBase>
</template>
<script setup lang="ts">
import IconBase from './IconBase.vue'
defineOptions({ inheritAttrs: false })
</script>
