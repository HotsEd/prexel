// Apps service — thin wrappers around endpoints that don't fit the
// CRUD-shaped useAppsStore (i.e. ad-hoc reads like the containers
// listing, or stream helpers like per-container log paths).
//
// The store remains the right place for state-bearing operations
// (list/create/patch/delete + the App cache); this module is for
// stateless reads and URL builders that views/composables compose
// into useSSE.

import { useApi } from '@/composables/useApi'
import type { AppContainer, AppStats, ContainerDetail, Deployment, Tag } from '@/types/api'

// Re-export so callers that already import from this module don't have
// to also import from types/api. The canonical declaration lives in
// types/api.ts so the store + components see the same shape.
export type { AppContainer }

interface ContainersResponse {
  containers: AppContainer[]
}

/**
 * fetchContainers returns the merged preview+live container list for
 * an app. Used by the AppLogsView to populate the per-container
 * selector, and by AppDetail's "Containers" tab.
 */
export async function fetchContainers(appId: string): Promise<AppContainer[]> {
  const api = useApi()
  const res = await api.get<ContainersResponse>(`/apps/${appId}/containers`)
  return res.data?.containers ?? []
}

/**
 * streamContainerLogsURL builds the SSE URL for per-container logs.
 * Pair with useSSE; the backend emits SSE events of type
 * "container.log" with {stream, line, ts} payloads.
 */
export function streamContainerLogsURL(appId: string, containerName: string, tail = 100): string {
  return `/api/v1/apps/${appId}/containers/${encodeURIComponent(containerName)}/logs?tail=${tail}`
}

/**
 * streamAppLogsURL is the legacy single-container endpoint. Kept for
 * apps that haven't deployed yet (no live containers to choose from)
 * and for the CLI's default behavior.
 */
export function streamAppLogsURL(appId: string, tail = 100): string {
  return `/api/v1/apps/${appId}/logs?tail=${tail}`
}

/**
 * fetchContainerDetail returns the rich projection of one container —
 * image, state, env, mounts, labels, etc. Returned shape includes a
 * `preview` flag set when the container is a Compose-YAML preview
 * (no live container yet); the UI hides runtime-only sections in
 * that case.
 *
 * 404 → container is gone (was pruned, never existed). Caller should
 * route the operator back to the app's containers tab.
 */
export async function fetchContainerDetail(appId: string, name: string): Promise<ContainerDetail> {
  const res = await useApi().get<ContainerDetail>(
    `/apps/${appId}/containers/${encodeURIComponent(name)}`,
  )
  return res.data as ContainerDetail
}

/**
 * fetchContainerStats returns a single CPU% + memory snapshot for one
 * container. The backend reads two Docker stats samples ~1s apart to
 * derive CPU%, so each call has roughly that latency. The detail
 * view polls every few seconds; nothing more frequent than that —
 * the SDK call is hot.
 *
 * 409 → container exists but isn't running (stats would be
 * meaningless). The UI handles that by hiding the panel.
 */
export async function fetchContainerStats(appId: string, name: string): Promise<AppStats> {
  const res = await useApi().get<AppStats>(
    `/apps/${appId}/containers/${encodeURIComponent(name)}/stats`,
  )
  return res.data as AppStats
}

/**
 * streamAppEventsURL is the SSE URL that fans out every deploy.* /
 * app.* event for one app. The DeploymentDetailView subscribes to this
 * (filtering by `deployment_id` on the payload) instead of a deploy-id
 * scoped endpoint — the bus already publishes on deploy.<id>.* topics
 * but we don't expose a per-deployment SSE route. Same source, just a
 * client-side filter, which keeps the backend surface small.
 */
export function streamAppEventsURL(appId: string): string {
  return `/api/v1/apps/${appId}/events`
}

/**
 * fetchDeployment returns a single deployment row. Used by the
 * deployment-detail view to render the header (commit/branch/status/
 * timing) and to decide whether to subscribe to live events vs.
 * tail the persisted log file.
 */
export async function fetchDeployment(deploymentId: string): Promise<Deployment> {
  const res = await useApi().get<Deployment>(`/deployments/${deploymentId}`)
  return res.data as Deployment
}

/**
 * fetchDeploymentLogs returns the persisted build log as a single
 * plain-text dump. Only used for finished deployments — while a
 * deployment is in-flight, the live SSE feed is the source of truth
 * (it carries the same lines plus state-change events).
 *
 * `tail` caps the tail length when the log is huge; 0 means "whole
 * file" (the backend default).
 */
export async function fetchDeploymentLogs(deploymentId: string, tail = 0): Promise<string> {
  const url = tail > 0
    ? `/deployments/${deploymentId}/logs?tail=${tail}`
    : `/deployments/${deploymentId}/logs`
  const res = await useApi().get<string>(url, {
    // The backend writes plain text, not JSON. Tell Axios so it
    // doesn't try to parse and silently swallow the body.
    transformResponse: [(d) => d],
    responseType: 'text',
  })
  return typeof res.data === 'string' ? res.data : ''
}

/**
 * Per-app tag operations. The backend (handler/tag.go) hangs these
 * under `/apps/:id/tags` so the per-resource RBAC borrows the app's
 * TeamID — global `/tags` is open to every authenticated user, but
 * attaching/detaching needs the same `apps:update` permission as any
 * other mutation on the row.
 *
 * Wrapped here (instead of in the apps store) because tags live in a
 * separate table and aren't part of the App's PATCH payload — keeping
 * them out of the store avoids the store conflating two write paths
 * into one "what does PATCH cover?" question.
 */
export const appsService = {
  /**
   * Returns the full Tag rows attached to an app — colour included
   * so the UI can render chips without a follow-up call to `/tags`.
   */
  async listTags(appId: string): Promise<Tag[]> {
    const res = await useApi().get<Tag[]>(`/apps/${appId}/tags`)
    return res.data ?? []
  },

  /**
   * Replace the full set of tags on an app. Unknown names are
   * created on the fly by the backend so the UI can send one PUT
   * after the user finishes editing chips instead of orchestrating
   * create-then-attach round-trips.
   */
  async updateTags(appId: string, tags: string[]): Promise<Tag[]> {
    const res = await useApi().put<Tag[]>(`/apps/${appId}/tags`, { tags })
    return res.data ?? []
  },
}
