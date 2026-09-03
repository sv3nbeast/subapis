package service

func canCopyAccountsFromGroupPlatform(targetPlatform, sourcePlatform string) bool {
	if targetPlatform == PlatformComposite {
		return sourcePlatform == PlatformComposite || isConcreteRequestPlatform(sourcePlatform)
	}
	return sourcePlatform == targetPlatform
}

func groupSupportsOpenAIFast(platform string) bool {
	return platform == PlatformOpenAI || platform == PlatformComposite
}
