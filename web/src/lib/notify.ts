/**
 * App-wide notification helper. `App.vue` registers the toast singleton once
 * mounted; anywhere else (services, interceptors, composables) calls
 * `notify.error(…)` and the toast appears.
 */

import type { ToastServiceMethods } from 'primevue/toastservice'
import { i18n } from '@/i18n'

let toast: ToastServiceMethods | null = null

export function setToast(t: ToastServiceMethods): void {
    toast = t
}

type Severity = 'success' | 'info' | 'warn' | 'error' | 'secondary' | 'contrast'

function push(severity: Severity, detail: string, summary?: string, life = 4000): void {
    if (!toast) {
        console.warn(`[notify ${severity}]`, summary, detail)
        return
    }
    toast.add({ severity, summary: summary ?? defaultSummary(severity), detail, life })
}

function defaultSummary(severity: Severity): string {
    const t = i18n.global.t
    switch (severity) {
        case 'success': return t('notify.success')
        case 'info': return t('notify.info')
        case 'warn': return t('notify.warn')
        case 'error': return t('notify.error')
        default: return ''
    }
}

export const notify = {
    success: (detail: string, summary?: string) => push('success', detail, summary),
    info: (detail: string, summary?: string) => push('info', detail, summary),
    warn: (detail: string, summary?: string) => push('warn', detail, summary),
    error: (detail: string, summary?: string) => push('error', detail, summary, 6000),
}
