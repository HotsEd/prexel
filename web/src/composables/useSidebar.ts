/**
 * Global sidebar visibility state for mobile.
 */

import { ref, watch } from 'vue'

const isOpen = ref(false)

watch(isOpen, (open) => {
    if (typeof document === 'undefined') return
    document.body.classList.toggle('is-sidebar-open', open)
})

export function useSidebar() {
    return {
        isOpen,
        open: () => { isOpen.value = true },
        close: () => { isOpen.value = false },
        toggle: () => { isOpen.value = !isOpen.value },
    }
}
