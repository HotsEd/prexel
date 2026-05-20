/**
 * Pure formatting helpers used across the app.
 */
import { i18n } from '@/i18n'

/** Up-to-two-letter initials from a name (e.g. "Rafael Moreira" → "RM"). */
export function getInitials(name: string): string {
    if (!name) return ''
    return name
        .split(/\s+/)
        .filter(Boolean)
        .slice(0, 2)
        .map((w) => w[0]!.toUpperCase())
        .join('')
}

function currentLocale(): string {
    return i18n.global.locale.value
}

function toDate(value: string | Date | number | null | undefined): Date | null {
    if (value == null || value === '') return null
    let date: Date
    if (typeof value === 'number') {
        // backend uses Unix seconds — anything below 10^12 is seconds, above is ms.
        date = new Date(value < 1e12 ? value * 1000 : value)
    } else {
        date = typeof value === 'string' ? new Date(value) : value
    }
    return Number.isNaN(date.getTime()) ? null : date
}

export function formatDate(value: string | Date | number | null | undefined): string {
    const date = toDate(value)
    if (!date) return ''
    return new Intl.DateTimeFormat(currentLocale(), { dateStyle: 'short' }).format(date)
}

export function formatDateTime(value: string | Date | number | null | undefined): string {
    const date = toDate(value)
    if (!date) return ''
    return new Intl.DateTimeFormat(currentLocale(), { dateStyle: 'short', timeStyle: 'short' }).format(date)
}

export function formatRelative(
    value: string | Date | number | null | undefined,
    now: Date = new Date(),
): string {
    const date = toDate(value)
    if (!date) return ''

    const diffMs = now.getTime() - date.getTime()
    const diffMin = Math.floor(diffMs / 60_000)
    const diffHour = Math.floor(diffMs / 3_600_000)
    const diffDay = Math.floor(diffMs / 86_400_000)
    const diffWeek = Math.floor(diffDay / 7)
    const rtf = new Intl.RelativeTimeFormat(currentLocale(), { numeric: 'auto', style: 'narrow' })

    if (diffMin < 1) return rtf.format(0, 'second')
    if (diffMin < 60) return rtf.format(-diffMin, 'minute')
    if (diffHour < 24) return rtf.format(-diffHour, 'hour')
    if (diffDay < 7) return rtf.format(-diffDay, 'day')
    if (diffWeek < 5) return rtf.format(-diffWeek, 'week')
    return formatDate(date)
}
