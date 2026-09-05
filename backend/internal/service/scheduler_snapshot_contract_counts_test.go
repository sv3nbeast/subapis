//go:build unit

package service

// Every concrete platform has single/forced buckets, with mixed buckets for
// Anthropic, Gemini and the local OpenAI/Kiro bridge. Single and forced share
// one DB read per platform. Keep lifecycle counts aligned with that contract.
var (
	canonicalTestBucketCount = 2*len(schedulerSnapshotPlatforms()) + 3
	canonicalTestQueryCount  = len(schedulerSnapshotPlatforms()) + 3
)
