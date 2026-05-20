// App environment service — env vars + secrets read/write.
//
// Lives in its own module instead of the apps store because:
//   - mutations are partial (per-key for secrets), not full-document
//     like the rest of the apps store
//   - the editor view holds local state already; a global store
//     wouldn't add anything beyond a second source of truth to keep
//     in sync
//
// Stateless on purpose. Components call these inline; errors bubble
// up to the caller's try/catch + notify pipeline.

import { useApi } from '@/composables/useApi'

export interface SecretMeta {
    id: string
    app_id: string
    key: string
    is_build_time: boolean
    is_multiline: boolean
    created_at: string
    updated_at: string
}

export interface UpsertSecretValue {
    value: string
    is_build_time?: boolean
    is_multiline?: boolean
}

export const appEnv = {
    /**
     * Replace the entire env_vars map. The backend treats the body
     * as the new authoritative document (keys absent here are
     * removed), so callers must send the FULL desired state.
     */
    async setEnvVars(appId: string, vars: Record<string, string>): Promise<void> {
        await useApi().put(`/apps/${appId}/env-vars`, vars)
    },

    /**
     * List secret metadata (key + flags). Values never leave the
     * server — they're decrypted only at build/runtime and used
     * directly by the deploy engine.
     */
    async listSecrets(appId: string): Promise<SecretMeta[]> {
        const res = await useApi().get<SecretMeta[]>(`/apps/${appId}/secrets`)
        return res.data ?? []
    },

    /**
     * Batch upsert one or more secrets. Send `{key: {value, ...flags}}`
     * — the backend re-encrypts and replaces just these keys. Other
     * existing secrets are untouched.
     *
     * Returns the new metadata list so the caller can refresh without
     * a separate List call.
     */
    async upsertSecrets(appId: string, items: Record<string, UpsertSecretValue>): Promise<SecretMeta[]> {
        const res = await useApi().put<SecretMeta[]>(`/apps/${appId}/secrets`, items)
        return res.data ?? []
    },

    async deleteSecret(appId: string, key: string): Promise<void> {
        await useApi().delete(`/apps/${appId}/secrets/${encodeURIComponent(key)}`)
    },
}
