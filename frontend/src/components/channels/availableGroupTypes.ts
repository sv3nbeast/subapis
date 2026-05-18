import type { UserAvailableGroup, UserSupportedModel } from '@/api/channels'

export interface AvailableGroupChannelView {
  channelName: string
  channelDescription: string
  platform: string
  supportedModels: UserSupportedModel[]
}

export interface AvailableGroupView {
  group: UserAvailableGroup
  channels: AvailableGroupChannelView[]
  channelCount: number
  modelCount: number
  billingModes: string[]
}
