// App volumes service — per-app persistent volume CRUD.
//
// Stateless wrapper around the REST endpoints under
// `/apps/{id}/volumes`. Mirrors the shape of `appEnv.ts`: components
// own local state, errors bubble up to the caller's notify pipeline.
//
// Two flavours behind the same shape:
//   - is_named=true  → Docker-managed volume; backend derives the host
//     name. `host_path` must be empty.
//   - is_named=false → bind mount; `host_path` required.

import { useApi } from '@/composables/useApi'
import type { AppVolume } from '@/types/api'

export type AppVolumeCreate = Omit<
    AppVolume,
    'id' | 'app_id' | 'created_at' | 'updated_at'
>

export type AppVolumePatch = Partial<AppVolumeCreate>

export const appVolumes = {
    async list(appId: string): Promise<AppVolume[]> {
        const res = await useApi().get<AppVolume[]>(`/apps/${appId}/volumes`)
        return res.data ?? []
    },

    async get(appId: string, volId: string): Promise<AppVolume> {
        const res = await useApi().get<AppVolume>(`/apps/${appId}/volumes/${volId}`)
        return res.data
    },

    async create(appId: string, payload: AppVolumeCreate): Promise<AppVolume> {
        const res = await useApi().post<AppVolume>(`/apps/${appId}/volumes`, payload)
        return res.data
    },

    async update(appId: string, volId: string, patch: AppVolumePatch): Promise<AppVolume> {
        const res = await useApi().patch<AppVolume>(`/apps/${appId}/volumes/${volId}`, patch)
        return res.data
    },

    async remove(appId: string, volId: string): Promise<void> {
        await useApi().delete(`/apps/${appId}/volumes/${volId}`)
    },
}
