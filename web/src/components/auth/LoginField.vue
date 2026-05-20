<template>
    <div>
        <div class="input-wrap">
            <span class="input-icon">
                <component :is="iconComponent" :size="16" :stroke-width="2" />
            </span>
            <slot />
        </div>
        <p v-if="error" class="login-field__error">{{ error }}</p>
    </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import IconMail from '@/components/icons/IconMail.vue'
import IconLock from '@/components/icons/IconLock.vue'

const props = defineProps<{ icon: 'mail' | 'lock'; error?: string }>()
const iconComponent = computed(() => (props.icon === 'mail' ? IconMail : IconLock))
</script>

<style scoped>
.login-field__error {
    font-size: 12px;
    color: var(--p-danger-text);
    margin: 4px 0 0;
}
</style>
