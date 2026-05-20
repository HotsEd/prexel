<template>
    <UiSelect
        v-model="innerValue"
        :options="options"
        :placeholder="placeholder ?? t('domainPicker.placeholder')"
        :disabled="disabled"
        :invalid="invalid"
        :clearable="clearable"
        :filter="options.length > 5"
    >
        <!--
            Selected value: compact, single-line, with TLS dot. When the
            slot is overridden, PrimeVue stops rendering its built-in
            placeholder, so we MUST render one ourselves (otherwise the
            label row collapses to 0px). The wrapper has `min-height` to
            keep the row visually stable regardless of content.
        -->
        <template #value="{ value }">
            <span class="domain-picker__value">
                <template v-if="!value">
                    <span class="domain-picker__placeholder">
                        {{ placeholder ?? t('domainPicker.placeholder') }}
                    </span>
                </template>
                <template v-else>
                    <span :class="['domain-picker__dot', dotToneFor(domainById(value as string))]" aria-hidden="true" />
                    <span class="domain-picker__value-host mono">{{ hostnameFor(value as string) }}</span>
                    <span v-if="isPrimary(value as string)" class="domain-picker__value-tag">
                        {{ t('domainPicker.primary_tag') }}
                    </span>
                </template>
            </span>
        </template>

        <!-- Option in the dropdown: two-line layout with badges. -->
        <template #option="{ option }">
            <div class="domain-picker__opt">
                <div class="domain-picker__opt-main">
                    <span :class="['domain-picker__dot', dotToneFor(option.domain)]" aria-hidden="true" />
                    <span class="domain-picker__opt-host mono">{{ option.domain.name }}</span>
                    <span v-if="option.isCurrent" class="domain-picker__opt-chip domain-picker__opt-chip--current">
                        {{ t('domainPicker.current') }}
                    </span>
                </div>
                <div class="domain-picker__opt-meta">
                    <span v-if="option.apex" class="domain-picker__opt-apex mono">{{ option.apex }}</span>
                    <span :class="['domain-picker__opt-badge', `is-tls-${option.domain.ssl_status}`]">
                        {{ tlsLabel(option.domain.ssl_status) }}
                    </span>
                    <span :class="['domain-picker__opt-badge', option.domain.dns_verified ? 'is-dns-ok' : 'is-dns-pending']">
                        {{ option.domain.dns_verified ? t('domainPicker.dns_verified') : t('domainPicker.dns_pending') }}
                    </span>
                    <span v-if="option.domain.covered_by_wildcard" class="domain-picker__opt-badge is-wildcard">
                        {{ t('domainPicker.wildcard') }}
                    </span>
                </div>
            </div>
        </template>
    </UiSelect>
</template>

<script setup lang="ts">
/*
    DomainPicker — picks a registered Domain by id, with sensible defaults
    so callers don't reimplement the same "what counts as available?" logic
    in every form (Settings → Instance, App → Domains, …).

    Filter rules (applied in this order):
      1. NEVER hide `currentValue` — it must stay selectable across hydration
         even if it would otherwise be filtered out (e.g. SSL still issuing,
         already bound to an app, etc.).
      2. Drop domains explicitly listed in `excludeIds`.
      3. Drop domains bound to an app (`app_id != null`) — apps own those.
      4. Drop the panel's current `instance_url` — that one is the panel's,
         not "available". Caller can pass it as `currentValue` to override.
      5. When `requireActive` is true (default) drop anything where
         dns_verified is false OR ssl_status !== 'active'. The intent is
         "show only domains the operator can actually use today"; pending
         rows are noise.

    Rendering: a thin wrapper over UiSelect that just hands it richer
    option/value templates. We keep all the focus/border styling in
    UiSelect so the visual stays consistent with every other form field.
*/
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import UiSelect from '@/components/ui/UiSelect.vue'
import { useDomainsStore } from '@/stores/domains'
import { useDNSZonesStore } from '@/stores/dnsZones'
import { useInstanceStore } from '@/stores/instance'
import { extractApex } from '@/utils/dns'
import type { Domain } from '@/types/api'

