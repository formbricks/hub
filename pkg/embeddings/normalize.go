// Package embeddings provides utilities for embedding vectors (e.g. L2 normalization).
package embeddings

import (
	"math"
)

// NormalizeL2 takes a raw embedding vector and normalizes it to a length of 1.
// It modifies the slice in-place to save memory allocations during high-volume webhook ingestion.
func NormalizeL2(vector []float32) {
	var sumSquares float64

	// 1. Calculate the sum of the squared values
	for _, v := range vector {
		sumSquares += float64(v) * float64(v)
	}

	// Avoid division by zero (though a valid AI embedding will never be all zeros)
	if sumSquares == 0 {
		return
	}

	// 2. Find the square root (the magnitude/length of the vector)
	magnitude := math.Sqrt(sumSquares)

	// 3. Divide each dimension by the magnitude
	for i := range vector {
		vector[i] = float32(float64(vector[i]) / magnitude)
	}
}
