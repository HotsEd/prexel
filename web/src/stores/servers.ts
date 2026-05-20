import { ref } from 'vue'
import { defineStore } from 'pinia'
import { useApi } from '@/composables/useApi'
import type { Server, ServerTestReport } from '@/types/api'

export const useServersStore = defineStore('servers', () => {
  const servers = ref<Server[]>([])
  const loading = ref(false)

  async function fetchAll() {
    loading.value = true
    try {
      const api = useApi()
      const res = await api.get<Server[]>('/servers')
      servers.value = res.data ?? []
    } finally {
      loading.value = false
    }
  }

  async function create(body: Partial<Server> & { generate_key?: boolean; private_key?: string }): Promise<Server> {
    const api = useApi()
    const res = await api.post<Server>('/servers', body)
    servers.value.push(res.data)
    return res.data
  }

  async function remove(id: string) {
    const api = useApi()
    await api.delete(`/servers/${id}`)
    servers.value = servers.value.filter((s) => s.id !== id)
  }

  async function test(id: string): Promise<ServerTestReport> {
    const api = useApi()
    const res = await api.post<ServerTestReport>(`/servers/${id}/test`)
    return res.data
  }

  return { servers, loading, fetchAll, create, remove, test }
})