const props = withDefaults(defineProps<{
    modelValue: string | null
    /**
     * Currently-selected domain id, if any. Always kept in the option list
     * regardless of every other filter, so hydration / "edit existing"
     * flows never lose the active row.
     */
    currentValue?: string | null
    /** Extra ids to drop from the list. */
    excludeIds?: string[]
    /**
     * When true (default) only domains with dns_verified=true AND
     * ssl_status='active' show up. Set to false in flows where pending
     * rows are still meaningful (e.g. a separate "track DNS" picker).
     */
    requireActive?: boolean
    placeholder?: string
    disabled?: boolean
    invalid?: boolean
    clearable?: boolean
}>(), {
    currentValue: null,
    excludeIds: () => [],
    requireActive: true,
    placeholder: undefined,
    disabled: false,
    invalid: false,
    clearable: false,
})

const emit = defineEmits<{
    'update:modelValue': [value: string | null]
    change: [value: string | null]
}>()

const { t } = useI18n()
const domainsStore = useDomainsStore()
const zonesStore = useDNSZonesStore()
const instanceStore = useInstanceStore()

const innerValue = computed<string | null>({
    get: () => props.modelValue,
    set: (v) => {
        const next = (v ?? null) as string | null
        emit('update:modelValue', next)
        emit('change', next)
    },
})

// Best-effort hydration so the picker works in isolation (e.g. opened
// before the host page primed the stores). All three calls are noops if
// already loaded; permission errors on the instance settings are
// swallowed because they only affect the "exclude panel domain" filter.
onMounted(() => {
    if (!domainsStore.domains.length) void domainsStore.fetchAll()
    if (!zonesStore.zones.length) void zonesStore.load()
    if (!instanceStore.settings) void instanceStore.load().catch(() => null)
})

