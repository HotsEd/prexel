/**
 * Tags service — wraps `/tags` endpoints.
 *
 * The tag dictionary is global (not per-team) and is the source of
 * truth for the filter dropdown in AppsView. Apps carry just the
 * `tags: string[]` field; this service feeds the filter UI with the
 * full list (plus colors when available).
 */
import { useApi } from '@/composables/useApi'
import type { Tag } from '@/types/api'

export const tagsService = {
    async list(): Promise<Tag[]> {
        const res = await useApi().get<Tag[]>('/tags')
        return res.data ?? []
    },

    /**
     * Upsert by normalised name. The backend treats this as idempotent
     * (200 on existing match, not 409) so the caller can fire it
     * blindly when the user creates a new tag from the chip editor —
     * no need to pre-check via `list()`.
     */
    async create(name: string, color?: string | null): Promise<Tag> {
        const res = await useApi().post<Tag>('/tags', { name, color })
        return res.data
    },

    /**
     * Delete a tag and (via CASCADE on `app_tags`) every attachment
     * to it. No undo — the filter views drop the entry immediately.
     */
    async remove(id: string): Promise<void> {
        await useApi().delete(`/tags/${id}`)
    },
}
