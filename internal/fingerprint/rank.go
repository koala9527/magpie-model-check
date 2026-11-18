// ModelTrace numeric scorer port, MIT License.
// Copyright (c) 2026 xqy2006. See reference/LICENSE.
package fingerprint

import (
	"fmt"
	"math"
	"sort"
)

// Rank returns every legacy candidate in descending robust-score order. Ties
// preserve the pinned bank order. Distance is descriptive and does not decide
// rank. No sample-adequacy cutoff, identity verdict or probability is inferred.
func Rank(numbers []int) ([]Candidate, error) {
	if err := validateNumbers(numbers); err != nil {
		return nil, err
	}
	bank, err := loadLegacyBank()
	if err != nil {
		return nil, err
	}
	_, _, scores := scoreNumbers(numbers, bank)
	candidates := rankCandidates(scores, countNumbers(numbers), bank)
	for _, c := range candidates {
		if math.IsNaN(c.Score) || math.IsInf(c.Score, 0) || math.IsNaN(c.Distance) || math.IsInf(c.Distance, 0) {
			return nil, fmt.Errorf("fingerprint: nonfinite legacy comparison for %s", c.Model)
		}
	}
	return candidates, nil
}

func scoreNumbers(numbers []int, bank *referenceBank) (marginal, ordered, blended []float64) {
	artifact := bank.Robust.Hellinger
	feature := standardizedFeature(hellingerFeature(countNumbers(numbers)), artifact)
	unit := normalized(subtractBasis(feature, artifact.NuisanceBasis))
	marginal = standardize(centroidScores(unit, artifact.Centroids))
	blocks := bank.Robust.OrderedBlocks
	if blocks == nil || blocks.Weight == 0 {
		return marginal, nil, append([]float64(nil), marginal...)
	}
	ordered = orderedScores(numbers, blocks)
	blended = make([]float64, len(marginal))
	for i, score := range marginal {
		blended[i] = (1-blocks.Weight)*score + blocks.Weight*ordered[i]
	}
	return marginal, ordered, blended
}

func orderedScores(numbers []int, artifact *orderedArtifact) []float64 {
	feature := standardizedFeature(orderedFeature(numbers), artifact.numericArtifact)
	unit := normalized(feature)
	bestEnvironment := make([]float64, len(artifact.Centroids))
	for i := range bestEnvironment {
		bestEnvironment[i] = math.Inf(-1)
	}
	for _, centroids := range artifact.EnvironmentCentroids {
		for i, score := range centroidScores(unit, centroids) {
			bestEnvironment[i] = math.Max(bestEnvironment[i], score)
		}
	}
	template := standardize(bestEnvironment)
	projected := normalized(subtractBasis(feature, artifact.NuisanceBasis))
	nuisance := standardize(centroidScores(projected, artifact.Centroids))
	combined := make([]float64, len(template))
	for i := range combined {
		combined[i] = 0.5*template[i] + 0.5*nuisance[i]
	}
	return standardize(combined)
}

func rankCandidates(scores, counts []float64, bank *referenceBank) []Candidate {
	candidates := make([]Candidate, len(bank.Models))
	for i, model := range bank.Models {
		family := model.Family
		if family == "" {
			family = "models"
		}
		candidates[i] = Candidate{model.ID, family, scores[i], jsDistance(counts, model.Counts)}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	return candidates
}

func jsDistance(left, right []float64) float64 {
	leftTotal, rightTotal := 0.0, 0.0
	for i, count := range left {
		leftTotal += count
		rightTotal += right[i]
	}
	rightTotal += alpha * featureDimension
	leftDivergence, rightDivergence := 0.0, 0.0
	for i, count := range left {
		p := count / leftTotal
		q := (right[i] + alpha) / rightTotal
		midpoint := (p + q) / 2
		if p != 0 {
			leftDivergence += p * math.Log(p/midpoint)
		}
		rightDivergence += q * math.Log(q/midpoint)
	}
	return math.Sqrt(((leftDivergence + rightDivergence) / 2) / math.Log(2))
}