const instanceHostname = computed<string | null>(() => {
    const raw = instanceStore.settings?.instance_url
    if (!raw) return null
    try {
        return new URL(raw).hostname.toLowerCase()
    } catch {
        return raw.replace(/^https?:\/\//, '').replace(/\/.*$/, '').toLowerCase() || null
    }
})

interface PickerOption {
    label: string  // for filter/search; UiSelect reads it via option-label
    value: string  // domain id
    domain: Domain
    apex: string
    isCurrent: boolean
}

const options = computed<PickerOption[]>(() => {
    const excluded = new Set(props.excludeIds)
    const cur = props.currentValue
    return domainsStore.domains
        .filter((d) => {
            // Rule 1: never hide the current selection.
            if (d.id === cur) return true
            // Rule 2: caller-specified exclusions.
            if (excluded.has(d.id)) return false
            // Rule 3: domains owned by an app aren't "available".
            if (d.app_id) return false
            // Rule 4: panel's own domain isn't available either (the panel
            // is itself a binding target — separate flow in Settings).
            if (instanceHostname.value && d.name.toLowerCase() === instanceHostname.value) return false
            // Rule 5: only verified+active when requireActive is on.
            if (props.requireActive) {
                if (!d.dns_verified) return false
                if (d.ssl_status !== 'active') return false
            }
            return true
        })
        .map<PickerOption>((d) => ({
            label: d.name,
            value: d.id,
            domain: d,
            apex: extractApex(d.name) || '',
            isCurrent: d.id === cur,
        }))
        .sort((a, b) => {
            // Current selection floats to the top; everything else
            // alphabetical so the user can scan/type-ahead reliably.
            if (a.isCurrent !== b.isCurrent) return a.isCurrent ? -1 : 1
            return a.domain.name.localeCompare(b.domain.name)
        })
})

function domainById(id: string | null): Domain | undefined {
    if (!id) return undefined
    return domainsStore.byId[id]
}

function hostnameFor(id: string): string {
    return domainById(id)?.name ?? id
}

function isPrimary(id: string): boolean {
    return !!domainById(id)?.is_primary
}

// Coloured dot before each name. Mirrors the SslBadge palette without
// pulling the full component into the row (we want a tight visual).
function dotToneFor(d: Domain | undefined): string {
    if (!d) return 'is-neutral'
    if (d.ssl_status === 'active' && d.dns_verified) return 'is-ok'
    if (d.ssl_status === 'failed' || (d.dns_verified === false && (d.dns_check_count ?? 0) > 5)) return 'is-err'
    return 'is-pending'
}

function tlsLabel(status: Domain['ssl_status']): string {
    switch (status) {
        case 'active': return t('domainPicker.tls_active')
        case 'pending': return t('domainPicker.tls_pending')
        case 'issuing': return t('domainPicker.tls_issuing')
        case 'failed': return t('domainPicker.tls_failed')
        case 'disabled': return t('domainPicker.tls_disabled')
        default: return String(status)
    }
}
</script>

<style scoped>
.domain-picker__placeholder {
    color: var(--p-text-muted);
}

/* ─────────────── Selected value (collapsed) ─────────────── */
.domain-picker__value {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    /* Match UiSelect's intrinsic input height so the row stays stable
       whether we're rendering the placeholder or a populated value. */
    min-height: 1.5em;
    line-height: 1.5;
}
.domain-picker__value-host {
    font-size: 13px;
    font-weight: 600;
    color: var(--p-text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.domain-picker__value-tag {
    padding: 1px 6px;
    border-radius: 999px;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    background: color-mix(in srgb, var(--p-primary-500), transparent 85%);
    color: var(--p-primary-500);
}

/* ─────────────── Option row (expanded) ─────────────── */
.domain-picker__opt {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 2px 0;
    min-width: 0;
}
.domain-picker__opt-main {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
}
.domain-picker__opt-host {
    font-size: 13px;
    font-weight: 600;
    color: var(--p-text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.domain-picker__opt-chip {
    padding: 1px 6px;
    border-radius: 999px;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
}
.domain-picker__opt-chip--current {
    background: color-mix(in srgb, var(--p-primary-500), transparent 85%);
    color: var(--p-primary-500);
}
.domain-picker__opt-meta {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
    padding-left: 16px; /* aligns under the host name, past the dot */
}
.domain-picker__opt-apex {
    font-size: 11px;
    color: var(--p-text-muted);
    padding: 1px 6px;
    border-radius: 4px;
    background: var(--p-surface-50);
    border: 1px solid var(--p-divider);
}

/* ─────────────── Shared badges ─────────────── */
.domain-picker__opt-badge {
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: 999px;
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.02em;
}
.is-tls-active {
    background: color-mix(in srgb, var(--p-success), transparent 85%);
    color: var(--p-success);
}
.is-tls-pending,
.is-tls-issuing {
    background: color-mix(in srgb, var(--p-warning), transparent 85%);
    color: var(--p-warning);
}
.is-tls-failed {
    background: color-mix(in srgb, var(--p-danger), transparent 85%);
    color: var(--p-danger);
}
.is-tls-disabled {
    background: var(--p-neutral-bg);
    color: var(--p-text-muted);
}
.is-dns-ok {
    background: color-mix(in srgb, var(--p-success), transparent 85%);
    color: var(--p-success);
}
.is-dns-pending {
    background: color-mix(in srgb, var(--p-warning), transparent 85%);
    color: var(--p-warning);
}
.is-wildcard {
    background: color-mix(in srgb, var(--p-info, var(--p-primary-500)), transparent 85%);
    color: var(--p-info, var(--p-primary-500));
}

/* ─────────────── Status dot ─────────────── */
.domain-picker__dot {
    flex-shrink: 0;
    width: 8px;
    height: 8px;
    border-radius: 999px;
    display: inline-block;
}
.domain-picker__dot.is-ok      { background: var(--p-success); }
.domain-picker__dot.is-pending { background: var(--p-warning); }
.domain-picker__dot.is-err     { background: var(--p-danger); }
.domain-picker__dot.is-neutral { background: var(--p-text-muted); }
</style>
