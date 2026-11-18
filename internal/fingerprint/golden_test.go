package fingerprint

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

type goldenCase struct {
	Name            string      `json:"name"`
	Numbers         []int       `json:"numbers"`
	Features        []float64   `json:"features"`
	OrderedFeatures []float64   `json:"ordered_features"`
	MarginalScores  []float64   `json:"marginal_scores"`
	OrderedScores   []float64   `json:"ordered_scores"`
	Candidates      []Candidate `json:"candidates"`
}

type goldenBatch struct {
	Name       string      `json:"name"`
	CaseNames  []string    `json:"case_names"`
	Candidates []Candidate `json:"candidates"`
}

type goldenFixture struct {
	Schema           int               `json:"schema"`
	AlgorithmVersion string            `json:"algorithm_version"`
	UpstreamCommit   string            `json:"upstream_commit"`
	CanonicalSHA256  map[string]string `json:"canonical_sha256"`
	Cases            []goldenCase      `json:"cases"`
	Batches          []goldenBatch     `json:"batches"`
	Challenges       []Challenge       `json:"challenges,omitempty"`
}

func TestGoldenParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected goldenFixture
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	if expected.Schema != 1 || expected.AlgorithmVersion != AlgorithmVersion ||
		expected.UpstreamCommit != "6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2" || len(expected.Cases) != 28 || len(expected.Batches) != 4 {
		t.Fatal("invalid pinned golden metadata/corpus")
	}
	bank, err := loadLegacyBank()
	if err != nil {
		t.Fatal(err)
	}
	maxError := map[string]float64{}
	compareVector := func(t *testing.T, label string, got, want []float64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s dimension=%d, want %d", label, len(got), len(want))
		}
		for i, value := range got {
			error := math.Abs(value - want[i])
			if math.IsNaN(value) || math.IsInf(value, 0) || error > 1e-12 {
				t.Fatalf("%s[%d] got=%.17g want=%.17g abs_error=%.17g", label, i, value, want[i], error)
			}
			maxError[label] = math.Max(maxError[label], error)
		}
	}
	compareCandidates := func(t *testing.T, got, want []Candidate) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("candidate count=%d, want %d", len(got), len(want))
		}
		for i, c := range got {
			if c.Model != want[i].Model || c.Family != want[i].Family {
				t.Fatalf("rank[%d]=%s/%s, want %s/%s", i, c.Model, c.Family, want[i].Model, want[i].Family)
			}
			compareVector(t, "score", []float64{c.Score}, []float64{want[i].Score})
			compareVector(t, "distance", []float64{c.Distance}, []float64{want[i].Distance})
		}
	}
	observed := expected
	observed.Cases = nil
	observed.Batches = nil
	observed.Challenges = DefaultChallenges()
	byName := map[string]goldenCase{}
	for _, sample := range expected.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			features, err := Features(sample.Numbers)
			if err != nil {
				t.Fatal(err)
			}
			ordered, err := OrderedFeatures(sample.Numbers)
			if err != nil {
				t.Fatal(err)
			}
			marginal, orderedScores, _ := scoreNumbers(sample.Numbers, bank)
			candidates, err := Rank(sample.Numbers)
			if err != nil {
				t.Fatal(err)
			}
			compareVector(t, "features", features, sample.Features)
			compareVector(t, "ordered_features", ordered, sample.OrderedFeatures)
			compareVector(t, "marginal_scores", marginal, sample.MarginalScores)
			compareVector(t, "ordered_scores", orderedScores, sample.OrderedScores)
			compareCandidates(t, candidates, sample.Candidates)
			item := goldenCase{sample.Name, sample.Numbers, features, ordered, marginal, orderedScores, candidates}
			observed.Cases = append(observed.Cases, item)
			byName[sample.Name] = item
		})
	}
	for _, batch := range expected.Batches {
		t.Run(batch.Name, func(t *testing.T) {
			counts := make([]float64, featureDimension)
			scores := make([]float64, len(bank.Models))
			for _, name := range batch.CaseNames {
				sample, ok := byName[name]
				if !ok {
					t.Fatalf("batch refers to missing case %q", name)
				}
				_, _, perOutput := scoreNumbers(sample.Numbers, bank)
				for i, value := range perOutput {
					scores[i] += value
				}
				for _, value := range sample.Numbers {
					counts[value-1]++
				}
			}
			for i := range scores {
				scores[i] /= float64(len(batch.CaseNames))
			}
			candidates := rankCandidates(scores, counts, bank)
			compareCandidates(t, candidates, batch.Candidates)
			observed.Batches = append(observed.Batches, goldenBatch{batch.Name, batch.CaseNames, candidates})
		})
	}
	if t.Failed() {
		return
	}
	t.Logf("JS/GO cases=%d batches=%d ranks_equal=true max_abs_features=%.17g max_abs_ordered=%.17g max_abs_score=%.17g max_abs_distance=%.17g",
		len(observed.Cases), len(observed.Batches), maxError["features"], maxError["ordered_features"], maxError["score"], maxError["distance"])
	if path := os.Getenv("FINGERPRINT_GO_OBSERVED"); path != "" {
		data, err := json.MarshalIndent(observed, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
