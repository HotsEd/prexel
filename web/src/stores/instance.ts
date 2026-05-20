/**
 * Instance settings store.
 *
 * Single source of truth for the InstanceSection screen. The store keeps the
 * full settings object plus a loading flag; each sub-form calls `update()`
 * with a partial patch and gets back the freshly persisted settings.
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { instanceService, type InstanceSettings, type InstanceUpdate } from '@/services/instance'

export const useInstanceStore = defineStore('instance', () => {
    const settings = ref<InstanceSettings | null>(null)
    const loading = ref(false)

    async function load(): Promise<InstanceSettings | null> {
        loading.value = true
        try {
            settings.value = await instanceService.get()
            return settings.value
        } finally {
            loading.value = false
        }
    }

    async function update(patch: InstanceUpdate): Promise<InstanceSettings> {
        const updated = await instanceService.update(patch)
        settings.value = updated
        return updated
    }

    return { settings, loading, load, update }
})
