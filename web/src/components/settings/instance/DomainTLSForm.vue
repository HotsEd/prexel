<template>
    <SCard :title="t('settings.instance.domain.title')" :sub="t('settings.instance.domain.sub')">
        <!-- ─────────── No domains registered yet ─────────── -->
        <!--
            "Linking the panel to a domain" is symmetric with linking a
            domain to an app: the operator picks one out of the registered
            domains, not a free-form FQDN. So the gating is on existence of
            ANY usable domain, not on existence of zones.
        -->
        <div v-if="!domainsStore.loading && availableDomains.length === 0" class="empty-domains">
            <div class="empty-domains__inner">
                <div class="empty-domains__icon"><IconNetwork :size="22" /></div>
                <div class="empty-domains__copy">
                    <strong>{{ t('settings.instance.domain.no_domains.title') }}</strong>
                    <p>{{ t('settings.instance.domain.no_domains.body') }}</p>
                </div>
                <Button
                    :label="t('settings.instance.domain.no_domains.cta')"
                    icon="pi pi-arrow-right"
                    icon-pos="right"
                    @click="goToDomains"
                />
            </div>

            <!-- The operator can still flip to IP mode (self-signed) even
                 without any registered domain — a brand-new instance always
                 starts there. No certificate picker here: IP-only locks the
                 cert to self-signed (Let's Encrypt needs a public FQDN). -->
            <form class="form form--ip-only" @submit.prevent="onSave" novalidate>
                <p class="cert-summary cert-summary--inline">
                    <i class="pi pi-shield cert-summary__icon" aria-hidden="true" />
                    {{ t('settings.instance.domain.cert_summary_ip') }}
                </p>
                <div class="actions">
                    <Button
                        type="submit"
                        :label="t('settings.instance.domain.save')"
                        :loading="saving"
                        :disabled="!hasChanges"
                    />
                </div>
            </form>
        </div>

        <!-- ─────────── Domains available ─────────── -->
        <form v-else class="form" @submit.prevent="onSave" novalidate>
            <SField :label="t('settings.instance.domain.mode_label')">
                <div class="radio-group">
                    <label class="radio-row">
                        <RadioButton v-model="accessMode" value="ip" name="instance-mode" />
                        <span>
                            <strong>{{ t('settings.instance.domain.mode_ip') }}</strong>
                            <small>{{ t('settings.instance.domain.mode_ip_hint') }}</small>
                        </span>
                    </label>
                    <label class="radio-row">
                        <RadioButton v-model="accessMode" value="domain" name="instance-mode" />
                        <span>
                            <strong>{{ t('settings.instance.domain.mode_domain') }}</strong>
                            <small>{{ t('settings.instance.domain.mode_domain_hint') }}</small>
                        </span>
                    </label>
                </div>
            </SField>

            <template v-if="accessMode === 'domain'">
                <SField
                    :label="t('settings.instance.domain.pick_label')"
                    :hint="t('settings.instance.domain.pick_hint')"
                    :error="selectError ?? undefined"
                >
                    <!--
                        DomainPicker filters internally to verified+active and
                        excludes app-owned / panel-owned rows. We pass the
                        hydrated `originalSelectedId` as `current-value` so the
                        domain currently pointing at the panel stays in the
                        list (even though it would otherwise be filtered out
                        as "panel's own domain" — see Rule 4 in the picker).
                    -->
                    <DomainPicker
                        v-model="selectedDomainId"
                        :current-value="originalSelectedId"
                        :placeholder="t('settings.instance.domain.pick_placeholder')"
                    />
                </SField>

                <p class="hint-link">
                    {{ t('settings.instance.domain.register_new_hint') }}
                    <RouterLink to="/domains/new" class="hint-link__cta">
                        {{ t('settings.instance.domain.register_new_cta') }}
                        <i class="pi pi-arrow-up-right" aria-hidden="true" />
                    </RouterLink>
                </p>

                <!--
                    Certificate decision is auto-applied: domain mode
                    defaults to Let's Encrypt (recommended). The Advanced
                    collapse only opens if the operator wants to flip to
                    self-signed (e.g. internal-only DNS + private CA in
                    front). Keeps the happy path one-click while leaving
                    an escape hatch for the unusual case.
                -->
                <details class="advanced" :open="advancedOpen" @toggle="advancedOpen = ($event.target as HTMLDetailsElement).open">
                    <summary class="advanced__summary">
                        <span class="advanced__current">
                            <i class="pi pi-shield advanced__icon" aria-hidden="true" />
                            {{ tlsMode === 'letsencrypt'
                                ? t('settings.instance.domain.cert_summary_letsencrypt')
                                : t('settings.instance.domain.cert_summary_self_signed') }}
                        </span>
                        <span class="advanced__toggle">
                            <span class="advanced__toggle-label">{{ t('settings.instance.domain.advanced') }}</span>
                            <i class="pi pi-chevron-down advanced__chevron" aria-hidden="true" />
                        </span>
                    </summary>
                    <div class="advanced__body">
                        <SField
                            :label="t('settings.instance.domain.cert_label')"
                            :hint="t('settings.instance.domain.advanced_hint')"
                            :error="serverError ?? undefined"
                        >
                            <div class="radio-group">
                                <label class="radio-row">
                                    <RadioButton v-model="tlsMode" value="letsencrypt" name="tls-mode" />
                                    <span>
                                        <strong>{{ t('settings.instance.domain.cert_letsencrypt') }}</strong>
                                        <small>{{ t('settings.instance.domain.cert_letsencrypt_hint') }}</small>
                                    </span>
                                </label>
                                <label class="radio-row">
                                    <RadioButton v-model="tlsMode" value="self-signed" name="tls-mode" />
                                    <span>
                                        <strong>{{ t('settings.instance.domain.cert_self_signed') }}</strong>
                                        <small>{{ t('settings.instance.domain.cert_self_signed_hint') }}</small>
                                    </span>
                                </label>
                            </div>
                        </SField>
                    </div>
                </details>

                <Message v-if="needsUrlWarning" severity="warn" :closable="false">
                    {{ t('settings.instance.domain.needs_url_warning') }}
                </Message>
            </template>

            <template v-else>
                <!-- IP mode: certificate is forced to self-signed; nothing to pick. -->
                <p class="cert-summary cert-summary--inline">
                    <i class="pi pi-shield cert-summary__icon" aria-hidden="true" />
                    {{ t('settings.instance.domain.cert_summary_ip') }}
                </p>
            </template>

            <div class="actions">
                <Button
                    type="submit"
                    :label="t('settings.instance.domain.save')"
                    :loading="saving"
                    :disabled="!canSave"
                />
            </div>
        </form>
    </SCard>
</template>

<script setup lang="ts">
/*
    Settings → Instância — "Domínio do painel".

    Mental model: the panel is a linking TARGET just like an app. The
    operator picks an already-registered domain to point at the panel; we
    do NOT let them type a free-form FQDN here (that lives in /domains/new).

    "Available" = the domain is registered AND has no app bound to it
    (either truly unbound, or already bound to the panel — that's the row
    we want to keep selected). Apps own their own domains via the bind
    flow in AppDetailView; we never steal those.
*/
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Message from 'primevue/message'
import RadioButton from 'primevue/radiobutton'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import DomainPicker from '@/components/domains/DomainPicker.vue'
import IconNetwork from '@/components/icons/IconNetwork.vue'
import { useDomainsStore } from '@/stores/domains'
import { notify } from '@/lib/notify'
import type { Domain } from '@/types/api'
import type { InstanceSettings, InstanceUpdate, TLSMode } from '@/services/instance'

const props = defineProps<{
    settings: InstanceSettings
}>()

const emit = defineEmits<{
    save: [patch: InstanceUpdate, done: (err?: { field?: 'instance_url'; message?: string }) => void]
}>()

const { t } = useI18n()
const router = useRouter()
const domainsStore = useDomainsStore()

type AccessMode = 'ip' | 'domain'

const accessMode = ref<AccessMode>(props.settings.instance_url ? 'domain' : 'ip')
const selectedDomainId = ref<string | null>(null)
const tlsMode = ref<TLSMode>(props.settings.tls_mode)
const saving = ref(false)
const serverError = ref<string | null>(null)
const selectError = ref<string | null>(null)
// "Avançado" do certificado é collapse: fechado por default em domain mode.
// Abrimos automaticamente se o operador já gravou anteriormente um TLS
// não-default (self-signed em modo domain) — esse é exatamente o caso em
// que ele quer ver o que tem ali sem clicar.
const advancedOpen = ref(false)

// Strip the URL down to a bare hostname so comparisons against `domain.name`
// (which is always a bare hostname) actually match.
const currentInstanceHostname = computed<string>(() => {
    const raw = (props.settings.instance_url || '').trim()
    if (!raw) return ''
    try {
        return new URL(raw).hostname.toLowerCase()
    } catch {
        return raw.replace(/^https?:\/\//, '').replace(/\/.*$/, '').toLowerCase()
    }
})

// The domain currently bound to the panel. DomainPicker would normally
// hide it (Rule 4: panel's own hostname is excluded), so we pass this as
// `current-value` to keep it pinned in the dropdown across hydration.
const originalSelectedId = computed<string | null>(() => {
    const cur = currentInstanceHostname.value
    if (!cur) return null
    return domainsStore.domains.find((d) => d.name.toLowerCase() === cur)?.id ?? null
})

// Used only by the empty-state gate. Same semantics as before: "is there
// anything the operator could plausibly attach to the panel today?".
const availableDomains = computed<Domain[]>(() => {
    const cur = currentInstanceHostname.value
    return domainsStore.domains
        .filter((d) => !d.app_id || d.name.toLowerCase() === cur)
})

onMounted(async () => {
    try {
        await domainsStore.fetchAll()
        hydrateFromSettings(props.settings)
    } catch (e) {
        // Non-blocking — operator can still flip TLS mode.
        console.warn('[DomainTLSForm] failed to load domains', e)
    }
})

watch(() => props.settings, (s) => {
    hydrateFromSettings(s)
    tlsMode.value = s.tls_mode
    serverError.value = null
}, { deep: true })

watch([selectedDomainId, tlsMode, accessMode], () => {
    serverError.value = null
    selectError.value = null
})

/*
    Auto-apply the certificate defaults whenever the access mode toggles:
      - domain  → Let's Encrypt (recommended; what the user wants 99% of the time)
      - ip      → self-signed   (the only valid option; LE needs a public FQDN)
    The operator can still override Let's Encrypt via the Advanced collapse
    in domain mode. There's no override in IP mode because there's nothing
    else that works.
*/
watch(accessMode, (next, prev) => {
    if (next === prev) return
    if (next === 'domain') {
        tlsMode.value = 'letsencrypt'
        advancedOpen.value = false
    } else {
        tlsMode.value = 'self-signed'
        advancedOpen.value = false
    }
})

const selectedHostname = computed(() => {
    if (!selectedDomainId.value) return ''
    return domainsStore.byId[selectedDomainId.value]?.name ?? ''
})

const computedInstanceUrl = computed(() => {
    if (accessMode.value === 'ip') return ''
    return selectedHostname.value
})

const needsUrlWarning = computed(() =>
    accessMode.value === 'domain' && tlsMode.value === 'letsencrypt' && !computedInstanceUrl.value,
)

const hasChanges = computed(() => {
    if (computedInstanceUrl.value !== currentInstanceHostname.value) return true
    if (tlsMode.value !== props.settings.tls_mode) return true
    return false
})

const canSave = computed(() => {
    if (!hasChanges.value) return false
    if (accessMode.value === 'domain') {
        if (!selectedDomainId.value) return false
        if (needsUrlWarning.value) return false
    }
    return true
})

function hydrateFromSettings(s: InstanceSettings) {
    const url = (s.instance_url || '').trim()
    if (!url) {
        accessMode.value = 'ip'
        selectedDomainId.value = null
        advancedOpen.value = false
        return
    }
    accessMode.value = 'domain'
    const host = currentInstanceHostname.value
    const match = domainsStore.domains.find((d) => d.name.toLowerCase() === host)
    selectedDomainId.value = match?.id ?? null
    // Pre-open Advanced when the persisted certificate isn't the default
    // for the current access mode — otherwise the user would have to
    // hunt for the toggle to understand why they're not on Let's Encrypt.
    advancedOpen.value = s.tls_mode === 'self-signed'
}

function onSave() {
    if (!canSave.value) return
    if (accessMode.value === 'domain' && !selectedDomainId.value) {
        selectError.value = t('settings.instance.domain.pick_required')
        return
    }
    saving.value = true
    serverError.value = null
    selectError.value = null
    const patch: InstanceUpdate = {
        instance_url: computedInstanceUrl.value,
        tls_mode: tlsMode.value,
    }
    emit('save', patch, (err) => {
        saving.value = false
        if (err) {
            const msg = err.message ?? t('errors.validation')
            // Backend rejects an instance_url whose apex isn't registered. With
            // the new model this is almost impossible (we only allow picking
            // registered domains), but the gate stays for safety — surface it
            // as an actionable toast that jumps to /domains.
            if (/zone_not_registered/i.test(msg) || /not registered/i.test(msg)) {
                notify.error(t('settings.instance.domain.zone_not_registered_toast'))
                selectError.value = msg
            } else {
                serverError.value = msg
            }
        }
    })
}

function goToDomains() {
    router.push('/domains/new')
}
</script>

<style scoped>
.form {
    display: flex;
    flex-direction: column;
    gap: 16px;
}
.form--ip-only {
    margin-top: 14px;
}

.empty-domains {
    display: flex;
    flex-direction: column;
    gap: 16px;
}
.empty-domains__inner {
    display: grid;
    grid-template-columns: auto 1fr auto;
    align-items: center;
    gap: 14px;
    padding: 16px;
    border: 1px dashed var(--p-content-border);
    border-radius: 12px;
    background: var(--p-surface-50);
}
.empty-domains__icon {
    width: 44px;
    height: 44px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 10px;
    background: rgba(16, 185, 129, 0.12);
    color: var(--p-primary-500);
}
.empty-domains__copy strong {
    display: block;
    font-size: 13px;
    font-weight: 600;
    color: var(--p-text);
}
.empty-domains__copy p {
    margin: 4px 0 0;
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.5;
}

.radio-group {
    display: flex;
    flex-direction: column;
    gap: 10px;
}
.radio-row {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 10px 12px;
    border: 1px solid var(--p-divider);
    border-radius: 10px;
    background: var(--p-content-bg);
    cursor: pointer;
    transition: border-color 120ms ease, background 120ms ease;
}
.radio-row:hover {
    border-color: var(--p-primary-color);
}
.radio-row span {
    display: flex;
    flex-direction: column;
    gap: 2px;
}
.radio-row strong {
    font-size: 13px;
    font-weight: 600;
    color: var(--p-text);
}
.radio-row small {
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.4;
}

.hint-link {
    margin: -4px 0 0;
    font-size: 12px;
    color: var(--p-text-muted);
}
.hint-link__cta {
    color: var(--p-primary-500);
    font-weight: 500;
    text-decoration: none;
    display: inline-flex;
    align-items: center;
    gap: 2px;
}
.hint-link__cta:hover { text-decoration: underline; }

.actions {
    display: flex;
    justify-content: flex-end;
}
:deep(.p-inputtext) { width: 100%; }

/* ─────────────── Certificate summary (inline, IP mode + closed advanced) ─────────────── */
.cert-summary {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    padding: 10px 12px;
    border: 1px solid var(--p-divider);
    border-radius: 10px;
    background: var(--p-surface-50);
    font-size: 13px;
    color: var(--p-text);
}
.cert-summary--inline {
    align-self: flex-start;
}
.cert-summary__icon {
    color: var(--p-primary-500);
    font-size: 14px;
}

/* ─────────────── Advanced collapse (native <details>) ─────────────── */
.advanced {
    border: 1px solid var(--p-divider);
    border-radius: 10px;
    background: var(--p-content-bg);
    overflow: hidden;
    transition: border-color 120ms ease;
}
.advanced[open] {
    border-color: color-mix(in srgb, var(--p-primary-500), transparent 70%);
}
.advanced__summary {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 12px 14px;
    cursor: pointer;
    list-style: none;
    user-select: none;
}
.advanced__summary::-webkit-details-marker { display: none; }
.advanced__current {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    color: var(--p-text);
    min-width: 0;
}
.advanced__icon {
    color: var(--p-primary-500);
    font-size: 14px;
    flex-shrink: 0;
}
.advanced__toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--p-text-muted);
    font-size: 12px;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    flex-shrink: 0;
}
.advanced__toggle-label { line-height: 1; }
.advanced__chevron {
    transition: transform 160ms ease;
    font-size: 11px;
}
.advanced[open] .advanced__chevron {
    transform: rotate(180deg);
}
.advanced__body {
    padding: 4px 14px 14px;
    border-top: 1px dashed var(--p-divider);
}
</style>
