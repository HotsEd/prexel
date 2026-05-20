<template>
    <IconBase :size="size" :stroke-width="strokeWidth">
        <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z" />
        <circle cx="12" cy="12" r="3" />
    </IconBase>
</template>

<script setup lang="ts">
import IconBase from './IconBase.vue'

withDefaults(defineProps<{ size?: number | string; strokeWidth?: number | string }>(), {
    size: 16,
    strokeWidth: 2,
})
</script>
