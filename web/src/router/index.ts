import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { fetchSetupStatus } from '@/composables/useSetupStatus'

const routes: RouteRecordRaw[] = [
  {
    path: '/setup',
    name: 'setup',
    component: () => import('@/pages/setup/SetupWizard.vue'),
    meta: { layout: 'bare' },
  },
  {
    path: '/login',
    name: 'login',
    component: () => import('@/pages/auth/Login.vue'),
    meta: { layout: 'bare' },
  },
  {
    path: '/two-factor-challenge',
    name: 'auth.two-factor-challenge',
    component: () => import('@/pages/auth/TwoFactorChallenge.vue'),
    meta: { layout: 'bare' },
  },
  {
    path: '/',
    component: () => import('@/layouts/AppLayout.vue'),
    meta: { requiresAuth: true },
    children: [
      { path: '', redirect: '/apps' },
      { path: 'apps', name: 'apps', component: () => import('@/pages/apps/AppsView.vue') },
      // Sits BEFORE apps/:id so the literal `new` doesn't get captured as an id.
      { path: 'apps/new', name: 'apps.new', component: () => import('@/pages/apps/NewAppView.vue') },
      { path: 'apps/:id', name: 'apps.detail', component: () => import('@/pages/apps/AppDetailView.vue') },
      { path: 'apps/:id/logs', name: 'apps.logs', component: () => import('@/pages/apps/AppLogsView.vue') },
      // Per-deployment detail page — full-screen pipeline + live build
      // log terminal. Linked from every "Deploy" / "Rollback" / history
      // row across the product, so the operator always lands here when
      // they want to *watch* a deploy happen.
      {
        path: 'apps/:id/deployments/:depId',
        name: 'apps.deployment',
        component: () => import('@/pages/apps/DeploymentDetailView.vue'),
      },
      // Per-container detail page — image, state, env, mounts, labels,
      // logs, terminal, and stats consolidated in one screen. Linked
      // from clicking a row in the AppDetail Containers tab.
      {
        path: 'apps/:id/containers/:name',
        name: 'apps.container',
        component: () => import('@/pages/apps/ContainerDetailView.vue'),
      },
      { path: 'git', name: 'git', component: () => import('@/pages/git/GitSourcesView.vue') },
      { path: 'git/:id', name: 'git.detail', component: () => import('@/pages/git/GitSourceDetailView.vue') },
      { path: 'servers', name: 'servers', component: () => import('@/pages/servers/ServersView.vue') },
      { path: 'domains', name: 'domains', component: () => import('@/pages/domains/DomainsView.vue') },
      { path: 'domains/new', name: 'domains.new', component: () => import('@/pages/domains/AddDomainView.vue') },
      { path: 'domains/:id', name: 'domains.detail', component: () => import('@/pages/domains/DomainDetailView.vue') },
      { path: 'dns-zones', redirect: '/domains' },
      { path: 'settings/:section?', name: 'settings', component: () => import('@/pages/settings/SettingsView.vue') },
    ],
  },
  {
    path: '/:catchAll(.*)*',
    redirect: '/apps',
  },
]

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.initialized) {
    await auth.initialize()
  }

  let setupCompleted = true
  try {
    const s = await fetchSetupStatus()
    setupCompleted = s.completed
  } catch {
    setupCompleted = true
  }

  if (!setupCompleted && to.path !== '/setup') {
    return { path: '/setup' }
  }
  if (setupCompleted && to.path === '/setup') {
    return { path: auth.isAuthenticated ? '/apps' : '/login' }
  }

  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return { path: '/login', query: { next: to.fullPath } }
  }

  if (to.path === '/login' && auth.isAuthenticated) {
    return { path: '/apps' }
  }

  return true
})

export default router
