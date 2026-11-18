package fingerprint

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestRankContract(t *testing.T) {
	numbers := []int{1, 1, 355, 23, 24}
	before := append([]int(nil), numbers...)
	candidates, err := Rank(numbers)
	if err != nil || len(candidates) != 18 {
		t.Fatalf("Rank got %d candidates, err=%v", len(candidates), err)
	}
	seen := map[string]bool{}
	for i, c := range candidates {
		if c.Model == "" || c.Family == "" || seen[c.Model] || math.IsNaN(c.Score) || math.IsInf(c.Score, 0) || c.Distance < 0 || c.Distance > 1 {
			t.Fatalf("invalid candidate: %+v", c)
		}
		seen[c.Model] = true
		if i > 0 && candidates[i-1].Score < c.Score {
			t.Fatal("rank is not descending by robust score")
		}
		bytes, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(bytes, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 4 || fields["model"] == nil || fields["family"] == nil || fields["score"] == nil || fields["distance"] == nil {
			t.Fatalf("Candidate JSON must only expose model/family/score/distance: %s", bytes)
		}
	}
	if !reflect.DeepEqual(numbers, before) {
		t.Fatal("Rank mutated the sequence")
	}
	candidates[0].Model = "mutated"
	if again, err := Rank(numbers); err != nil || again[0].Model == "mutated" {
		t.Fatal("returned candidates share mutable state")
	}
}

func TestRankRejectsInvalidInput(t *testing.T) {
	for _, numbers := range [][]int{nil, {}, {0}, {-1}, {356}, {1, 356}} {
		if candidates, err := Rank(numbers); err == nil || candidates != nil {
			t.Fatalf("Rank(%v) = %v, %v; want nil and error", numbers, candidates, err)
		}
	}
}
