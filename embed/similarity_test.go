package embed

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCosineSimilarity_Identical(t *testing.T) {
	a := []float32{1, 2, 3}
	got := CosineSimilarity(a, a)
	assert.InDelta(t, 1.0, got, 1e-6)
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	got := CosineSimilarity(a, b)
	assert.InDelta(t, 0.0, got, 1e-6)
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{-1, -2, -3}
	got := CosineSimilarity(a, b)
	assert.InDelta(t, -1.0, got, 1e-6)
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	a := []float32{0, 0, 0}
	b := []float32{1, 2, 3}
	assert.Equal(t, 0.0, CosineSimilarity(a, b))
	assert.Equal(t, 0.0, CosineSimilarity(b, a))
}

func TestCosineSimilarity_BothZero(t *testing.T) {
	a := []float32{0, 0, 0}
	assert.Equal(t, 0.0, CosineSimilarity(a, a))
}

func TestCosineSimilarity_DifferentLengths(t *testing.T) {
	a := []float32{1, 2}
	b := []float32{1, 2, 3}
	assert.Equal(t, 0.0, CosineSimilarity(a, b))
}

func TestCosineSimilarity_Empty(t *testing.T) {
	assert.Equal(t, 0.0, CosineSimilarity(nil, nil))
	assert.Equal(t, 0.0, CosineSimilarity([]float32{}, []float32{}))
}

func TestCosineSimilarity_KnownValue(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{4, 5, 6}
	// dot = 32, |a| = sqrt(14), |b| = sqrt(77)
	expected := 32.0 / (math.Sqrt(14) * math.Sqrt(77))
	got := CosineSimilarity(a, b)
	assert.InDelta(t, expected, got, 1e-6)
}
