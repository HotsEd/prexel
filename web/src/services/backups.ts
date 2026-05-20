/**
 * Backup service — wraps /backups.
 *
 * Operational model: the panel hosts encrypted snapshot files on the
 * server's filesystem. The operator's job is to download them off-box
 * (and remember the passphrase used at creation time); restore is a
 * separate CLI command — there's deliberately no in-panel restore
 * path because hot-swapping the SQLite under a running daemon is
 * unsafe.
 */
import { useApi } from '@/composables/useApi'

export interface BackupEntry {
    id: string
    file_name: string
    size_bytes: number
    /** ISO-8601 timestamp. */
    created_at: string
}

export interface CreateBackupResponse extends BackupEntry {}

/**
 * Build the absolute URL the browser hits for the download. We don't
 * use the axios instance here — the browser needs a plain `<a href>`
 * so it can stream the response straight to disk. The auth cookie
 * still rides along because the request stays same-origin.
 */
export function backupDownloadURL(id: string): string {
    return `/api/v1/backups/${encodeURIComponent(id)}/download`
}

export const backupsService = {
    async list(): Promise<BackupEntry[]> {
        const res = await useApi().get<{ backups: BackupEntry[] }>('/backups')
        return res.data?.backups ?? []
    },

    async create(passphrase: string): Promise<CreateBackupResponse> {
        const res = await useApi().post<CreateBackupResponse>('/backups', { passphrase })
        return res.data
    },

    async remove(id: string): Promise<void> {
        await useApi().delete(`/backups/${id}`)
    },
}
