export const identificationEffortOptions = [
  { value: '', label: 'modelIdentification.defaultEffort' },
  { value: 'none', label: 'modelIdentification.effortNone' },
  { value: 'minimal', label: 'modelIdentification.effortMinimal' },
  { value: 'low', label: 'modelIdentification.effortLow' },
  { value: 'medium', label: 'modelIdentification.effortMedium' },
  { value: 'high', label: 'modelIdentification.effortHigh' },
  { value: 'xhigh', label: 'modelIdentification.effortXhigh' }
]

export function formatIdentificationEffort(
  effort: string | null | undefined,
  t: (key: string) => string
): string {
  const value = effort ?? ''
  const option = identificationEffortOptions.find((item) => item.value === value)
  return option ? t(option.label) : value
}
