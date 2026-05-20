// Setup status — fetched once per app load. The router guard uses this to
// decide whether to send the user to /setup or to the dashboard.

import axios from 'axios'
import { ref } from 'vue'
import type { SetupStatus } from '@/types/api'

const status = ref<SetupStatus | null>(null)
let pending: Promise<SetupStatus> | null = null

export async function fetchSetupStatus(force = false): Promise<SetupStatus> {
  if (!force && status.value) return status.value
  if (pending) return pending
  pending = axios
    .get<SetupStatus>('/api/v1/setup/status')
    .then((r) => {
      status.value = r.data
      return r.data
    })
    .catch((err) => {
      status.value = { completed: false, instance_id: '', version: 'unknown' }
      throw err
    })
    .finally(() => {
      pending = null
    })
  return pending
}

export function useSetupStatus() {
  return { status, fetchSetupStatus }
}
