<template>
    <div class="auth-shell">
        <div class="auth-shell__bg" aria-hidden="true" />
        <div class="auth-shell__dots" aria-hidden="true" />

        <div class="auth-shell__center">
            <div class="auth-shell__card-wrap" :style="{ maxWidth: `${width}px` }">
                <div class="auth-shell__card">
                    <slot />
                </div>

                <div v-if="trust" class="auth-shell__trust">
                    <span><IconShield :size="13" /> {{ t('authShell.trust.tls') }}</span>
                    <span><IconCheck :size="13" /> {{ t('authShell.trust.soc2') }}</span>
                    <span><IconCheck :size="13" /> {{ t('authShell.trust.lgpd') }}</span>
                </div>

                <div class="auth-shell__foot">
                    © {{ new Date().getFullYear() }} Prexel ·
                    <a href="#">{{ t('common.terms') }}</a> ·
                    <a href="#">{{ t('common.privacy') }}</a>
                </div>
            </div>
        </div>
    </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import IconShield from '@/components/icons/IconShield.vue'
import IconCheck from '@/components/icons/IconCheck.vue'

const { t } = useI18n()

withDefaults(defineProps<{
    width?: number
    trust?: boolean
}>(), { width: 460, trust: true })
</script>

<style scoped>
.auth-shell {
    min-height: 100vh;
    padding: 0;
    background: var(--p-bg);
    position: relative;
    overflow: hidden;
    display: flex;
    flex-direction: column;
}

.auth-shell__bg,
.auth-shell__dots {
    position: absolute;
    inset: 0;
    pointer-events: none;
}

.auth-shell__bg {
    background:
        radial-gradient(ellipse 800px 500px at 50% -10%, rgba(16, 185, 129, 0.12), transparent 60%),
        radial-gradient(ellipse 600px 400px at 50% 110%, rgba(16, 185, 129, 0.08), transparent 60%);
}
.auth-shell__dots {
    opacity: 0.4;
    background-image: radial-gradient(circle, rgba(16, 185, 129, 0.12) 1px, transparent 1.2px);
    background-size: 24px 24px;
    mask-image: radial-gradient(ellipse 600px 500px at 50% 50%, black, transparent 75%);
    -webkit-mask-image: radial-gradient(ellipse 600px 500px at 50% 50%, black, transparent 75%);
}

.auth-shell__center {
    flex: 1;
    position: relative;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding: 56px 24px 80px;
}
.auth-shell__card-wrap { width: 100%; }
.auth-shell__card {
    background: var(--p-content-bg);
    border: 1px solid var(--p-content-border);
    border-radius: 16px;
    padding: 36px 36px 28px;
    box-shadow:
        0 24px 60px -20px rgba(20, 17, 42, 0.18),
        0 4px 12px -4px rgba(20, 17, 42, 0.08);
}
html.dark .auth-shell__card {
    box-shadow:
        0 24px 60px -20px rgba(0, 0, 0, 0.55),
        0 4px 12px -4px rgba(0, 0, 0, 0.35);
}

.auth-shell__trust {
    display: flex;
    justify-content: center;
    gap: 22px;
    flex-wrap: wrap;
    margin-top: 22px;
    font-size: 11px;
    color: var(--p-text-muted);
}
.auth-shell__trust span {
    display: inline-flex;
    align-items: center;
    gap: 5px;
}

.auth-shell__foot {
    text-align: center;
    margin-top: 18px;
    font-size: 11px;
    color: var(--p-text-muted);
}
.auth-shell__foot a { color: inherit; text-decoration: none; }

@media (max-width: 600px) {
    .auth-shell__center { padding: 24px 16px 40px; }
    .auth-shell__card { padding: 28px 20px 24px; }
}
</style>
