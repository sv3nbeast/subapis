//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPsComputeValidityDaysNormalizesUnitAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		days int
		unit string
		want int
	}{
		{name: "blank unit is already days", days: 30, unit: "", want: 30},
		{name: "day unit", days: 30, unit: "day", want: 30},
		{name: "plural days unit", days: 30, unit: "days", want: 30},
		{name: "week unit", days: 2, unit: "week", want: 14},
		{name: "plural weeks unit", days: 2, unit: "weeks", want: 14},
		{name: "month unit", days: 1, unit: "month", want: 30},
		{name: "plural months unit", days: 1, unit: "months", want: 30},
		{name: "unit is trimmed and case insensitive", days: 1, unit: " Months ", want: 30},
		{name: "unknown unit falls back to days", days: 3, unit: "custom", want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, psComputeValidityDays(tt.days, tt.unit))
		})
	}
}
