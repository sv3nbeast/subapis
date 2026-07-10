const MIXED_CHANNEL_PLATFORMS = new Set([
  'anthropic',
  'antigravity',
  'kiro',
  'droid'
])

export function needsMixedChannelCheck(platform: string | null | undefined): boolean {
  return MIXED_CHANNEL_PLATFORMS.has((platform || '').trim().toLowerCase())
}
