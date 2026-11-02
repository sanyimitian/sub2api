import { apiClient } from '../client'

export interface PublicTransitExportSections {
  billing: boolean
  groups: boolean
  monitoring: boolean
  passive_monitoring: boolean
  cache: boolean
  disclosure: boolean
  limits: boolean
  completeness: boolean
  metrics?: Record<string, PublicTransitMetricRange>
}

export interface PublicTransitMetricRange {
  enabled: boolean
  min?: number | null
  max?: number | null
}

export interface PublicTransitMetricField {
  path: string
  kind: 'integer' | 'decimal'
  unit: string
}

export interface PublicTransitExportPreview {
  full: Record<string, unknown>
  public: Record<string, unknown>
}

export async function getPublicTransitExportSections(): Promise<PublicTransitExportSections> {
  const { data } = await apiClient.get<PublicTransitExportSections>('/admin/settings/public-transit-export')
  return data
}

export async function getPublicTransitExportMetrics(): Promise<PublicTransitMetricField[]> {
  const { data } = await apiClient.get<PublicTransitMetricField[]>('/admin/settings/public-transit-export/metrics')
  return data
}

export async function updatePublicTransitExportSections(sections: PublicTransitExportSections): Promise<PublicTransitExportSections> {
  const { data } = await apiClient.put<PublicTransitExportSections>('/admin/settings/public-transit-export', sections)
  return data
}

export async function getPublicTransitExportPreview(range: '90m' | '24h' | '7d' | '30d' = '24h'): Promise<PublicTransitExportPreview> {
  const { data } = await apiClient.get<PublicTransitExportPreview>('/admin/settings/public-transit-export/preview', { params: { range } })
  return data
}
