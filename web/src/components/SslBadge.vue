<template>
    <span :class="['p-tag', tagClass]">
        <span class="p-tag-dot" />
        {{ label }}
    </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SSLStatus } from '@/types/api'

const props = defineProps<{ status: SSLStatus }>()
const { t } = useI18n()

const TONE: Record<SSLStatus, string> = {
    none: 'neutral',
    pending: 'warn',
    issuing: 'warn',
    active: 'success',
    failed: 'danger',
    expired: 'danger',
}

const tagClass = computed(() => `p-tag-${TONE[props.status] ?? 'neutral'}`)
const label = computed(() => t(`ssl.${props.status}`))
</script>
