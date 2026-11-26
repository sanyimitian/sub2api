import { apiClient } from '../client'
import type { LocaleCode } from '@/i18n'

export interface RegionAccessSettings {
  enabled: boolean
  site_wide: boolean
  default_language: LocaleCode
}

export async function getRegionAccessSettings(): Promise<RegionAccessSettings> {
  const { data } = await apiClient.get<RegionAccessSettings>('/admin/settings/region-access')
  return data
}

export async function updateRegionAccessSettings(settings: RegionAccessSettings): Promise<RegionAccessSettings> {
  const { data } = await apiClient.put<RegionAccessSettings>('/admin/settings/region-access', settings)
  return data
}
