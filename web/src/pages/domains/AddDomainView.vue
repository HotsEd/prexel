<template>
    <div class="add-domain">
        <!-- Top bar: back link + step pill (mobile shows here, desktop in rail) -->
        <header class="add-domain__top">
            <RouterLink to="/domains" class="back-link">
                <IconChevronLeft :size="14" />
                <span>{{ t('domains.wizard.back_to_list') }}</span>
            </RouterLink>
        </header>

        <div class="add-domain__grid">
            <!-- ─────────── Stepper rail ─────────── -->
            <aside class="rail">
                <h1 class="rail__title">{{ t('domains.wizard.title') }}</h1>
                <p class="rail__sub">{{ t('domains.wizard.intro') }}</p>

                <ol class="rail__steps">
                    <li
                        v-for="(s, i) in steps"
                        :key="s.label"
                        :class="[
                            'rail-step',
                            i === step && 'is-current',
                            i < step && 'is-done',
                            i > step && 'is-future',
                        ]"
                    >
                        <div class="rail-step__bullet">
                            <IconCheck v-if="i < step" :size="14" />
                            <span v-else>{{ i + 1 }}</span>
                        </div>
                        <div class="rail-step__text">
                            <span class="rail-step__label">{{ s.label }}</span>
                            <span class="rail-step__desc">{{ s.desc }}</span>
                        </div>
                    </li>
                </ol>
            </aside>

            <!-- ─────────── Main pane ─────────── -->
            <!--
                The pane is one big <form>: Enter inside any input submits
                it, and submitting maps to "advance step" (or "create
                domain" on the last step). Buttons that should NOT trigger
                that flow (Back / Cancel / Verify) carry an explicit
                type="button" so they don't accidentally double as submit
                actions when focused.
            -->
            <form class="pane" @submit.prevent="onFormSubmit">
                <div class="pane__card">
                    <header class="pane__head">
                        <span class="pane__kicker">{{ t('domains.wizard.step_kicker', { current: step + 1, total: steps.length }) }}</span>
                        <h2 class="pane__title">{{ steps[step].label }}</h2>
                        <p class="pane__sub">{{ steps[step].desc }}</p>
                    </header>

                    <div class="pane__body">
                        <!-- ── STEP 1: Type ── -->
                        <section v-if="step === 0" class="step">
                            <div class="choice-grid">
                                <label class="choice" :class="{ active: kind === 'apex' }">
                                    <RadioButton v-model="kind" value="apex" name="domain-kind" />
                                    <div class="choice__body">
                                        <strong class="choice__label">{{ t('domains.wizard.step1.apex_label') }}</strong>
                                        <span class="choice__desc">{{ t('domains.wizard.step1.apex_desc') }}</span>
                                        <code class="choice__sample mono">meudominio.com</code>
                                    </div>
                                </label>
                                <label class="choice" :class="{ active: kind === 'sub' }">
                                    <RadioButton v-model="kind" value="sub" name="domain-kind" />
                                    <div class="choice__body">
                                        <strong class="choice__label">{{ t('domains.wizard.step1.sub_label') }}</strong>
                                        <span class="choice__desc">{{ t('domains.wizard.step1.sub_desc') }}</span>
                                        <code class="choice__sample mono">api.meudominio.com</code>
                                    </div>
                                </label>
                            </div>
                        </section>

                        <!-- ── STEP 2: Hostname ── -->
                        <section v-else-if="step === 1" class="step">
                            <template v-if="zoneLocked">
                                <SField
                                    :label="t('domains.wizard.step2.sub_only_label', { apex: lockedApex })"
                                    :error="hostnameError"
                                    :hint="t('domains.wizard.step2.sub_only_hint')"
                                >
                                    <div class="sub-input">
                                        <InputText
                                            v-model="subInput"
                                            :placeholder="t('domains.wizard.step2.sub_only_placeholder')"
                                            class="sub-input__field"
                                            autofocus
                                        />
                                        <span class="sub-input__suffix mono">.{{ lockedApex }}</span>
                                    </div>
                                </SField>
                            </template>

                            <template v-else>
                                <SField :label="t('domains.wizard.step2.label')" :error="hostnameError">
                                    <InputText
                                        v-model="hostname"
                                        :placeholder="t('domains.wizard.step2.placeholder')"
                                        autofocus
                                        @blur="hostname = normalizeHostname(hostname)"
                                    />
                                </SField>
                            </template>

                            <div v-if="apexPreview" class="apex-card">
                                <div class="apex-card__icon"><IconCheck :size="16" /></div>
                                <div class="apex-card__text">
                                    <span class="apex-card__label">{{ t('domains.wizard.step2.apex_preview', { apex: apexPreview }) }}</span>
                                    <span v-if="effectiveKind === 'sub'" class="apex-card__hint">
                                        {{ t('domains.wizard.step2.apex_preview_hint') }}
                                    </span>
                                </div>
                            </div>

                            <Message v-if="kindMismatchWarning" severity="warn" :closable="false">
                                {{ kindMismatchWarning }}
                            </Message>
                        </section>

                        <!-- ── STEP 3: DNS instructions ── -->
                        <section v-else-if="step === 2" class="step">
                            <!-- Apex: one and only one record makes sense. -->
                            <template v-if="effectiveKind === 'apex'">
                                <p class="prose">{{ t('domains.wizard.step3.apex_instructions', { apex: apexPreview }) }}</p>
                                <DnsRecordsTable :rows="[{ type: 'A', name: '@', value: displayIp }]" />
                            </template>

                            <!-- Subdomain: show the single record for this host. The
                                 wildcard-vs-single decision is an infra choice the
                                 operator makes at their DNS provider, not in the
                                 wizard — so we always print the specific record and
                                 hint that wildcard owners can skip the creation. -->
                            <template v-else>
                                <!-- Already detected wildcard for the zone — nothing to do here. -->
                                <Message
                                    v-if="hasWildcard"
                                    severity="success"
                                    :closable="false"
                                >
                                    {{ t('domains.wizard.step3.wildcard_already_active', { apex: apexPreview }) }}
                                </Message>

                                <p class="prose">{{ t('domains.wizard.step3.sub_instructions', { host: hostname }) }}</p>
                                <DnsRecordsTable :rows="[{ type: 'A', name: subdomainName, value: displayIp }]" />

                                <!-- Hint: people who run a wildcard at their provider
                                     don't need this record at all — we still let them
                                     advance and let Step 4 confirm via probe. -->
                                <Message v-if="!hasWildcard" severity="info" :closable="false">
                                    {{ t('domains.wizard.step3.wildcard_hint', { apex: apexPreview }) }}
                                </Message>
                            </template>

                            <Message v-if="!hasIp" severity="info" :closable="false">
                                {{ t('domains.wizard.step3.no_ip_warning') }}
                            </Message>
                        </section>

                        <!-- ── STEP 4: Verification ── -->
                        <section v-else-if="step === 3" class="step">
                            <p class="prose">{{ t('domains.wizard.step4.intro') }}</p>

                            <div class="verify-bar">
                                <Button
                                    type="button"
                                    :label="lastResult ? t('domains.wizard.step4.verify_again') : t('domains.wizard.step4.verify_now')"
                                    :loading="verifying"
                                    icon="pi pi-refresh"
                                    @click="runVerification(true)"
                                />
                                <span v-if="polling && pollCountdown > 0" class="verify-bar__hint">
                                    <span class="dot dot--pulse" />
                                    {{ t('domains.wizard.step4.polling', { seconds: pollCountdown }) }}
                                </span>
                                <span v-else-if="lastResult && !polling && !allOk" class="verify-bar__hint muted">
                                    {{ t('domains.wizard.step4.polling_stopped') }}
                                </span>
                                <span v-else-if="lastResult && allOk" class="verify-bar__hint verify-bar__hint--ok">
                                    <IconCheck :size="14" /> {{ t('domains.wizard.step4.all_ok') }}
                                </span>
                            </div>

                            <div v-if="!lastResult && !verifying" class="probe-placeholder">
                                <IconNetwork :size="32" />
                                <p>{{ t('domains.wizard.step4.placeholder') }}</p>
                            </div>

                            <!-- Probe cards. We render only what's relevant for
                                 the chosen flow so the operator isn't confused
                                 by a red apex card when they only configured
                                 a single subdomain.
                                   - apex flow      → Hostname (==apex) + Wildcard
                                   - subdomain flow → Hostname (specific) + Wildcard
                                 The hostname card always leads because that's
                                 the one the operator actually needs to pass. -->
                            <div v-if="lastResult" class="probe-grid">
                                <ProbeResultCard
                                    v-if="lastResult.hostname"
                                    :title="effectiveKind === 'apex' ? t('domains.wizard.step4.apex_card') : t('domains.wizard.step4.hostname_card')"
                                    :host="hostname"
                                    :result="lastResult.hostname"
                                />
                                <ProbeResultCard
                                    :title="t('domains.wizard.step4.wildcard_card')"
                                    :host="`*.${apexPreview}`"
                                    :result="lastResult.wildcard"
                                />
                            </div>

                            <Message v-if="lastResult && !primaryOk" severity="warn" :closable="false">
                                {{ t('domains.wizard.step4.pending_warning') }}
                            </Message>
                        </section>

                        <!-- ── STEP 5: App + primary ── -->
                        <section v-else-if="step === 4" class="step">
                            <SField :label="t('domains.wizard.step5.app_label')" :hint="t('domains.wizard.step5.app_hint')">
                                <UiSelect
                                    v-model="selectedAppId"
                                    :options="appOptions"
                                    :placeholder="t('domains.wizard.step5.instance_option')"
                                    clearable
                                />
                            </SField>
                            <SField v-if="selectedAppId" :hint="t('domains.wizard.step5.primary_hint')">
                                <label class="check-row">
                                    <Checkbox v-model="isPrimary" binary />
                                    <span>{{ t('domains.wizard.step5.primary_label') }}</span>
                                </label>
                            </SField>

                            <div class="summary">
                                <h3>{{ t('domains.wizard.step5.summary_title') }}</h3>
                                <dl>
                                    <dt>{{ t('domains.wizard.step5.summary_host') }}</dt>
                                    <dd class="mono">{{ hostname }}</dd>
                                    <dt>{{ t('domains.wizard.step5.summary_zone') }}</dt>
                                    <dd class="mono">{{ apexPreview }}</dd>
                                    <dt>{{ t('domains.wizard.step5.summary_target') }}</dt>
                                    <dd>{{ selectedAppId ? appNameById(selectedAppId) : t('domains.wizard.step5.instance_option') }}</dd>
                                </dl>
                            </div>

                            <Message v-if="submitError" severity="error" :closable="false">{{ submitError }}</Message>
                        </section>
                    </div>
                </div>

                <!--
                    Sticky footer with actions.
                    - "Continuar" / "Adicionar domínio" are type=submit so a
                      keyboard Enter inside any focused input triggers them
                      via the form's submit listener (onFormSubmit).
                    - "Voltar" / "Cancelar" are explicit type=button so Enter
                      in the inputs never accidentally goes back / closes.
                -->
                <footer class="pane__footer">
                    <Button type="button" text :label="t('common.cancel')" :disabled="busy" @click="goBackToList" />
                    <div class="pane__footer-right">
                        <Button
                            v-if="canGoBack"
                            type="button"
                            text
                            :label="t('common.back')"
                            icon="pi pi-arrow-left"
                            :disabled="busy"
                            @click="goBack"
                        />
                        <Button
                            v-if="step < 4"
                            type="submit"
                            :label="t('common.continue')"
                            icon="pi pi-arrow-right"
                            icon-pos="right"
                            :disabled="!canContinue"
                        />
                        <Button
                            v-else
                            type="submit"
                            :label="t('domains.wizard.step5.submit')"
                            icon="pi pi-check"
                            :loading="submitting"
                            :disabled="!apexPreview"
                        />
                    </div>
                </footer>
            </form>
        </div>
    </div>
</template>

<script setup lang="ts">
/*
    Add Domain — full-page wizard.

    Lives at /domains/new (optionally `?zone={id}` to bind to a zone, in
    which case Step 1 is hidden and Step 2 shows a fixed `.apex` suffix
    next to the subdomain input).

    Replaces the old modal AddDomainWizard; the lifecycle changed:
      - On mount, prefetch apps + zones, then resolve the zone-lock from
        the query string.
      - "Cancel" + "Cancel back" both navigate to /domains.
      - On successful submit we navigate back to /domains (toast + the
        list refreshes on mount of the destination view).

    The step bodies are unchanged in spirit from the modal version — the
    extraction into ProbeResultCard / DnsRecordsTable just keeps the
    template breathable now that the page can show much more at once.
*/
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import RadioButton from 'primevue/radiobutton'
import Checkbox from 'primevue/checkbox'
import Message from 'primevue/message'
import SField from '@/components/settings/SField.vue'
import UiSelect from '@/components/ui/UiSelect.vue'
import IconCheck from '@/components/icons/IconCheck.vue'
import IconChevronLeft from '@/components/icons/IconChevronLeft.vue'
// Note: the icon import above is named `IconChevronLeft` because that's the
// only "back-pointing" arrow in our set today. Renders perfectly as a back
// affordance.
import IconNetwork from '@/components/icons/IconNetwork.vue'
import DnsRecordsTable from '@/components/domains/DnsRecordsTable.vue'
import ProbeResultCard from '@/components/domains/ProbeResultCard.vue'
import { useAppsStore } from '@/stores/apps'
import { useDomainsStore } from '@/stores/domains'
import { useDNSZonesStore } from '@/stores/dnsZones'
import { extractApex, isApex, isLikelyFqdn, normalizeHostname, subdomainPart } from '@/utils/dns'
import { notify } from '@/lib/notify'
import { apiErrorMessage } from '@/composables/useApi'
import type { VerificationResult } from '@/services/dnsZones'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const appsStore = useAppsStore()
const domainsStore = useDomainsStore()
const zonesStore = useDNSZonesStore()

const steps = computed(() => [
    { label: t('domains.wizard.stepLabels.type'),     desc: t('domains.wizard.stepDescs.type') },
    { label: t('domains.wizard.stepLabels.hostname'), desc: t('domains.wizard.stepDescs.hostname') },
    { label: t('domains.wizard.stepLabels.dns'),      desc: t('domains.wizard.stepDescs.dns') },
    { label: t('domains.wizard.stepLabels.verify'),   desc: t('domains.wizard.stepDescs.verify') },
    { label: t('domains.wizard.stepLabels.app'),      desc: t('domains.wizard.stepDescs.app') },
])

const step = ref(0)
const kind = ref<'apex' | 'sub'>('sub')
const hostname = ref('')
const subInput = ref('')
const selectedAppId = ref<string | null>(null)
const isPrimary = ref(false)

const verifying = ref(false)
const submitting = ref(false)
const submitError = ref<string | null>(null)
const lastResult = ref<VerificationResult | null>(null)
const currentZoneId = ref<string | null>(null)
const polling = ref(false)
const pollCountdown = ref(0)
let pollTimer: ReturnType<typeof setInterval> | null = null
let pollDeadline = 0

const busy = computed(() => verifying.value || submitting.value)

const initialZoneId = computed(() => {
    const v = route.query.zone
    return typeof v === 'string' && v ? v : null
})

const lockedZone = computed(() =>
    initialZoneId.value ? zonesStore.getById(initialZoneId.value) ?? null : null,
)
const zoneLocked = computed(() => !!lockedZone.value)
const lockedApex = computed(() => lockedZone.value?.apex ?? '')

const apexPreview = computed(() => extractApex(hostname.value))
const subdomainName = computed(() => subdomainPart(hostname.value))
const effectiveKind = computed<'apex' | 'sub'>(() => {
    if (zoneLocked.value) return 'sub'
    if (!hostname.value) return kind.value
    return isApex(hostname.value) ? 'apex' : 'sub'
})

const existingZone = computed(() => apexPreview.value ? zonesStore.getByApex(apexPreview.value) : undefined)
const hasWildcard = computed(() => !!existingZone.value?.wildcard_verified)
const targetIp = computed(() => existingZone.value?.target_ip ?? '')
const hasIp = computed(() => !!targetIp.value)
const displayIp = computed(() => targetIp.value || '<IP-do-servidor>')

const hostnameError = computed(() => {
    if (!hostname.value) return ''
    if (!isLikelyFqdn(hostname.value)) return t('domains.wizard.step2.invalid')
    return ''
})

const kindMismatchWarning = computed(() => {
    if (zoneLocked.value) return ''
    if (!hostname.value || hostnameError.value) return ''
    const isApx = isApex(hostname.value)
    if (kind.value === 'apex' && !isApx) return t('domains.wizard.step2.sub_warning')
    if (kind.value === 'sub' && isApx) return t('domains.wizard.step2.apex_warning')
    return ''
})

// Keep `hostname` derived from `subInput.<lockedApex>` while in zone-locked mode.
watch([subInput, lockedApex, zoneLocked], () => {
    if (!zoneLocked.value) return
    const trimmed = subInput.value.trim().replace(/\.+$/, '').replace(/^\.+/, '')
    hostname.value = trimmed ? `${trimmed.toLowerCase()}.${lockedApex.value}` : ''
})

// Verification verdict.
//
// `primaryOk` answers the question "does the thing the operator just
// configured actually resolve?". For the wizard's perspective:
//
//   - The hostname probe is THE source of truth for the FQDN being added.
//     If it resolves to the expected IP, the operator is good — even if
//     apex doesn't (legitimate: operator only set up the subdomain) and
//     even if wildcard doesn't (legitimate: operator chose specific A).
//   - Wildcard coverage is a valid "yes" too: if `*.zone` answers to the
//     expected IP, the operator's chosen subdomain is reachable through
//     it, regardless of whether they bothered creating a specific record.
//
// `allOk` (used by the polling loop) stops re-probing as soon as primaryOk
// flips true — no point hammering DNS once we've already proven success.
const primaryOk = computed(() => {
    if (!lastResult.value) return false
    if (lastResult.value.hostname?.ok) return true
    if (lastResult.value.wildcard.ok) return true
    return false
})
const allOk = primaryOk

const appOptions = computed(() => appsStore.apps.map((a) => ({ label: a.name, value: a.id })))

function appNameById(id: string): string {
    return appsStore.apps.find(a => a.id === id)?.name ?? id
}

const canContinue = computed(() => {
    if (busy.value) return false
    switch (step.value) {
        case 0: return !!kind.value
        case 1: return !!hostname.value && !hostnameError.value && !!apexPreview.value
        case 2: return true
        case 3: return true
        case 4: return !!apexPreview.value
        default: return false
    }
})

const canGoBack = computed(() => {
    if (step.value === 0) return false
    if (zoneLocked.value && step.value <= 1) return false
    return true
})

function goBackToList() {
    stopPolling()
    router.push('/domains')
}

onMounted(async () => {
    try {
        await Promise.all([appsStore.fetchAll(), zonesStore.load()])
        if (initialZoneId.value && lockedZone.value) {
            kind.value = 'sub'
            currentZoneId.value = lockedZone.value.id
            step.value = 1
        }
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
})

function goBack() {
    if (step.value === 0) return
    if (zoneLocked.value && step.value <= 1) return
    step.value -= 1
}

async function goNext() {
    if (!canContinue.value) return
    if (step.value === 2) {
        step.value = 3
        await runVerification(false)
        startPolling()
        return
    }
    step.value += 1
}

// onFormSubmit is the single Enter-key handler for the whole wizard.
// Submitting the form on the last step is "create the domain"; on every
// other step it means "advance". canContinue / submitting guards keep
// rapid Enter presses from re-firing while a probe or POST is in flight.
function onFormSubmit() {
    if (busy.value) return
    if (step.value === 4) {
        void submit()
        return
    }
    if (canContinue.value) void goNext()
}

async function ensureZone(): Promise<string | null> {
    if (currentZoneId.value) return currentZoneId.value
    if (!apexPreview.value) return null
    const existing = existingZone.value
    if (existing) {
        currentZoneId.value = existing.id
        return existing.id
    }
    try {
        const z = await zonesStore.create(apexPreview.value)
        currentZoneId.value = z.id
        return z.id
    } catch (e) {
        notify.error(apiErrorMessage(e))
        return null
    }
}

async function runVerification(manual: boolean) {
    if (!apexPreview.value || verifying.value) return
    verifying.value = true
    try {
        const id = await ensureZone()
        if (!id) return
        // Always pass the exact FQDN the operator is adding so the result
        // carries a dedicated hostname probe. Apex+wildcard come back too,
        // but it's the hostname probe that "decides" the wizard.
        lastResult.value = await zonesStore.verify(id, hostname.value || undefined)
        if (allOk.value) {
            stopPolling()
            if (manual) notify.success(t('dnsZones.verify.success'))
        }
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        verifying.value = false
    }
}

const POLL_INTERVAL_MS = 15_000
const POLL_WINDOW_MS = 120_000

function startPolling() {
    stopPolling()
    if (allOk.value) return
    pollDeadline = Date.now() + POLL_WINDOW_MS
    polling.value = true
    pollCountdown.value = Math.ceil(POLL_INTERVAL_MS / 1000)
    let nextProbeAt = Date.now() + POLL_INTERVAL_MS
    pollTimer = setInterval(() => {
        if (Date.now() >= pollDeadline) {
            stopPolling()
            return
        }
        const remaining = nextProbeAt - Date.now()
        pollCountdown.value = Math.max(0, Math.ceil(remaining / 1000))
        if (remaining <= 0 && !verifying.value) {
            nextProbeAt = Date.now() + POLL_INTERVAL_MS
            void runVerification(false)
        }
    }, 1000)
}

function stopPolling() {
    if (pollTimer) {
        clearInterval(pollTimer)
        pollTimer = null
    }
    polling.value = false
    pollCountdown.value = 0
}

onBeforeUnmount(stopPolling)

async function submit() {
    submitError.value = null
    submitting.value = true
    try {
        await domainsStore.create({
            name: hostname.value,
            app_id: selectedAppId.value,
            is_primary: selectedAppId.value ? isPrimary.value : false,
        })
        notify.success(t('domains.added'))
        stopPolling()
        router.push('/domains')
    } catch (e) {
        submitError.value = apiErrorMessage(e)
    } finally {
        submitting.value = false
    }
}
</script>

<style scoped>
/* ────────────────────────────────────────────────────────────────────
   Layout
   ──────────────────────────────────────────────────────────────────── */
.add-domain {
    max-width: 1100px;
    margin: 0 auto;
}

.add-domain__top {
    margin-bottom: 16px;
}

.back-link {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    color: var(--p-text-muted);
    text-decoration: none;
    transition: color 120ms;
}
.back-link:hover {
    color: var(--p-text);
}

.add-domain__grid {
    display: grid;
    grid-template-columns: 280px 1fr;
    gap: 32px;
    align-items: start;
}

@media (max-width: 880px) {
    .add-domain__grid {
        grid-template-columns: 1fr;
        gap: 16px;
    }
}

/* ────────────────────────────────────────────────────────────────────
   Stepper rail
   ──────────────────────────────────────────────────────────────────── */
.rail {
    position: sticky;
    top: 24px;
    padding: 24px;
    background: var(--p-content-bg);
    border: 1px solid var(--p-content-border);
    border-radius: 14px;
}

.rail__title {
    margin: 0 0 4px;
    font-size: 18px;
    font-weight: 700;
    color: var(--p-text);
}
.rail__sub {
    margin: 0 0 20px;
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.45;
}

.rail__steps {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0;
}

.rail-step {
    position: relative;
    display: flex;
    gap: 12px;
    padding: 10px 0;
}
/* Vertical connector — fades into the next step. */
.rail-step:not(:last-child)::before {
    content: '';
    position: absolute;
    left: 13px;
    top: 36px;
    bottom: 0;
    width: 2px;
    background: var(--p-content-border);
}
.rail-step.is-done:not(:last-child)::before {
    background: var(--p-primary-500);
    opacity: 0.45;
}

.rail-step__bullet {
    flex-shrink: 0;
    width: 28px;
    height: 28px;
    border-radius: 999px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    font-weight: 700;
    border: 2px solid var(--p-content-border);
    background: var(--p-content-bg);
    color: var(--p-text-muted);
    transition: all 160ms ease;
    z-index: 1;
}
.rail-step.is-current .rail-step__bullet {
    border-color: var(--p-primary-500);
    color: var(--p-primary-500);
    box-shadow: 0 0 0 4px color-mix(in srgb, var(--p-primary-500), transparent 85%);
}
.rail-step.is-done .rail-step__bullet {
    background: var(--p-primary-500);
    border-color: var(--p-primary-500);
    color: #fff;
}

.rail-step__text {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding-top: 3px;
}
.rail-step__label {
    font-size: 13px;
    font-weight: 600;
    color: var(--p-text);
    line-height: 1.2;
}
.rail-step.is-future .rail-step__label {
    color: var(--p-text-muted);
}
.rail-step__desc {
    font-size: 11px;
    color: var(--p-text-muted);
    line-height: 1.4;
}

@media (max-width: 880px) {
    .rail {
        position: static;
        padding: 16px;
    }
    .rail__title { font-size: 16px; }
    .rail__sub { display: none; }
    .rail__steps { flex-direction: row; gap: 8px; overflow-x: auto; padding-bottom: 4px; }
    .rail-step { flex-direction: column; align-items: center; gap: 6px; padding: 0; min-width: 64px; }
    .rail-step:not(:last-child)::before { display: none; }
    .rail-step__text { padding-top: 0; align-items: center; text-align: center; }
    .rail-step__desc { display: none; }
}

/* ────────────────────────────────────────────────────────────────────
   Main pane
   ──────────────────────────────────────────────────────────────────── */
.pane {
    display: flex;
    flex-direction: column;
    gap: 16px;
    min-height: calc(100vh - 200px);
}

.pane__card {
    background: var(--p-content-bg);
    border: 1px solid var(--p-content-border);
    border-radius: 14px;
    overflow: hidden;
}

.pane__head {
    padding: 28px 32px 18px;
    border-bottom: 1px solid var(--p-content-border);
    background: linear-gradient(
        180deg,
        color-mix(in srgb, var(--p-primary-500), transparent 96%) 0%,
        transparent 100%
    );
}
.pane__kicker {
    display: block;
    margin-bottom: 6px;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--p-primary-500);
}
.pane__title {
    margin: 0;
    font-size: 22px;
    font-weight: 700;
    color: var(--p-text);
    letter-spacing: -0.01em;
}
.pane__sub {
    margin: 6px 0 0;
    font-size: 13px;
    color: var(--p-text-muted);
    line-height: 1.5;
    max-width: 580px;
}

.pane__body {
    padding: 28px 32px;
}

.pane__footer {
    position: sticky;
    bottom: 0;
    z-index: 5;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 18px;
    background: var(--p-content-bg);
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    box-shadow: 0 -4px 12px -6px rgba(0, 0, 0, 0.12);
}
.pane__footer-right {
    display: inline-flex;
    align-items: center;
    gap: 8px;
}

/* ────────────────────────────────────────────────────────────────────
   Step content primitives
   ──────────────────────────────────────────────────────────────────── */
.step {
    display: flex;
    flex-direction: column;
    gap: 18px;
}

.prose {
    margin: 0;
    color: var(--p-text-muted);
    line-height: 1.55;
    font-size: 14px;
}

/* — Choice cards (radio with body) — */
.choice-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: 12px;
}
.choice-grid--tight { gap: 10px; }

