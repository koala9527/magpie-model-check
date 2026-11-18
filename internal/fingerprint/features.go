// ModelTrace numeric feature port, MIT License.
// Copyright (c) 2026 xqy2006. See reference/LICENSE.
package fingerprint

import (
	"errors"
	"fmt"
	"math"
)

const (
	valueMin         = 1
	valueMax         = 355
	featureDimension = 355
	orderedDimension = 74
	alpha            = 0.5
)

func validateNumbers(numbers []int) error {
	if len(numbers) == 0 {
		return errors.New("fingerprint: empty sequence")
	}
	for i, value := range numbers {
		if value < valueMin || value > valueMax {
			return fmt.Errorf("fingerprint: value at index %d is outside 1..355: %d", i, value)
		}
	}
	return nil
}

func countNumbers(numbers []int) []float64 {
	counts := make([]float64, featureDimension)
	for _, value := range numbers {
		counts[value-valueMin]++
	}
	return counts
}

// Features returns the 355-dimensional, alpha=0.5 smoothed Hellinger feature.
// Any nonempty sequence in 1..355 is supported, independent of parser counts.
func Features(numbers []int) ([]float64, error) {
	if err := validateNumbers(numbers); err != nil {
		return nil, err
	}
	return hellingerFeature(countNumbers(numbers)), nil
}

func hellingerFeature(counts []float64) []float64 {
	total := alpha * featureDimension
	for _, count := range counts {
		total += count
	}
	feature := make([]float64, featureDimension)
	for i, count := range counts {
		feature[i] = math.Sqrt((count + alpha) / total)
	}
	return feature
}

// OrderedFeatures returns four 16-bin Hellinger blocks followed by ten last-digit
// bins. Remainders go to the earliest chunks, exactly as in JS/np.array_split.
// Empty chunks of short nonempty samples still receive the legacy 0.5 smoothing.
func OrderedFeatures(numbers []int) ([]float64, error) {
	if err := validateNumbers(numbers); err != nil {
		return nil, err
	}
	return orderedFeature(numbers), nil
}

func orderedFeature(numbers []int) []float64 {
	feature := make([]float64, 0, orderedDimension)
	base, remainder := len(numbers)/4, len(numbers)%4
	start := 0
	for block := range 4 {
		size := base
		if block < remainder {
			size++
		}
		bins := make([]float64, 16)
		for i := range bins {
			bins[i] = alpha
		}
		for _, value := range numbers[start : start+size] {
			bin := min(15, int(math.Floor((float64(value-1)/355)*16)))
			bins[bin]++
		}
		for _, count := range bins {
			feature = append(feature, math.Sqrt(count/(float64(size)+8)))
		}
		start += size
	}
	lastDigits := make([]float64, 10)
	for i := range lastDigits {
		lastDigits[i] = alpha
	}
	for _, value := range numbers {
		lastDigits[value%10]++
	}
	for _, count := range lastDigits {
		feature = append(feature, math.Sqrt(count/(float64(len(numbers))+5)))
	}
	return feature
}
