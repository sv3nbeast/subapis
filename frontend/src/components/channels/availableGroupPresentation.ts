import { platformLabel } from '@/utils/platformColors'

/**
 * User-facing provider label for the available-groups page.
 *
 * `kiro` is an internal routing/account platform. End users consume the
 * Anthropic-compatible service exposed by those AWS-backed groups, so the
 * implementation detail must not leak into the public group catalogue. Admin
 * and gateway code continue to use the original platform value.
 */
export function availableGroupPlatformLabel(platform: string): string {
  if (platform.trim().toLowerCase() === 'kiro') return 'Anthropic'
  return platformLabel(platform)
}
