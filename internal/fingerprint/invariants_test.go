package fingerprint

import (
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
)

func TestRankTieOrderAndLegacyFallback(t *testing.T) {
	bank, err := decodeBank(LegacyBankJSON())
	if err != nil {
		t.Fatal(err)
	}
	bank.Models[0].Family = ""
	ties := rankCandidates(make([]float64, len(bank.Models)), countNumbers([]int{1, 355}), bank)
	for i, candidate := range ties {
		if candidate.Model != bank.Models[i].ID {
			t.Fatal("ties did not preserve model order")
		}
	}
	if ties[0].Family != "models" {
		t.Fatal("legacy family fallback changed")
	}
	blocks := bank.Robust.OrderedBlocks
	for _, artifact := range []*orderedArtifact{nil, blocks} {
		bank.Robust.OrderedBlocks = artifact
		if artifact != nil {
			artifact.Weight = 0
		}
		marginal, ordered, blended := scoreNumbers([]int{1, 355}, bank)
		if ordered != nil || !reflect.DeepEqual(marginal, blended) {
			t.Fatal("missing/zero-weight ordered fallback changed")
		}
		blended[0] = 99
		if marginal[0] == 99 {
			t.Fatal("fallback scores alias")
		}
	}
}

func TestNumericFloorsAndNonfiniteValidation(t *testing.T) {
	if got := standardize([]float64{2, 2, 2}); !reflect.DeepEqual(got, []float64{0, 0, 0}) {
		t.Fatalf("constant standardization: %v", got)
	}
	if got := normalized([]float64{0, 0}); !reflect.DeepEqual(got, []float64{0, 0}) {
		t.Fatalf("zero normalization: %v", got)
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if err := validateVector("test", []float64{value}, 1); err == nil {
			t.Fatal("accepted nonfinite artifact vector")
		}
	}
}

func TestConcurrentRankAndBankCopies(t *testing.T) {
	numbers := []int{1, 1, 355, 23, 24}
	expected, err := Rank(numbers)
	if err != nil {
		t.Fatal(err)
	}
	digest := LegacyBankSHA256()
	var wg sync.WaitGroup
	errors := make(chan error, 24)
	for range 24 {
		wg.Go(func() {
			copy := LegacyBankJSON()
			copy[0] = 'x'
			actual, err := Rank(numbers)
			if err != nil || !reflect.DeepEqual(actual, expected) || LegacyBankSHA256() != digest {
				errors <- fmt.Errorf("concurrent state changed: %v", err)
			}
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
