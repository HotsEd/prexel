<template>
    <IconBase v-bind="$attrs">
        <circle cx="12" cy="12" r="10" />
        <polyline points="12 6 12 12 16 14" />
    </IconBase>
</template>
<script setup lang="ts">
import IconBase from './IconBase.vue'
defineOptions({ inheritAttrs: false })
</script>
