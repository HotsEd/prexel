<template>
    <IconBase v-bind="$attrs">
        <rect x="9" y="2" width="6" height="6" rx="1" />
        <rect x="3" y="16" width="6" height="6" rx="1" />
        <rect x="15" y="16" width="6" height="6" rx="1" />
        <path d="M12 8v4" />
        <path d="M6 16v-2h12v2" />
    </IconBase>
</template>
<script setup lang="ts">
import IconBase from './IconBase.vue'
defineOptions({ inheritAttrs: false })
</script>
