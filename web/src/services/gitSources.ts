import { useApi } from '@/composables/useApi'
import type { GitBranch, GitRepository, GitSource, RepositoryInspect } from '@/types/api'

type ListResponse<T> = { items: T[]; error?: string }

/**
 * Service for /git-sources.
 *
 * Post-009 model: each git_source carries its own GitHub App
 * credentials. The only path to create a github_app row is the
 * manifest flow (manifestURL → opener navigates → manifestCallback
 * creates the row → install callback → finalize). Direct POST with
 * type='github_app' is rejected by the backend.
 */
export const gitSourcesService = {
  async list(): Promise<GitSource[]> {
    const api = useApi()
    const res = await api.get<ListResponse<GitSource>>('/git-sources')
    return res.data.items ?? []
  },

  async create(body: {
    type: 'ssh_key' | 'token'
    name: string
    generate?: boolean
    private_key?: string
    token?: string
  }): Promise<GitSource> {
    const api = useApi()
    const res = await api.post<GitSource>('/git-sources', body)
    return res.data
  },

  async remove(id: string): Promise<void> {
    const api = useApi()
    await api.delete(`/git-sources/${id}`)
  },

  /**
   * Per-source install URL. Used when the operator wants to add the
   * App to another repo, or to reopen the install on the GitHub side
   * after it was revoked. Empty until the source has an app_slug.
   */
  async installURL(sourceId: string): Promise<string> {
    const api = useApi()
    const res = await api.get<{ url: string }>(`/git-sources/${sourceId}/install-url`)
    return res.data.url
  },

  async manifestURL(baseURL: string, opts: { account?: 'personal' | 'org'; org?: string } = {}): Promise<string> {
    const api = useApi()
    const res = await api.post<{ url: string }>('/git-sources/github/manifest-url', {
      base_url: baseURL,
      account: opts.account ?? 'personal',
      org: opts.org,
    })
    return res.data.url
  },

  /**
   * Stamps a pending github_app source with the installation_id the
   * operator just authorised on GitHub. Backend calls the GitHub API
   * with the source's own App JWT to resolve account_login /
   * account_type, then writes them onto the row.
   */
  async finalize(sourceId: string, installationId: string): Promise<GitSource> {
    const api = useApi()
    const res = await api.post<GitSource>(`/git-sources/${sourceId}/finalize`, {
      installation_id: installationId,
    })
    return res.data
  },

  async repositories(sourceId: string, q = ''): Promise<ListResponse<GitRepository>> {
    const api = useApi()
    const res = await api.get<ListResponse<GitRepository>>(`/git-sources/${sourceId}/repositories`, {
      params: { q, per_page: 60 },
    })
    return res.data
  },

  async branches(sourceId: string, fullName: string): Promise<ListResponse<GitBranch>> {
    const [owner, repo] = fullName.split('/')
    const api = useApi()
    const res = await api.get<ListResponse<GitBranch>>(`/git-sources/${sourceId}/repositories/${owner}/${repo}/branches`)
    return res.data
  },

  async inspect(sourceId: string, fullName: string, branch: string): Promise<RepositoryInspect> {
    const [owner, repo] = fullName.split('/')
    const api = useApi()
    const res = await api.post<RepositoryInspect>(`/git-sources/${sourceId}/repositories/inspect`, { owner, repo, branch })
    return res.data
  },
}
