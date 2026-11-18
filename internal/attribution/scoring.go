package attribution

import (
	"math"
	"sort"
)

type comparison struct {
	candidates []Candidate
	distances  map[string]map[string]float64
}

// A center is the unit-normalized mean of reference-group square-root
// probabilities for the same model and challenge. No holdout enters it.
func (e *Engine) rebuildCenters() []string {
	e.centers = map[string]map[string][]float64{}
	counts := map[string]int{}
	for _, g := range e.groups {
		if g.split != Reference {
			continue
		}
		if e.centers[g.model] == nil {
			e.centers[g.model] = map[string][]float64{}
		}
		counts[g.model]++
		for _, id := range e.challengeIDs {
			if e.centers[g.model][id] == nil {
				e.centers[g.model][id] = make([]float64, 355)
			}
			for i, v := range g.features[id] {
				e.centers[g.model][id][i] += v
			}
		}
	}
	reasons := []string{}
	for _, m := range e.bank.Models {
		if counts[m.ID] == 0 {
			reasons = append(reasons, "reference_centroid_missing:"+m.ID)
			continue
		}
		for _, id := range e.challengeIDs {
			vector := e.centers[m.ID][id]
			norm := 0.0
			for i := range vector {
				vector[i] /= float64(counts[m.ID])
				norm += vector[i] * vector[i]
			}
			norm = math.Sqrt(norm)
			for i := range vector {
				vector[i] /= norm
			}
		}
	}
	return reasons
}

// Hellinger distance compares unit square-root probability vectors, [0,1].
func hellinger(a, b []float64) float64 {
	sum := 0.0
	for i, v := range a {
		delta := v - b[i]
		sum += delta * delta
	}
	return math.Sqrt(sum / 2)
}

func (e *Engine) compare(features map[string][]float64) comparison {
	out := comparison{distances: map[string]map[string]float64{}}
	for _, m := range e.bank.Models {
		if len(e.centers[m.ID]) != len(e.challengeIDs) {
			return comparison{}
		}
		out.distances[m.ID] = map[string]float64{}
		distance := 0.0
		for _, id := range e.challengeIDs {
			d := hellinger(features[id], e.centers[m.ID][id])
			out.distances[m.ID][id] = d
			distance += d
		}
		distance /= float64(len(e.challengeIDs))
		out.candidates = append(out.candidates, Candidate{Model: m.ID, Family: m.Family, Distance: distance, Score: 1 - distance})
	}
	for _, id := range e.challengeIDs {
		winner := -1
		best := math.Inf(1)
		tie := false
		for i, c := range out.candidates {
			d := out.distances[c.Model][id]
			if d < best {
				best = d
				winner = i
				tie = false
			} else if d == best {
				tie = true
			}
		}
		if !tie && winner >= 0 {
			out.candidates[winner].Consistency += 1 / float64(len(e.challengeIDs))
		}
	}
	sort.SliceStable(out.candidates, func(i, j int) bool { return out.candidates[i].Distance < out.candidates[j].Distance })
	return out
}

// The minimal calibration method is an empirical genuine-group envelope,
// together with the smallest observed positive margin and consistency. It is
// not a probability, quantile guarantee, or universal acceptance threshold.
func (e *Engine) deriveThresholds() []string {
	e.thresholds = map[string]Threshold{}
	reasons := []string{}
	for _, m := range e.bank.Models {
		th := Threshold{MinMargin: math.Inf(1), MinConsistency: 1, MaxChallengeDistance: map[string]float64{}}
		separable := true
		for _, g := range e.groups {
			if g.split != Calibration || g.model != m.ID {
				continue
			}
			cmp := e.compare(g.features)
			if len(cmp.candidates) < 2 || cmp.candidates[0].Model != m.ID {
				separable = false
				continue
			}
			best := cmp.candidates[0]
			margin := cmp.candidates[1].Distance - best.Distance
			if margin <= 0 || best.Consistency <= 0 {
				separable = false
				continue
			}
			th.CalibrationGroups++
			th.MaxDistance = max(th.MaxDistance, best.Distance)
			th.MinMargin = min(th.MinMargin, margin)
			th.MinConsistency = min(th.MinConsistency, best.Consistency)
			for _, id := range e.challengeIDs {
				th.MaxChallengeDistance[id] = max(th.MaxChallengeDistance[id], cmp.distances[m.ID][id])
			}
		}
		if !separable || th.CalibrationGroups == 0 {
			reasons = append(reasons, "calibration_not_separable:"+m.ID)
			continue
		}
		e.thresholds[m.ID] = th
	}
	if len(e.challengeIDs) < 2 {
		reasons = append(reasons, "cross_challenge_consistency_missing")
	}
	return reasons
}

func (e *Engine) classify(features map[string][]float64) Decision {
	cmp := e.compare(features)
	d := Decision{Status: Inconclusive, Candidates: cmp.candidates, Reasons: []string{}}
	if len(cmp.candidates) < 2 || len(e.thresholds) != len(e.bank.Models) {
		d.Reasons = append(d.Reasons, "thresholds_not_calibrated")
		return d
	}
	allOutside := true
	for _, c := range cmp.candidates {
		if c.Distance <= e.thresholds[c.Model].MaxDistance {
			allOutside = false
		}
	}
	if allOutside {
		for _, id := range e.challengeIDs {
			for _, c := range cmp.candidates {
				if cmp.distances[c.Model][id] <= e.thresholds[c.Model].MaxChallengeDistance[id] {
					d.Reasons = append(d.Reasons, "cross_challenge_disagreement")
					return d
				}
			}
		}
		d.Status = Unknown
		d.Reasons = append(d.Reasons, "outside_all_calibrated_classes")
		return d
	}
	best := cmp.candidates[0]
	th := e.thresholds[best.Model]
	if best.Distance > th.MaxDistance {
		d.Reasons = append(d.Reasons, "top_candidate_outside_class")
		return d
	}
	margin := cmp.candidates[1].Distance - best.Distance
	if margin <= 0 || margin < th.MinMargin {
		d.Reasons = append(d.Reasons, "candidate_margin_not_met")
		return d
	}
	if best.Consistency < th.MinConsistency {
		d.Reasons = append(d.Reasons, "cross_challenge_consistency_not_met")
		return d
	}
	for _, id := range e.challengeIDs {
		if cmp.distances[best.Model][id] > th.MaxChallengeDistance[id] {
			d.Reasons = append(d.Reasons, "cross_challenge_distance_not_met")
			return d
		}
	}
	for _, other := range cmp.candidates[1:] {
		if other.Distance <= e.thresholds[other.Model].MaxDistance {
			d.Reasons = append(d.Reasons, "class_envelopes_overlap")
			return d
		}
	}
	d.Status = Matched
	d.PredictedModel = best.Model
	d.PredictedFamily = best.Family
	return d
}
