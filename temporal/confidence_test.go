package temporal

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDecayedConfidenceAt(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		baseConfidence float64
		decayRate      float64
		lastVerified   time.Time
		now            time.Time
		wantApprox     float64
		tolerance      float64
	}{
		{
			name:           "365 days decay",
			baseConfidence: 0.9,
			decayRate:      0.05,
			lastVerified:   base,
			now:            base.AddDate(1, 0, 0), // 365 days
			wantApprox:     0.9 * math.Exp(-0.05*365),
			tolerance:      0.001,
		},
		{
			name:           "zero rate returns base",
			baseConfidence: 0.9,
			decayRate:      0,
			lastVerified:   base,
			now:            base.AddDate(10, 0, 0),
			wantApprox:     0.9,
			tolerance:      1e-9,
		},
		{
			name:           "zero lastVerified returns base",
			baseConfidence: 0.8,
			decayRate:      0.1,
			lastVerified:   time.Time{},
			now:            base,
			wantApprox:     0.8,
			tolerance:      1e-9,
		},
		{
			name:           "future lastVerified clamps days to 0",
			baseConfidence: 0.7,
			decayRate:      0.05,
			lastVerified:   base.AddDate(0, 0, 10),
			now:            base,
			wantApprox:     0.7, // days = 0
			tolerance:      1e-9,
		},
		{
			name:           "base above 1 clamped",
			baseConfidence: 1.5,
			decayRate:      0,
			lastVerified:   time.Time{},
			now:            base,
			wantApprox:     1.0,
			tolerance:      1e-9,
		},
		{
			name:           "base below 0 clamped",
			baseConfidence: -0.5,
			decayRate:      0,
			lastVerified:   time.Time{},
			now:            base,
			wantApprox:     0.0,
			tolerance:      1e-9,
		},
		{
			name:           "exactly 0 confidence",
			baseConfidence: 0,
			decayRate:      0.05,
			lastVerified:   base,
			now:            base.AddDate(0, 0, 30),
			wantApprox:     0,
			tolerance:      1e-9,
		},
		{
			name:           "exactly 1 confidence no decay",
			baseConfidence: 1.0,
			decayRate:      0,
			lastVerified:   base,
			now:            base,
			wantApprox:     1.0,
			tolerance:      1e-9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecayedConfidenceAt(tt.baseConfidence, tt.decayRate, tt.lastVerified, tt.now)
			assert.InDelta(t, tt.wantApprox, got, tt.tolerance)
		})
	}
}

func TestIsValid(t *testing.T) {
	now := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		validFrom  time.Time
		validUntil time.Time
		now        time.Time
		want       bool
	}{
		{"within window", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), now, true},
		{"before window", time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), now, false},
		{"after window", time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), now, false},
		{"unbounded from", time.Time{}, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), now, true},
		{"unbounded until", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), time.Time{}, now, true},
		{"fully unbounded", time.Time{}, time.Time{}, now, true},
		{"same from and until as now", now, now, now, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValid(tt.validFrom, tt.validUntil, tt.now)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEffectiveConfidence(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)

	t.Run("within window returns decayed", func(t *testing.T) {
		got := EffectiveConfidence(0.9, 0.01, base, base, base.AddDate(1, 0, 0), now)
		expected := DecayedConfidenceAt(0.9, 0.01, base, now)
		assert.InDelta(t, expected, got, 1e-9)
	})

	t.Run("outside window returns 0", func(t *testing.T) {
		got := EffectiveConfidence(0.9, 0.01, base, base, base.AddDate(0, 3, 0), now)
		assert.Equal(t, 0.0, got)
	})
}

func TestClamp01(t *testing.T) {
	tests := []struct {
		in, want float64
	}{
		{-1, 0},
		{0, 0},
		{0.5, 0.5},
		{1, 1},
		{2, 1},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, clamp01(tt.in))
	}
}
