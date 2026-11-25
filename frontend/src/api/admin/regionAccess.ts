import { apiClient } from '../client'

export interface RegionAccessSettings {
  enabled: boolean
}

export async function getRegionAccessSettings(): Promise<RegionAccessSettings> {
  const { data } = await apiClient.get<RegionAccessSettings>('/admin/settings/region-access')
  return data
}

export async function updateRegionAccessSettings(settings: RegionAccessSettings): Promise<RegionAccessSettings> {
  const { data } = await apiClient.put<RegionAccessSettings>('/admin/settings/region-access', settings)
  return data
}
