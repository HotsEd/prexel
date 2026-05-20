/**
 * Domains service — wraps `/domains` endpoints.
 *
 * The bulk of the domain CRUD used to live inline inside `useDomainsStore`.
 * As the linkage editor on the detail page needs a typed PATCH helper, we
 * extracted the network layer here so the store can stay focused on cache
 * mutations.
 */
import { useApi } from '@/composables/useApi'
import type { Domain } from '@/types/api'

/**
 * Body accepted by `PATCH /domains/{id}`.
 *
 * - `app_id` — set to a string to link to a specific app. Pair with
 *   `clear_app_id: true` to detach (the backend rejects an explicit `null`
 *   here; we keep that contract honest by only ever sending one or the other).
 * - `clear_app_id` — set to `true` to make the domain an "instance" domain.
 * - `is_primary` — toggles the per-app primary flag. The backend will
 *   demote whichever other domain currently owns the flag, if any.
 */
export interface DomainPatch {
    app_id?: string
    clear_app_id?: boolean
    is_primary?: boolean
    /**
     * Per-service routing for Compose apps. `service` + `port`
     * must travel together; `clear_service: true` wipes both back
     * to NULL (domain reverts to app-level routing).
     */
    service?: string
    port?: number
    clear_service?: boolean
    /**
     * When true, Caddy 308-redirects HTTP→HTTPS for this domain
     * (default). Set false to also serve plain HTTP — useful for
     * external health checks or legacy clients.
     */
    force_https?: boolean
}

export const domainsService = {
    async list(): Promise<Domain[]> {
        const res = await useApi().get<Domain[]>('/domains')
        return res.data ?? []
    },

    async create(body: Partial<Domain>): Promise<Domain> {
        const res = await useApi().post<Domain>('/domains', body)
        return res.data
    },

    async update(id: string, patch: DomainPatch): Promise<Domain> {
        const res = await useApi().patch<Domain>(`/domains/${id}`, patch)
        return res.data
    },

    async remove(id: string): Promise<void> {
        await useApi().delete(`/domains/${id}`)
    },
}