.choice {
    display: flex;
    gap: 12px;
    padding: 16px 18px;
    border: 1px solid var(--p-content-border);
    border-radius: 12px;
    cursor: pointer;
    background: var(--p-content-bg);
    transition: border-color 120ms, background 120ms;
}
.choice:hover { border-color: color-mix(in srgb, var(--p-primary-500), transparent 60%); }
.choice.active {
    border-color: var(--p-primary-500);
    background: color-mix(in srgb, var(--p-primary-500), transparent 94%);
}
.choice__body {
    display: flex;
    flex-direction: column;
    gap: 4px;
    flex: 1;
    min-width: 0;
}
.choice__label {
    font-size: 14px;
    font-weight: 600;
    color: var(--p-text);
    line-height: 1.25;
}
.choice__desc {
    font-size: 12px;
    color: var(--p-text-muted);
    line-height: 1.5;
}
.choice__sample {
    margin-top: 4px;
    font-size: 11px;
    padding: 3px 8px;
    background: var(--p-bg);
    border: 1px solid var(--p-content-border);
    border-radius: 6px;
    color: var(--p-text-muted);
    align-self: flex-start;
}

/* — Subdomain split input — */
.sub-input {
    display: flex;
    align-items: stretch;
    border: 1px solid var(--p-input-border);
    border-radius: var(--p-r-md);
    overflow: hidden;
    transition: border-color 120ms, box-shadow 120ms;
}
.sub-input:focus-within {
    border-color: var(--p-primary-500);
    box-shadow: var(--p-focus-ring);
}
.sub-input__field {
    border: 0 !important;
    border-radius: 0 !important;
    box-shadow: none !important;
    flex: 1;
    min-width: 0;
}
.sub-input__suffix {
    display: inline-flex;
    align-items: center;
    padding: 0 14px;
    background: var(--p-bg);
    color: var(--p-text-muted);
    border-left: 1px solid var(--p-input-border);
    font-size: 13px;
    white-space: nowrap;
}

