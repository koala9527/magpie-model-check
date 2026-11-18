package fingerprint

import (
	"math"
	"reflect"
	"testing"
)

func TestFeaturesHellinger(t *testing.T) {
	got, err := Features([]int{1, 1, 355})
	if err != nil || len(got) != 355 {
		t.Fatalf("Features dimension=%d, err=%v", len(got), err)
	}
	total := 3.0 + 0.5*355
	for i, value := range got {
		count := 0.0
		if i == 0 {
			count = 2
		} else if i == 354 {
			count = 1
		}
		if want := math.Sqrt((count + 0.5) / total); value != want {
			t.Fatalf("feature[%d]=%.17g, want %.17g", i, value, want)
		}
	}
}

func TestOrderedFeaturesSplitAndLastDigit(t *testing.T) {
	// np.array_split and the JS equivalent assign the remainder to early chunks:
	// [1, 355], [23], [24], [10]. This covers a bin edge and last-digit zero.
	got, err := OrderedFeatures([]int{1, 355, 23, 24, 10})
	if err != nil || len(got) != 74 {
		t.Fatalf("OrderedFeatures dimension=%d, err=%v", len(got), err)
	}
	for block, bins := range []map[int]int{{0: 1, 15: 1}, {0: 1}, {1: 1}, {0: 1}} {
		count := 1.0
		if block == 0 {
			count = 2
		}
		for bin := range 16 {
			want := math.Sqrt((float64(bins[bin]) + 0.5) / (count + 8))
			if got[block*16+bin] != want {
				t.Fatalf("block=%d bin=%d got=%.17g want=%.17g", block, bin, got[block*16+bin], want)
			}
		}
	}
	for digit := range 10 {
		count := 0.0
		if digit == 0 || digit == 1 || digit == 3 || digit == 4 || digit == 5 {
			count = 1
		}
		if want := math.Sqrt((count + 0.5) / 10); got[64+digit] != want {
			t.Fatalf("last digit=%d got=%.17g want=%.17g", digit, got[64+digit], want)
		}
	}
}

func TestFeaturesOrderAndNormalization(t *testing.T) {
	numbers := []int{1, 1, 355, 355, 23, 24, 178, 179, 2}
	original := append([]int(nil), numbers...)
	reversed := make([]int, len(numbers))
	for i, v := range numbers {
		reversed[len(numbers)-1-i] = v
	}
	first, _ := Features(numbers)
	second, _ := Features(reversed)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("marginal feature depends on order")
	}
	ordered, _ := OrderedFeatures(numbers)
	other, _ := OrderedFeatures(reversed)
	if reflect.DeepEqual(ordered, other) {
		t.Fatal("ordered blocks ignored input order")
	}
	if !reflect.DeepEqual(numbers, original) {
		t.Fatal("feature functions mutated the input")
	}
	for _, span := range [][]float64{first, ordered[:16], ordered[16:32], ordered[32:48], ordered[48:64], ordered[64:]} {
		squares := 0.0
		for _, value := range span {
			squares += value * value
		}
		if math.Abs(squares-1) > 1e-14 {
			t.Fatalf("Hellinger block squared norm=%.17g, want 1", squares)
		}
	}
}

func TestFeaturesRejectInvalidInput(t *testing.T) {
	for _, fn := range []struct {
		name string
		call func([]int) ([]float64, error)
	}{{"Features", Features}, {"OrderedFeatures", OrderedFeatures}} {
		for _, numbers := range [][]int{nil, {}, {0}, {-1}, {356}, {1, 2, 356}} {
			if values, err := fn.call(numbers); err == nil || values != nil {
				t.Fatalf("%s(%v) = %v, %v; want nil and error", fn.name, numbers, values, err)
			}
		}
		if values, err := fn.call([]int{355}); err != nil || values == nil {
			t.Fatalf("%s rejected a nonempty boundary sample: %v", fn.name, err)
		}
	}
}
