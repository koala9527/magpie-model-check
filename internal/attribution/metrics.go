package attribution

import (
	"fmt"
	"math"
)

// Wilson returns a two-sided Wilson score interval at explicit confidence.
// Each trial must be an independent full group, not a challenge or claim pair.
// Missing populations retain nil value/interval rather than reporting 0%.
func Wilson(events, total int, confidence float64) (Metric, error) {
	m := Metric{Events: events, Total: total, Confidence: confidence}
	if total < 0 || events < 0 || events > total || !finite(confidence) || confidence <= 0 || confidence >= 1 {
		return Metric{}, fmt.Errorf("invalid Wilson events=%d total=%d confidence=%g", events, total, confidence)
	}
	if total == 0 {
		return m, nil
	}
	n := float64(total)
	p := float64(events) / n
	z := math.Sqrt2 * math.Erfinv(confidence)
	z2 := z * z
	denominator := 1 + z2/n
	center := (p + z2/(2*n)) / denominator
	radius := z * math.Sqrt(p*(1-p)/n+z2/(4*n*n)) / denominator
	m.Value = &p
	m.Interval = &Interval{Lower: max(0, center-radius), Upper: min(1, center+radius)}
	return m, nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func validatePolicy(p Policy) error {
	if !finite(p.Confidence) || p.Confidence <= 0 || p.Confidence >= 1 {
		return fmt.Errorf("policy confidence must be finite and strictly between zero and one")
	}
	for name, v := range map[string]float64{
		"max_false_match_rate":       p.MaxFalseMatchRate,
		"max_false_replacement_rate": p.MaxFalseReplacementRate,
		"min_unknown_recall":         p.MinUnknownRecall,
		"max_known_rejection_rate":   p.MaxKnownRejectionRate,
		"min_top1_accuracy":          p.MinTop1Accuracy,
		"min_family_accuracy":        p.MinFamilyAccuracy,
	} {
		if !finite(v) || v < 0 || v > 1 {
			return fmt.Errorf("policy %s must be finite and within [0,1]", name)
		}
	}
	return nil
}