/* — Apex preview card — */
.apex-card {
    display: flex;
    gap: 12px;
    padding: 12px 14px;
    border: 1px solid color-mix(in srgb, var(--p-success), transparent 65%);
    border-radius: 10px;
    background: color-mix(in srgb, var(--p-success), transparent 92%);
}
.apex-card__icon {
    flex-shrink: 0;
    width: 28px;
    height: 28px;
    border-radius: 8px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: var(--p-success);
    color: #fff;
}
.apex-card__text { display: flex; flex-direction: column; gap: 2px; }
.apex-card__label { font-size: 13px; font-weight: 600; color: var(--p-text); }
.apex-card__hint { font-size: 12px; color: var(--p-text-muted); }

/* — Verify bar (Step 4) — */
.verify-bar {
    display: flex;
    align-items: center;
    gap: 14px;
    flex-wrap: wrap;
}
.verify-bar__hint {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--p-text);
}
.verify-bar__hint.muted { color: var(--p-text-muted); }
.verify-bar__hint--ok { color: var(--p-success); font-weight: 600; }

.dot {
    width: 8px;
    height: 8px;
    border-radius: 999px;
    background: var(--p-primary-500);
    display: inline-block;
}
.dot--pulse {
    animation: dot-pulse 1.2s ease-in-out infinite;
}
@keyframes dot-pulse {
    0%, 100% { transform: scale(1); opacity: 1; }
    50% { transform: scale(1.5); opacity: 0.4; }
}

.probe-placeholder {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    padding: 36px 16px;
    border: 1px dashed var(--p-content-border);
    border-radius: 12px;
    color: var(--p-text-muted);
}
.probe-placeholder p { margin: 0; font-size: 13px; }

.probe-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
}
@media (max-width: 720px) {
    .probe-grid { grid-template-columns: 1fr; }
}

/* — Step 5 summary — */
.summary {
    margin-top: 4px;
    padding: 16px 18px;
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    background: var(--p-bg);
}
.summary h3 {
    margin: 0 0 10px;
    font-size: 12px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--p-text-muted);
}
.summary dl {
    display: grid;
    grid-template-columns: 140px 1fr;
    gap: 8px 16px;
    margin: 0;
    font-size: 13px;
}
.summary dt { color: var(--p-text-muted); }
.summary dd { margin: 0; color: var(--p-text); font-weight: 500; }

.check-row {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
    font-size: 13px;
    color: var(--p-text);
}
</style>
