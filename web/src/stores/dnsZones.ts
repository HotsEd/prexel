/**
 * DNS Zones store.
 *
 * Holds the full list of zones plus thin helpers used by the domain wizard
 * (e.g. to look up the existing zone for an apex before issuing a Create).
 * Mutations always update the local cache so the table reflects the latest
 * verification timestamps without a refetch.
 */
import { ref } from 'vue'
import { defineStore } from 'pinia'
import { dnsZonesService, type DNSZone, type VerificationResult } from '@/services/dnsZones'

export const useDNSZonesStore = defineStore('dnsZones', () => {
    const zones = ref<DNSZone[]>([])
    const loading = ref(false)
    let loaded = false

    async function load(force = false): Promise<DNSZone[]> {
        if (loaded && !force) return zones.value
        loading.value = true
        try {
            zones.value = await dnsZonesService.list()
            loaded = true
            return zones.value
        } finally {
            loading.value = false
        }
    }

    function getByApex(apex: string): DNSZone | undefined {
        const needle = apex.toLowerCase()
        return zones.value.find((z) => z.apex.toLowerCase() === needle)
    }

    function getById(id: string): DNSZone | undefined {
        return zones.value.find((z) => z.id === id)
    }

    function upsert(zone: DNSZone): DNSZone {
        const idx = zones.value.findIndex((z) => z.id === zone.id)
        if (idx === -1) zones.value.push(zone)
        else zones.value[idx] = { ...zones.value[idx], ...zone }
        return zone
    }

    async function create(apex: string, notes?: string): Promise<DNSZone> {
        const zone = await dnsZonesService.create(apex, notes)
        upsert(zone)
        return zone
    }

    async function updateNotes(id: string, notes: string): Promise<DNSZone> {
        const zone = await dnsZonesService.updateNotes(id, notes)
        upsert(zone)
        return zone
    }

    async function remove(id: string): Promise<void> {
        await dnsZonesService.remove(id)
        zones.value = zones.value.filter((z) => z.id !== id)
    }

    async function verify(id: string, hostname?: string): Promise<VerificationResult> {
        const result = await dnsZonesService.verify(id, hostname)
        // Refetch the zone so we get the new verified_at timestamps.
        const fresh = await dnsZonesService.get(id)
        upsert(fresh)
        return result
    }

    return {
        zones, loading,
        load, getByApex, getById, upsert,
        create, updateNotes, remove, verify,
    }
})
