package service

import (
	"fmt"
	"sort"
)

func validateAndNormalizeAccountSchedulingThresholds(input map[string]int) (map[string]int, error) {
	if input == nil {
		return defaultAccountSchedulingThresholds(), nil
	}
	out := defaultAccountSchedulingThresholds()
	for platform, threshold := range input {
		if _, ok := out[platform]; !ok {
			return nil, fmt.Errorf("unsupported account scheduling threshold platform %q", platform)
		}
		if threshold < 1 || threshold > 100 {
			return nil, fmt.Errorf("account scheduling threshold for %q must be between 1 and 100", platform)
		}
		out[platform] = threshold
	}
	return out, nil
}

func sortedAccountSchedulingThresholdPlatforms() []string {
	defaults := defaultAccountSchedulingThresholds()
	platforms := make([]string, 0, len(defaults))
	for platform := range defaults {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	return platforms
}
