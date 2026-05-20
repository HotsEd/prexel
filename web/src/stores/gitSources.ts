import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { gitSourcesService } from '@/services/gitSources'
import type { GitBranch, GitRepository, GitSource, RepositoryInspect } from '@/types/api'

export const useGitSourcesStore = defineStore('gitSources', () => {
  const sources = ref<GitSource[]>([])
  const repositories = ref<Record<string, GitRepository[]>>({})
  const branches = ref<Record<string, GitBranch[]>>({})
  const loading = ref(false)
  const repoLoading = ref(false)
  const lastRepoError = ref<string | null>(null)

  // Only github_app sources that have completed the install flow show
  // up in the picker — pending rows would mislead callers (the
  // backend rejects repo listings / clones on them anyway).
  const githubSources = computed(() =>
    sources.value.filter((source) => source.type === 'github_app' && !source.pending_install),
  )

  // Pending rows are surfaced separately so the UI can render a
  // "Finalize install" CTA at the top of the git-sources list.
  const pendingGitHubSources = computed(() =>
    sources.value.filter((source) => source.type === 'github_app' && source.pending_install),
  )

  async function fetchAll() {
    loading.value = true
    try {
      sources.value = await gitSourcesService.list()
    } finally {
      loading.value = false
    }
  }

  /**
   * Open the manifest flow window. Always goes through manifest now:
   * post-009 there is no global GitHub App to reuse — every install
   * starts a fresh App registration. The opener should listen for
   * the `prexel:github-app-created` postMessage (carrying `source_id`)
   * and then for `prexel:github-app-installed` (carrying
   * `installation_id`), then call `finalizeGitHubSource`.
   */
  async function connectGitHub(opts: { account?: 'personal' | 'org'; org?: string } = {}): Promise<Window | null> {
    const url = await gitSourcesService.manifestURL(window.location.origin, opts)
    return window.open(url, 'prexel-github-install', 'width=1040,height=760')
  }

  /**
   * Reopen the install picker for an existing source (e.g. operator
   * wants to add the App to a new repo). Uses the source's own
   * install URL — different from `connectGitHub` which starts a brand
   * new App registration.
   */
  async function openInstall(sourceId: string): Promise<Window | null> {
    const url = await gitSourcesService.installURL(sourceId)
    return window.open(url, 'prexel-github-install', 'width=1040,height=760')
  }

  /**
   * Stamp installation_id + account_login/account_type onto a row
   * created earlier by the manifest callback. Replaces the legacy
   * `createGitHubSource(installationId)` — there's no separate
   * "create" step now, only "finalize an existing pending row".
   */
  async function finalizeGitHubSource(sourceId: string, installationId: string): Promise<GitSource> {
    const updated = await gitSourcesService.finalize(sourceId, installationId)
    sources.value = sources.value.map((s) => (s.id === updated.id ? updated : s))
    return updated
  }

  async function createTokenSource(name: string, token: string): Promise<GitSource> {
    const source = await gitSourcesService.create({ type: 'token', name, token })
    sources.value = [source, ...sources.value]
    return source
  }

  async function createSSHSource(name: string): Promise<GitSource> {
    const source = await gitSourcesService.create({ type: 'ssh_key', name, generate: true })
    sources.value = [source, ...sources.value]
    return source
  }

  async function remove(id: string) {
    await gitSourcesService.remove(id)
    sources.value = sources.value.filter((source) => source.id !== id)
    delete repositories.value[id]
  }

  async function fetchRepositories(sourceId: string, q = ''): Promise<GitRepository[]> {
    repoLoading.value = true
    lastRepoError.value = null
    try {
      const res = await gitSourcesService.repositories(sourceId, q)
      repositories.value[sourceId] = res.items ?? []
      lastRepoError.value = res.error ?? null
      return repositories.value[sourceId] ?? []
    } finally {
      repoLoading.value = false
    }
  }

  async function fetchBranches(sourceId: string, fullName: string): Promise<GitBranch[]> {
    const key = `${sourceId}:${fullName}`
    const res = await gitSourcesService.branches(sourceId, fullName)
    branches.value[key] = res.items ?? []
    return branches.value[key] ?? []
  }

  async function inspectRepository(sourceId: string, fullName: string, branch: string): Promise<RepositoryInspect> {
    return gitSourcesService.inspect(sourceId, fullName, branch)
  }

  return {
    sources,
    repositories,
    branches,
    loading,
    repoLoading,
    lastRepoError,
    githubSources,
    pendingGitHubSources,
    fetchAll,
    connectGitHub,
    openInstall,
    finalizeGitHubSource,
    createTokenSource,
    createSSHSource,
    remove,
    fetchRepositories,
    fetchBranches,
    inspectRepository,
  }
})
