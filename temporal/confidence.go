// Package temporal provides confidence decay calculations and time-window
// validity checks for knowledge graph pages.
package temporal

import (
	"math"
	"time"
)

// DecayedConfidence computes the decayed confidence value using exponential
// decay: base × exp(-rate × daysSinceVerified). If lastVerified is the zero
// time or rate is 0 the base confidence is returned unchanged. The result is
// clamped to [0, 1].
func DecayedConfidence(baseConfidence, decayRate float64, lastVerified time.Time) float64 {
	return DecayedConfidenceAt(baseConfidence, decayRate, lastVerified, time.Now())
}

// DecayedConfidenceAt is the deterministic variant of DecayedConfidence. It
// accepts an explicit "now" time so callers (especially tests) can control the
// reference point.
func DecayedConfidenceAt(baseConfidence, decayRate float64, lastVerified, now time.Time) float64 {
	if lastVerified.IsZero() || decayRate == 0 {
		return clamp01(baseConfidence)
	}
	days := now.Sub(lastVerified).Hours() / 24
	if days < 0 {
		days = 0
	}
	return clamp01(baseConfidence * math.Exp(-decayRate*days))
}

// IsValid returns true when now falls within the [validFrom, validUntil]
// window. A zero time on either bound is treated as unbounded on that side.
func IsValid(validFrom, validUntil, now time.Time) bool {
	if !validFrom.IsZero() && now.Before(validFrom) {
		return false
	}
	if !validUntil.IsZero() && now.After(validUntil) {
		return false
	}
	return true
}

// EffectiveConfidence returns the decayed confidence if now is within the
// validity window, or 0 if outside it.
func EffectiveConfidence(baseConfidence, decayRate float64, lastVerified, validFrom, validUntil, now time.Time) float64 {
	if !IsValid(validFrom, validUntil, now) {
		return 0
	}
	return DecayedConfidenceAt(baseConfidence, decayRate, lastVerified, now)
}

// clamp01 restricts v to the range [0, 1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
