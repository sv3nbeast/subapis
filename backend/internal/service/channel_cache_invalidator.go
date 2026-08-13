package service

// ChannelCacheInvalidator is the narrow dependency used by group updates to
// invalidate the in-process channel lookup cache.
type ChannelCacheInvalidator interface {
	InvalidateCache()
}
