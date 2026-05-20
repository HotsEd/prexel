/**
 * Thin wrapper over `@/lib/notify`. Components and composables can either
 * call `useToast()` (returns the same singleton) or import `{ notify }` from
 * '@/lib/notify' directly.
 */
import { notify } from '@/lib/notify'

export function useToast() {
    return notify
}
