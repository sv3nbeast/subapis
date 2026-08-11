// Source-faithful, namespaced integration of gateway_kiro_helpers.go from
// github.com/nianzs/sub2api at d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb.
// Only package identifiers and the kiro package import are rewritten so the
// legacy engine remains available for an immediate rollback.

package service

import "github.com/tidwall/gjson"

func nianzsKiroCreditsFromUsageGJSON(usage gjson.Result) float64 {
	if !usage.Exists() {
		return 0
	}
	for _, key := range []string{"_sub2api_kiro_credits", "kiro_credits", "kiroCredits", "credits", "creditsUsed", "creditUsage"} {
		if v := usage.Get(key); v.Exists() && v.Float() > 0 {
			return v.Float()
		}
	}
	return 0
}
