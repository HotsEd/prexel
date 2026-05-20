<template>
    <IconBase v-bind="$attrs">
        <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
        <circle cx="12" cy="7" r="4" />
    </IconBase>
</template>
<script setup lang="ts">
import IconBase from './IconBase.vue'
defineOptions({ inheritAttrs: false })
</script>
