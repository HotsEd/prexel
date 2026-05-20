/**
 * Instance settings service — wraps GET/PATCH /instance/settings.
 *
 * The backend stores a single global config row; PATCH accepts a partial
 * payload and we send only the fields the user actually touched.
 */
import { useApi } from '@/composables/useApi'

export type TLSMode = 'self-signed' | 'letsencrypt'

export interface InstanceSettings {
    instance_url: string
    tls_mode: TLSMode
    default_memory_limit: string
    default_cpu_limit: string
    cleanup_enabled: boolean
    cleanup_schedule: string
    cleanup_disk_threshold: number
    cleanup_image_retention: number
    max_concurrent_deploys: number
    maintenance_mode: boolean
    maintenance_message: string
    updated_at: number
}

export type InstanceUpdate = Partial<InstanceSettings>

/**
 * What `/instance/latest-version` returns. Backend treats every failure
 * as "I don't know" and answers 200 with an empty payload — callers
 * should test for `version === ''` to gate any "Update available!" UI.
 *
 * `published_at` is ISO-8601; empty when version is empty.
 */
export interface LatestVersion {
    version: string
    url: string
    published_at: string
}

export const instanceService = {
    async get(): Promise<InstanceSettings> {
        const res = await useApi().get<InstanceSettings>('/instance/settings')
        return res.data
    },

    async update(patch: InstanceUpdate): Promise<InstanceSettings> {
        const res = await useApi().patch<InstanceSettings>('/instance/settings', patch)
        return res.data
    },

    async latestVersion(): Promise<LatestVersion> {
        const res = await useApi().get<LatestVersion>('/instance/latest-version')
        return res.data
    },
}
