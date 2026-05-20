<template>
    <aside class="login-aside">
        <div class="aside-pill aside-pill--1" />
        <div class="aside-pill aside-pill--2" />
        <div class="aside-pill aside-pill--3" />
        <div class="aside-pill aside-pill--4" />

        <div class="login-aside__status">
            <span class="login-aside__status-dot" />
            {{ t('loginAside.status') }}
        </div>

        <div>
            <div class="aside-quote" v-html="t('loginAside.quote')" />
            <p class="login-aside__copy">
                {{ t('loginAside.copy') }}
            </p>
            <div class="deploy-preview" aria-hidden="true">
                <div class="deploy-preview__head">
                    <span class="deploy-preview__dot" />
                    {{ t('loginAside.preview.envName') }}
                    <span class="deploy-preview__hash">main · 8f4a91c</span>
                </div>
                <div class="deploy-preview__line is-ok"><span /> {{ t('loginAside.preview.buildCompleted') }}</div>
                <div class="deploy-preview__line is-ok"><span /> {{ t('loginAside.preview.healthOk') }}</div>
                <div class="deploy-preview__line is-live"><span /> {{ t('loginAside.preview.routed') }}</div>
            </div>
            <div class="aside-strip">
                <div class="aside-stat">
                    <div class="aside-stat-value">1×</div>
                    <div class="aside-stat-label">{{ t('loginAside.stats.deployCmd') }}</div>
                </div>
                <div class="aside-stat aside-stat--bordered">
                    <div class="aside-stat-value">TLS</div>
                    <div class="aside-stat-label">{{ t('loginAside.stats.tlsAuto') }}</div>
                </div>
                <div class="aside-stat aside-stat--bordered">
                    <div class="aside-stat-value">∞</div>
                    <div class="aside-stat-label">{{ t('loginAside.stats.appsPerServer') }}</div>
                </div>
            </div>
        </div>

        <div class="login-aside__copyright">© {{ new Date().getFullYear() }} Prexel · {{ t('loginAside.copyright') }}</div>
    </aside>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
</script>

<style scoped>
.aside-pill--1 { width: 320px; height: 70px; top: -20px; right: -40px; opacity: 0.30; }
.aside-pill--2 { width: 280px; height: 60px; top: 80px; right: 80px; opacity: 0.20; }
.aside-pill--3 { width: 360px; height: 70px; bottom: 60px; right: -30px; opacity: 0.15; }
.aside-pill--4 { width: 240px; height: 55px; bottom: 180px; right: 200px; opacity: 0.22; }

.login-aside__status {
    position: relative;
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 13px;
    opacity: 0.85;
    font-weight: 500;
}
.login-aside__status-dot {
    display: inline-block;
    width: 8px; height: 8px;
    border-radius: 999px;
    background: #22c55e;
    box-shadow: 0 0 0 3px rgba(34, 197, 94, 0.25);
}

.login-aside__copy {
    position: relative;
    font-size: 14px;
    opacity: 0.75;
    margin-top: 16px;
    max-width: 460px;
    line-height: 1.55;
}

.deploy-preview {
    position: relative;
    margin-top: 24px;
    max-width: 520px;
    border: 1px solid rgba(148, 163, 184, 0.22);
    background: rgba(2, 6, 23, 0.54);
    border-radius: 14px;
    padding: 14px;
    box-shadow: 0 20px 54px -36px rgba(0, 0, 0, 0.85);
}
.deploy-preview__head {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    font-weight: 700;
    color: #f8fafc;
    margin-bottom: 12px;
}
.deploy-preview__dot {
    width: 8px;
    height: 8px;
    border-radius: 999px;
    background: #10b981;
    box-shadow: 0 0 0 4px rgba(16, 185, 129, 0.16);
}
.deploy-preview__hash {
    margin-left: auto;
    color: rgba(226, 232, 240, 0.58);
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11px;
}
.deploy-preview__line {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 0;
    border-top: 1px solid rgba(148, 163, 184, 0.12);
    color: rgba(226, 232, 240, 0.78);
    font-family: ui-monospace, Menlo, monospace;
    font-size: 12px;
}
.deploy-preview__line span {
    width: 7px;
    height: 7px;
    border-radius: 999px;
    background: #10b981;
}
.deploy-preview__line.is-live span { background: #38bdf8; }

.aside-stat--bordered {
    padding-left: 32px;
    border-left: 1px solid rgba(255, 255, 255, 0.18);
}

.login-aside__copyright { font-size: 12px; opacity: 0.5; }
</style>
