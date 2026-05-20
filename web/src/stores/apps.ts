import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { useApi } from '@/composables/useApi'
import { fetchContainers as fetchContainersAPI } from '@/services/apps'
import type { App, AppContainer, AppStats, Deployment } from '@/types/api'

export const useAppsStore = defineStore('apps', () => {
  const apps = ref<App[]>([])
  const loading = ref(false)
  const lastError = ref<string | null>(null)

  const byId = computed(() => Object.fromEntries(apps.value.map((a) => [a.id, a])))

  async function fetchAll() {
    loading.value = true
    lastError.value = null
    try {
      const api = useApi()
      const res = await api.get<App[]>('/apps')
      apps.value = res.data ?? []
    } catch (e) {
      lastError.value = String(e)
      throw e
    } finally {
      loading.value = false
    }
  }

  async function fetchOne(id: string): Promise<App> {
    const api = useApi()
    const res = await api.get<App>(`/apps/${id}`)
    const idx = apps.value.findIndex((a) => a.id === res.data.id)
    if (idx === -1) apps.value.push(res.data)
    else apps.value[idx] = res.data
    return res.data
  }

  async function create(body: Partial<App>): Promise<App> {
    const api = useApi()
    const res = await api.post<App>('/apps', body)
    apps.value.push(res.data)
    return res.data
  }

  async function patch(id: string, body: Partial<App>): Promise<App> {
    const api = useApi()
    const res = await api.patch<App>(`/apps/${id}`, body)
    const idx = apps.value.findIndex((a) => a.id === id)
    if (idx >= 0) apps.value[idx] = res.data
    return res.data
  }

  async function remove(id: string) {
    const api = useApi()
    await api.delete(`/apps/${id}`)
    apps.value = apps.value.filter((a) => a.id !== id)
  }

  /**
   * Fires a deploy. The backend creates the deployment row in a
   * goroutine but pre-allocates the id synchronously and returns
   * it in the 202 body — see internal/api/handler/deployment.go.
   * The UI uses the id to navigate the operator straight into the
   * deployment-detail view ("watch this happen").
   *
   * The response shape is `{deployment_id, status, app_id, message}`
   * (not a full Deployment row). We synthesize a minimal Deployment
   * so callers that just want `.id` keep working without changes.
   */
  async function deploy(id: string): Promise<Deployment> {
    const api = useApi()
    const res = await api.post<{ deployment_id: string; status: string; app_id: string }>(
      `/apps/${id}/deploy`,
    )
    return {
      id: res.data.deployment_id,
      app_id: res.data.app_id,
      status: 'pending',
      created_at: Math.floor(Date.now() / 1000),
    } as Deployment
  }

  async function stop(id: string) {
    const api = useApi()
    await api.post(`/apps/${id}/stop`)
    await fetchOne(id)
  }

  async function restart(id: string) {
    const api = useApi()
    await api.post(`/apps/${id}/restart`)
    await fetchOne(id)
  }

  /**
   * Fires a rollback. Same shape as `deploy` — the backend returns
   * `{deployment_id, status, app_id, message}` and we synthesize a
   * minimal Deployment so callers can navigate via `.id`.
   */
  async function rollback(id: string, toDeploymentId?: string): Promise<Deployment> {
    const api = useApi()
    // Backend's rollback handler accepts `to`, not `to_deployment_id`
    // (see internal/api/handler/deployment.go's rollbackReq). The
    // previous mismatch was harmless because the field was ignored;
    // we fix it here so explicit-target rollback actually works.
    const body = toDeploymentId ? { to: toDeploymentId } : {}
    const res = await api.post<{ deployment_id: string; status: string; app_id: string }>(
      `/apps/${id}/rollback`, body,
    )
    return {
      id: res.data.deployment_id,
      app_id: res.data.app_id,
      status: 'pending',
      rollback_of: toDeploymentId ?? null,
      created_at: Math.floor(Date.now() / 1000),
    } as Deployment
  }

  async function listDeployments(id: string): Promise<Deployment[]> {
    const api = useApi()
    const res = await api.get<Deployment[]>(`/apps/${id}/deployments`)
    return res.data ?? []
  }

  async function setEnvVars(id: string, envVars: Record<string, string>) {
    const api = useApi()
    await api.put(`/apps/${id}/env-vars`, envVars)
  }

  /**
   * Containers list for an app. Backend merges live containers
   * (via Docker label `prexel.app_id=<id>`) with the Compose YAML
   * preview when the app is Compose-based. Single-container apps
   * always return exactly one row.
   *
   * Returns [] on any HTTP error so the caller can treat empty as
   * "nothing to show" without try/catch noise. Errors that
   * actually matter (auth, 404 app) are already surfaced by the
   * shared error toast pipeline.
   */
  async function fetchContainers(id: string): Promise<AppContainer[]> {
    // Delegates to the stateless service helper so URL/shape live in
    // one place. Try/catch keeps callers free of error-handling
    // boilerplate when "no containers" and "fetch failed" should both
    // render as an empty state.
    try {
      return await fetchContainersAPI(id)
    } catch {
      return []
    }
  }

  /**
   * Container live stats — CPU% + memory snapshot for an app.
   * Backend takes ~1s to gather two Docker stats samples, so callers
   * should poll on the order of seconds, not faster. Returns null on
   * any HTTP failure so the caller can treat it as "unavailable"
   * without a try/catch in every component.
   */
  async function fetchStats(id: string): Promise<AppStats | null> {
    try {
      const api = useApi()
      const res = await api.get<AppStats>(`/apps/${id}/stats`)
      return res.data
    } catch {
      return null
    }
  }

  return {
    apps, loading, lastError, byId,
    fetchAll, fetchOne, create, patch, remove,
    deploy, stop, restart, rollback, listDeployments, setEnvVars,
    fetchStats, fetchContainers,
  }
})
