package embeddings

import (
	"math"
	"testing"
)

func TestNormalizeL2(t *testing.T) {
	t.Run("unit vector unchanged", func(t *testing.T) {
		v := []float32{1, 0, 0}
		NormalizeL2(v)

		if v[0] != 1 || v[1] != 0 || v[2] != 0 {
			t.Errorf("unit vector changed: got %v", v)
		}
	})

	t.Run("normalizes to unit length", func(t *testing.T) {
		vec := []float32{3, 4}
		NormalizeL2(vec)
		// 3-4-5 triangle => magnitude 5 => expected (0.6, 0.8)
		const tol = 1e-5
		if math.Abs(float64(vec[0])-0.6) > tol || math.Abs(float64(vec[1])-0.8) > tol {
			t.Errorf("expected (0.6, 0.8), got (%f, %f)", vec[0], vec[1])
		}

		mag := math.Sqrt(float64(vec[0]*vec[0] + vec[1]*vec[1]))
		if math.Abs(mag-1) > tol {
			t.Errorf("magnitude should be 1, got %f", mag)
		}
	})

	t.Run("zero vector does not panic", func(t *testing.T) {
		v := []float32{0, 0, 0}
		NormalizeL2(v)

		if v[0] != 0 || v[1] != 0 || v[2] != 0 {
			t.Errorf("zero vector should remain unchanged: got %v", v)
		}
	})

	t.Run("modifies in place", func(t *testing.T) {
		vec := []float32{1, 1, 1}
		NormalizeL2(vec)

		expected := float32(1 / math.Sqrt(3))

		const tol = 1e-5
		for i := range vec {
			if math.Abs(float64(vec[i]-expected)) > tol {
				t.Errorf("vec[%d] = %f, want %f", i, vec[i], expected)
			}
		}
	})
}
