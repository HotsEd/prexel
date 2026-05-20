import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { domainsService, type DomainPatch } from '@/services/domains'
import type { Domain } from '@/types/api'

export const useDomainsStore = defineStore('domains', () => {
  const domains = ref<Domain[]>([])
  const loading = ref(false)

  const byId = computed<Record<string, Domain>>(() =>
    Object.fromEntries(domains.value.map((d) => [d.id, d])),
  )

  async function fetchAll() {
    loading.value = true
    try {
      domains.value = await domainsService.list()
    } finally {
      loading.value = false
    }
  }

  async function create(body: Partial<Domain>): Promise<Domain> {
    const created = await domainsService.create(body)
    domains.value.push(created)
    return created
  }

  async function update(id: string, patch: DomainPatch): Promise<Domain> {
    const updated = await domainsService.update(id, patch)
    // PATCH may flip is_primary on a sibling domain (the backend demotes the
    // previous primary). Refresh the whole row locally and clear any other
    // primary flag inside the same app so the UI stays consistent without a
    // refetch round-trip.
    const idx = domains.value.findIndex((d) => d.id === id)
    if (idx >= 0) domains.value[idx] = updated
    if (updated.is_primary && updated.app_id) {
      domains.value = domains.value.map((d) =>
        d.id !== updated.id && d.app_id === updated.app_id && d.is_primary
          ? { ...d, is_primary: false }
          : d,
      )
    }
    return updated
  }

  async function remove(id: string) {
    await domainsService.remove(id)
    domains.value = domains.value.filter((d) => d.id !== id)
  }

  return { domains, loading, byId, fetchAll, create, update, remove }
})
