package houses

import (
	"math"
	"testing"
)

func TestSpearman(t *testing.T) {
	if r := spearman([]float64{1, 2, 3, 4}, []float64{10, 20, 30, 40}); math.Abs(r-1) > 1e-9 {
		t.Fatalf("тот же порядок — +1, а не %v", r)
	}
	if r := spearman([]float64{1, 2, 3, 4}, []float64{4, 3, 2, 1}); math.Abs(r+1) > 1e-9 {
		t.Fatalf("обратный порядок — −1, а не %v", r)
	}
	if r := ranks([]float64{5, 1, 5}); r[0] != 2.5 || r[1] != 1 || r[2] != 2.5 {
		t.Fatalf("одинаковым — средний ранг: %v", r)
	}
}
