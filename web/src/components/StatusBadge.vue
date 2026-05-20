<template>
    <span :class="['p-tag', tagClass]">
        <span class="p-tag-dot" />
        {{ label }}
    </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AppStatus, ServerStatus, DeploymentStatus } from '@/types/api'

const props = defineProps<{
    status: AppStatus | ServerStatus | DeploymentStatus | string
    kind?: 'app' | 'server' | 'deployment'
}>()

const { t } = useI18n()

const TONE_MAP: Record<string, string> = {
    running: 'success',
    success: 'success',
    connected: 'success',
    active: 'success',

    building: 'warn',
    deploying: 'warn',
    pending: 'warn',
    issuing: 'warn',
    // Container-specific transitional states share the warn tone
    // with deploying/building — same colour, same semantic ("we're
    // working on it, don't panic if it's not green yet").
    restarting: 'warn',
    created: 'warn',

    stopped: 'neutral',
    idle: 'neutral',
    cancelled: 'neutral',
    unknown: 'neutral',
    // Docker's "exited" is the same operator-meaning as "stopped":
    // the container is not running, nothing is on fire — go look at
    // the exit code if you care.
    exited: 'neutral',
    paused: 'neutral',
    // Preview rows are placeholders from the Compose YAML — no
    // runtime state to colour. Plain neutral pill.
    preview: 'neutral',

    error: 'danger',
    failed: 'danger',
    unreachable: 'danger',
    disconnected: 'danger',
}

const tagClass = computed(() => {
    const tone = TONE_MAP[props.status] ?? 'neutral'
    return `p-tag-${tone}`
})

const label = computed(() => {
    const key = `status.${props.status}`
    const out = t(key)
    return out === key ? props.status : out
})
</script>
