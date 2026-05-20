/**
 * DNS Zones service — wraps `/dns-zones` endpoints.
 *
 * A DNS zone represents the apex domain (e.g. `meudominio.com`) under which
 * one or more `Domain` rows live. The zone object tracks the most recent
 * apex-probe and wildcard-probe results so the UI can guide the user through
 * the DNS-propagation step without re-issuing the probe on every render.
 */
import { useApi } from '@/composables/useApi'

export interface DNSZone {
    id: string
    apex: string
    target_ip?: string | null
    apex_verified: boolean
    apex_verified_at?: number | null
    apex_last_check?: number | null
    wildcard_verified: boolean
    wildcard_verified_at?: number | null
    wildcard_last_check?: number | null
    notes?: string | null
    subdomain_count?: number
    created_at: number
    updated_at: number
}

export interface ProbeResult {
    ok: boolean
    resolved_to?: string[]
    expected?: string
    probed?: string
    error?: string
}

export interface VerificationResult {
    apex: ProbeResult
    wildcard: ProbeResult
    /**
     * Populated only when the caller asked for a specific hostname (e.g. the
     * wizard probing the exact subdomain the user is adding). Lets the UI
     * highlight the probe that matters for the current flow without scaring
     * the operator with a red apex card they don't care about.
     */
    hostname?: ProbeResult
}

export const dnsZonesService = {
    async list(): Promise<DNSZone[]> {
        const res = await useApi().get<DNSZone[]>('/dns-zones')
        return res.data ?? []
    },

    async get(id: string): Promise<DNSZone> {
        const res = await useApi().get<DNSZone>(`/dns-zones/${id}`)
        return res.data
    },

    async create(apex: string, notes?: string): Promise<DNSZone> {
        const body: { apex: string; notes?: string } = { apex }
        if (notes) body.notes = notes
        const res = await useApi().post<DNSZone>('/dns-zones', body)
        return res.data
    },

    async updateNotes(id: string, notes: string): Promise<DNSZone> {
        const res = await useApi().patch<DNSZone>(`/dns-zones/${id}`, { notes })
        return res.data
    },

    async remove(id: string): Promise<void> {
        await useApi().delete(`/dns-zones/${id}`)
    },

    /**
     * Trigger the verification probes for the zone.
     *
     * Pass `hostname` to also probe a specific FQDN (the wizard does this
     * for the subdomain the operator is adding). When omitted, the backend
     * only runs apex + wildcard — the cheap default used by the periodic
     * background re-verifier.
     */
    async verify(id: string, hostname?: string): Promise<VerificationResult> {
        const path = hostname
            ? `/dns-zones/${id}/verify?hostname=${encodeURIComponent(hostname)}`
            : `/dns-zones/${id}/verify`
        const res = await useApi().post<VerificationResult>(path)
        return res.data
    },
}
